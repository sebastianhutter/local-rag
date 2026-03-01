"""NetNewsWire RSS indexer.

Indexes RSS articles from NetNewsWire's SQLite databases into the "rss"
system collection. Discovers account directories automatically and opens
all databases in read-only mode with retry logic for handling lock contention.
"""

import json
import logging
import sqlite3
import time
from collections.abc import Callable
from datetime import datetime
from pathlib import Path

from local_rag.chunker import chunk_email
from local_rag.config import Config
from local_rag.db import get_or_create_collection
from local_rag.embeddings import get_embeddings, serialize_float32
from local_rag.indexers.base import BaseIndexer, IndexResult
from local_rag.parsers.rss import Article, find_account_dirs, parse_articles

logger = logging.getLogger(__name__)

MAX_LOCK_RETRIES = 3
LOCK_RETRY_DELAY = 2.0  # seconds


class RSSIndexer(BaseIndexer):
    """Indexes RSS articles from NetNewsWire into the RAG database."""

    def __init__(self, db_path: str | None = None):
        """Initialize the RSS indexer.

        Args:
            db_path: Optional explicit path to the NetNewsWire Accounts
                directory or a specific account directory. If not provided,
                will be determined from config.
        """
        self._explicit_db_path = db_path

    def index(
        self,
        conn: sqlite3.Connection,
        config: Config,
        force: bool = False,
        progress_callback: Callable[[int, int, str], None] | None = None,
    ) -> IndexResult:
        """Index RSS articles from NetNewsWire into the "rss" collection.

        Discovers all account directories and indexes articles from each.

        Args:
            conn: SQLite connection to the RAG database.
            config: Application configuration.
            force: If True, re-index all articles regardless of prior indexing.
            progress_callback: Optional callback invoked per article with
                (current, total, article_title).

        Returns:
            IndexResult summarizing what was done.
        """
        result = IndexResult()

        # Locate NetNewsWire account directories
        if self._explicit_db_path:
            explicit_path = Path(self._explicit_db_path).expanduser()
            if (explicit_path / "DB.sqlite3").is_file():
                account_dirs = [explicit_path]
            else:
                account_dirs = find_account_dirs(explicit_path)

            if not account_dirs:
                msg = f"No NetNewsWire databases found at {explicit_path}"
                logger.error(msg)
                result.errors = 1
                result.error_messages.append(msg)
                return result
        else:
            account_dirs = find_account_dirs(config.netnewswire_db_path)
            if not account_dirs:
                msg = (
                    f"Cannot find NetNewsWire account directories. "
                    f"Checked: {config.netnewswire_db_path}. "
                    "Set netnewswire_db_path in config or pass the path explicitly."
                )
                logger.error(msg)
                result.errors = 1
                result.error_messages.append(msg)
                return result

        logger.info("Found %d NetNewsWire account(s)", len(account_dirs))

        # Get/create the rss system collection
        collection_id = get_or_create_collection(
            conn, "rss", "system", "RSS articles from NetNewsWire"
        )

        # Determine watermark for incremental indexing
        since_ts = None
        if not force:
            since_ts = self._get_watermark(conn, collection_id)
            if since_ts:
                logger.info("Incremental index: fetching articles since ts=%s", since_ts)

        latest_ts = since_ts or 0.0

        # Index each account
        for account_dir in account_dirs:
            logger.info("Indexing account: %s", account_dir.name)
            account_result, account_latest = self._index_account(
                conn, config, collection_id, account_dir, since_ts, force,
                progress_callback,
            )

            result.total_found += account_result.total_found
            result.indexed += account_result.indexed
            result.skipped += account_result.skipped
            result.errors += account_result.errors
            result.error_messages.extend(account_result.error_messages)

            if account_latest > latest_ts:
                latest_ts = account_latest

        # Update watermark
        if latest_ts > 0:
            self._set_watermark(conn, collection_id, latest_ts)

        logger.info("RSS indexing complete: %s", result)
        return result

    def _index_account(
        self,
        conn: sqlite3.Connection,
        config: Config,
        collection_id: int,
        account_dir: Path,
        since_ts: float | None,
        force: bool,
        progress_callback: Callable[[int, int, str], None] | None = None,
    ) -> tuple[IndexResult, float]:
        """Index articles from a single account directory.

        Returns:
            Tuple of (IndexResult, latest_timestamp).
        """
        result = IndexResult()
        latest_ts = 0.0

        articles = self._parse_with_retry(account_dir, since_ts)
        if articles is None:
            msg = f"Failed to open NetNewsWire database in {account_dir.name} after retries"
            logger.error(msg)
            result.errors = 1
            result.error_messages.append(msg)
            return result, latest_ts

        total_articles = len(articles)
        logger.info("Found %d articles to process in %s", total_articles, account_dir.name)

        for article in articles:
            result.total_found += 1
            if progress_callback:
                progress_callback(
                    result.total_found,
                    total_articles,
                    article.title or "(no title)",
                )

            # Skip if already indexed (unless force)
            if not force and self._is_indexed(conn, collection_id, article.article_id):
                result.skipped += 1
                continue

            try:
                chunks_count = self._index_article(conn, config, collection_id, article)
                result.indexed += 1

                logger.info(
                    "Indexed article [%d/%d]: %s (%d chunks)",
                    result.total_found,
                    total_articles,
                    (article.title or "(no title)")[:60],
                    chunks_count,
                )

                # Track latest timestamp for watermark
                if article.date_published_ts > latest_ts:
                    latest_ts = article.date_published_ts

            except Exception as e:
                result.errors += 1
                msg = f"Error indexing article {article.article_id}: {e}"
                if result.errors <= 10:
                    logger.warning(msg)
                    result.error_messages.append(msg)
                elif result.errors == 11:
                    logger.warning("Suppressing further indexing errors...")

            # Periodic progress summary
            if result.total_found % 500 == 0:
                logger.info(
                    "Progress: %d/%d processed (%d indexed, %d skipped, %d errors)",
                    result.total_found,
                    total_articles,
                    result.indexed,
                    result.skipped,
                    result.errors,
                )

        logger.info(
            "Account %s complete: %d found, %d indexed, %d skipped, %d errors",
            account_dir.name,
            result.total_found,
            result.indexed,
            result.skipped,
            result.errors,
        )

        return result, latest_ts

    def _parse_with_retry(
        self, account_dir: Path, since_ts: float | None
    ) -> list[Article] | None:
        """Try to parse articles with retry on database lock."""
        for attempt in range(1, MAX_LOCK_RETRIES + 1):
            try:
                articles = list(parse_articles(account_dir, since_ts))
                return articles
            except sqlite3.OperationalError as e:
                if "locked" in str(e).lower() or "busy" in str(e).lower():
                    if attempt < MAX_LOCK_RETRIES:
                        logger.warning(
                            "NetNewsWire database is locked (attempt %d/%d), "
                            "retrying in %ds...",
                            attempt,
                            MAX_LOCK_RETRIES,
                            LOCK_RETRY_DELAY,
                        )
                        time.sleep(LOCK_RETRY_DELAY)
                    else:
                        logger.error(
                            "NetNewsWire database is locked after %d attempts. "
                            "Try closing NetNewsWire and running again.",
                            MAX_LOCK_RETRIES,
                        )
                        return None
                else:
                    logger.error("Database error: %s", e)
                    return None

        return None

    def _is_indexed(
        self, conn: sqlite3.Connection, collection_id: int, article_id: str
    ) -> bool:
        """Check if an article is already indexed."""
        row = conn.execute(
            "SELECT id FROM sources WHERE collection_id = ? AND source_path = ?",
            (collection_id, article_id),
        ).fetchone()
        return row is not None

    def _index_article(
        self,
        conn: sqlite3.Connection,
        config: Config,
        collection_id: int,
        article: Article,
    ) -> int:
        """Index a single article: chunk, embed, and insert.

        Returns:
            Number of chunks indexed.
        """
        # Reuse the email chunker — RSS articles have a similar structure
        # (title + body text, usually short-to-medium length)
        chunks = chunk_email(
            subject=article.title,
            body=article.body_text,
            chunk_size=config.chunk_size_tokens,
            overlap=config.chunk_overlap_tokens,
        )

        if not chunks:
            return 0

        # Embed all chunks
        chunk_texts = [c.text for c in chunks]
        embeddings = get_embeddings(chunk_texts, config)

        # Build metadata
        metadata: dict = {
            "url": article.url,
            "feed_name": article.feed_name,
            "date": article.date_published,
        }
        if article.feed_category:
            metadata["feed_category"] = article.feed_category
        if article.authors:
            metadata["authors"] = article.authors

        metadata_json = json.dumps(metadata)

        now = datetime.now().isoformat()

        # Delete existing source if re-indexing (force mode)
        conn.execute(
            "DELETE FROM sources WHERE collection_id = ? AND source_path = ?",
            (collection_id, article.article_id),
        )

        # Insert source
        cursor = conn.execute(
            """
            INSERT INTO sources (collection_id, source_type, source_path, last_indexed_at)
            VALUES (?, 'rss', ?, ?)
            """,
            (collection_id, article.article_id, now),
        )
        source_id = cursor.lastrowid

        # Insert documents and vectors
        for chunk, embedding in zip(chunks, embeddings):
            doc_cursor = conn.execute(
                """
                INSERT INTO documents (source_id, collection_id, chunk_index,
                                       title, content, metadata)
                VALUES (?, ?, ?, ?, ?, ?)
                """,
                (
                    source_id,
                    collection_id,
                    chunk.chunk_index,
                    article.title or "(no title)",
                    chunk.text,
                    metadata_json,
                ),
            )
            doc_id = doc_cursor.lastrowid

            conn.execute(
                "INSERT INTO vec_documents (rowid, embedding, document_id) VALUES (?, ?, ?)",
                (doc_id, serialize_float32(embedding), doc_id),
            )

        conn.commit()
        return len(chunks)

    def _get_watermark(
        self, conn: sqlite3.Connection, collection_id: int
    ) -> float | None:
        """Get the latest indexed article timestamp for incremental updates."""
        row = conn.execute(
            """
            SELECT MAX(json_extract(d.metadata, '$.date')) as latest_date
            FROM documents d
            WHERE d.collection_id = ?
            """,
            (collection_id,),
        ).fetchone()

        if row and row["latest_date"]:
            # Convert ISO date back to timestamp for comparison
            try:
                dt = datetime.fromisoformat(row["latest_date"])
                return dt.timestamp()
            except (ValueError, OSError):
                return None
        return None

    def _set_watermark(
        self, conn: sqlite3.Connection, collection_id: int, ts: float
    ) -> None:
        """Store the watermark timestamp in the collection description."""
        from local_rag.parsers.rss import _ts_to_iso

        date_str = _ts_to_iso(ts)
        conn.execute(
            "UPDATE collections SET description = ? WHERE id = ?",
            (f"RSS articles from NetNewsWire (indexed through {date_str})", collection_id),
        )
        conn.commit()
