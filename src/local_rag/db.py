"""Database initialization, connection management, and migrations for local-rag."""

import json
import logging
import sqlite3

import sqlite_vec

from local_rag.config import Config

logger = logging.getLogger(__name__)

SCHEMA_VERSION = 3


def get_connection(config: Config) -> sqlite3.Connection:
    """Open a SQLite connection with sqlite-vec loaded and pragmas set.

    Args:
        config: Application configuration.

    Returns:
        Configured sqlite3.Connection.
    """
    db_path = config.db_path
    db_path.parent.mkdir(parents=True, exist_ok=True)

    conn = sqlite3.connect(str(db_path))
    conn.enable_load_extension(True)
    sqlite_vec.load(conn)
    conn.enable_load_extension(False)

    conn.execute("PRAGMA journal_mode=WAL")
    conn.execute("PRAGMA foreign_keys=ON")
    conn.row_factory = sqlite3.Row

    return conn


def init_db(conn: sqlite3.Connection, config: Config) -> None:
    """Create all tables, virtual tables, and triggers if they don't exist.

    Args:
        conn: SQLite connection.
        config: Application configuration (used for embedding dimensions).
    """
    dim = config.embedding_dimensions

    conn.executescript(
        f"""
        CREATE TABLE IF NOT EXISTS collections (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT NOT NULL UNIQUE,
            collection_type TEXT NOT NULL DEFAULT 'project',
            description TEXT,
            paths TEXT,
            created_at TEXT DEFAULT (datetime('now'))
        );

        CREATE TABLE IF NOT EXISTS sources (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            collection_id INTEGER NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
            source_type TEXT NOT NULL,
            source_path TEXT NOT NULL,
            file_hash TEXT,
            file_modified_at TEXT,
            last_indexed_at TEXT,
            UNIQUE(collection_id, source_path)
        );

        CREATE TABLE IF NOT EXISTS documents (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            source_id INTEGER NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
            collection_id INTEGER NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
            chunk_index INTEGER NOT NULL,
            title TEXT,
            content TEXT NOT NULL,
            metadata TEXT,
            created_at TEXT DEFAULT (datetime('now')),
            UNIQUE(source_id, chunk_index)
        );

        CREATE VIRTUAL TABLE IF NOT EXISTS vec_documents USING vec0(
            embedding float[{dim}],
            document_id INTEGER
        );

        CREATE VIRTUAL TABLE IF NOT EXISTS documents_fts USING fts5(
            title,
            content,
            content='documents',
            content_rowid='id'
        );

        -- Triggers to keep FTS in sync with documents table
        CREATE TRIGGER IF NOT EXISTS documents_ai AFTER INSERT ON documents BEGIN
            INSERT INTO documents_fts(rowid, title, content)
            VALUES (new.id, new.title, new.content);
        END;

        CREATE TRIGGER IF NOT EXISTS documents_ad AFTER DELETE ON documents BEGIN
            INSERT INTO documents_fts(documents_fts, rowid, title, content)
            VALUES('delete', old.id, old.title, old.content);
        END;

        CREATE TRIGGER IF NOT EXISTS documents_au AFTER UPDATE ON documents BEGIN
            INSERT INTO documents_fts(documents_fts, rowid, title, content)
            VALUES('delete', old.id, old.title, old.content);
            INSERT INTO documents_fts(rowid, title, content)
            VALUES (new.id, new.title, new.content);
        END;

        -- Schema version tracking
        CREATE TABLE IF NOT EXISTS meta (
            key TEXT PRIMARY KEY,
            value TEXT
        );
    """
    )

    # Set schema version if not already set
    row = conn.execute("SELECT value FROM meta WHERE key = 'schema_version'").fetchone()
    if row is None:
        conn.execute(
            "INSERT INTO meta (key, value) VALUES ('schema_version', ?)",
            (str(SCHEMA_VERSION),),
        )
    conn.commit()

    # Run any pending migrations
    migrate(conn, config)

    logger.info(
        "Database initialized (schema version %d, embedding dim %d)",
        SCHEMA_VERSION,
        dim,
    )


def migrate(conn: sqlite3.Connection, config: Config) -> None:
    """Run any pending schema migrations.

    Args:
        conn: SQLite connection.
        config: Application configuration.
    """
    row = conn.execute("SELECT value FROM meta WHERE key = 'schema_version'").fetchone()
    if row is None:
        # Fresh database, init will handle it
        init_db(conn, config)
        return

    current_version = int(row["value"])

    if current_version >= SCHEMA_VERSION:
        logger.debug("Database schema is up to date (version %d)", current_version)
        return

    if current_version < 2:
        # Reclassify git repo collections from 'project' to 'code'.
        # Git repos are identified by their watermark description (starts with "git-").
        conn.execute(
            "UPDATE collections SET collection_type = 'code' "
            "WHERE collection_type = 'project' AND description LIKE 'git-%'"
        )
        conn.commit()
        logger.info("Migration v2: reclassified git repo collections as 'code'")

    if current_version < 3:
        # Add paths column to collections for storing project source paths.
        conn.execute("ALTER TABLE collections ADD COLUMN paths TEXT")
        conn.commit()
        logger.info("Migration v3: added paths column to collections")

    conn.execute(
        "UPDATE meta SET value = ? WHERE key = 'schema_version'",
        (str(SCHEMA_VERSION),),
    )
    conn.commit()
    logger.info("Database migrated to schema version %d", SCHEMA_VERSION)


def get_or_create_collection(
    conn: sqlite3.Connection,
    name: str,
    collection_type: str = "project",
    description: str | None = None,
    paths: list[str] | None = None,
) -> int:
    """Get or create a collection by name.

    Args:
        conn: SQLite connection.
        name: Collection name.
        collection_type: 'system', 'project', or 'code'.
        description: Optional description.
        paths: Optional list of source paths (stored as JSON). If provided
            on an existing collection, the stored paths are updated.

    Returns:
        The collection ID.
    """
    paths_json = json.dumps(paths) if paths else None

    row = conn.execute("SELECT id FROM collections WHERE name = ?", (name,)).fetchone()
    if row:
        if paths_json:
            conn.execute(
                "UPDATE collections SET paths = ? WHERE id = ?",
                (paths_json, row["id"]),
            )
            conn.commit()
        return row["id"]

    cursor = conn.execute(
        "INSERT INTO collections (name, collection_type, description, paths) "
        "VALUES (?, ?, ?, ?)",
        (name, collection_type, description, paths_json),
    )
    conn.commit()
    collection_id: int = cursor.lastrowid  # type: ignore[assignment]
    logger.info(
        "Created collection '%s' (type=%s, id=%d)", name, collection_type, collection_id
    )
    return collection_id
