"""Calibre library indexer for local-rag.

Indexes ebooks from Calibre libraries into the "calibre" system collection,
enriching every chunk with book-level metadata (authors, tags, series, etc.).
"""

import hashlib
import json
import logging
import sqlite3
from collections.abc import Callable
from datetime import datetime, timezone
from pathlib import Path

from local_rag.chunker import Chunk, chunk_plain
from local_rag.config import Config
from local_rag.db import get_or_create_collection
from local_rag.embeddings import get_embeddings, serialize_float32
from local_rag.indexers.base import BaseIndexer, IndexResult
from local_rag.parsers.calibre import CalibreBook, get_book_file_path, parse_calibre_library
from local_rag.parsers.epub import parse_epub
from local_rag.parsers.pdf import parse_pdf

logger = logging.getLogger(__name__)

PREFERRED_FORMATS = ["EPUB", "PDF"]


class CalibreIndexer(BaseIndexer):
    """Indexes ebooks from Calibre libraries with rich metadata."""

    def __init__(self, library_paths: list[Path]) -> None:
        self.library_paths = library_paths

    def index(
        self,
        conn: sqlite3.Connection,
        config: Config,
        force: bool = False,
        progress_callback: Callable[[int, int, str], None] | None = None,
    ) -> IndexResult:
        """Index all configured Calibre libraries.

        Args:
            conn: SQLite database connection.
            config: Application configuration.
            force: If True, re-index all books regardless of hash match.
            progress_callback: Optional callback invoked per book with
                (current, total, book_title).

        Returns:
            IndexResult with counts of indexed/skipped/errored books.
        """
        collection_id = get_or_create_collection(conn, "calibre", "system")

        # Collect all books across libraries first so we can report total progress
        all_books: list[tuple[Path, CalibreBook]] = []
        for library_path in self.library_paths:
            library_path = library_path.expanduser().resolve()
            if not library_path.is_dir():
                logger.warning("Calibre library path does not exist: %s", library_path)
                continue
            logger.info("Indexing Calibre library: %s", library_path)
            books = parse_calibre_library(library_path)
            logger.info("Found %d books in %s", len(books), library_path)
            all_books.extend((library_path, book) for book in books)

        total_found = len(all_books)
        indexed = 0
        skipped = 0
        errors = 0

        for i, (library_path, book) in enumerate(all_books, 1):
            if progress_callback:
                progress_callback(i, total_found, book.title)
            try:
                result = _index_book(
                    conn, config, collection_id, library_path, book, force
                )
                if result == "indexed":
                    indexed += 1
                elif result == "skipped":
                    skipped += 1
            except Exception:
                logger.exception("Error indexing book: %s", book.title)
                errors += 1

        logger.info(
            "Calibre indexing complete: %d found, %d indexed, %d skipped, %d errors",
            total_found, indexed, skipped, errors,
        )
        return IndexResult(indexed=indexed, skipped=skipped, errors=errors, total_found=total_found)


def _file_hash(file_path: Path) -> str:
    """Compute SHA256 hash of file content."""
    h = hashlib.sha256()
    with open(file_path, "rb") as f:
        for block in iter(lambda: f.read(8192), b""):
            h.update(block)
    return h.hexdigest()


def _build_book_metadata(
    book: CalibreBook, library_path: Path, fmt: str | None
) -> dict:
    """Build the metadata dict to attach to every chunk of a book."""
    meta: dict = {}
    if book.authors:
        meta["authors"] = book.authors
    if book.tags:
        meta["tags"] = book.tags
    if book.series:
        meta["series"] = book.series
    if book.series_index is not None:
        meta["series_index"] = book.series_index
    if book.publisher:
        meta["publisher"] = book.publisher
    if book.pubdate:
        meta["pubdate"] = book.pubdate
    if book.rating is not None:
        meta["rating"] = book.rating
    if book.languages:
        meta["languages"] = book.languages
    if book.identifiers:
        meta["identifiers"] = book.identifiers
    meta["calibre_id"] = book.book_id
    if fmt:
        meta["format"] = fmt
    meta["library"] = str(library_path)
    return meta


