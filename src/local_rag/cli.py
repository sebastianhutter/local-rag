"""Click CLI entry point for local-rag."""

import logging
import signal
import sys
from pathlib import Path

import click

from local_rag.config import load_config


def _handle_sigint(_sig: int, _frame: object) -> None:
    """Handle Ctrl+C gracefully."""
    click.echo("\nInterrupted. Shutting down...", err=True)
    sys.exit(130)


signal.signal(signal.SIGINT, _handle_sigint)

logger = logging.getLogger(__name__)


def _setup_logging(verbose: bool) -> None:
    """Configure logging level."""
    level = logging.DEBUG if verbose else logging.INFO
    logging.basicConfig(
        level=level,
        format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
        datefmt="%H:%M:%S",
    )
    # Silence noisy HTTP request logging from httpx/httpcore
    logging.getLogger("httpx").setLevel(logging.WARNING)
    logging.getLogger("httpcore").setLevel(logging.WARNING)


def _get_db(config):
    """Get initialized database connection."""
    from local_rag.db import get_connection, init_db

    conn = get_connection(config)
    init_db(conn, config)
    return conn


@click.group()
@click.option("--verbose", "-v", is_flag=True, help="Enable debug logging.")
@click.pass_context
def main(ctx: click.Context, verbose: bool) -> None:
    """local-rag: Fully local RAG system for personal knowledge."""
    _setup_logging(verbose)
    ctx.ensure_object(dict)
    ctx.obj["verbose"] = verbose


# ── Index commands ──────────────────────────────────────────────────────


@main.group()
def index() -> None:
    """Index sources into the RAG database."""


@index.command("obsidian")
@click.option("--vault", "-v", "vaults", multiple=True, type=click.Path(exists=True, path_type=Path),
              help="Vault path(s). If omitted, uses config.")
@click.option("--force", is_flag=True, help="Force re-index all files.")
def index_obsidian(vaults: tuple[Path, ...], force: bool) -> None:
    """Index Obsidian vault(s)."""
    from local_rag.indexers.obsidian import ObsidianIndexer

    config = load_config()
    vault_paths = list(vaults) if vaults else config.obsidian_vaults

    if not vault_paths:
        click.echo("Error: No vault paths provided. Use --vault or set obsidian_vaults in config.", err=True)
        sys.exit(1)

    conn = _get_db(config)
    try:
        indexer = ObsidianIndexer(vault_paths, config.obsidian_exclude_folders)
        result = indexer.index(conn, config, force=force)
        click.echo(
            f"Obsidian indexing complete: {result.indexed} indexed, "
            f"{result.skipped} skipped, {result.errors} errors "
            f"(out of {result.total_found} files found)"
        )
    finally:
        conn.close()


@index.command("email")
@click.option("--force", is_flag=True, help="Force re-index all emails.")
def index_email(force: bool) -> None:
    """Index eM Client emails."""
    from local_rag.indexers.email_indexer import EmailIndexer

    config = load_config()
    conn = _get_db(config)
    try:
        indexer = EmailIndexer(str(config.emclient_db_path))
        result = indexer.index(conn, config, force=force)
        click.echo(
            f"Email indexing complete: {result.indexed} indexed, "
            f"{result.skipped} skipped, {result.errors} errors "
            f"(out of {result.total_found} emails found)"
        )
    finally:
        conn.close()


@index.command("calibre")
@click.option("--library", "-l", "libraries", multiple=True, type=click.Path(exists=True, path_type=Path),
              help="Library path(s). If omitted, uses config.")
@click.option("--force", is_flag=True, help="Force re-index all books.")
def index_calibre(libraries: tuple[Path, ...], force: bool) -> None:
    """Index Calibre ebook library/libraries."""
    from local_rag.indexers.calibre_indexer import CalibreIndexer

    config = load_config()
    library_paths = list(libraries) if libraries else config.calibre_libraries

    if not library_paths:
        click.echo("Error: No library paths provided. Use --library or set calibre_libraries in config.", err=True)
        sys.exit(1)

    conn = _get_db(config)
    try:
        indexer = CalibreIndexer(library_paths)
        result = indexer.index(conn, config, force=force)
        click.echo(
            f"Calibre indexing complete: {result.indexed} indexed, "
            f"{result.skipped} skipped, {result.errors} errors "
            f"(out of {result.total_found} books found)"
        )
    finally:
        conn.close()


@index.command("rss")
@click.option("--force", is_flag=True, help="Force re-index all articles.")
def index_rss(force: bool) -> None:
    """Index NetNewsWire RSS articles."""
    from local_rag.indexers.rss_indexer import RSSIndexer

    config = load_config()
    conn = _get_db(config)
    try:
        indexer = RSSIndexer(str(config.netnewswire_db_path))
        result = indexer.index(conn, config, force=force)
        click.echo(
            f"RSS indexing complete: {result.indexed} indexed, "
            f"{result.skipped} skipped, {result.errors} errors "
            f"(out of {result.total_found} articles found)"
        )
    finally:
        conn.close()


