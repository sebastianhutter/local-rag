"""MCP server exposing local-rag search and index tools."""

import json
import logging
from typing import Any

from mcp.server.fastmcp import FastMCP

from local_rag.config import load_config
from local_rag.db import get_connection, get_or_create_collection, init_db

logger = logging.getLogger(__name__)


def create_server() -> FastMCP:
    """Create and configure the MCP server with all tools registered."""
    mcp = FastMCP("local-rag", instructions="Local RAG system for searching personal knowledge.")

    @mcp.tool()
    def rag_search(
        query: str,
        collection: str | None = None,
        top_k: int = 10,
        source_type: str | None = None,
        date_from: str | None = None,
        date_to: str | None = None,
        author: str | None = None,
    ) -> list[dict[str, Any]]:
        """Search across indexed collections using hybrid vector + full-text search.

        Args:
            query: The search query text.
            collection: Optional collection name to search within.
            top_k: Number of results to return (default 10).
            source_type: Filter by source type (e.g., 'pdf', 'markdown', 'email').
            date_from: Only results after this date (YYYY-MM-DD).
            date_to: Only results before this date (YYYY-MM-DD).
            author: Filter by book author (case-insensitive substring match).
        """
        from local_rag.embeddings import OllamaConnectionError, get_embedding
        from local_rag.search import SearchFilters
        from local_rag.search import search as do_search

        config = load_config()
        conn = get_connection(config)
        init_db(conn, config)

        try:
            try:
                query_embedding = get_embedding(query, config)
            except OllamaConnectionError as e:
                return [{"error": str(e)}]

            filters = SearchFilters(
                collection=collection,
                source_type=source_type,
                date_from=date_from,
                date_to=date_to,
                author=author,
            )

            results = do_search(conn, query_embedding, query, top_k, filters, config)

            return [
                {
                    "title": r.title,
                    "content": r.content,
                    "collection": r.collection,
                    "source_type": r.source_type,
                    "source_path": r.source_path,
                    "score": round(r.score, 4),
                    "metadata": r.metadata,
                }
                for r in results
            ]
        finally:
            conn.close()

    @mcp.tool()
    def rag_list_collections() -> list[dict[str, Any]]:
        """List all available collections with source file counts, chunk counts, and metadata."""
        config = load_config()
        conn = get_connection(config)
        init_db(conn, config)

        try:
            rows = conn.execute("""
                SELECT c.name, c.collection_type, c.description, c.created_at,
                       (SELECT COUNT(*) FROM sources s WHERE s.collection_id = c.id) as source_count,
                       (SELECT COUNT(*) FROM documents d WHERE d.collection_id = c.id) as chunk_count,
                       (SELECT MAX(s.last_indexed_at) FROM sources s WHERE s.collection_id = c.id) as last_indexed
                FROM collections c
                ORDER BY c.name
            """).fetchall()

            return [
                {
                    "name": row["name"],
                    "type": row["collection_type"],
                    "description": row["description"],
                    "source_count": row["source_count"],
                    "chunk_count": row["chunk_count"],
                    "last_indexed": row["last_indexed"],
                    "created_at": row["created_at"],
                }
                for row in rows
            ]
        finally:
            conn.close()

    @mcp.tool()
    def rag_index(collection: str, path: str | None = None) -> dict[str, Any]:
        """Trigger indexing for a collection.

        For 'obsidian' and 'email', uses configured paths.
        For project collections, a path argument is required.

        Args:
            collection: Collection name ('obsidian', 'email', or a project name).
            path: Path to index (required for project collections).
        """
        from pathlib import Path as P

        from local_rag.indexers.calibre_indexer import CalibreIndexer
        from local_rag.indexers.email_indexer import EmailIndexer
        from local_rag.indexers.obsidian import ObsidianIndexer
        from local_rag.indexers.project import ProjectIndexer

        config = load_config()
        conn = get_connection(config)
        init_db(conn, config)

        try:
            if collection == "obsidian":
                indexer = ObsidianIndexer(config.obsidian_vaults)
            elif collection == "email":
                indexer = EmailIndexer(str(config.emclient_db_path))
            elif collection == "calibre":
                indexer = CalibreIndexer(config.calibre_libraries)
            else:
                if not path:
                    return {"error": f"Path required for project collection '{collection}'."}
                indexer = ProjectIndexer(collection, [P(path)])

            result = indexer.index(conn, config)
            return {
                "collection": collection,
                "indexed": result.indexed,
                "skipped": result.skipped,
                "errors": result.errors,
                "total_found": result.total_found,
            }
        finally:
            conn.close()

    @mcp.tool()
    def rag_collection_info(collection: str) -> dict[str, Any]:
        """Get detailed information about a specific collection.

        Args:
            collection: The collection name.
        """
        config = load_config()
        conn = get_connection(config)
        init_db(conn, config)

        try:
            row = conn.execute(
                "SELECT * FROM collections WHERE name = ?", (collection,)
            ).fetchone()

            if not row:
                return {"error": f"Collection '{collection}' not found."}

            coll_id = row["id"]

            doc_count = conn.execute(
                "SELECT COUNT(*) as cnt FROM documents WHERE collection_id = ?",
                (coll_id,),
            ).fetchone()["cnt"]

            source_count = conn.execute(
                "SELECT COUNT(*) as cnt FROM sources WHERE collection_id = ?",
                (coll_id,),
            ).fetchone()["cnt"]

            type_breakdown = conn.execute(
                "SELECT source_type, COUNT(*) as cnt FROM sources WHERE collection_id = ? GROUP BY source_type",
                (coll_id,),
            ).fetchall()

            last_indexed = conn.execute(
                "SELECT MAX(last_indexed_at) as ts FROM sources WHERE collection_id = ?",
                (coll_id,),
            ).fetchone()["ts"]

            sample_titles = conn.execute(
                "SELECT DISTINCT title FROM documents WHERE collection_id = ? LIMIT 10",
                (coll_id,),
            ).fetchall()

            return {
                "name": collection,
                "type": row["collection_type"],
                "description": row["description"],
                "created_at": row["created_at"],
                "source_count": source_count,
                "chunk_count": doc_count,
                "last_indexed": last_indexed,
                "source_types": {tb["source_type"]: tb["cnt"] for tb in type_breakdown},
                "sample_titles": [st["title"] for st in sample_titles],
            }
        finally:
            conn.close()

    return mcp
