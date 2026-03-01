"""Email indexer for eM Client.

Indexes emails from the eM Client SQLite databases into the "email" system
collection. Discovers account directories automatically and opens all
databases in read-only mode with retry logic for handling lock contention.
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
from local_rag.parsers.email import EmailMessage, find_account_dirs, parse_emails

logger = logging.getLogger(__name__)

MAX_LOCK_RETRIES = 3
LOCK_RETRY_DELAY = 2.0  # seconds


def _find_account_dirs(config: Config) -> list[Path]:
    """Locate eM Client account directories.

    Args:
        config: Application configuration.

    Returns:
        List of account directories containing mail databases.
    """
    base = config.emclient_db_path

    # If the configured path points directly to an account dir
    # (contains mail_index.dat), use it as-is
    if (base / "mail_index.dat").is_file():
        return [base]

    return find_account_dirs(base)


class EmailIndexer(BaseIndexer):
    """Indexes emails from eM Client into the RAG database."""

    def __init__(self, db_path: str | None = None):
        """Initialize the email indexer.

        Args:
            db_path: Optional explicit path to the eM Client data directory
                or a specific account directory. If not provided, will be
                determined from config.
        """
        self._explicit_db_path = db_path

    def index(
        self,
        conn: sqlite3.Connection,
        config: Config,
        force: bool = False,
        progress_callback: Callable[[int, int, str], None] | None = None,
    ) -> IndexResult:
        """Index emails from eM Client into the "email" collection.

        Discovers all account directories and indexes emails from each.

        Args:
            conn: SQLite connection to the RAG database.
            config: Application configuration.
            force: If True, re-index all emails regardless of prior indexing.
            progress_callback: Optional callback invoked per email with
                (current, total, email_subject).

        Returns:
            IndexResult summarizing what was done.
        """
        result = IndexResult()

        # Locate eM Client account directories
        if self._explicit_db_path:
            explicit_path = Path(self._explicit_db_path).expanduser()
            if (explicit_path / "mail_index.dat").is_file():
                account_dirs = [explicit_path]
            else:
                account_dirs = find_account_dirs(explicit_path)

            if not account_dirs:
                msg = f"No eM Client mail databases found at {explicit_path}"
                logger.error(msg)
                result.errors = 1
                result.error_messages.append(msg)
                return result
        else:
            account_dirs = _find_account_dirs(config)
            if not account_dirs:
                msg = (
                    f"Cannot find eM Client account directories. "
                    f"Checked: {config.emclient_db_path}. "
                    "Set emclient_db_path in config or pass the path explicitly."
                )
                logger.error(msg)
                result.errors = 1
                result.error_messages.append(msg)
                return result

        logger.info("Found %d eM Client account(s)", len(account_dirs))

        # Get/create the email system collection
        collection_id = get_or_create_collection(
            conn, "email", "system", "Emails from eM Client"
        )

        # Determine watermark for incremental indexing
        since_date = None
        if not force:
            since_date = self._get_watermark(conn, collection_id)
            if since_date:
                logger.info("Incremental index: fetching emails since %s", since_date)

        latest_date = since_date or ""

        # Index each account
        for account_dir in account_dirs:
            logger.info("Indexing account: %s", account_dir.name)
            account_result = self._index_account(
                conn, config, collection_id, account_dir, since_date, force,
                progress_callback,
            )

            result.total_found += account_result.total_found
            result.indexed += account_result.indexed
            result.skipped += account_result.skipped
            result.errors += account_result.errors
            result.error_messages.extend(account_result.error_messages)

            if account_result.latest_date and account_result.latest_date > latest_date:
                latest_date = account_result.latest_date

        # Update watermark
        if latest_date:
            self._set_watermark(conn, collection_id, latest_date)

        logger.info("Email indexing complete: %s", result)
        return result

    def _index_account(
        self,
        conn: sqlite3.Connection,
        config: Config,
        collection_id: int,
        account_dir: Path,
        since_date: str | None,
        force: bool,
        progress_callback: Callable[[int, int, str], None] | None = None,
    ) -> "AccountIndexResult":
        """Index emails from a single account directory."""
        result = AccountIndexResult()

        emails = self._parse_with_retry(account_dir, since_date)
        if emails is None:
            msg = f"Failed to open eM Client database in {account_dir.name} after retries"
            logger.error(msg)
            result.errors = 1
            result.error_messages.append(msg)
            return result

        total_emails = len(emails)
        logger.info("Found %d emails to process in %s", total_emails, account_dir.name)

        for email_msg in emails:
            result.total_found += 1
            if progress_callback:
                progress_callback(
                    result.total_found,
                    total_emails,
                    email_msg.subject or "(no subject)",
                )

            # Skip if already indexed (unless force)
            if not force and self._is_indexed(conn, collection_id, email_msg.message_id):
                result.skipped += 1
                continue

            try:
                chunks_count = self._index_email(conn, config, collection_id, email_msg)
                result.indexed += 1

                logger.info(
                    "Indexed email [%d/%d]: %s (%d chunks)",
                    result.total_found,
                    total_emails,
                    (email_msg.subject or "(no subject)")[:60],
                    chunks_count,
                )

                # Track latest date for watermark
                if email_msg.date and email_msg.date > result.latest_date:
                    result.latest_date = email_msg.date

            except Exception as e:
                result.errors += 1
                msg = f"Error indexing email {email_msg.message_id}: {e}"
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
                    total_emails,
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

        return result

    def _parse_with_retry(
        self, account_dir: Path, since_date: str | None
    ) -> list[EmailMessage] | None:
        """Try to parse emails with retry on database lock.

        Returns a list of emails, or None if all retries failed.
        """
        for attempt in range(1, MAX_LOCK_RETRIES + 1):
            try:
                emails = list(parse_emails(account_dir, since_date))
                return emails
            except sqlite3.OperationalError as e:
                if "locked" in str(e).lower() or "busy" in str(e).lower():
                    if attempt < MAX_LOCK_RETRIES:
                        logger.warning(
                            "eM Client database is locked (attempt %d/%d), "
                            "retrying in %ds...",
                            attempt,
                            MAX_LOCK_RETRIES,
                            LOCK_RETRY_DELAY,
                        )
                        time.sleep(LOCK_RETRY_DELAY)
                    else:
                        logger.error(
                            "eM Client database is locked after %d attempts. "
                            "Try closing eM Client and running again.",
                            MAX_LOCK_RETRIES,
                        )
                        return None
                else:
                    logger.error("Database error: %s", e)
                    return None

        return None

    def _is_indexed(
        self, conn: sqlite3.Connection, collection_id: int, message_id: str
    ) -> bool:
        """Check if an email with this message_id is already indexed."""
        row = conn.execute(
            "SELECT id FROM sources WHERE collection_id = ? AND source_path = ?",
            (collection_id, message_id),
        ).fetchone()
        return row is not None

    def _index_email(
        self,
        conn: sqlite3.Connection,
        config: Config,
        collection_id: int,
        email_msg: EmailMessage,
    ) -> int:
        """Index a single email: chunk, embed, and insert.

        Returns:
            Number of chunks indexed.
        """
        # Chunk the email
        chunks = chunk_email(
            subject=email_msg.subject,
            body=email_msg.body_text,
            chunk_size=config.chunk_size_tokens,
            overlap=config.chunk_overlap_tokens,
        )

        if not chunks:
            return 0

        # Embed all chunks
        chunk_texts = [c.text for c in chunks]
        embeddings = get_embeddings(chunk_texts, config)

        # Build metadata
        metadata = {
            "sender": email_msg.sender,
            "recipients": email_msg.recipients,
            "date": email_msg.date,
            "folder": email_msg.folder,
        }
        metadata_json = json.dumps(metadata)

        now = datetime.now().isoformat()

        # Delete existing source if re-indexing (force mode)
        conn.execute(
            "DELETE FROM sources WHERE collection_id = ? AND source_path = ?",
            (collection_id, email_msg.message_id),
        )

        # Insert source
        cursor = conn.execute(
            """
            INSERT INTO sources (collection_id, source_type, source_path, last_indexed_at)
            VALUES (?, 'email', ?, ?)
            """,
            (collection_id, email_msg.message_id, now),
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
                    email_msg.subject or "(no subject)",
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
    ) -> str | None:
        """Get the latest indexed email date for incremental updates."""
        row = conn.execute(
            """
            SELECT MAX(json_extract(d.metadata, '$.date')) as latest_date
            FROM documents d
            WHERE d.collection_id = ?
            """,
            (collection_id,),
        ).fetchone()

        if row and row["latest_date"]:
            return row["latest_date"]
        return None

    def _set_watermark(
        self, conn: sqlite3.Connection, collection_id: int, date: str
    ) -> None:
        """Store the watermark date in the collection description for tracking."""
        conn.execute(
            "UPDATE collections SET description = ? WHERE id = ?",
            (f"Emails from eM Client (indexed through {date})", collection_id),
        )
        conn.commit()


class AccountIndexResult:
    """Tracks indexing results for a single account."""

    def __init__(self) -> None:
        self.total_found: int = 0
        self.indexed: int = 0
        self.skipped: int = 0
        self.errors: int = 0
        self.error_messages: list[str] = []
        self.latest_date: str = ""
