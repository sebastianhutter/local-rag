"""Index progress dialog for local-rag GUI."""

import logging

from PySide6.QtCore import Qt, Slot
from PySide6.QtWidgets import (
    QDialog,
    QLabel,
    QProgressBar,
    QPushButton,
    QTextEdit,
    QVBoxLayout,
    QWidget,
)

logger = logging.getLogger(__name__)


class IndexProgressDialog(QDialog):
    """Modal dialog showing indexing progress."""

    def __init__(self, parent: QWidget | None = None) -> None:
        super().__init__(parent)
        self.setWindowTitle("Indexing...")
        self.resize(500, 400)
        self.setModal(True)
        self.setWindowFlags(
            self.windowFlags() & ~Qt.WindowType.WindowCloseButtonHint
        )

        self._cancelled = False

        self._build_ui()

    def _build_ui(self) -> None:
        """Build the progress dialog layout."""
        layout = QVBoxLayout(self)

        # Collection label
        self._collection_label = QLabel("Preparing...")
        self._collection_label.setStyleSheet("font-weight: bold;")
        layout.addWidget(self._collection_label)

        # Progress bar
        self._progress_bar = QProgressBar()
        self._progress_bar.setRange(0, 100)
        self._progress_bar.setValue(0)
        layout.addWidget(self._progress_bar)

        # Current item label
        self._item_label = QLabel("")
        self._item_label.setWordWrap(True)
        layout.addWidget(self._item_label)

        # Error log
        self._error_log = QTextEdit()
        self._error_log.setReadOnly(True)
        self._error_log.setPlaceholderText("Errors will appear here...")
        layout.addWidget(self._error_log)

        # Buttons
        self._cancel_btn = QPushButton("Cancel")
        self._cancel_btn.clicked.connect(self._on_cancel)
        layout.addWidget(self._cancel_btn)

        self._close_btn = QPushButton("Close")
        self._close_btn.clicked.connect(self.accept)
        self._close_btn.setVisible(False)
        layout.addWidget(self._close_btn)

    def _on_cancel(self) -> None:
        """Handle cancel button click."""
        self._cancelled = True
        self._cancel_btn.setEnabled(False)
        self._cancel_btn.setText("Cancelling...")

    @property
    def cancelled(self) -> bool:
        """Whether the user has requested cancellation."""
        return self._cancelled

    def set_collection(self, name: str) -> None:
        """Update the collection label.

        Args:
            name: Name of the collection being indexed.
        """
        self._collection_label.setText(f"Indexing: {name}")

    @Slot(int, int, str)
    def update_progress(self, current: int, total: int, item_name: str) -> None:
        """Update progress bar and item label.

        Args:
            current: Current item number.
            total: Total number of items.
            item_name: Name of the current item being processed.
        """
        if total > 0:
            percent = int((current / total) * 100)
            self._progress_bar.setValue(percent)
            self._item_label.setText(f"[{current}/{total}] {item_name}")
        else:
            self._progress_bar.setValue(0)
            self._item_label.setText(item_name)

    @Slot(dict)
    def on_finished(self, results: dict) -> None:
        """Handle indexing completion.

        Args:
            results: Dict with summary info (e.g. indexed_count, error_count).
        """
        self._progress_bar.setValue(100)
        indexed = results.get("indexed_count", 0)
        errors = results.get("error_count", 0)
        self._collection_label.setText("Indexing complete")
        self._item_label.setText(
            f"Indexed {indexed} items with {errors} error(s)."
        )
        self._cancel_btn.setVisible(False)
        self._close_btn.setVisible(True)

    @Slot(str)
    def on_error(self, message: str) -> None:
        """Append an error message to the error log.

        Args:
            message: Error description.
        """
        self._error_log.append(message)
        logger.warning("Indexing error: %s", message)
