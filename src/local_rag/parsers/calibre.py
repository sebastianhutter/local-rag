"""Calibre metadata.db parser for extracting book metadata."""

import logging
import sqlite3
from dataclasses import dataclass, field
from pathlib import Path

from bs4 import BeautifulSoup

logger = logging.getLogger(__name__)


@dataclass
class CalibreBook:
    """A book entry from a Calibre library with full metadata."""

    book_id: int
    title: str
    authors: list[str]
    tags: list[str]
    series: str | None
    series_index: float | None
    publisher: str | None
    pubdate: str | None
    rating: int | None  # 0-10 from Calibre
    languages: list[str]
    identifiers: dict[str, str]  # {"isbn": "...", "amazon": "..."}
    description: str | None  # plain text (stripped from HTML)
    formats: dict[str, str]  # {"PDF": "filename_without_ext", ...}
    relative_path: str  # books.path: "Author/Title (ID)"
    last_modified: str


def parse_calibre_library(library_path: Path) -> list[CalibreBook]:
    """Load all books with metadata from a Calibre library.

    Opens metadata.db in read-only mode to avoid any writes or lock conflicts.

    Args:
        library_path: Path to the Calibre library root directory.

    Returns:
        List of CalibreBook objects with full metadata.
    """
    db_path = library_path / "metadata.db"
    if not db_path.exists():
        logger.error("Calibre metadata.db not found at: %s", db_path)
        return []

    uri = f"file:{db_path}?mode=ro"
    try:
        conn = sqlite3.connect(uri, uri=True)
        conn.row_factory = sqlite3.Row
    except sqlite3.OperationalError as e:
        logger.error("Failed to open Calibre database at %s: %s", db_path, e)
        return []

    try:
        return _load_all_books(conn)
    except Exception as e:
        logger.error("Failed to parse Calibre library at %s: %s", library_path, e)
        return []
    finally:
        conn.close()


def _load_all_books(conn: sqlite3.Connection) -> list[CalibreBook]:
    """Bulk-load all books with metadata from the database."""
    # Load base book data
    books_rows = conn.execute(
        "SELECT id, title, path, pubdate, last_modified FROM books"
    ).fetchall()

    if not books_rows:
        return []

    # Build lookup maps for related tables
    authors_map = _load_book_authors(conn)
    tags_map = _load_book_tags(conn)
    series_map = _load_book_series(conn)
    publishers_map = _load_book_publishers(conn)
    ratings_map = _load_book_ratings(conn)
    languages_map = _load_book_languages(conn)
    identifiers_map = _load_book_identifiers(conn)
    comments_map = _load_book_comments(conn)
    formats_map = _load_book_formats(conn)

    books: list[CalibreBook] = []
    for row in books_rows:
        book_id = row["id"]

        description_html = comments_map.get(book_id)
        description = _strip_html_description(description_html) if description_html else None

        series_info = series_map.get(book_id)

        books.append(
            CalibreBook(
                book_id=book_id,
                title=row["title"],
                authors=authors_map.get(book_id, []),
                tags=tags_map.get(book_id, []),
                series=series_info[0] if series_info else None,
                series_index=series_info[1] if series_info else None,
                publisher=publishers_map.get(book_id),
                pubdate=row["pubdate"],
                rating=ratings_map.get(book_id),
                languages=languages_map.get(book_id, []),
                identifiers=identifiers_map.get(book_id, {}),
                description=description,
                formats=formats_map.get(book_id, {}),
                relative_path=row["path"],
                last_modified=row["last_modified"],
            )
        )

    logger.info("Loaded %d books from Calibre library", len(books))
    return books


def _load_book_authors(conn: sqlite3.Connection) -> dict[int, list[str]]:
    """Load author names grouped by book ID."""
    result: dict[int, list[str]] = {}
    rows = conn.execute(
        """SELECT bal.book, a.name
           FROM books_authors_link bal
           JOIN authors a ON bal.author = a.id
           ORDER BY bal.book, bal.id"""
    ).fetchall()
    for row in rows:
        result.setdefault(row["book"], []).append(row["name"])
    return result


def _load_book_tags(conn: sqlite3.Connection) -> dict[int, list[str]]:
    """Load tags grouped by book ID."""
    result: dict[int, list[str]] = {}
    rows = conn.execute(
        """SELECT btl.book, t.name
           FROM books_tags_link btl
           JOIN tags t ON btl.tag = t.id"""
    ).fetchall()
    for row in rows:
        result.setdefault(row["book"], []).append(row["name"])
    return result


