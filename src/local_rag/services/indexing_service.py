"""Indexing service for local-rag.

Thread-safe wrapper around individual indexers. Creates its own database
connections per invocation.
"""

import json
import logging
import threading
from collections.abc import Callable
from pathlib import Path

from local_rag.config import Config
from local_rag.indexers.base import IndexResult

logger = logging.getLogger(__name__)


class IndexingService:
    """Service for running indexing operations."""

    def index_collection(
        self,
        collection_name: str,
        config: Config,
        force: bool = False,
        progress_callback: Callable[[int, int, str], None] | None = None,
        cancel_event: threading.Event | None = None,
    ) -> IndexResult:
        """Index a single collection by name.

        Creates its own database connection for thread safety.

        Args:
            collection_name: Name of the collection to index.
            config: Application configuration.
            force: If True, re-index everything regardless of change detection.
            progress_callback: Optional callback (current, total, item_name).
            cancel_event: Optional event to signal cancellation.

        Returns:
            IndexResult summarizing the indexing run.
        """
        from local_rag.db import get_connection, init_db

        if not config.is_collection_enabled(collection_name):
            logger.warning("Collection '%s' is disabled, skipping", collection_name)
            return IndexResult()

        conn = get_connection(config)
        init_db(conn, config)

        try:
            return self._run_indexer(
                collection_name, conn, config, force, progress_callback
            )
        finally:
            conn.close()

    def index_all(
        self,
        config: Config,
        force: bool = False,
        progress_callback: Callable[[int, int, str], None] | None = None,
        cancel_event: threading.Event | None = None,
    ) -> dict[str, IndexResult]:
        """Index all enabled collections.

        Creates its own database connection for thread safety.

        Args:
            config: Application configuration.
            force: If True, re-index everything.
            progress_callback: Optional callback (current, total, item_name).
            cancel_event: Optional event to signal cancellation.

        Returns:
            Dict mapping collection name to IndexResult.
        """
        from local_rag.db import get_connection, init_db

        conn = get_connection(config)
        init_db(conn, config)

        results: dict[str, IndexResult] = {}

        try:
            # Gather all collections to index
            collections = self._get_indexable_collections(config, conn)

            for i, name in enumerate(collections, 1):
                if cancel_event and cancel_event.is_set():
                    logger.info("Indexing cancelled")
                    break

                if not config.is_collection_enabled(name):
                    continue

                logger.info("Indexing collection %d/%d: %s", i, len(collections), name)
                try:
                    results[name] = self._run_indexer(
                        name, conn, config, force, progress_callback
                    )
                except Exception as e:
                    logger.error("Error indexing '%s': %s", name, e)
                    results[name] = IndexResult(
                        errors=1, error_messages=[str(e)]
                    )
        finally:
            conn.close()

        return results

    def _get_indexable_collections(self, config: Config, conn) -> list[str]:
        """Build the list of collections to index.

        Args:
            config: Application configuration.
            conn: SQLite connection.

        Returns:
            List of collection names.
        """
        collections: list[str] = []

        if config.obsidian_vaults:
            collections.append("obsidian")
        if config.emclient_db_path and config.emclient_db_path.exists():
            collections.append("email")
        if config.calibre_libraries:
            collections.append("calibre")
        if config.netnewswire_db_path and config.netnewswire_db_path.exists():
            collections.append("rss")

        # Code groups: each group becomes a collection entry
        for group_name in config.code_groups:
            collections.append(group_name)

        # Project collections from DB
        project_rows = conn.execute(
            "SELECT name FROM collections WHERE collection_type = 'project' AND paths IS NOT NULL"
        ).fetchall()
        for row in project_rows:
            collections.append(row["name"])

        return collections

    def _run_indexer(
        self,
        collection_name: str,
        conn,
        config: Config,
        force: bool,
        progress_callback: Callable[[int, int, str], None] | None,
    ) -> IndexResult:
        """Dispatch to the correct indexer for a collection.

        Args:
            collection_name: Collection to index.
            conn: SQLite connection.
            config: Application configuration.
            force: Re-index everything.
            progress_callback: Optional progress callback.

        Returns:
            IndexResult from the indexer.
        """
        if collection_name == "obsidian":
            from local_rag.indexers.obsidian import ObsidianIndexer

            indexer = ObsidianIndexer(config.obsidian_vaults, config.obsidian_exclude_folders)
            return indexer.index(conn, config, force=force, progress_callback=progress_callback)

        if collection_name == "email":
            from local_rag.indexers.email_indexer import EmailIndexer

            indexer = EmailIndexer(str(config.emclient_db_path))
            return indexer.index(conn, config, force=force, progress_callback=progress_callback)

        if collection_name == "calibre":
            from local_rag.indexers.calibre_indexer import CalibreIndexer

            indexer = CalibreIndexer(config.calibre_libraries)
            return indexer.index(conn, config, force=force, progress_callback=progress_callback)

        if collection_name == "rss":
            from local_rag.indexers.rss_indexer import RSSIndexer

            indexer = RSSIndexer(str(config.netnewswire_db_path))
            return indexer.index(conn, config, force=force, progress_callback=progress_callback)

        # Code groups
        if collection_name in config.code_groups:
            from local_rag.indexers.git_indexer import GitRepoIndexer

            total_result = IndexResult()
            for repo_path in config.code_groups[collection_name]:
                idx = GitRepoIndexer(repo_path, collection_name=collection_name)
                r = idx.index(
                    conn, config, force=force, index_history=True,
                    progress_callback=progress_callback,
                )
                total_result.indexed += r.indexed
                total_result.skipped += r.skipped
                total_result.errors += r.errors
                total_result.total_found += r.total_found
                total_result.error_messages.extend(r.error_messages)
            return total_result

        # Project collections: look up paths from DB
        row = conn.execute(
            "SELECT paths FROM collections WHERE name = ? AND collection_type = 'project'",
            (collection_name,),
        ).fetchone()
        if row and row["paths"]:
            from local_rag.indexers.project import ProjectIndexer

            proj_paths = [Path(p) for p in json.loads(row["paths"])]
            indexer = ProjectIndexer(collection_name, proj_paths)
            return indexer.index(
                conn, config, force=force, progress_callback=progress_callback
            )

        logger.warning("Unknown collection: %s", collection_name)
        return IndexResult(errors=1, error_messages=[f"Unknown collection: {collection_name}"])
