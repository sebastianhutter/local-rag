"""Obsidian vault indexer for local-rag.

Indexes all supported file types found in Obsidian vaults (markdown, PDF,
DOCX, HTML, plaintext, etc.) into the "obsidian" system collection.
"""

import hashlib
import json
import logging
import sqlite3
from collections.abc import Callable
from datetime import datetime, timezone
from pathlib import Path

from local_rag.config import Config
from local_rag.db import get_or_create_collection
from local_rag.embeddings import get_embeddings, serialize_float32
from local_rag.indexers.base import BaseIndexer, IndexResult
from local_rag.indexers.project import _EXTENSION_MAP, _parse_and_chunk

logger = logging.getLogger(__name__)

# Directories to skip when walking an Obsidian vault
_SKIP_DIRS = {".obsidian", ".trash", ".git"}


class ObsidianIndexer(BaseIndexer):
    """Indexes all supported files in Obsidian vaults."""

    def __init__(self, vault_paths: list[Path], exclude_folders: list[str] | None = None) -> None:
        self.vault_paths = vault_paths
        self.exclude_folders = set(exclude_folders or [])

    def index(
        self,
        conn: sqlite3.Connection,
        config: Config,
        force: bool = False,
        progress_callback: Callable[[int, int, str], None] | None = None,
    ) -> IndexResult:
        """Index all configured Obsidian vaults.

        Args:
            conn: SQLite database connection.
            config: Application configuration.
            force: If True, re-index all files regardless of hash match.
            progress_callback: Optional callback invoked per file with
                (current, total, file_name).

        Returns:
            IndexResult with counts of indexed/skipped/errored files.
        """
        collection_id = get_or_create_collection(conn, "obsidian", "system")

        # Collect all files across vaults first so we can report total progress
        all_files: list[Path] = []
        for vault_path in self.vault_paths:
            vault_path = vault_path.expanduser().resolve()
            if not vault_path.is_dir():
                logger.warning("Vault path does not exist or is not a directory: %s", vault_path)
                continue
            logger.info("Indexing Obsidian vault: %s", vault_path)
            files = _walk_vault(vault_path, self.exclude_folders)
            logger.info("Found %d supported files in %s", len(files), vault_path)
            all_files.extend(files)

        total_found = len(all_files)
        indexed = 0
        skipped = 0
        errors = 0

        for i, file_path in enumerate(all_files, 1):
            if progress_callback:
                progress_callback(i, total_found, file_path.name)
            try:
                result = _index_file(conn, config, collection_id, file_path, force)
                if result == "indexed":
                    indexed += 1
                elif result == "skipped":
                    skipped += 1
            except Exception:
                logger.exception("Error indexing %s", file_path)
                errors += 1

        logger.info(
            "Obsidian indexing complete: %d found, %d indexed, %d skipped, %d errors",
            total_found, indexed, skipped, errors,
        )
        return IndexResult(indexed=indexed, skipped=skipped, errors=errors, total_found=total_found)


def _walk_vault(vault_path: Path, exclude_folders: set[str] | None = None) -> list[Path]:
    """Walk an Obsidian vault, yielding all supported files while skipping hidden/system dirs."""
    exclude = exclude_folders or set()
    results: list[Path] = []
    for item in sorted(vault_path.rglob("*")):
        if not item.is_file():
            continue
        # Skip files in hidden, system, or user-excluded directories
        parts = item.relative_to(vault_path).parts
        if any(part.startswith(".") or part in _SKIP_DIRS or part in exclude for part in parts[:-1]):
            continue
        # Skip hidden files
        if item.name.startswith("."):
            continue
        # Only include files with supported extensions
        if item.suffix.lower() not in _EXTENSION_MAP:
            continue
        results.append(item)
    return results


def _file_hash(file_path: Path) -> str:
    """Compute SHA256 hash of file content."""
    h = hashlib.sha256()
    h.update(file_path.read_bytes())
    return h.hexdigest()


def _index_file(
    conn: sqlite3.Connection,
    config: Config,
    collection_id: int,
    file_path: Path,
    force: bool,
) -> str:
    """Index a single file of any supported type.

    Returns:
        'indexed' if the file was processed, 'skipped' if unchanged.
    """
    source_path = str(file_path)
    content_hash = _file_hash(file_path)
    source_type = _EXTENSION_MAP.get(file_path.suffix.lower(), "plaintext")
    mtime = datetime.fromtimestamp(file_path.stat().st_mtime, tz=timezone.utc).isoformat()

    # Check if source already exists with same hash
    if not force:
        row = conn.execute(
            "SELECT id, file_hash FROM sources WHERE collection_id = ? AND source_path = ?",
            (collection_id, source_path),
        ).fetchone()
        if row and row["file_hash"] == content_hash:
            logger.debug("Skipping unchanged file: %s", file_path.name)
            return "skipped"

    # Parse and chunk using the shared dispatch from project indexer
    chunks = _parse_and_chunk(file_path, source_type, config)
    if not chunks:
        logger.warning("No content extracted from %s, skipping", file_path)
        return "skipped"

    # Embed all chunks in a batch
    chunk_texts = [c.text for c in chunks]
    embeddings = get_embeddings(chunk_texts, config)

    # Persist within a transaction
    now = datetime.now(tz=timezone.utc).isoformat()

    # Upsert source row
    existing = conn.execute(
        "SELECT id FROM sources WHERE collection_id = ? AND source_path = ?",
        (collection_id, source_path),
    ).fetchone()

    if existing:
        source_id = existing["id"]
        # Delete old documents (triggers handle FTS cleanup)
        old_doc_ids = [
            r["id"]
            for r in conn.execute(
                "SELECT id FROM documents WHERE source_id = ?", (source_id,)
            ).fetchall()
        ]
        if old_doc_ids:
            conn.execute(
                f"DELETE FROM vec_documents WHERE document_id IN ({','.join('?' * len(old_doc_ids))})",
                old_doc_ids,
            )
            conn.execute("DELETE FROM documents WHERE source_id = ?", (source_id,))

        conn.execute(
            "UPDATE sources SET file_hash = ?, file_modified_at = ?, last_indexed_at = ?, source_type = ? WHERE id = ?",
            (content_hash, mtime, now, source_type, source_id),
        )
    else:
        cursor = conn.execute(
            "INSERT INTO sources (collection_id, source_type, source_path, file_hash, file_modified_at, last_indexed_at) VALUES (?, ?, ?, ?, ?, ?)",
            (collection_id, source_type, source_path, content_hash, mtime, now),
        )
        source_id = cursor.lastrowid

    # Insert new documents and vectors
    for chunk, embedding in zip(chunks, embeddings):
        metadata_json = json.dumps(chunk.metadata) if chunk.metadata else None
        cursor = conn.execute(
            "INSERT INTO documents (source_id, collection_id, chunk_index, title, content, metadata) VALUES (?, ?, ?, ?, ?, ?)",
            (
                source_id,
                collection_id,
                chunk.chunk_index,
                chunk.title,
                chunk.text,
                metadata_json,
            ),
        )
        doc_id = cursor.lastrowid

        conn.execute(
            "INSERT INTO vec_documents (embedding, document_id) VALUES (?, ?)",
            (serialize_float32(embedding), doc_id),
        )

    conn.commit()
    logger.info("Indexed %s [%s] (%d chunks)", file_path.name, source_type, len(chunks))
    return "indexed"
