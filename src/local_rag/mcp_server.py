"""MCP server exposing local-rag search and index tools."""

import logging
from typing import Any

from mcp.server.fastmcp import FastMCP

from local_rag.config import load_config
from local_rag.db import get_connection, init_db

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
        sender: str | None = None,
        author: str | None = None,
    ) -> list[dict[str, Any]]:
        """Search personal knowledge using hybrid vector + full-text search with Reciprocal Rank Fusion.

        Searches across all indexed collections by default. Combines semantic similarity
        (understands meaning) with keyword matching (finds exact phrases) for best results.

        ## Collections and their metadata

        **obsidian** (system) — Obsidian vault notes and attachments.
          Source types: markdown, pdf, docx, epub, html, txt.
          Metadata: tags, heading_path.
          Useful filters: source_type, date_from/date_to.

        **email** (system) — eM Client emails.
          Source types: email.
          Metadata: sender, recipients, date, folder.
          Useful filters: sender, date_from/date_to.

        **calibre** (system) — Calibre ebook library.
          Source types: pdf, epub.
          Metadata: authors, tags, series, publisher, page_number.
          Useful filters: author, date_from/date_to.

        **rss** (system) — NetNewsWire RSS articles.
          Source types: rss.
          Metadata: feed_name, url, date.
          Useful filters: date_from/date_to.

        **Git repositories** (code) — Indexed git repos, one collection per repo.
          Source types: code.
          Metadata: language, symbol_name, symbol_type, start_line.
          Useful filters: collection=<repo-name>, or collection=code for all repos.

        **Project folders** (project) — User-created document collections.
          Source types: vary by content (markdown, pdf, docx, etc.).
          Useful filters: collection=<project-name>.

        ## Collection filtering

        The `collection` parameter accepts either a collection name or a collection type:
        - Name (e.g., "obsidian", "email", "my-project") — searches that specific collection.
        - Type ("system", "project", "code") — searches all collections of that type.

        ## Examples

        - Search everything: query="kubernetes deployment strategy"
        - Search emails from someone: query="invoice", sender="john@example.com"
        - Search a specific repo: query="authentication middleware", collection="my-api"
        - Search all code repos: query="database connection pool", collection="code"
        - Search books by author: query="machine learning", author="Bishop"
        - Search PDFs in Obsidian: query="tax return", collection="obsidian", source_type="pdf"
        - Search recent emails: query="project update", sender="boss", date_from="2025-01-01"
        - Search RSS articles: query="AI regulation", collection="rss", date_from="2025-06-01"

        Args:
            query: The search query text. Can be a natural language question or keywords.
            collection: Filter by collection name (e.g., 'obsidian', 'email', 'my-project')
                or collection type ('system', 'project', 'code'). Omit to search everything.
            top_k: Number of results to return (default 10).
            source_type: Filter by source type: 'markdown', 'pdf', 'docx', 'epub', 'html',
                'txt', 'email', 'code', 'rss'.
            date_from: Only results after this date (YYYY-MM-DD).
            date_to: Only results before this date (YYYY-MM-DD).
            sender: Filter by email sender (case-insensitive substring match).
            author: Filter by book author (case-insensitive substring match).
        """
        from local_rag.embeddings import OllamaConnectionError
        from local_rag.search import perform_search

        try:
            results = perform_search(
                query=query,
                collection=collection,
                top_k=top_k,
                source_type=source_type,
                date_from=date_from,
                date_to=date_to,
                sender=sender,
                author=author,
            )
        except OllamaConnectionError as e:
            return [{"error": str(e)}]

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
        from local_rag.indexers.git_indexer import GitRepoIndexer, _is_git_repo, _parse_watermark
        from local_rag.indexers.obsidian import ObsidianIndexer
        from local_rag.indexers.project import ProjectIndexer
        from local_rag.indexers.rss_indexer import RSSIndexer

        config = load_config()
        conn = get_connection(config)
        init_db(conn, config)

        try:
            if collection == "obsidian":
                if not config.is_collection_enabled("obsidian"):
                    return {"error": "Collection 'obsidian' is disabled in config."}
                indexer = ObsidianIndexer(config.obsidian_vaults, config.obsidian_exclude_folders)
            elif collection == "email":
                if not config.is_collection_enabled("email"):
                    return {"error": "Collection 'email' is disabled in config."}
                indexer = EmailIndexer(str(config.emclient_db_path))
            elif collection == "calibre":
                if not config.is_collection_enabled("calibre"):
                    return {"error": "Collection 'calibre' is disabled in config."}
                indexer = CalibreIndexer(config.calibre_libraries)
            elif collection == "rss":
                if not config.is_collection_enabled("rss"):
                    return {"error": "Collection 'rss' is disabled in config."}
                indexer = RSSIndexer(str(config.netnewswire_db_path))
            else:
                # Check if this is an existing git repo collection
                row = conn.execute(
                    "SELECT description FROM collections WHERE name = ?",
                    (collection,),
                ).fetchone()
                watermark = _parse_watermark(row["description"] if row else None)

                if not config.is_collection_enabled(collection):
                    return {"error": f"Collection '{collection}' is disabled in config."}

                if watermark:
                    # Existing git repo collection — re-index from stored repo path
                    repo_path_str, _ = watermark
                    indexer = GitRepoIndexer(P(repo_path_str), collection_name=collection)
                elif path and P(path).is_dir() and _is_git_repo(P(path)):
                    # New git repo collection
                    indexer = GitRepoIndexer(P(path), collection_name=collection)
                elif path:
                    indexer = ProjectIndexer(collection, [P(path)])
                else:
                    return {"error": f"Path required for project collection '{collection}'."}

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
