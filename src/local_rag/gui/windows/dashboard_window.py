"""Dashboard window for local-rag GUI."""

import logging

from PySide6.QtCore import Qt
from PySide6.QtWidgets import (
    QHBoxLayout,
    QHeaderView,
    QLabel,
    QMessageBox,
    QPushButton,
    QTableWidget,
    QTableWidgetItem,
    QVBoxLayout,
    QWidget,
)

from local_rag.services import ConfigService, StatusService

logger = logging.getLogger(__name__)


class DashboardWindow(QWidget):
    """Dashboard showing collections and system status."""

    def __init__(self, parent: QWidget | None = None) -> None:
        super().__init__(parent)
        self.setWindowTitle("local-rag Dashboard")
        self.resize(800, 500)
        self.setWindowFlags(Qt.WindowType.Window)

        self._config_service = ConfigService()
        self._status_service = StatusService()

        self._build_ui()
        self._refresh()

    def _build_ui(self) -> None:
        """Build the dashboard layout."""
        layout = QVBoxLayout(self)

        # Summary bar
        self._summary_label = QLabel()
        layout.addWidget(self._summary_label)

        # Collections table
        self._table = QTableWidget()
        self._table.setColumnCount(5)
        self._table.setHorizontalHeaderLabels(
            ["Name", "Type", "Sources", "Chunks", "Last Indexed"]
        )
        self._table.setSelectionBehavior(QTableWidget.SelectionBehavior.SelectRows)
        self._table.setSelectionMode(QTableWidget.SelectionMode.SingleSelection)
        self._table.setEditTriggers(QTableWidget.EditTrigger.NoEditTriggers)
        header = self._table.horizontalHeader()
        if header:
            header.setStretchLastSection(True)
            header.setSectionResizeMode(0, QHeaderView.ResizeMode.Stretch)
        layout.addWidget(self._table)

        # Buttons
        buttons = QHBoxLayout()
        refresh_btn = QPushButton("Refresh")
        refresh_btn.clicked.connect(self._refresh)
        delete_btn = QPushButton("Delete Collection")
        delete_btn.clicked.connect(self._delete_collection)
        buttons.addWidget(refresh_btn)
        buttons.addWidget(delete_btn)
        buttons.addStretch()
        layout.addLayout(buttons)

    def _refresh(self) -> None:
        """Reload data from services and update the UI."""
        config = self._config_service.load()

        # Update summary
        overview = self._status_service.get_overview(config)
        ollama_ok = self._status_service.check_ollama()
        ollama_status = "OK" if ollama_ok else "Error"
        self._summary_label.setText(
            f"DB: {overview['db_size_mb']} MB | "
            f"{overview['collection_count']} collections | "
            f"{overview['chunk_count']} chunks | "
            f"Ollama: {ollama_status}"
        )

        # Update table
        collections = self._status_service.get_collections(config)
        self._table.setRowCount(len(collections))
        for row, coll in enumerate(collections):
            self._table.setItem(row, 0, QTableWidgetItem(coll["name"]))
            self._table.setItem(row, 1, QTableWidgetItem(coll["type"]))
            self._table.setItem(
                row, 2, QTableWidgetItem(str(coll["source_count"]))
            )
            self._table.setItem(
                row, 3, QTableWidgetItem(str(coll["chunk_count"]))
            )
            self._table.setItem(
                row, 4, QTableWidgetItem(coll["last_indexed"] or "Never")
            )

    def _delete_collection(self) -> None:
        """Delete the selected collection after confirmation."""
        row = self._table.currentRow()
        if row < 0:
            QMessageBox.information(
                self, "No Selection", "Select a collection to delete."
            )
            return

        name_item = self._table.item(row, 0)
        if not name_item:
            return
        name = name_item.text()

        reply = QMessageBox.question(
            self,
            "Confirm Deletion",
            f'Delete collection "{name}" and all its data?\n\n'
            "This will remove all sources, documents, and embeddings.",
            QMessageBox.StandardButton.Yes | QMessageBox.StandardButton.No,
            QMessageBox.StandardButton.No,
        )
        if reply != QMessageBox.StandardButton.Yes:
            return

        try:
            config = self._config_service.load()
            from local_rag.db import get_connection, init_db

            conn = get_connection(config)
            init_db(conn, config)
            try:
                # Get collection id
                row_data = conn.execute(
                    "SELECT id FROM collections WHERE name = ?", (name,)
                ).fetchone()
                if not row_data:
                    QMessageBox.warning(
                        self, "Not Found", f'Collection "{name}" not found.'
                    )
                    return

                coll_id = row_data["id"]

                # Delete vector embeddings first (not covered by CASCADE)
                conn.execute(
                    "DELETE FROM vec_documents WHERE document_id IN "
                    "(SELECT id FROM documents WHERE collection_id = ?)",
                    (coll_id,),
                )

                # Delete collection (CASCADE removes sources and documents)
                conn.execute("DELETE FROM collections WHERE id = ?", (coll_id,))
                conn.commit()
                logger.info("Deleted collection: %s", name)
            finally:
                conn.close()

            self._refresh()
        except Exception:
            logger.exception("Failed to delete collection: %s", name)
            QMessageBox.warning(
                self,
                "Deletion Failed",
                f'Failed to delete collection "{name}". Check the logs.',
            )
