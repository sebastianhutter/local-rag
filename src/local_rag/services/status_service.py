"""Status service for local-rag.

Provides database statistics and health checks. Each method creates
its own database connection for thread safety.
"""

import logging

from local_rag.config import Config

logger = logging.getLogger(__name__)


class StatusService:
    """Service for querying system status and database statistics."""

    def get_overview(self, config: Config) -> dict:
        """Get high-level system statistics.

        Args:
            config: Application configuration.

        Returns:
            Dict with keys: collection_count, source_count, chunk_count,
            db_size_mb, last_indexed, embedding_model.
        """
        from local_rag.db import get_connection, init_db

        if not config.db_path.exists():
            return {
                "collection_count": 0,
                "source_count": 0,
                "chunk_count": 0,
                "db_size_mb": 0.0,
                "last_indexed": None,
                "embedding_model": config.embedding_model,
            }

        conn = get_connection(config)
        init_db(conn, config)
        try:
            coll_count = conn.execute(
                "SELECT COUNT(*) as cnt FROM collections"
            ).fetchone()["cnt"]
            source_count = conn.execute(
                "SELECT COUNT(*) as cnt FROM sources"
            ).fetchone()["cnt"]
            chunk_count = conn.execute(
                "SELECT COUNT(*) as cnt FROM documents"
            ).fetchone()["cnt"]
            last_indexed = conn.execute(
                "SELECT MAX(last_indexed_at) as ts FROM sources"
            ).fetchone()["ts"]

            db_size_mb = config.db_path.stat().st_size / (1024 * 1024)

            return {
                "collection_count": coll_count,
                "source_count": source_count,
                "chunk_count": chunk_count,
                "db_size_mb": round(db_size_mb, 1),
                "last_indexed": last_indexed,
                "embedding_model": config.embedding_model,
            }
        finally:
            conn.close()

    def get_collections(self, config: Config) -> list[dict]:
        """Get all collections with their statistics.

        Args:
            config: Application configuration.

        Returns:
            List of dicts with keys: name, type, source_count, chunk_count, last_indexed.
        """
        from local_rag.db import get_connection, init_db

        if not config.db_path.exists():
            return []

        conn = get_connection(config)
        init_db(conn, config)
        try:
            rows = conn.execute("""
                SELECT c.name, c.collection_type,
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
                    "source_count": row["source_count"],
                    "chunk_count": row["chunk_count"],
                    "last_indexed": row["last_indexed"],
                }
                for row in rows
            ]
        finally:
            conn.close()

    def check_ollama(self) -> bool:
        """Check if Ollama is running and reachable.

        Returns:
            True if Ollama responds, False otherwise.
        """
        try:
            import ollama
            client = ollama.Client()
            client.list()
            return True
        except Exception:
            return False
