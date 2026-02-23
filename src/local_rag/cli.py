"""Click CLI entry point for local-rag."""

import contextlib
import json
import logging
import signal
import sys
from collections.abc import Callable, Generator
from pathlib import Path

import click
from rich.console import Console
from rich.progress import BarColumn, MofNCompleteColumn, Progress, TextColumn
from rich.table import Table
from rich.text import Text

from local_rag.config import load_config

console = Console()


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


def _print_index_result(label: str, result) -> None:
    """Print a colored index result summary."""
    parts = [
        f"[bold]{label}[/bold] indexing complete:",
        f"  [green]{result.indexed} indexed[/green],",
        f"  [dim]{result.skipped} skipped[/dim],",
    ]
    error_style = "red" if result.errors > 0 else "dim"
    parts.append(f"  [{error_style}]{result.errors} errors[/{error_style}]")
    parts.append(f"  [dim](out of {result.total_found} found)[/dim]")
    console.print(" ".join(parts))


def _check_collection_enabled(config, name: str) -> None:
    """Exit with a message if the collection is disabled in config."""
    if not config.is_collection_enabled(name):
        click.echo(
            f"Collection '{name}' is disabled in config (disabled_collections). "
            "Remove it from disabled_collections to re-enable.",
            err=True,
        )
        sys.exit(1)


@contextlib.contextmanager
def _file_progress(label: str) -> Generator[Callable[[int, int, Path], None], None, None]:
    """Context manager that yields a progress callback for file indexing."""
    progress = Progress(
        TextColumn("[bold]{task.description}"),
        BarColumn(),
        MofNCompleteColumn(),
        TextColumn("[dim]{task.fields[filename]}"),
        console=console,
        transient=True,
    )
    with progress:
        task_id = progress.add_task(label, total=None, filename="")

        def callback(current: int, total: int, file_path: Path) -> None:
            progress.update(task_id, total=total, completed=current - 1, filename=file_path.name)

        yield callback
        # Ensure bar shows completion
        progress.update(task_id, completed=progress.tasks[task_id].total)


@index.command("obsidian")
@click.option("--vault", "-v", "vaults", multiple=True, type=click.Path(exists=True, path_type=Path),
              help="Vault path(s). If omitted, uses config.")
@click.option("--force", is_flag=True, help="Force re-index all files.")
def index_obsidian(vaults: tuple[Path, ...], force: bool) -> None:
    """Index Obsidian vault(s)."""
    from local_rag.indexers.obsidian import ObsidianIndexer

    config = load_config()
    _check_collection_enabled(config, "obsidian")
    vault_paths = list(vaults) if vaults else config.obsidian_vaults

    if not vault_paths:
        click.echo("Error: No vault paths provided. Use --vault or set obsidian_vaults in config.", err=True)
        sys.exit(1)

    conn = _get_db(config)
    try:
        indexer = ObsidianIndexer(vault_paths, config.obsidian_exclude_folders)
        result = indexer.index(conn, config, force=force)
        _print_index_result("Obsidian", result)
    finally:
        conn.close()


@index.command("email")
@click.option("--force", is_flag=True, help="Force re-index all emails.")
def index_email(force: bool) -> None:
    """Index eM Client emails."""
    from local_rag.indexers.email_indexer import EmailIndexer

    config = load_config()
    _check_collection_enabled(config, "email")
    conn = _get_db(config)
    try:
        indexer = EmailIndexer(str(config.emclient_db_path))
        result = indexer.index(conn, config, force=force)
        _print_index_result("Email", result)
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
    _check_collection_enabled(config, "calibre")
    library_paths = list(libraries) if libraries else config.calibre_libraries

    if not library_paths:
        click.echo("Error: No library paths provided. Use --library or set calibre_libraries in config.", err=True)
        sys.exit(1)

    conn = _get_db(config)
    try:
        indexer = CalibreIndexer(library_paths)
        result = indexer.index(conn, config, force=force)
        _print_index_result("Calibre", result)
    finally:
        conn.close()


@index.command("rss")
@click.option("--force", is_flag=True, help="Force re-index all articles.")
def index_rss(force: bool) -> None:
    """Index NetNewsWire RSS articles."""
    from local_rag.indexers.rss_indexer import RSSIndexer

    config = load_config()
    _check_collection_enabled(config, "rss")
    conn = _get_db(config)
    try:
        indexer = RSSIndexer(str(config.netnewswire_db_path))
        result = indexer.index(conn, config, force=force)
        _print_index_result("RSS", result)
    finally:
        conn.close()