def _load_book_series(conn: sqlite3.Connection) -> dict[int, tuple[str, float | None]]:
    """Load series info grouped by book ID. Returns (series_name, series_index)."""
    result: dict[int, tuple[str, float | None]] = {}
    rows = conn.execute(
        """SELECT bsl.book, s.name, b.series_index
           FROM books_series_link bsl
           JOIN series s ON bsl.series = s.id
           JOIN books b ON bsl.book = b.id"""
    ).fetchall()
    for row in rows:
        result[row["book"]] = (row["name"], row["series_index"])
    return result


def _load_book_publishers(conn: sqlite3.Connection) -> dict[int, str]:
    """Load publisher names grouped by book ID."""
    result: dict[int, str] = {}
    rows = conn.execute(
        """SELECT bpl.book, p.name
           FROM books_publishers_link bpl
           JOIN publishers p ON bpl.publisher = p.id"""
    ).fetchall()
    for row in rows:
        result[row["book"]] = row["name"]
    return result


def _load_book_ratings(conn: sqlite3.Connection) -> dict[int, int]:
    """Load ratings grouped by book ID."""
    result: dict[int, int] = {}
    rows = conn.execute(
        """SELECT brl.book, r.rating
           FROM books_ratings_link brl
           JOIN ratings r ON brl.rating = r.id"""
    ).fetchall()
    for row in rows:
        result[row["book"]] = row["rating"]
    return result


def _load_book_languages(conn: sqlite3.Connection) -> dict[int, list[str]]:
    """Load languages grouped by book ID."""
    result: dict[int, list[str]] = {}
    rows = conn.execute(
        """SELECT bll.book, l.lang_code
           FROM books_languages_link bll
           JOIN languages l ON bll.lang_code = l.id"""
    ).fetchall()
    for row in rows:
        result.setdefault(row["book"], []).append(row["lang_code"])
    return result


def _load_book_identifiers(conn: sqlite3.Connection) -> dict[int, dict[str, str]]:
    """Load identifiers grouped by book ID."""
    result: dict[int, dict[str, str]] = {}
    rows = conn.execute(
        "SELECT book, type, val FROM identifiers"
    ).fetchall()
    for row in rows:
        result.setdefault(row["book"], {})[row["type"]] = row["val"]
    return result


def _load_book_comments(conn: sqlite3.Connection) -> dict[int, str]:
    """Load comments (descriptions) grouped by book ID."""
    result: dict[int, str] = {}
    rows = conn.execute(
        "SELECT book, text FROM comments"
    ).fetchall()
    for row in rows:
        result[row["book"]] = row["text"]
    return result


def _load_book_formats(conn: sqlite3.Connection) -> dict[int, dict[str, str]]:
    """Load available formats grouped by book ID. Returns {format: filename_without_ext}."""
    result: dict[int, dict[str, str]] = {}
    rows = conn.execute(
        "SELECT book, format, name FROM data"
    ).fetchall()
    for row in rows:
        result.setdefault(row["book"], {})[row["format"]] = row["name"]
    return result


def get_book_file_path(
    library_path: Path, book: CalibreBook, preferred_formats: list[str] | None = None
) -> tuple[Path, str] | None:
    """Resolve the absolute file path for the best available format of a book.

    Args:
        library_path: Root path of the Calibre library.
        book: The CalibreBook to find the file for.
        preferred_formats: Ordered list of preferred formats (e.g. ["EPUB", "PDF"]).
            Defaults to ["EPUB", "PDF"].

    Returns:
        (absolute_path, format_lower) tuple, or None if no supported file found.
    """
    if preferred_formats is None:
        preferred_formats = ["EPUB", "PDF"]

    for fmt in preferred_formats:
        filename_base = book.formats.get(fmt)
        if filename_base is None:
            continue
        file_path = library_path / book.relative_path / f"{filename_base}.{fmt.lower()}"
        if file_path.exists():
            return file_path, fmt.lower()

    return None


def _strip_html_description(html: str) -> str:
    """Convert an HTML book description/synopsis to plain text."""
    soup = BeautifulSoup(html, "html.parser")
    text = soup.get_text(separator="\n")
    lines = [line.strip() for line in text.splitlines()]
    return "\n".join(line for line in lines if line)
