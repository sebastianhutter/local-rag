"""Hybrid search engine with vector + FTS and Reciprocal Rank Fusion."""

import json
import logging
import sqlite3
from dataclasses import dataclass

from local_rag.config import Config
from local_rag.embeddings import serialize_float32

logger = logging.getLogger(__name__)


@dataclass
class SearchResult:
    """A single search result."""

    content: str
    title: str
    metadata: dict
    score: float
    collection: str
    source_path: str
    source_type: str


@dataclass
class SearchFilters:
    """Optional filters for search queries."""

    collection: str | None = None
    source_type: str | None = None
    date_from: str | None = None
    date_to: str | None = None
    sender: str | None = None
    author: str | None = None


def _vector_search(
    conn: sqlite3.Connection,
    query_embedding: list[float],
    top_k: int,
    filters: SearchFilters | None,
) -> list[tuple[int, float]]:
    """Run vector similarity search via sqlite-vec.

    Returns list of (document_id, distance) tuples.
    """
    query_blob = serialize_float32(query_embedding)

    # Get candidate document IDs from vec_documents
    rows = conn.execute(
        """
        SELECT document_id, distance
        FROM vec_documents
        WHERE embedding MATCH ?
        ORDER BY distance
        LIMIT ?
        """,
        (query_blob, top_k * 3),  # Fetch more to allow for filtering
    ).fetchall()

    if not filters:
        return [(row["document_id"], row["distance"]) for row in rows[:top_k]]

    # Apply filters by looking up document metadata
    filtered = []
    for row in rows:
        doc_id = row["document_id"]
        if _passes_filters(conn, doc_id, filters):
            filtered.append((doc_id, row["distance"]))
            if len(filtered) >= top_k:
                break

    return filtered


def _fts_search(
    conn: sqlite3.Connection,
    query_text: str,
    top_k: int,
    filters: SearchFilters | None,
) -> list[tuple[int, float]]:
    """Run full-text search via FTS5.

    Returns list of (document_id, rank_score) tuples.
    """
    # Escape FTS5 special characters in query
    safe_query = _escape_fts_query(query_text)
    if not safe_query:
        return []

    try:
        rows = conn.execute(
            """
            SELECT rowid, rank
            FROM documents_fts
            WHERE documents_fts MATCH ?
            ORDER BY rank
            LIMIT ?
            """,
            (safe_query, top_k * 3),
        ).fetchall()
    except sqlite3.OperationalError as e:
        logger.warning("FTS query failed for '%s': %s", safe_query, e)
        return []

    if not filters:
        return [(row["rowid"], row["rank"]) for row in rows[:top_k]]

    filtered = []
    for row in rows:
        doc_id = row["rowid"]
        if _passes_filters(conn, doc_id, filters):
            filtered.append((doc_id, row["rank"]))
            if len(filtered) >= top_k:
                break

    return filtered


def _escape_fts_query(query: str) -> str:
    """Convert a natural language query into a safe FTS5 query.

    Wraps each token in double quotes to treat them as literal terms.
    """
    tokens = query.split()
    if not tokens:
        return ""
    # Quote each token to avoid FTS5 syntax errors from special chars
    return " ".join(f'"{token}"' for token in tokens)


def _passes_filters(
    conn: sqlite3.Connection, document_id: int, filters: SearchFilters
) -> bool:
    """Check if a document passes the given filters."""
    row = conn.execute(
        """
        SELECT d.metadata, c.name as collection_name, s.source_type, s.source_path
        FROM documents d
        JOIN collections c ON d.collection_id = c.id
        JOIN sources s ON d.source_id = s.id
        WHERE d.id = ?
        """,
        (document_id,),
    ).fetchone()

    if not row:
        return False

    if filters.collection and row["collection_name"] != filters.collection:
        return False

    if filters.source_type and row["source_type"] != filters.source_type:
        return False

    if filters.sender or filters.author or filters.date_from or filters.date_to:
        metadata = json.loads(row["metadata"]) if row["metadata"] else {}

        if filters.sender:
            doc_sender = metadata.get("sender", "")
            if filters.sender.lower() not in doc_sender.lower():
                return False

        if filters.author:
            authors = metadata.get("authors", [])
            author_lower = filters.author.lower()
            if not any(author_lower in a.lower() for a in authors):
                return False

        doc_date = metadata.get("date", "")
        if filters.date_from and doc_date and doc_date < filters.date_from:
            return False
        if filters.date_to and doc_date and doc_date > filters.date_to:
            return False

    return True


def rrf_merge(
    vec_results: list[tuple[int, float]],
    fts_results: list[tuple[int, float]],
    k: int = 60,
    vector_weight: float = 0.7,
    fts_weight: float = 0.3,
) -> list[tuple[int, float]]:
    """Merge two ranked lists using Reciprocal Rank Fusion.

    Args:
        vec_results: (document_id, distance) from vector search.
        fts_results: (document_id, rank) from FTS search.
        k: RRF parameter (default 60).
        vector_weight: Weight for vector search scores.
        fts_weight: Weight for FTS search scores.

    Returns:
        Merged list of (document_id, rrf_score) sorted by score descending.
    """
    scores: dict[int, float] = {}

    for rank, (doc_id, _) in enumerate(vec_results):
        scores[doc_id] = scores.get(doc_id, 0.0) + vector_weight / (k + rank + 1)

    for rank, (doc_id, _) in enumerate(fts_results):
        scores[doc_id] = scores.get(doc_id, 0.0) + fts_weight / (k + rank + 1)

    return sorted(scores.items(), key=lambda x: x[1], reverse=True)


def search(
    conn: sqlite3.Connection,
    query_embedding: list[float],
    query_text: str,
    top_k: int,
    filters: SearchFilters | None,
    config: Config,
) -> list[SearchResult]:
    """Run hybrid search combining vector similarity and full-text search.

    Args:
        conn: SQLite connection.
        query_embedding: Embedding vector for the query.
        query_text: The raw query text for FTS.
        top_k: Number of results to return.
        filters: Optional search filters.
        config: Application configuration.

    Returns:
        List of SearchResult objects sorted by relevance.
    """
    vec_results = _vector_search(conn, query_embedding, top_k, filters)
    fts_results = _fts_search(conn, query_text, top_k, filters)

    merged = rrf_merge(
        vec_results,
        fts_results,
        k=config.search_defaults.rrf_k,
        vector_weight=config.search_defaults.vector_weight,
        fts_weight=config.search_defaults.fts_weight,
    )

    results: list[SearchResult] = []
    for doc_id, score in merged[:top_k]:
        row = conn.execute(
            """
            SELECT d.content, d.title, d.metadata,
                   c.name as collection_name,
                   s.source_path, s.source_type
            FROM documents d
            JOIN collections c ON d.collection_id = c.id
            JOIN sources s ON d.source_id = s.id
            WHERE d.id = ?
            """,
            (doc_id,),
        ).fetchone()

        if row:
            metadata = json.loads(row["metadata"]) if row["metadata"] else {}
            results.append(
                SearchResult(
                    content=row["content"],
                    title=row["title"] or "",
                    metadata=metadata,
                    score=score,
                    collection=row["collection_name"],
                    source_path=row["source_path"],
                    source_type=row["source_type"],
                )
            )

    return results