@index.command("project")
@click.argument("name")
@click.argument("paths", nargs=-1, required=False, type=click.Path(exists=True, path_type=Path))
@click.option("--force", is_flag=True, help="Force re-index all files.")
def index_project(name: str, paths: tuple[Path, ...], force: bool) -> None:
    """Index documents into a named project collection.

    If PATHS are given, they are indexed and saved for future runs.
    If omitted, re-indexes using the paths stored from a previous run.
    """
    from local_rag.indexers.project import ProjectIndexer

    config = load_config()
    _check_collection_enabled(config, name)
    conn = _get_db(config)
    try:
        if paths:
            project_paths = list(paths)
        else:
            # Look up stored paths from the database
            row = conn.execute(
                "SELECT paths FROM collections WHERE name = ? AND collection_type = 'project'",
                (name,),
            ).fetchone()
            if not row or not row["paths"]:
                click.echo(
                    f"Error: No paths provided and no stored paths found for project '{name}'.\n"
                    f"Usage: local-rag index project \"{name}\" /path/to/docs",
                    err=True,
                )
                sys.exit(1)
            project_paths = [Path(p) for p in json.loads(row["paths"])]

        indexer = ProjectIndexer(name, project_paths)
        with _file_progress(name) as progress_cb:
            result = indexer.index(conn, config, force=force, progress_callback=progress_cb)
        _print_index_result(name, result)
    finally:
        conn.close()


@index.command("group")
@click.argument("name", required=False)
@click.option("--force", is_flag=True, help="Force re-index all files.")
@click.option("--history", is_flag=True, help="Also index commit history (last N months).")
def index_group(name: str | None, force: bool, history: bool) -> None:
    """Index code group(s) from config.

    If NAME is given, indexes only that group's repos. If omitted, indexes
    all groups defined in code_groups config.
    """
    from local_rag.indexers.git_indexer import GitRepoIndexer

    config = load_config()

    if name:
        if name not in config.code_groups:
            click.echo(f"Error: Group '{name}' not found in code_groups config.", err=True)
            sys.exit(1)
        groups = {name: config.code_groups[name]}
    elif config.code_groups:
        groups = config.code_groups
    else:
        click.echo("Error: No code_groups configured in ~/.local-rag/config.json.", err=True)
        sys.exit(1)

    conn = _get_db(config)
    try:
        for group_name, repo_paths in groups.items():
            _check_collection_enabled(config, group_name)
            for repo_path in repo_paths:
                click.echo(f"  {group_name}: {repo_path}")
                indexer = GitRepoIndexer(repo_path, collection_name=group_name)
                result = indexer.index(conn, config, force=force, index_history=history)
                _print_index_result(f"{group_name}/{repo_path.name}", result)
    finally:
        conn.close()