@index.command("project")
@click.argument("name")
@click.argument("paths", nargs=-1, required=True, type=click.Path(exists=True, path_type=Path))
@click.option("--force", is_flag=True, help="Force re-index all files.")
def index_project(name: str, paths: tuple[Path, ...], force: bool) -> None:
    """Index documents into a named project collection."""
    from local_rag.indexers.project import ProjectIndexer

    config = load_config()
    conn = _get_db(config)
    try:
        indexer = ProjectIndexer(name, list(paths))
        result = indexer.index(conn, config, force=force)
        click.echo(
            f"Project '{name}' indexing complete: {result.indexed} indexed, "
            f"{result.skipped} skipped, {result.errors} errors "
            f"(out of {result.total_found} files found)"
        )
    finally:
        conn.close()


@index.command("repo")
@click.argument("path", required=False, type=click.Path(exists=True, path_type=Path))
@click.option("--name", "-n", default=None, help="Collection name (defaults to repo dir name).")
@click.option("--force", is_flag=True, help="Force re-index all files.")
def index_repo(path: Path | None, name: str | None, force: bool) -> None:
    """Index a git repository's code files.

    If PATH is given, indexes that single repo. If omitted, indexes all repos
    listed in the git_repos config.
    """
    from local_rag.indexers.git_indexer import GitRepoIndexer

    config = load_config()

    if path:
        repo_paths = [path]
    elif config.git_repos:
        repo_paths = config.git_repos
    else:
        click.echo("Error: No path provided and no git_repos configured.", err=True)
        sys.exit(1)

    conn = _get_db(config)
    try:
        for repo_path in repo_paths:
            # --name only applies when indexing a single explicit repo
            coll_name = name if (path and name) else None
            indexer = GitRepoIndexer(repo_path, collection_name=coll_name)
            result = indexer.index(conn, config, force=force)
            click.echo(
                f"Repo '{indexer.collection_name}' indexing complete: {result.indexed} indexed, "
                f"{result.skipped} skipped, {result.errors} errors "
                f"(out of {result.total_found} files found)"
            )
    finally:
        conn.close()


# ── Search command ──────────────────────────────────────────────────────


@main.command()
@click.argument("query")
@click.option("--collection", "-c", help="Search within a specific collection.")
@click.option("--type", "source_type", help="Filter by source type (e.g., pdf, markdown, email).")
@click.option("--from", "sender", help="Filter by email sender.")
@click.option("--author", help="Filter by book author (case-insensitive substring match).")
@click.option("--after", help="Only results after this date (YYYY-MM-DD).")
@click.option("--before", help="Only results before this date (YYYY-MM-DD).")
@click.option("--top", default=10, show_default=True, help="Number of results to return.")
def search(query: str, collection: str | None, source_type: str | None,
           sender: str | None, author: str | None, after: str | None, before: str | None, top: int) -> None:
    """Search across indexed collections."""
    from local_rag.embeddings import OllamaConnectionError, get_embedding
    from local_rag.search import SearchFilters
    from local_rag.search import search as do_search

    config = load_config()
    conn = _get_db(config)
    try:
        try:
            query_embedding = get_embedding(query, config)
        except OllamaConnectionError as e:
            click.echo(f"Error: {e}", err=True)
            sys.exit(1)

        filters = SearchFilters(
            collection=collection,
            source_type=source_type,
            date_from=after,
            date_to=before,
            sender=sender,
            author=author,
        )

        results = do_search(conn, query_embedding, query, top, filters, config)

        if not results:
            click.echo("No results found.")
            return

        for i, r in enumerate(results, 1):
            click.echo(f"\n{'─' * 60}")
            click.echo(f"  [{i}] {r.title}")
            click.echo(f"  Collection: {r.collection}  |  Type: {r.source_type}  |  Score: {r.score:.4f}")
            click.echo(f"  Source: {r.source_path}")
            if r.metadata:
                meta_str = ", ".join(f"{k}={v}" for k, v in r.metadata.items() if k != "heading_path")
                if meta_str:
                    click.echo(f"  Meta: {meta_str}")

            # Show snippet (first 200 chars)
            snippet = r.content[:200].replace("\n", " ")
            if len(r.content) > 200:
                snippet += "..."
            click.echo(f"  {snippet}")

        click.echo(f"\n{'─' * 60}")
        click.echo(f"  {len(results)} result(s) found.")
    finally:
        conn.close()


# ── Collections commands ────────────────────────────────────────────────


@main.group()
def collections() -> None:
    """Manage collections."""


@collections.command("list")
def collections_list() -> None:
    """List all collections with document counts."""
    config = load_config()
    conn = _get_db(config)
    try:
        rows = conn.execute("""
            SELECT c.name, c.collection_type, c.created_at,
                   (SELECT COUNT(*) FROM sources s WHERE s.collection_id = c.id) as source_count,
                   (SELECT COUNT(*) FROM documents d WHERE d.collection_id = c.id) as chunk_count
            FROM collections c
            ORDER BY c.name
        """).fetchall()

        if not rows:
            click.echo("No collections found.")
            return

        click.echo(f"\n{'Name':<30} {'Type':<10} {'Sources':<10} {'Chunks':<10} {'Created'}")
        click.echo("─" * 80)
        for row in rows:
            click.echo(
                f"{row['name']:<30} {row['collection_type']:<10} "
                f"{row['source_count']:<10} {row['chunk_count']:<10} {row['created_at']}"
            )
    finally:
        conn.close()


