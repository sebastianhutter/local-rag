"""Settings window for local-rag GUI."""

import logging
from pathlib import Path

from PySide6.QtCore import Qt
from PySide6.QtWidgets import (
    QCheckBox,
    QComboBox,
    QDialogButtonBox,
    QDoubleSpinBox,
    QFileDialog,
    QFormLayout,
    QGroupBox,
    QHBoxLayout,
    QLabel,
    QLineEdit,
    QListWidget,
    QListWidgetItem,
    QMessageBox,
    QPushButton,
    QSpinBox,
    QTabWidget,
    QTreeWidget,
    QTreeWidgetItem,
    QVBoxLayout,
    QWidget,
)

from local_rag.config import Config, save_config
from local_rag.services import ConfigService, MCPService

logger = logging.getLogger(__name__)


class SettingsWindow(QWidget):
    """Configuration settings window with tabbed interface."""

    def __init__(self, parent: QWidget | None = None) -> None:
        super().__init__(parent)
        self.setWindowTitle("local-rag Settings")
        self.resize(700, 500)
        self.setWindowFlags(Qt.WindowType.Window)

        self._config_service = ConfigService()
        self._mcp_service = MCPService()
        self._config = self._config_service.load()

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

        self._mcp_transport_combo = QComboBox()
        self._mcp_transport_combo.addItems(["stdio", "sse"])
        mcp_form.addRow("Transport:", self._mcp_transport_combo)

        self._mcp_port_spin = QSpinBox()
        self._mcp_port_spin.setRange(1024, 65535)
        mcp_form.addRow("Port:", self._mcp_port_spin)

        register_desktop_btn = QPushButton("Register with Claude Desktop")
        register_desktop_btn.clicked.connect(self._register_claude_desktop)
        mcp_form.addRow(register_desktop_btn)

        register_code_btn = QPushButton("Register with Claude Code...")
        register_code_btn.clicked.connect(self._register_claude_code)
        mcp_form.addRow(register_code_btn)

        layout.addWidget(mcp_group)

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

    def _register_claude_desktop(self) -> None:
        """Register local-rag with Claude Desktop."""
        if self._mcp_service.register_claude_desktop():
            QMessageBox.information(
                self,
                "Registered",
                "local-rag has been registered with Claude Desktop.",
            )
        else:
            QMessageBox.warning(
                self,
                "Registration Failed",
                "Failed to register with Claude Desktop. Check the logs.",
            )

    def _register_claude_code(self) -> None:
        """Register local-rag with Claude Code for a project directory."""
        path = QFileDialog.getExistingDirectory(
            self, "Select project directory for Claude Code"
        )
        if path:
            if self._mcp_service.register_claude_code(Path(path)):
                QMessageBox.information(
                    self,
                    "Registered",
                    f"local-rag has been registered with Claude Code in:\n{path}",
                )
            else:
                QMessageBox.warning(
                    self,
                    "Registration Failed",
                    "Failed to register with Claude Code. Check the logs.",
                )

    def _on_auto_reindex_toggled(self, checked: bool) -> None:
        """Enable/disable the interval spinbox based on the checkbox."""
        self._reindex_interval_spin.setEnabled(checked)

    # -- Collections tab ------------------------------------------------------

    def _build_collections_tab(self) -> None:
        """Build the Collections enable/disable tab."""
        tab = QWidget()
        layout = QVBoxLayout(tab)

        layout.addWidget(
            QLabel("Uncheck a collection to disable it from indexing.")
        )

        self._collections_list = QListWidget()
        layout.addWidget(self._collections_list)

        layout.addStretch()
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
        idx = self._mcp_transport_combo.findText(cfg.gui.mcp_transport)
        if idx >= 0:
            self._mcp_transport_combo.setCurrentIndex(idx)
        self._mcp_port_spin.setValue(cfg.gui.mcp_port)
        self._auto_reindex_check.setChecked(cfg.gui.auto_reindex)
        self._reindex_interval_spin.setValue(cfg.gui.auto_reindex_interval_hours)
        self._reindex_interval_spin.setEnabled(cfg.gui.auto_reindex)
        self._start_on_login_check.setChecked(cfg.gui.start_on_login)

        # Collections
        all_names = self._config_service.get_all_collection_names(cfg)
        for name in all_names:
            item = QListWidgetItem(name)
            item.setFlags(item.flags() | Qt.ItemFlag.ItemIsUserCheckable)
            if name not in cfg.disabled_collections:
                item.setCheckState(Qt.CheckState.Checked)
            else:
                item.setCheckState(Qt.CheckState.Unchecked)
            self._collections_list.addItem(item)

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
        cfg.gui.mcp_transport = self._mcp_transport_combo.currentText()
        cfg.gui.mcp_port = self._mcp_port_spin.value()
        cfg.gui.auto_reindex = self._auto_reindex_check.isChecked()
        cfg.gui.auto_reindex_interval_hours = self._reindex_interval_spin.value()
        cfg.gui.start_on_login = self._start_on_login_check.isChecked()

        # Collections - disabled set
        disabled: set[str] = set()
        for i in range(self._collections_list.count()):
            item = self._collections_list.item(i)
            if item and item.checkState() == Qt.CheckState.Unchecked:
                disabled.add(item.text())
        cfg.disabled_collections = disabled

        save_config(cfg)
        logger.info("Settings saved")
        self.close()
