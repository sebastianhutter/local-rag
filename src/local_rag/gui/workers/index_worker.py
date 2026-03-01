"""Background indexing worker thread for local-rag GUI."""

import logging
import threading

from PySide6.QtCore import QThread, Signal

from local_rag.config import Config
from local_rag.indexers.base import IndexResult

logger = logging.getLogger(__name__)


class IndexWorker(QThread):
    """QThread that runs indexing in the background.

    Signals:
        progress: (current, total, item_name) emitted per file/item.
        finished: dict mapping collection name to IndexResult summary.
        error: error message string.
    """

    progress = Signal(int, int, str)
    finished = Signal(dict)
    error = Signal(str)

    def __init__(
        self,
        config: Config,
        collection_name: str | None = None,
        force: bool = False,
        parent=None,
    ) -> None:
        """Initialize the index worker.

        Args:
            config: Application configuration.
            collection_name: Specific collection to index, or None for all.
            force: If True, re-index everything.
            parent: Qt parent object.
        """
        super().__init__(parent)
        self.config = config
        self.collection_name = collection_name
        self.force = force
        self.cancel_event = threading.Event()

    def run(self) -> None:
        """Run the indexing operation in the background thread."""
        from local_rag.services.indexing_service import IndexingService

        service = IndexingService()

        def progress_callback(current: int, total: int, item_name: str) -> None:
            self.progress.emit(current, total, item_name)

        try:
            if self.collection_name:
                result = service.index_collection(
                    self.collection_name,
                    self.config,
                    force=self.force,
                    progress_callback=progress_callback,
                    cancel_event=self.cancel_event,
                )
                results = {self.collection_name: self._result_to_dict(result)}
            else:
                raw_results = service.index_all(
                    self.config,
                    force=self.force,
                    progress_callback=progress_callback,
                    cancel_event=self.cancel_event,
                )
                results = {
                    name: self._result_to_dict(r)
                    for name, r in raw_results.items()
                }

            self.finished.emit(results)
        except Exception as e:
            logger.exception("Indexing failed")
            self.error.emit(str(e))

    def cancel(self) -> None:
        """Request cancellation of the indexing operation."""
        self.cancel_event.set()

    @staticmethod
    def _result_to_dict(result: IndexResult) -> dict:
        """Convert IndexResult to a serializable dict."""
        return {
            "indexed": result.indexed,
            "skipped": result.skipped,
            "errors": result.errors,
            "total_found": result.total_found,
        }