@collections.command("info")
@click.argument("name")
def collections_info(name: str) -> None:
    """Show detailed info about a collection."""
    config = load_config()
    conn = _get_db(config)
    try:
        row = conn.execute(
            "SELECT * FROM collections WHERE name = ?", (name,)
        ).fetchone()
        if not row:
            click.echo(f"Error: Collection '{name}' not found.", err=True)
            sys.exit(1)

        coll_id = row["id"]

        doc_count = conn.execute(
            "SELECT COUNT(*) as cnt FROM documents WHERE collection_id = ?", (coll_id,)
        ).fetchone()["cnt"]

        source_count = conn.execute(
            "SELECT COUNT(*) as cnt FROM sources WHERE collection_id = ?", (coll_id,)
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
            "SELECT DISTINCT title FROM documents WHERE collection_id = ? LIMIT 5",
            (coll_id,),
        ).fetchall()

        click.echo(f"\nCollection: {name}")
        click.echo(f"  Type: {row['collection_type']}")
        click.echo(f"  Created: {row['created_at']}")
        click.echo(f"  Description: {row['description'] or '(none)'}")
        click.echo(f"  Sources: {source_count}")
        click.echo(f"  Documents (chunks): {doc_count}")
        click.echo(f"  Last indexed: {last_indexed or 'never'}")

        if type_breakdown:
            click.echo("  Source types:")
            for tb in type_breakdown:
                click.echo(f"    {tb['source_type']}: {tb['cnt']}")

        if sample_titles:
            click.echo("  Sample titles:")
            for st in sample_titles:
                click.echo(f"    - {st['title']}")
    finally:
        conn.close()


@collections.command("delete")
@click.argument("name")
@click.option("--yes", "-y", is_flag=True, help="Skip confirmation prompt.")
def collections_delete(name: str, yes: bool) -> None:
    """Delete a collection and all its data."""
    config = load_config()
    conn = _get_db(config)
    try:
        row = conn.execute(
            "SELECT id FROM collections WHERE name = ?", (name,)
        ).fetchone()
        if not row:
            click.echo(f"Error: Collection '{name}' not found.", err=True)
            sys.exit(1)

        if not yes:
            doc_count = conn.execute(
                "SELECT COUNT(*) as cnt FROM documents WHERE collection_id = ?",
                (row["id"],),
            ).fetchone()["cnt"]
            if not click.confirm(
                f"Delete collection '{name}' and all {doc_count} documents?"
            ):
                click.echo("Cancelled.")
                return

        coll_id = row["id"]

        # Delete vec_documents entries for documents in this collection
        conn.execute(
            "DELETE FROM vec_documents WHERE document_id IN (SELECT id FROM documents WHERE collection_id = ?)",
            (coll_id,),
        )
        # CASCADE will handle sources and documents
        conn.execute("DELETE FROM collections WHERE id = ?", (coll_id,))
        conn.commit()

        click.echo(f"Collection '{name}' deleted.")
    finally:
        conn.close()


# ── Status command ──────────────────────────────────────────────────────


@main.command()
def status() -> None:
    """Show overall RAG status and statistics."""
    config = load_config()

    if not config.db_path.exists():
        click.echo("Database not found. Run 'local-rag index' to get started.")
        return

    conn = _get_db(config)
    try:
        coll_count = conn.execute("SELECT COUNT(*) as cnt FROM collections").fetchone()["cnt"]
        doc_count = conn.execute("SELECT COUNT(*) as cnt FROM documents").fetchone()["cnt"]
        source_count = conn.execute("SELECT COUNT(*) as cnt FROM sources").fetchone()["cnt"]

        db_size_mb = config.db_path.stat().st_size / (1024 * 1024)

        last_indexed = conn.execute(
            "SELECT MAX(last_indexed_at) as ts FROM sources"
        ).fetchone()["ts"]

        click.echo(f"\nlocal-rag status")
        click.echo(f"  Database: {config.db_path}")
        click.echo(f"  Size: {db_size_mb:.1f} MB")
        click.echo(f"  Collections: {coll_count}")
        click.echo(f"  Sources: {source_count}")
        click.echo(f"  Documents (chunks): {doc_count}")
        click.echo(f"  Last indexed: {last_indexed or 'never'}")
        click.echo(f"  Embedding model: {config.embedding_model} ({config.embedding_dimensions}d)")
    finally:
        conn.close()


# ── Serve command ───────────────────────────────────────────────────────


@main.command()
@click.option("--port", type=int, default=None, help="Port for HTTP/SSE transport. If omitted, uses stdio.")
def serve(port: int | None) -> None:
    """Start the MCP server."""
    from local_rag.mcp_server import create_server

    server = create_server()

    if port:
        click.echo(f"Starting MCP server on port {port}...")
        server.run(transport="sse", port=port)
    else:
        server.run(transport="stdio")
