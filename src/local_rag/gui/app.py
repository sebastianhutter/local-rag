"""Main menu bar application for local-rag.

Uses rumps for the macOS menu bar and PySide6 for complex windows.
"""

import logging
import threading
from datetime import datetime, timezone

import rumps

from local_rag.config import Config, load_config
from local_rag.services.indexing_service import IndexingService
from local_rag.services.mcp_service import MCPService
from local_rag.services.status_service import StatusService

logger = logging.getLogger(__name__)


class LocalRAGApp(rumps.App):
    """Menu bar application for local-rag."""

    def __init__(self, log_handler=None) -> None:
        super().__init__(
            name="local-rag",
            title="RAG",
            quit_button=None,
        )
        self.config: Config = load_config()
        self.mcp_service = MCPService()
        self.status_service = StatusService()
        self.indexing_service = IndexingService()
        self._log_handler = log_handler

        # Track open windows to prevent garbage collection
        self._settings_window = None
        self._log_window = None
        self._progress_dialog = None
        self._index_worker = None
        self._indexing_label = None  # what's currently being indexed

        # Build menu
        self._mcp_item = rumps.MenuItem(
            "\U0001f534 MCP Server: Stopped", callback=self._toggle_mcp
        )
        self._status_item = rumps.MenuItem("Loading...", callback=None)
        self._status_item.set_callback(None)

        self._index_menu = rumps.MenuItem("Index")
        self._index_menu["All Collections"] = rumps.MenuItem(
            "All Collections", callback=self._index_all
        )
        self._index_menu[None] = rumps.separator  # separator in submenu

        # Add per-collection items
        for name in ["obsidian", "email", "calibre", "rss"]:
            self._index_menu[name] = rumps.MenuItem(
                name.capitalize(),
                callback=lambda sender, n=name: self._index_collection(n),
            )

        # Code groups from config
        for group_name in self.config.code_groups:
            self._index_menu[group_name] = rumps.MenuItem(
                group_name,
                callback=lambda sender, n=group_name: self._index_collection(n),
            )

        self.menu = [
            self._mcp_item,
            None,  # separator
            self._status_item,
            None,
            self._index_menu,
            None,
            rumps.MenuItem("Settings...", callback=self._open_settings),
            rumps.MenuItem("View Logs...", callback=self._open_logs),
            None,
            rumps.MenuItem("Quit", callback=self._quit),
        ]

        # Auto-start MCP if configured
        if self.config.gui.auto_start_mcp:
            self._start_mcp()

        # Status update timer (every 30s)
        self._status_timer = rumps.Timer(self._update_status, 30)
        self._status_timer.start()

        # MCP health check timer (every 5s)
        self._health_timer = rumps.Timer(self._check_mcp_health, 5)
        self._health_timer.start()

        # Scheduled reindex timer (skip first fire — rumps.Timer fires immediately on start)
        self._reindex_first_fire = True
        if self.config.gui.auto_reindex:
            interval_hours = self.config.gui.auto_reindex_interval_hours
            self._reindex_timer = rumps.Timer(
                self._scheduled_reindex, interval_hours * 3600
            )
            self._reindex_timer.start()

        # Initial status update
        self._update_status(None)

        # Register for macOS wake notifications
        self._register_wake_handler()

        # Start Qt event processing
        from local_rag.gui.qt_bridge import start_qt_timer
        start_qt_timer(self)

    # -- Wake notification ----------------------------------------------------

    def _register_wake_handler(self) -> None:
        """Subscribe to macOS wake-from-sleep notifications."""
        try:
            import objc
            from Foundation import NSObject
            from AppKit import NSWorkspace, NSWorkspaceDidWakeNotification

            class WakeObserver(NSObject):
                def initWithApp_(self, app):
                    self = objc.super(WakeObserver, self).init()
                    if self is not None:
                        self.app = app
                    return self

                def handleWake_(self, notification):
                    self.app._on_wake()

            self._wake_observer = WakeObserver.alloc().initWithApp_(self)
            NSWorkspace.sharedWorkspace().notificationCenter().addObserver_selector_name_object_(
                self._wake_observer,
                self._wake_observer.handleWake_,
                NSWorkspaceDidWakeNotification,
                None,
            )
            logger.info("Registered macOS wake notification handler")
        except ImportError:
            logger.warning("PyObjC not available, wake-based reindexing disabled")

    def _on_wake(self) -> None:
        """Handle macOS wake from sleep — check if reindex is overdue."""
        if not self.config.gui.auto_reindex:
            return

        try:
            overview = self.status_service.get_overview(self.config)
            last_indexed = overview.get("last_indexed")
            if not last_indexed:
                logger.info("No previous index found, triggering reindex on wake")
                self._run_indexing(None)
                return

            last_dt = datetime.fromisoformat(last_indexed).replace(tzinfo=timezone.utc)
            now = datetime.now(timezone.utc)
            hours_since = (now - last_dt).total_seconds() / 3600
            interval = self.config.gui.auto_reindex_interval_hours

            if hours_since >= interval:
                logger.info(
                    "Reindex overdue (%.1fh since last index, interval=%dh), triggering",
                    hours_since,
                    interval,
                )
                self._run_indexing(None)
            else:
                logger.debug(
                    "Reindex not due yet (%.1fh since last, interval=%dh)",
                    hours_since,
                    interval,
                )
        except Exception:
            logger.exception("Error checking reindex on wake")

    # -- MCP server -----------------------------------------------------------

    def _start_mcp(self) -> None:
        """Start the MCP server."""
        try:
            self.mcp_service.start(self.config)
            self._mcp_item.title = "\U0001f7e2 MCP Server: Running"
            logger.info("MCP server started")
        except Exception:
            logger.exception("Failed to start MCP server")
            self._mcp_item.title = "\U0001f534 MCP Server: Error"

    def _stop_mcp(self) -> None:
        """Stop the MCP server."""
        self.mcp_service.stop()
        self._mcp_item.title = "\U0001f534 MCP Server: Stopped"
        logger.info("MCP server stopped")

    def _toggle_mcp(self, sender: rumps.MenuItem) -> None:
        """Toggle MCP server on/off."""
        if self.mcp_service.is_running():
            self._stop_mcp()
        else:
            self._start_mcp()

    def _check_mcp_health(self, _timer) -> None:
        """Check if MCP server is still alive."""
        if "\U0001f7e2" in self._mcp_item.title:
            if not self.mcp_service.is_running():
                self._mcp_item.title = "\U0001f534 MCP Server: Stopped"
                logger.warning("MCP server process exited unexpectedly")

    # -- Status ---------------------------------------------------------------

    def _update_status(self, _timer) -> None:
        """Update the status line in the menu."""
        try:
            overview = self.status_service.get_overview(self.config)
            count = overview["collection_count"]
            chunks = overview["chunk_count"]
            self._status_item.title = f"{count} collections, {chunks:,} chunks"
        except Exception:
            self._status_item.title = "Status unavailable"

    # -- Indexing --------------------------------------------------------------

    def _is_indexing(self) -> bool:
        """Check if an indexing operation is in progress."""
        return self._index_worker is not None and self._index_worker.is_alive()

    def _set_indexing_state(self, active: bool, label: str = "") -> None:
        """Update menu state for indexing.

        Args:
            active: True when indexing starts, False when it ends.
            label: Description of what's being indexed.
        """
        self._indexing_label = label if active else None

        # Update Index menu title with status indicator
        if active:
            self._index_menu.title = f"\U0001f535 Indexing: {label}"
        else:
            self._index_menu.title = "Index"

        if active:
            # Disable all index sub-items by removing callbacks
            for key in list(self._index_menu):
                item = self._index_menu[key]
                if item is not None and isinstance(item, rumps.MenuItem):
                    item.set_callback(None)
        else:
            # Re-bind all callbacks
            self._rebind_index_callbacks()

    def _rebind_index_callbacks(self) -> None:
        """Re-bind callbacks on index menu items after re-enabling."""
        self._index_menu["All Collections"].set_callback(self._index_all)
        for name in ["obsidian", "email", "calibre", "rss"]:
            if name in self._index_menu:
                n = name  # capture
                self._index_menu[name].set_callback(
                    lambda sender, n=n: self._index_collection(n)
                )
        for group_name in self.config.code_groups:
            if group_name in self._index_menu:
                gn = group_name  # capture
                self._index_menu[group_name].set_callback(
                    lambda sender, n=gn: self._index_collection(n)
                )

    def _index_all(self, sender: rumps.MenuItem) -> None:
        """Trigger indexing of all collections."""
        self._run_indexing(None)

    def _index_collection(self, collection_name: str) -> None:
        """Trigger indexing of a specific collection."""
        self._run_indexing(collection_name)

    def _run_indexing(self, collection_name: str | None) -> None:
        """Run indexing in a background thread."""
        if self._is_indexing():
            rumps.notification(
                title="local-rag",
                subtitle="Indexing",
                message="Indexing is already in progress.",
            )
            return

        label = collection_name or "all collections"
        self._set_indexing_state(True, label)
        rumps.notification(
            title="local-rag",
            subtitle="Indexing Started",
            message=f"Indexing {label}...",
        )

        def run():
            try:
                if collection_name:
                    result = self.indexing_service.index_collection(
                        collection_name, self.config
                    )
                    summary = f"{collection_name}: {result.indexed} indexed, {result.errors} errors"
                else:
                    results = self.indexing_service.index_all(self.config)
                    total_indexed = sum(r.indexed for r in results.values())
                    total_errors = sum(r.errors for r in results.values())
                    summary = f"{total_indexed} indexed across {len(results)} collections, {total_errors} errors"

                rumps.notification(
                    title="local-rag",
                    subtitle="Indexing Complete",
                    message=summary,
                )
                self._update_status(None)
            except Exception as e:
                logger.exception("Indexing failed")
                rumps.notification(
                    title="local-rag",
                    subtitle="Indexing Failed",
                    message=str(e)[:100],
                )
            finally:
                self._set_indexing_state(False)

        self._index_worker = threading.Thread(target=run, daemon=True)
        self._index_worker.start()

    def _scheduled_reindex(self, _timer) -> None:
        """Run scheduled re-indexing."""
        if self._reindex_first_fire:
            self._reindex_first_fire = False
            return
        logger.info("Running scheduled re-index")
        self._run_indexing(None)

    # -- Windows --------------------------------------------------------------

    def _open_settings(self, sender: rumps.MenuItem) -> None:
        """Open the settings window."""
        from local_rag.gui.qt_bridge import show_window
        from local_rag.gui.windows.settings_window import SettingsWindow

        if self._settings_window and self._settings_window.isVisible():
            self._settings_window.raise_()
            self._settings_window.activateWindow()
            return

        self._settings_window = show_window(SettingsWindow)
        self._settings_window.destroyed.connect(self._on_settings_closed)

    def _on_settings_closed(self) -> None:
        """Reload config after settings window closes."""
        self._settings_window = None
        self.config = load_config()
        self._update_status(None)

    def _open_logs(self, sender: rumps.MenuItem) -> None:
        """Open the log viewer window."""
        from local_rag.gui.qt_bridge import ensure_qapp, show_window
        from local_rag.gui.windows.log_window import LogWindow

        if self._log_window and self._log_window.isVisible():
            self._log_window.raise_()
            self._log_window.activateWindow()
            return

        if not self._log_handler:
            return

        ensure_qapp()
        self._log_window = LogWindow(self._log_handler)
        self._log_window.show()
        self._log_window.raise_()
        self._log_window.activateWindow()
        self._log_window.destroyed.connect(
            lambda: setattr(self, "_log_window", None)
        )

    def _quit(self, sender: rumps.MenuItem) -> None:
        """Quit the application."""
        logger.info("Quitting local-rag")
        self.mcp_service.stop()
        rumps.quit_application()


def main() -> None:
    """Entry point for the menu bar app."""
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
        datefmt="%H:%M:%S",
    )
    logging.getLogger("httpx").setLevel(logging.WARNING)
    logging.getLogger("httpcore").setLevel(logging.WARNING)

    # Install GUI log handler on root logger so all log output is captured
    from local_rag.gui.windows.log_window import GUILogHandler

    gui_handler = GUILogHandler()
    logging.getLogger().addHandler(gui_handler)

    app = LocalRAGApp(log_handler=gui_handler)
    app.run()


if __name__ == "__main__":
    main()
