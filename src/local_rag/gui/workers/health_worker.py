"""Health check worker thread for local-rag GUI."""

import logging

from PySide6.QtCore import QThread, Signal

from local_rag.config import Config

logger = logging.getLogger(__name__)


class HealthWorker(QThread):
    """QThread that periodically checks Ollama and system status.

    Signals:
        status_updated: dict with ollama_ok, collection_count, chunk_count, db_size_mb.
    """

    status_updated = Signal(dict)

    def __init__(self, config: Config, parent=None) -> None:
        super().__init__(parent)
        self.config = config

    def run(self) -> None:
        """Check system health once and emit the result."""
        from local_rag.services.status_service import StatusService

        service = StatusService()

        try:
            overview = service.get_overview(self.config)
            ollama_ok = service.check_ollama()

            self.status_updated.emit({
                "ollama_ok": ollama_ok,
                "collection_count": overview["collection_count"],
                "chunk_count": overview["chunk_count"],
                "db_size_mb": overview["db_size_mb"],
                "last_indexed": overview["last_indexed"],
            })
        except Exception:
            logger.exception("Health check failed")
            self.status_updated.emit({
                "ollama_ok": False,
                "collection_count": 0,
                "chunk_count": 0,
                "db_size_mb": 0.0,
                "last_indexed": None,
            })