def _index_book(
    conn: sqlite3.Connection,
    config: Config,
    collection_id: int,
    library_path: Path,
    book: CalibreBook,
    force: bool,
) -> str:
    """Index a single Calibre book.

    Returns:
        'indexed' if the book was processed, 'skipped' if unchanged.
    """
    file_info = get_book_file_path(library_path, book, PREFERRED_FORMATS)

    if file_info:
        file_path, fmt = file_info
        source_path = str(file_path)
        content_hash = _file_hash(file_path)
        source_type = fmt  # "epub" or "pdf"
    else:
        # No EPUB or PDF available — index description only if available
        if not book.description:
            logger.warning(
                "Book '%s' has no EPUB/PDF and no description, skipping", book.title
            )
            return "skipped"
        source_path = f"calibre://{library_path}/{book.relative_path}"
        content_hash = hashlib.sha256(book.description.encode()).hexdigest()
        source_type = "calibre-description"
        fmt = None

    # Check if source already exists with same hash
    if not force:
        row = conn.execute(
            "SELECT id, file_hash FROM sources WHERE collection_id = ? AND source_path = ?",
            (collection_id, source_path),
        ).fetchone()
        if row and row["file_hash"] == content_hash:
            # File content unchanged — check if metadata changed
            if _metadata_changed(conn, row["id"], book):
                _refresh_metadata(conn, row["id"], book, library_path, fmt)
                conn.commit()
                logger.info("Metadata refreshed for: %s", book.title)
                return "indexed"
            logger.debug("Skipping unchanged book: %s", book.title)
            return "skipped"

    # Build chunks from book content
    book_meta = _build_book_metadata(book, library_path, fmt)
    chunks = _extract_and_chunk_book(book, file_info, config, book_meta)

    if not chunks:
        logger.warning("No content extracted from book '%s', skipping", book.title)
        return "skipped"

    # Embed all chunks
    chunk_texts = [c.text for c in chunks]
    embeddings = get_embeddings(chunk_texts, config)

    now = datetime.now(tz=timezone.utc).isoformat()

    # Upsert source
    existing = conn.execute(
        "SELECT id FROM sources WHERE collection_id = ? AND source_path = ?",
        (collection_id, source_path),
    ).fetchone()

    if existing:
        source_id = existing["id"]
        # Delete old documents and vectors
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
            (content_hash, book.last_modified, now, source_type, source_id),
        )
    else:
        cursor = conn.execute(
            "INSERT INTO sources (collection_id, source_type, source_path, file_hash, file_modified_at, last_indexed_at) VALUES (?, ?, ?, ?, ?, ?)",
            (collection_id, source_type, source_path, content_hash, book.last_modified, now),
        )
        source_id = cursor.lastrowid

    # Insert new documents and vectors
    for chunk, embedding in zip(chunks, embeddings):
        metadata_json = json.dumps(chunk.metadata) if chunk.metadata else None
        cursor = conn.execute(
            "INSERT INTO documents (source_id, collection_id, chunk_index, title, content, metadata) VALUES (?, ?, ?, ?, ?, ?)",
            (source_id, collection_id, chunk.chunk_index, chunk.title, chunk.text, metadata_json),
        )
        doc_id = cursor.lastrowid
        conn.execute(
            "INSERT INTO vec_documents (embedding, document_id) VALUES (?, ?)",
            (serialize_float32(embedding), doc_id),
        )

    conn.commit()
    logger.info("Indexed book: %s [%s] (%d chunks)", book.title, source_type, len(chunks))
    return "indexed"


def _extract_and_chunk_book(
    book: CalibreBook,
    file_info: tuple[Path, str] | None,
    config: Config,
    book_meta: dict,
) -> list[Chunk]:
    """Extract text from the book file and produce enriched chunks."""
    chunk_size = config.chunk_size_tokens
    overlap = config.chunk_overlap_tokens
    chunks: list[Chunk] = []
    chunk_idx = 0

    if file_info:
        file_path, fmt = file_info

        if fmt == "epub":
            sections = parse_epub(file_path)
            section_label = "chapter"
        elif fmt == "pdf":
            sections = parse_pdf(file_path)
            section_label = "page"
        else:
            sections = []
            section_label = "section"

        for section_num, text in sections:
            section_title = f"{book.title} ({section_label} {section_num})"
            section_chunks = chunk_plain(text, section_title, chunk_size, overlap)
            for chunk in section_chunks:
                chunk.chunk_index = chunk_idx
                meta = dict(book_meta)
                meta[f"{section_label}_number"] = section_num
                chunk.metadata = meta
                chunks.append(chunk)
                chunk_idx += 1

    # Add description chunk(s) if available
    if book.description:
        desc_title = f"{book.title} (description)"
        desc_chunks = chunk_plain(book.description, desc_title, chunk_size, overlap)
        for chunk in desc_chunks:
            chunk.chunk_index = chunk_idx
            meta = dict(book_meta)
            meta["chunk_type"] = "description"
            chunk.metadata = meta
            chunks.append(chunk)
            chunk_idx += 1

    return chunks


def _metadata_changed(
    conn: sqlite3.Connection, source_id: int, book: CalibreBook
) -> bool:
    """Check if Calibre metadata has changed since last index by comparing a sample doc's metadata."""
    row = conn.execute(
        "SELECT metadata FROM documents WHERE source_id = ? LIMIT 1",
        (source_id,),
    ).fetchone()
    if not row or not row["metadata"]:
        return True

    stored_meta = json.loads(row["metadata"])
    # Compare key metadata fields — use `or None` to match what _build_book_metadata stores
    # (it skips falsy values, so stored_meta won't have the key for None/[])
    if stored_meta.get("authors") != (book.authors or None):
        return True
    if stored_meta.get("tags") != (book.tags or None):
        return True
    if stored_meta.get("series") != book.series:
        return True
    if stored_meta.get("rating") != book.rating:
        return True
    if stored_meta.get("publisher") != book.publisher:
        return True

    return False


def _refresh_metadata(
    conn: sqlite3.Connection,
    source_id: int,
    book: CalibreBook,
    library_path: Path,
    fmt: str | None,
) -> None:
    """Update metadata JSON on existing document rows without re-embedding."""
    book_meta = _build_book_metadata(book, library_path, fmt)

    rows = conn.execute(
        "SELECT id, metadata FROM documents WHERE source_id = ?",
        (source_id,),
    ).fetchall()

    for row in rows:
        existing_meta = json.loads(row["metadata"]) if row["metadata"] else {}
        # Preserve chunk-specific fields (page_number, chapter_number, chunk_type)
        merged = dict(book_meta)
        for key in ("page_number", "chapter_number", "chunk_type"):
            if key in existing_meta:
                merged[key] = existing_meta[key]

        conn.execute(
            "UPDATE documents SET metadata = ? WHERE id = ?",
            (json.dumps(merged), row["id"]),
        )
