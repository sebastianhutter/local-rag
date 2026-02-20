"""MCP server exposing local-rag search and index tools."""

import logging
from typing import Any
from urllib.parse import quote

from mcp.server.fastmcp import FastMCP

from local_rag.config import load_config
from local_rag.db import get_connection, init_db

logger = logging.getLogger(__name__)


def _build_source_uri(
    source_path: str,
    source_type: str,
    metadata: dict,
    collection: str,
    obsidian_vaults: list | None = None,
) -> str | None:
    """Build a clickable URI for a search result's original source.

    Returns an obsidian://, vscode://, file://, https://, or None depending on the source:
    - Obsidian vault files: obsidian://open URI (opens directly in Obsidian app)
    - Code files: vscode://file URI (opens in VS Code at the correct line)
    - Other file-based sources (calibre, project): file:// URI
    - RSS articles: https:// URL from metadata
    - Email and git commits: None (no meaningful URI available)

    Args:
        source_path: The source_path from the sources table.
        source_type: The source_type from the sources table.
        metadata: Parsed metadata dict from the documents table.
        collection: The collection name the result belongs to.
        obsidian_vaults: List of configured Obsidian vault paths (needed
            to construct obsidian:// URIs).

    Returns:
        A URI string or None if no meaningful link can be constructed.
    """
    # RSS articles have a web URL in metadata
    if source_type == "rss":
        return metadata.get("url") or None

    # Email messages and git commits have no openable URI
    if source_type in ("email", "commit"):
        return None

    # Virtual paths (calibre description-only, git commit refs) are not openable
    if source_path.startswith(("calibre://", "git://")):
        return None

    # Obsidian vault files — use obsidian:// URI to open in the app
    if collection == "obsidian" and obsidian_vaults:
        obsidian_uri = _build_obsidian_uri(source_path, obsidian_vaults)
        if obsidian_uri:
            return obsidian_uri

    # Code files — use vscode:// URI to open in VS Code at the correct line
    if source_type == "code":
        start_line = metadata.get("start_line", 1)
        return f"vscode://file{quote(source_path, safe='/')}:{start_line}"

    # Everything else is a real file path — return as file:// URI
    return f"file://{quote(source_path, safe='/')}"


