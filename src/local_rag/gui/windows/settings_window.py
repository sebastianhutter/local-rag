"""Settings window for local-rag GUI."""

import logging
import threading
from pathlib import Path

from PySide6.QtCore import Qt, Signal
from PySide6.QtWidgets import (
    QCheckBox,
    QComboBox,
    QDialogButtonBox,
    QDoubleSpinBox,
    QFileDialog,
    QFormLayout,
    QGroupBox,
    QHBoxLayout,
    QHeaderView,
    QLabel,
    QLineEdit,
    QListWidget,
    QListWidgetItem,
    QMessageBox,
    QPlainTextEdit,
    QPushButton,
    QSpinBox,
    QTabWidget,
    QTableWidget,
    QTableWidgetItem,
    QTreeWidget,
    QTreeWidgetItem,
    QVBoxLayout,
    QWidget,
)

from local_rag.config import Config, save_config
from local_rag.services import ConfigService, StatusService

logger = logging.getLogger(__name__)


class SettingsWindow(QWidget):
    """Configuration settings window with tabbed interface."""

    # Emitted from background thread with (summary_text, collection_data)
    _collections_loaded = Signal(str, list)

    def __init__(self, parent: QWidget | None = None) -> None:
        super().__init__(parent)
        self.setWindowTitle("local-rag Settings")
        self.resize(700, 500)
        self.setWindowFlags(Qt.WindowType.Window)

        self._config_service = ConfigService()
        self._config = self._config_service.load()

        self._collections_loaded.connect(self._on_collections_loaded)

        self._build_ui()
        self._populate_fields()

    def _build_ui(self) -> None:
        """Build the complete UI layout."""
        layout = QVBoxLayout(self)

        self._tabs = QTabWidget()
        layout.addWidget(self._tabs)

        self._build_general_tab()
        self._build_sources_tab()
        self._build_code_groups_tab()
        self._build_search_tab()
        self._build_mcp_tab()
        self._build_collections_tab()

        # Bottom buttons
        button_box = QDialogButtonBox(
            QDialogButtonBox.StandardButton.Save
            | QDialogButtonBox.StandardButton.Cancel
        )
        button_box.accepted.connect(self._save)
        button_box.rejected.connect(self.close)
        layout.addWidget(button_box)

    # -- General tab ----------------------------------------------------------

    def _build_general_tab(self) -> None:
        """Build the General settings tab."""
        tab = QWidget()
        layout = QVBoxLayout(tab)

        group = QGroupBox("Database & Embedding")
        group_layout = QVBoxLayout(group)

        # DB path — full-width row like Sources tab
        db_row = QHBoxLayout()
        db_row.addWidget(QLabel("DB path:"))
        self._db_path_edit = QLineEdit()
        db_row.addWidget(self._db_path_edit, 1)
        db_browse = QPushButton("Browse...")
        db_browse.clicked.connect(self._browse_db_path)
        db_row.addWidget(db_browse)
        group_layout.addLayout(db_row)

        # Embedding model + dimensions in a form below
        form = QFormLayout()
        self._embedding_model_combo = QComboBox()
        self._embedding_model_combo.addItems(
            ["bge-m3", "mxbai-embed-large", "nomic-embed-text"]
        )
        form.addRow("Embedding model:", self._embedding_model_combo)

        self._embedding_dims_spin = QSpinBox()
        self._embedding_dims_spin.setRange(256, 4096)
        self._embedding_dims_spin.setSingleStep(256)
        form.addRow("Embedding dimensions:", self._embedding_dims_spin)
        group_layout.addLayout(form)

        layout.addWidget(group)

        chunk_group = QGroupBox("Chunking")
        chunk_form = QFormLayout(chunk_group)

        self._chunk_size_spin = QSpinBox()
        self._chunk_size_spin.setRange(100, 2000)
        chunk_form.addRow("Chunk size (tokens):", self._chunk_size_spin)

        self._chunk_overlap_spin = QSpinBox()
        self._chunk_overlap_spin.setRange(0, 500)
        chunk_form.addRow("Chunk overlap (tokens):", self._chunk_overlap_spin)

        layout.addWidget(chunk_group)
        layout.addStretch()

        self._tabs.addTab(tab, "General")

    def _browse_db_path(self) -> None:
        """Open a directory picker for the DB path."""
        path = QFileDialog.getExistingDirectory(self, "Select database directory")
        if path:
            self._db_path_edit.setText(path)

    # -- Sources tab ----------------------------------------------------------

    def _build_sources_tab(self) -> None:
        """Build the Sources tab."""
        tab = QWidget()
        layout = QVBoxLayout(tab)

        # Obsidian vaults
        obsidian_group = QGroupBox("Obsidian Vaults")
        obsidian_layout = QVBoxLayout(obsidian_group)
        self._obsidian_list = QListWidget()
        self._obsidian_list.setMaximumHeight(90)
        obsidian_layout.addWidget(self._obsidian_list)
        obsidian_buttons = QHBoxLayout()
        add_vault_btn = QPushButton("Add...")
        add_vault_btn.clicked.connect(self._add_obsidian_vault)
        remove_vault_btn = QPushButton("Remove")
        remove_vault_btn.clicked.connect(
            lambda: self._remove_selected(self._obsidian_list)
        )
        obsidian_buttons.addWidget(add_vault_btn)
        obsidian_buttons.addWidget(remove_vault_btn)
        obsidian_buttons.addStretch()
        obsidian_layout.addLayout(obsidian_buttons)

        # Exclude folders
        obsidian_layout.addWidget(QLabel("Exclude folders:"))
        self._obsidian_exclude_list = QListWidget()
        self._obsidian_exclude_list.setMaximumHeight(60)
        obsidian_layout.addWidget(self._obsidian_exclude_list)
        exclude_buttons = QHBoxLayout()
        add_exclude_btn = QPushButton("Add...")
        add_exclude_btn.clicked.connect(self._add_obsidian_exclude)
        remove_exclude_btn = QPushButton("Remove")
        remove_exclude_btn.clicked.connect(
            lambda: self._remove_selected(self._obsidian_exclude_list)
        )
        exclude_buttons.addWidget(add_exclude_btn)
        exclude_buttons.addWidget(remove_exclude_btn)
        exclude_buttons.addStretch()
        obsidian_layout.addLayout(exclude_buttons)

        layout.addWidget(obsidian_group)

        # eM Client path
        email_group = QGroupBox("eM Client")
        email_layout = QVBoxLayout(email_group)
        email_row = QHBoxLayout()
        self._emclient_path_edit = QLineEdit()
        emclient_browse = QPushButton("Browse...")
        emclient_browse.clicked.connect(self._browse_emclient_path)
        email_row.addWidget(QLabel("Database path:"))
        email_row.addWidget(self._emclient_path_edit, 1)
        email_row.addWidget(emclient_browse)
        email_layout.addLayout(email_row)
        layout.addWidget(email_group)

        # Calibre libraries
        calibre_group = QGroupBox("Calibre Libraries")
        calibre_layout = QVBoxLayout(calibre_group)
        self._calibre_list = QListWidget()
        self._calibre_list.setMaximumHeight(90)
        calibre_layout.addWidget(self._calibre_list)
        calibre_buttons = QHBoxLayout()
        add_calibre_btn = QPushButton("Add...")
        add_calibre_btn.clicked.connect(self._add_calibre_library)
        remove_calibre_btn = QPushButton("Remove")
        remove_calibre_btn.clicked.connect(
            lambda: self._remove_selected(self._calibre_list)
        )
        calibre_buttons.addWidget(add_calibre_btn)
        calibre_buttons.addWidget(remove_calibre_btn)
        calibre_buttons.addStretch()
        calibre_layout.addLayout(calibre_buttons)
        layout.addWidget(calibre_group)

        # NetNewsWire path
        nnw_group = QGroupBox("NetNewsWire")
        nnw_layout = QVBoxLayout(nnw_group)
        nnw_row = QHBoxLayout()
        self._nnw_path_edit = QLineEdit()
        nnw_browse = QPushButton("Browse...")
        nnw_browse.clicked.connect(self._browse_nnw_path)
        nnw_row.addWidget(QLabel("Database path:"))
        nnw_row.addWidget(self._nnw_path_edit, 1)
        nnw_row.addWidget(nnw_browse)
        nnw_layout.addLayout(nnw_row)
        layout.addWidget(nnw_group)

        layout.addStretch()
        self._tabs.addTab(tab, "Sources")

    def _add_obsidian_vault(self) -> None:
        """Add an Obsidian vault directory."""
        path = QFileDialog.getExistingDirectory(self, "Select Obsidian vault")
        if path:
            self._obsidian_list.addItem(path)

    def _add_calibre_library(self) -> None:
        """Add a Calibre library directory."""
        path = QFileDialog.getExistingDirectory(self, "Select Calibre library")
        if path:
            self._calibre_list.addItem(path)

    def _browse_emclient_path(self) -> None:
        """Browse for eM Client database directory."""
        path = QFileDialog.getExistingDirectory(self, "Select eM Client directory")
        if path:
            self._emclient_path_edit.setText(path)

    def _browse_nnw_path(self) -> None:
        """Browse for NetNewsWire database directory."""
        path = QFileDialog.getExistingDirectory(
            self, "Select NetNewsWire accounts directory"
        )
        if path:
            self._nnw_path_edit.setText(path)

    @staticmethod
    def _remove_selected(list_widget: QListWidget) -> None:
        """Remove selected items from a QListWidget."""
        for item in list_widget.selectedItems():
            list_widget.takeItem(list_widget.row(item))

    def _add_obsidian_exclude(self) -> None:
        """Add an Obsidian exclude folder name."""
        from PySide6.QtWidgets import QInputDialog

        name, ok = QInputDialog.getText(self, "Exclude Folder", "Folder name:")
        if ok and name.strip():
            self._obsidian_exclude_list.addItem(name.strip())

    # -- Code Groups tab ------------------------------------------------------

    def _build_code_groups_tab(self) -> None:
        """Build the Code Groups tab."""
        tab = QWidget()
        layout = QVBoxLayout(tab)

        self._code_tree = QTreeWidget()
        self._code_tree.setHeaderLabels(["Group", "Path"])
        self._code_tree.setColumnWidth(0, 200)
        layout.addWidget(self._code_tree)

        buttons = QHBoxLayout()
        add_group_btn = QPushButton("Add Group")
        add_group_btn.clicked.connect(self._add_code_group)
        add_repo_btn = QPushButton("Add Repo")
        add_repo_btn.clicked.connect(self._add_code_repo)
        remove_btn = QPushButton("Remove")
        remove_btn.clicked.connect(self._remove_code_item)
        buttons.addWidget(add_group_btn)
        buttons.addWidget(add_repo_btn)
        buttons.addWidget(remove_btn)
        buttons.addStretch()
        layout.addLayout(buttons)

        # Git settings
        git_group = QGroupBox("Git Settings")
        git_layout = QVBoxLayout(git_group)

        history_row = QHBoxLayout()
        history_row.addWidget(QLabel("Commit history:"))
        self._git_history_spin = QSpinBox()
        self._git_history_spin.setRange(1, 120)
        self._git_history_spin.setSuffix(" months")
        history_row.addWidget(self._git_history_spin)
        history_row.addStretch()
        git_layout.addLayout(history_row)

        git_layout.addWidget(QLabel("Ignore commits matching:"))
        self._git_blacklist_list = QListWidget()
        self._git_blacklist_list.setMaximumHeight(60)
        git_layout.addWidget(self._git_blacklist_list)
        blacklist_buttons = QHBoxLayout()
        add_blacklist_btn = QPushButton("Add...")
        add_blacklist_btn.clicked.connect(self._add_git_blacklist)
        remove_blacklist_btn = QPushButton("Remove")
        remove_blacklist_btn.clicked.connect(
            lambda: self._remove_selected(self._git_blacklist_list)
        )
        blacklist_buttons.addWidget(add_blacklist_btn)
        blacklist_buttons.addWidget(remove_blacklist_btn)
        blacklist_buttons.addStretch()
        git_layout.addLayout(blacklist_buttons)

        layout.addWidget(git_group)

        layout.addStretch()
        self._tabs.addTab(tab, "Code Groups")

    def _add_code_group(self) -> None:
        """Add a new code group."""
        from PySide6.QtWidgets import QInputDialog

        name, ok = QInputDialog.getText(self, "New Code Group", "Group name:")
        if ok and name.strip():
            item = QTreeWidgetItem([name.strip(), ""])
            self._code_tree.addTopLevelItem(item)
            item.setExpanded(True)

    def _add_code_repo(self) -> None:
        """Add a repository path to the selected code group."""
        current = self._code_tree.currentItem()
        if not current:
            QMessageBox.information(
                self, "No Selection", "Select a group to add a repository to."
            )
            return

        # Navigate to top-level parent if a child is selected
        parent = current
        while parent.parent():
            parent = parent.parent()

        path = QFileDialog.getExistingDirectory(self, "Select repository directory")
        if path:
            child = QTreeWidgetItem(["", path])
            parent.addChild(child)
            parent.setExpanded(True)

    def _remove_code_item(self) -> None:
        """Remove the selected code group or repo."""
        current = self._code_tree.currentItem()
        if not current:
            return

        if current.parent():
            current.parent().removeChild(current)
        else:
            index = self._code_tree.indexOfTopLevelItem(current)
            self._code_tree.takeTopLevelItem(index)

    def _add_git_blacklist(self) -> None:
        """Add a git commit subject blacklist entry."""
        from PySide6.QtWidgets import QInputDialog

        text, ok = QInputDialog.getText(
            self, "Ignore Commits", "Commit subject pattern:"
        )
        if ok and text.strip():
            self._git_blacklist_list.addItem(text.strip())

    # -- Search tab -----------------------------------------------------------

    def _build_search_tab(self) -> None:
        """Build the Search defaults tab."""
        tab = QWidget()
        layout = QVBoxLayout(tab)

        group = QGroupBox("Search Defaults")
        form = QFormLayout(group)

        self._top_k_spin = QSpinBox()
        self._top_k_spin.setRange(1, 100)
        form.addRow("Top K:", self._top_k_spin)

        self._rrf_k_spin = QSpinBox()
        self._rrf_k_spin.setRange(1, 200)
        form.addRow("RRF K:", self._rrf_k_spin)

        self._vector_weight_spin = QDoubleSpinBox()
        self._vector_weight_spin.setRange(0.0, 1.0)
        self._vector_weight_spin.setSingleStep(0.1)
        form.addRow("Vector weight:", self._vector_weight_spin)

        self._fts_weight_spin = QDoubleSpinBox()
        self._fts_weight_spin.setRange(0.0, 1.0)
        self._fts_weight_spin.setSingleStep(0.1)
        form.addRow("FTS weight:", self._fts_weight_spin)

        layout.addWidget(group)
        layout.addStretch()

        self._tabs.addTab(tab, "Search")

    # -- MCP & Scheduling tab -------------------------------------------------

    def _build_mcp_tab(self) -> None:
        """Build the MCP & Scheduling tab."""
        tab = QWidget()
        layout = QVBoxLayout(tab)

        mcp_group = QGroupBox("MCP Server")
        mcp_form = QFormLayout(mcp_group)

        self._auto_start_mcp_check = QCheckBox("Auto-start MCP server")
        mcp_form.addRow(self._auto_start_mcp_check)

        self._mcp_port_spin = QSpinBox()
        self._mcp_port_spin.setRange(1024, 65535)
        mcp_form.addRow("Port:", self._mcp_port_spin)

        layout.addWidget(mcp_group)

        # MCP registration help
        reg_group = QGroupBox("MCP Registration")
        reg_layout = QVBoxLayout(reg_group)

        from local_rag.services.mcp_service import _PROJECT_DIR

        project_dir = str(_PROJECT_DIR)
        port = self._config.gui.mcp_port

        help_text = (
            "WITH GUI (SSE)\n"
            "The GUI runs the MCP server on port {port}.\n"
            "\n"
            "Claude Code — run in your project directory:\n"
            "\n"
            "claude mcp add --transport sse \\\n"
            "  local-rag http://localhost:{port}/sse\n"
            "\n"
            "Claude Desktop — add to\n"
            "~/Library/Application Support/Claude/\n"
            "claude_desktop_config.json:\n"
            "\n"
            '{{\n'
            '  "mcpServers": {{\n'
            '    "local-rag": {{\n'
            '      "command": "uvx",\n'
            '      "args": [\n'
            '        "mcp-proxy",\n'
            '        "http://localhost:{port}/sse"\n'
            '      ]\n'
            '    }}\n'
            '  }}\n'
            '}}\n'
            "\n"
            "─────────────────────────────────────────────\n"
            "\n"
            "WITHOUT GUI (stdio)\n"
            "Claude manages the server process directly.\n"
            "\n"
            "Claude Code — run in your project directory:\n"
            "\n"
            "claude mcp add local-rag -- \\\n"
            "  uv run --directory {project_dir} \\\n"
            "  local-rag serve\n"
            "\n"
            "Claude Desktop — add to\n"
            "~/Library/Application Support/Claude/\n"
            "claude_desktop_config.json:\n"
            "\n"
            '{{\n'
            '  "mcpServers": {{\n'
            '    "local-rag": {{\n'
            '      "command": "uv",\n'
            '      "args": [\n'
            '        "run", "--directory",\n'
            '        "{project_dir}",\n'
            '        "local-rag", "serve"\n'
            '      ]\n'
            '    }}\n'
            '  }}\n'
            '}}'
        ).format(port=port, project_dir=project_dir)

        from PySide6.QtGui import QFont

        self._reg_help_text = QPlainTextEdit()
        self._reg_help_text.setReadOnly(True)
        self._reg_help_text.setPlainText(help_text)
        self._reg_help_text.setFont(QFont("Menlo", 11))
        reg_layout.addWidget(self._reg_help_text)

        layout.addWidget(reg_group)

        sched_group = QGroupBox("Scheduling")
        sched_form = QFormLayout(sched_group)

        self._auto_reindex_check = QCheckBox("Auto-reindex on schedule")
        self._auto_reindex_check.toggled.connect(self._on_auto_reindex_toggled)
        sched_form.addRow(self._auto_reindex_check)

        self._reindex_interval_spin = QSpinBox()
        self._reindex_interval_spin.setRange(1, 168)
        self._reindex_interval_spin.setSuffix(" hours")
        self._reindex_interval_spin.setEnabled(False)
        sched_form.addRow("Reindex every:", self._reindex_interval_spin)

        self._start_on_login_check = QCheckBox("Start on login")
        sched_form.addRow(self._start_on_login_check)

        layout.addWidget(sched_group)
        layout.addStretch()

        self._tabs.addTab(tab, "MCP & Scheduling")

    def _on_auto_reindex_toggled(self, checked: bool) -> None:
        """Enable/disable the interval spinbox based on the checkbox."""
        self._reindex_interval_spin.setEnabled(checked)

    # -- Collections tab ------------------------------------------------------

    def _build_collections_tab(self) -> None:
        """Build the Collections tab with stats, enable/disable, and delete."""
        tab = QWidget()
        layout = QVBoxLayout(tab)

        self._status_service = StatusService()

        # Summary bar
        self._coll_summary_label = QLabel()
        layout.addWidget(self._coll_summary_label)

        layout.addWidget(
            QLabel("Uncheck a collection to disable it from indexing.")
        )

        # Collections table (replaces the old QListWidget + separate dashboard)
        self._coll_table = QTableWidget()
        self._coll_table.setColumnCount(5)
        self._coll_table.setHorizontalHeaderLabels(
            ["Enabled", "Name", "Type", "Chunks", "Last Indexed"]
        )
        self._coll_table.setSelectionBehavior(QTableWidget.SelectionBehavior.SelectRows)
        self._coll_table.setSelectionMode(QTableWidget.SelectionMode.SingleSelection)
        self._coll_table.setEditTriggers(QTableWidget.EditTrigger.NoEditTriggers)
        header = self._coll_table.horizontalHeader()
        if header:
            header.setStretchLastSection(True)
            header.setSectionResizeMode(1, QHeaderView.ResizeMode.Stretch)
        layout.addWidget(self._coll_table)

        # Buttons
        buttons = QHBoxLayout()
        refresh_btn = QPushButton("Refresh")
        refresh_btn.clicked.connect(self._refresh_collections)
        delete_btn = QPushButton("Delete Collection")
        delete_btn.clicked.connect(self._delete_collection)
        buttons.addWidget(refresh_btn)
        buttons.addWidget(delete_btn)
        buttons.addStretch()
        layout.addLayout(buttons)

        self._tabs.addTab(tab, "Collections")

    # -- Populate / Save ------------------------------------------------------

    def _populate_fields(self) -> None:
        """Populate all UI fields from the loaded config."""
        cfg = self._config

        # General
        self._db_path_edit.setText(str(cfg.db_path))
        idx = self._embedding_model_combo.findText(cfg.embedding_model)
        if idx >= 0:
            self._embedding_model_combo.setCurrentIndex(idx)
        self._embedding_dims_spin.setValue(cfg.embedding_dimensions)
        self._chunk_size_spin.setValue(cfg.chunk_size_tokens)
        self._chunk_overlap_spin.setValue(cfg.chunk_overlap_tokens)

        # Sources
        for vault in cfg.obsidian_vaults:
            self._obsidian_list.addItem(str(vault))
        for folder in cfg.obsidian_exclude_folders:
            self._obsidian_exclude_list.addItem(folder)
        self._emclient_path_edit.setText(str(cfg.emclient_db_path))
        for lib in cfg.calibre_libraries:
            self._calibre_list.addItem(str(lib))
        self._nnw_path_edit.setText(str(cfg.netnewswire_db_path))

        # Code Groups
        for group_name, paths in cfg.code_groups.items():
            group_item = QTreeWidgetItem([group_name, ""])
            self._code_tree.addTopLevelItem(group_item)
            for p in paths:
                child = QTreeWidgetItem(["", str(p)])
                group_item.addChild(child)
            group_item.setExpanded(True)
        self._git_history_spin.setValue(cfg.git_history_in_months)
        for entry in cfg.git_commit_subject_blacklist:
            self._git_blacklist_list.addItem(entry)

        # Search
        self._top_k_spin.setValue(cfg.search_defaults.top_k)
        self._rrf_k_spin.setValue(cfg.search_defaults.rrf_k)
        self._vector_weight_spin.setValue(cfg.search_defaults.vector_weight)
        self._fts_weight_spin.setValue(cfg.search_defaults.fts_weight)

        # MCP & Scheduling
        self._auto_start_mcp_check.setChecked(cfg.gui.auto_start_mcp)
        self._mcp_port_spin.setValue(cfg.gui.mcp_port)
        self._auto_reindex_check.setChecked(cfg.gui.auto_reindex)
        self._reindex_interval_spin.setValue(cfg.gui.auto_reindex_interval_hours)
        self._reindex_interval_spin.setEnabled(cfg.gui.auto_reindex)
        self._start_on_login_check.setChecked(cfg.gui.start_on_login)

        # Collections
        self._refresh_collections()

    def _save(self) -> None:
        """Read all fields back into config and save to disk."""
        cfg = self._config

        # General
        db_text = self._db_path_edit.text().strip()
        if db_text:
            cfg.db_path = Path(db_text)
        cfg.embedding_model = self._embedding_model_combo.currentText()
        cfg.embedding_dimensions = self._embedding_dims_spin.value()
        cfg.chunk_size_tokens = self._chunk_size_spin.value()
        cfg.chunk_overlap_tokens = self._chunk_overlap_spin.value()

        # Sources - Obsidian vaults
        cfg.obsidian_vaults = [
            Path(self._obsidian_list.item(i).text())
            for i in range(self._obsidian_list.count())
        ]
        cfg.obsidian_exclude_folders = [
            self._obsidian_exclude_list.item(i).text()
            for i in range(self._obsidian_exclude_list.count())
        ]

        # Sources - eM Client
        emclient_text = self._emclient_path_edit.text().strip()
        if emclient_text:
            cfg.emclient_db_path = Path(emclient_text)

        # Sources - Calibre
        cfg.calibre_libraries = [
            Path(self._calibre_list.item(i).text())
            for i in range(self._calibre_list.count())
        ]

        # Sources - NetNewsWire
        nnw_text = self._nnw_path_edit.text().strip()
        if nnw_text:
            cfg.netnewswire_db_path = Path(nnw_text)

        # Code groups
        code_groups: dict[str, list[Path]] = {}
        for i in range(self._code_tree.topLevelItemCount()):
            group_item = self._code_tree.topLevelItem(i)
            if group_item is None:
                continue
            group_name = group_item.text(0)
            paths: list[Path] = []
            for j in range(group_item.childCount()):
                child = group_item.child(j)
                if child and child.text(1):
                    paths.append(Path(child.text(1)))
            code_groups[group_name] = paths
        cfg.code_groups = code_groups
        cfg.git_history_in_months = self._git_history_spin.value()
        cfg.git_commit_subject_blacklist = [
            self._git_blacklist_list.item(i).text()
            for i in range(self._git_blacklist_list.count())
        ]

        # Search
        cfg.search_defaults.top_k = self._top_k_spin.value()
        cfg.search_defaults.rrf_k = self._rrf_k_spin.value()
        cfg.search_defaults.vector_weight = self._vector_weight_spin.value()
        cfg.search_defaults.fts_weight = self._fts_weight_spin.value()

        # MCP & Scheduling
        cfg.gui.auto_start_mcp = self._auto_start_mcp_check.isChecked()
        cfg.gui.mcp_port = self._mcp_port_spin.value()
        cfg.gui.auto_reindex = self._auto_reindex_check.isChecked()
        cfg.gui.auto_reindex_interval_hours = self._reindex_interval_spin.value()
        cfg.gui.start_on_login = self._start_on_login_check.isChecked()

        # Collections - disabled set from the Enabled checkbox column
        disabled: set[str] = set()
        for i in range(self._coll_table.rowCount()):
            checkbox_item = self._coll_table.item(i, 0)
            name_item = self._coll_table.item(i, 1)
            if checkbox_item and name_item:
                if checkbox_item.checkState() == Qt.CheckState.Unchecked:
                    disabled.add(name_item.text())
        cfg.disabled_collections = disabled

        save_config(cfg)
        logger.info("Settings saved")
        self.close()

    def _refresh_collections(self) -> None:
        """Kick off a background thread to load collection data."""
        self._coll_summary_label.setText("Loading...")
        threading.Thread(target=self._fetch_collections, daemon=True).start()

    def _fetch_collections(self) -> None:
        """Fetch collection data in a background thread and emit signal."""
        try:
            self._fetch_collections_inner()
        except Exception:
            logger.exception("Error fetching collection data")
            self._collections_loaded.emit("Error loading collections", [])

    def _fetch_collections_inner(self) -> None:
        """Inner implementation of collection fetching."""
        cfg = self._config

        # Build summary text
        try:
            overview = self._status_service.get_overview(cfg)
            ollama_ok = self._status_service.check_ollama()
            ollama_status = "OK" if ollama_ok else "Not running"
            summary = (
                f"DB: {overview['db_size_mb']} MB  |  "
                f"{overview['collection_count']} collections  |  "
                f"{overview['chunk_count']:,} chunks  |  "
                f"Ollama: {ollama_status}"
            )
        except Exception:
            logger.debug("Failed to get status overview", exc_info=True)
            summary = "Status unavailable"

        # Get all known collection names (from config + DB)
        all_names = self._config_service.get_all_collection_names(cfg)

        # Get stats from DB
        try:
            collections = self._status_service.get_collections(cfg)
            coll_map = {c["name"]: c for c in collections}
        except Exception:
            logger.debug("Failed to get collection stats", exc_info=True)
            coll_map = {}

        # Build row data: list of (name, type, chunks, last_indexed)
        rows = []
        for name in all_names:
            info = coll_map.get(name, {})
            rows.append({
                "name": name,
                "type": info.get("type", "-"),
                "chunks": info.get("chunk_count", 0),
                "last_indexed": info.get("last_indexed") or "Never",
            })

        # Signal back to the main thread
        self._collections_loaded.emit(summary, rows)

    def _on_collections_loaded(self, summary: str, rows: list) -> None:
        """Update the Collections tab UI from fetched data (main thread)."""
        cfg = self._config
        self._coll_summary_label.setText(summary)

        self._coll_table.setRowCount(len(rows))
        for i, row_data in enumerate(rows):
            name = row_data["name"]

            # Enabled checkbox
            checkbox_item = QTableWidgetItem()
            checkbox_item.setFlags(
                Qt.ItemFlag.ItemIsUserCheckable | Qt.ItemFlag.ItemIsEnabled
            )
            if name not in cfg.disabled_collections:
                checkbox_item.setCheckState(Qt.CheckState.Checked)
            else:
                checkbox_item.setCheckState(Qt.CheckState.Unchecked)
            self._coll_table.setItem(i, 0, checkbox_item)

            # Name
            self._coll_table.setItem(i, 1, QTableWidgetItem(name))

            # Stats
            self._coll_table.setItem(
                i, 2, QTableWidgetItem(row_data["type"])
            )
            chunks = row_data["chunks"]
            self._coll_table.setItem(
                i, 3, QTableWidgetItem(str(chunks) if chunks else "-")
            )
            self._coll_table.setItem(
                i, 4, QTableWidgetItem(row_data["last_indexed"])
            )

    def _delete_collection(self) -> None:
        """Delete the selected collection after confirmation."""
        row = self._coll_table.currentRow()
        if row < 0:
            QMessageBox.information(
                self, "No Selection", "Select a collection to delete."
            )
            return

        name_item = self._coll_table.item(row, 1)
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
            from local_rag.db import get_connection, init_db

            conn = get_connection(self._config)
            init_db(conn, self._config)
            try:
                row_data = conn.execute(
                    "SELECT id FROM collections WHERE name = ?", (name,)
                ).fetchone()
                if not row_data:
                    QMessageBox.warning(
                        self, "Not Found", f'Collection "{name}" not found in database.'
                    )
                    return

                coll_id = row_data["id"]
                conn.execute(
                    "DELETE FROM vec_documents WHERE document_id IN "
                    "(SELECT id FROM documents WHERE collection_id = ?)",
                    (coll_id,),
                )
                conn.execute("DELETE FROM collections WHERE id = ?", (coll_id,))
                conn.commit()
                logger.info("Deleted collection: %s", name)
            finally:
                conn.close()

            self._refresh_collections()
        except Exception as e:
            logger.exception("Failed to delete collection: %s", name)
            import sqlite3
            if isinstance(e, sqlite3.OperationalError) and "locked" in str(e).lower():
                msg = (
                    f'Cannot delete "{name}" — the database is locked.\n\n'
                    "This usually means indexing is in progress. "
                    "Wait for it to finish and try again."
                )
            else:
                msg = f'Failed to delete collection "{name}":\n{e}'
            QMessageBox.warning(self, "Deletion Failed", msg)