@index.command("all")
@click.option("--force", is_flag=True, help="Force re-index all sources.")
def index_all(force: bool) -> None:
    """Index all configured sources at once.

    Indexes obsidian, email, calibre, rss, code groups, and project
    collections. System sources and code groups come from
    ~/.local-rag/config.json. Project collections are discovered from
    the database (paths are saved when you first run 'index project').
    """
    from local_rag.indexers.calibre_indexer import CalibreIndexer
    from local_rag.indexers.email_indexer import EmailIndexer
    from local_rag.indexers.git_indexer import GitRepoIndexer
    from local_rag.indexers.obsidian import ObsidianIndexer
    from local_rag.indexers.project import ProjectIndexer
    from local_rag.indexers.rss_indexer import RSSIndexer

    config = load_config()
    conn = _get_db(config)

    sources: list[tuple[str, object]] = []

    if config.is_collection_enabled("obsidian") and config.obsidian_vaults:
        sources.append(("obsidian", ObsidianIndexer(config.obsidian_vaults, config.obsidian_exclude_folders)))

    if config.is_collection_enabled("email") and config.emclient_db_path and config.emclient_db_path.exists():
        sources.append(("email", EmailIndexer(str(config.emclient_db_path))))

    if config.is_collection_enabled("calibre") and config.calibre_libraries:
        sources.append(("calibre", CalibreIndexer(config.calibre_libraries)))

    if config.is_collection_enabled("rss") and config.netnewswire_db_path and config.netnewswire_db_path.exists():
        sources.append(("rss", RSSIndexer(str(config.netnewswire_db_path))))

    git_indexers: list[str] = []
    for group_name, repo_paths in config.code_groups.items():
        if config.is_collection_enabled(group_name):
            for repo_path in repo_paths:
                label = f"{group_name}/{repo_path.name}"
                sources.append((label, GitRepoIndexer(repo_path, collection_name=group_name)))
                git_indexers.append(label)

    # Load project collections from the database
    project_labels: list[str] = []
    project_rows = conn.execute(
        "SELECT name, paths FROM collections WHERE collection_type = 'project' AND paths IS NOT NULL"
    ).fetchall()
    for row in project_rows:
        proj_name = row["name"]
        if config.is_collection_enabled(proj_name):
            proj_paths = [Path(p) for p in json.loads(row["paths"])]
            sources.append((proj_name, ProjectIndexer(proj_name, proj_paths)))
            project_labels.append(proj_name)

    if not sources:
        click.echo("No sources configured. Set paths in ~/.local-rag/config.json.", err=True)
        sys.exit(1)

    click.echo(f"Indexing {len(sources)} source(s)...\n")

    summary_rows: list[tuple[str, int, int, int, int, str | None]] = []

    try:
        for label, indexer in sources:
            try:
                if label in git_indexers:
                    click.echo(f"  {label}...")
                    result = indexer.index(conn, config, force=force, index_history=True)
                elif label in project_labels:
                    with _file_progress(f"  {label}") as progress_cb:
                        result = indexer.index(conn, config, force=force, progress_callback=progress_cb)
                else:
                    click.echo(f"  {label}...")
                    result = indexer.index(conn, config, force=force)
                summary_rows.append((label, result.indexed, result.skipped, result.errors, result.total_found, None))
            except Exception as e:
                summary_rows.append((label, 0, 0, 0, 0, str(e)))
    finally:
        conn.close()

    table = Table(title="Indexing Summary")
    table.add_column("Collection", style="bold")
    table.add_column("Indexed", justify="right", style="green")
    table.add_column("Skipped", justify="right", style="dim")
    table.add_column("Errors", justify="right")
    table.add_column("Total", justify="right")

    for label, indexed, skipped, errors, total, error_msg in summary_rows:
        error_style = "red" if errors > 0 else "dim"
        if error_msg:
            table.add_row(label, "-", "-", "[red]failed[/red]", "-")
        else:
            table.add_row(
                label,
                str(indexed),
                str(skipped),
                Text(str(errors), style=error_style),
                str(total),
            )

    console.print(table)


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
    from local_rag.embeddings import OllamaConnectionError
    from local_rag.search import perform_search

    try:
        results = perform_search(
            query=query,
            collection=collection,
            top_k=top,
            source_type=source_type,
            date_from=after,
            date_to=before,
            sender=sender,
            author=author,
        )
    except OllamaConnectionError as e:
        click.echo(f"Error: {e}", err=True)
        sys.exit(1)

    if not results:
        click.echo("No results found.")
        return

    for i, r in enumerate(results, 1):
        # Color-code score
        score = r.score
        if score >= 0.7:
            score_text = Text(f"{score:.4f}", style="green")
        elif score >= 0.4:
            score_text = Text(f"{score:.4f}", style="yellow")
        else:
            score_text = Text(f"{score:.4f}", style="red")

        console.print()
        console.rule(f"[bold]\\[{i}] {r.title}[/bold]", style="dim")

        meta_table = Table(show_header=False, box=None, padding=(0, 2))
        meta_table.add_column("Key", style="bold")
        meta_table.add_column("Value")
        meta_table.add_row("Collection", r.collection)
        meta_table.add_row("Type", r.source_type)
        meta_table.add_row("Score", score_text)
        meta_table.add_row("Source", r.source_path)

        if r.metadata:
            meta_items = {k: v for k, v in r.metadata.items() if k != "heading_path"}
            if meta_items:
                meta_str = ", ".join(f"{k}={v}" for k, v in meta_items.items())
                meta_table.add_row("Meta", meta_str)

        console.print(meta_table)

        # Show snippet (first 300 chars)
        snippet = r.content[:300].replace("\n", " ")
        if len(r.content) > 300:
            snippet += "..."
        console.print(f"  [dim]{snippet}[/dim]")

    console.print()
    console.print(f"[bold]{len(results)}[/bold] result(s) found.")


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

        table = Table(title="Collections")
        table.add_column("Name", style="bold")
        table.add_column("Type", style="dim")
        table.add_column("Sources", justify="right")
        table.add_column("Chunks", justify="right")
        table.add_column("Created", style="dim")

        for row in rows:
            table.add_row(
                row["name"],
                row["collection_type"],
                str(row["source_count"]),
                str(row["chunk_count"]),
                row["created_at"],
            )

        console.print(table)
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

        info = Table(show_header=False, box=None, padding=(0, 2))
        info.add_column("Key", style="bold")
        info.add_column("Value")
        info.add_row("Collection", name)
        info.add_row("Type", row["collection_type"])
        info.add_row("Created", row["created_at"])
        info.add_row("Description", row["description"] or "(none)")
        info.add_row("Sources", str(source_count))
        info.add_row("Chunks", str(doc_count))
        info.add_row("Last indexed", last_indexed or "never")
        console.print(info)

        if type_breakdown:
            console.print()
            types_table = Table(title="Source Types")
            types_table.add_column("Type")
            types_table.add_column("Count", justify="right")
            for tb in type_breakdown:
                types_table.add_row(tb["source_type"], str(tb["cnt"]))
            console.print(types_table)

        if sample_titles:
            console.print()
            console.print("[bold]Sample titles[/bold]")
            for st in sample_titles:
                console.print(f"  - {st['title']}")
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

        table = Table(title="local-rag status", show_header=False, box=None, padding=(0, 2))
        table.add_column("Key", style="bold")
        table.add_column("Value")
        table.add_row("Database", str(config.db_path))
        table.add_row("Size", f"{db_size_mb:.1f} MB")
        table.add_row("Collections", str(coll_count))
        table.add_row("Sources", str(source_count))
        table.add_row("Chunks", str(doc_count))
        table.add_row("Last indexed", last_indexed or "never")
        table.add_row("Embedding model", f"{config.embedding_model} ({config.embedding_dimensions}d)")
        console.print(table)
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