def _build_obsidian_uri(source_path: str, vault_paths: list) -> str | None:
    """Build an obsidian://open URI for a file inside an Obsidian vault.

    Matches the source_path against known vault paths to extract the vault
    name and the file path relative to the vault root.

    The URI format is: obsidian://open?vault=VAULT_NAME&file=RELATIVE/PATH

    Args:
        source_path: Absolute file path of the indexed document.
        vault_paths: List of configured Obsidian vault root paths.

    Returns:
        An obsidian:// URI string, or None if the path doesn't match any vault.
    """
    from pathlib import Path

    for vault_path in vault_paths:
        vault_str = str(Path(vault_path).expanduser().resolve())
        if source_path.startswith(vault_str + "/"):
            vault_name = Path(vault_str).name
            relative_path = source_path[len(vault_str) + 1:]
            return (
                f"obsidian://open?vault={quote(vault_name, safe='')}"
                f"&file={quote(relative_path, safe='/')}"
            )
    return None


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

        **Code groups** (code) — Groups of git repos indexed together by topic or org.
          Each group is a collection containing code from one or more repos.
          Source types: code, commit.
          Code metadata: language, symbol_name, symbol_type, start_line.
          Commit metadata: commit_sha, commit_sha_short, author_name, author_email,
            author_date, commit_message, file_path, additions, deletions.
          Useful filters: collection=<group-name>, source_type=commit, or collection=code for all.

        **Project folders** (project) — User-created document collections.
          Source types: vary by content (markdown, pdf, docx, etc.).
          Useful filters: collection=<project-name>.

        ## Collection filtering

        The `collection` parameter accepts either a collection name or a collection type:
        - Name (e.g., "obsidian", "email", "rustyquill", "terraform") — searches that specific collection.
        - Type ("system", "project", "code") — searches all collections of that type.
          Use "code" to search across all code groups at once.

        ## Source URIs

        Each result includes a `source_uri` field with a clickable link to the original source
        when available. Use these to let the user open or navigate to the original document.

        - **Obsidian vault files** (markdown, PDFs, etc. inside a vault):
          Returns an `obsidian://open?vault=...&file=...` URI that opens the note directly
          in the Obsidian app. Example: `obsidian://open?vault=MyVault&file=notes/report.md`.
        - **Code files** (from code groups):
          Returns a `vscode://file/...` URI that opens the file in VS Code at the correct line.
          Example: `vscode://file/Users/you/repos/project/src/main.py:42`.
        - **Other file-based sources** (calibre books, project docs):
          Returns a `file://` URI, e.g. `file:///Users/you/CalibreLibrary/book.epub`.
          These open the file in the default macOS application (Preview for PDFs, editor for code, etc.).
        - **RSS articles**: Returns the original article `https://` URL from metadata.
          Opens the article in the default browser.
        - **Email and git commits**: Returns `null` — no meaningful URI is available for these.
        - **Calibre description-only entries**: Returns `null` — no actual file exists.

        When presenting results to the user, include the `source_uri` as a markdown link so
        the user can click to open the original. Example:
          [Open in Obsidian](obsidian://open?vault=MyVault&file=notes/report.md)
          [Open PDF](file:///Users/you/CalibreLibrary/Author/book.pdf)
          [Read article](https://example.com/article)

        ## Examples

        - Search everything: query="kubernetes deployment strategy"
        - Search emails from someone: query="invoice", sender="john@example.com"
        - Search a code group: query="authentication middleware", collection="rustyquill"
        - Search all code groups: query="database connection pool", collection="code"
        - Search cross-cutting group: query="module structure", collection="terraform"
        - Search books by author: query="machine learning", author="Bishop"
        - Search PDFs in Obsidian: query="tax return", collection="obsidian", source_type="pdf"
        - Search recent emails: query="project update", sender="boss", date_from="2025-01-01"
        - Search RSS articles: query="AI regulation", collection="rss", date_from="2025-06-01"
        - Search commit history: query="refactored auth", collection="rustyquill", source_type="commit"

        Args:
            query: The search query text. Can be a natural language question or keywords.
            collection: Filter by collection name (e.g., 'obsidian', 'email', 'my-project')
                or collection type ('system', 'project', 'code'). Omit to search everything.
            top_k: Number of results to return (default 10).
            source_type: Filter by source type: 'markdown', 'pdf', 'docx', 'epub', 'html',
                'txt', 'email', 'code', 'commit', 'rss'.
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

        config = load_config()
        obsidian_vaults = config.obsidian_vaults

        return [
            {
                "title": r.title,
                "content": r.content,
                "collection": r.collection,
                "source_type": r.source_type,
                "source_path": r.source_path,
                "source_uri": _build_source_uri(
                    r.source_path, r.source_type, r.metadata,
                    r.collection, obsidian_vaults,
                ),
                "score": round(r.score, 4),
                "metadata": r.metadata,
            }
            for r in results
        ]

    @mcp.tool()
    def rag_list_collections() -> list[dict[str, Any]]:
        """List all available collections with source file counts, chunk counts, and metadata.

        Collections of type 'code' represent code groups that may contain multiple git repos.
        """
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

        For system collections ('obsidian', 'email', 'calibre', 'rss'), uses configured paths.
        For code groups (matching a key in config code_groups), indexes all repos in that group.
        For project collections, a path argument is required.

        Args:
            collection: Collection name ('obsidian', 'email', 'calibre', 'rss', a code group
                name, or a project name).
            path: Path to index (required for project collections, or to add a single repo
                to a code group).
        """
        from pathlib import Path as P

        from local_rag.indexers.calibre_indexer import CalibreIndexer
        from local_rag.indexers.email_indexer import EmailIndexer
        from local_rag.indexers.git_indexer import GitRepoIndexer
        from local_rag.indexers.obsidian import ObsidianIndexer
        from local_rag.indexers.project import ProjectIndexer
        from local_rag.indexers.rss_indexer import RSSIndexer

        config = load_config()
        conn = get_connection(config)
        init_db(conn, config)

        try:
            if not config.is_collection_enabled(collection):
                return {"error": f"Collection '{collection}' is disabled in config."}

            if collection == "obsidian":
                indexer = ObsidianIndexer(config.obsidian_vaults, config.obsidian_exclude_folders)
                result = indexer.index(conn, config)
            elif collection == "email":
                indexer = EmailIndexer(str(config.emclient_db_path))
                result = indexer.index(conn, config)
            elif collection == "calibre":
                indexer = CalibreIndexer(config.calibre_libraries)
                result = indexer.index(conn, config)
            elif collection == "rss":
                indexer = RSSIndexer(str(config.netnewswire_db_path))
                result = indexer.index(conn, config)
            elif collection in config.code_groups:
                # Code group — index all repos in this group (with commit history)
                total_indexed = 0
                total_skipped = 0
                total_errors = 0
                total_found = 0
                for repo_path in config.code_groups[collection]:
                    idx = GitRepoIndexer(repo_path, collection_name=collection)
                    r = idx.index(conn, config, index_history=True)
                    total_indexed += r.indexed
                    total_skipped += r.skipped
                    total_errors += r.errors
                    total_found += r.total_found
                return {
                    "collection": collection,
                    "indexed": total_indexed,
                    "skipped": total_skipped,
                    "errors": total_errors,
                    "total_found": total_found,
                }
            elif path:
                indexer = ProjectIndexer(collection, [P(path)])
                result = indexer.index(conn, config)
            else:
                return {"error": f"Unknown collection '{collection}'. Provide a path for project indexing."}

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

        Returns source count, chunk count, source type breakdown, last indexed
        timestamp, and a sample of document titles for the given collection.

        Use rag_list_collections() first to discover available collection names.

        Args:
            collection: The collection name (required). Must be an existing collection,
                e.g. 'obsidian', 'email', 'calibre', 'rss', a code group name, or a
                project name. Use rag_list_collections() to see all available names.
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
