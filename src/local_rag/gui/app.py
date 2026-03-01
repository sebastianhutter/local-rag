"""Main menu bar application for local-rag.

Uses rumps for the macOS menu bar and PySide6 for complex windows.
"""

import logging
import threading

import rumps

from local_rag.config import Config, load_config
from local_rag.services.indexing_service import IndexingService
from local_rag.services.mcp_service import MCPService
from local_rag.services.status_service import StatusService

logger = logging.getLogger(__name__)


class LocalRAGApp(rumps.App):
    """Menu bar application for local-rag."""

    def __init__(self) -> None:
        super().__init__(
            name="local-rag",
            title="RAG",
            quit_button=None,
        )
        self.config: Config = load_config()
        self.mcp_service = MCPService()
        self.status_service = StatusService()
        self.indexing_service = IndexingService()

        # Track open windows to prevent garbage collection
        self._settings_window = None
        self._dashboard_window = None
        self._progress_dialog = None
        self._index_worker = None

        # Build menu
        self._mcp_item = rumps.MenuItem("MCP Server: Stopped", callback=self._toggle_mcp)
        self._status_item = rumps.MenuItem("Loading...", callback=None)
        self._status_item.set_callback(None)

        index_menu = rumps.MenuItem("Index")
        index_menu["All Collections"] = rumps.MenuItem(
            "All Collections", callback=self._index_all
        )
        index_menu[None] = rumps.separator  # separator in submenu

        # Add per-collection items
        for name in ["obsidian", "email", "calibre", "rss"]:
            index_menu[name] = rumps.MenuItem(
                name.capitalize(),
                callback=lambda sender, n=name: self._index_collection(n),
            )

        # Code groups from config
        for group_name in self.config.code_groups:
            index_menu[group_name] = rumps.MenuItem(
                group_name,
                callback=lambda sender, n=group_name: self._index_collection(n),
            )

        self.menu = [
            self._mcp_item,
            None,  # separator
            self._status_item,
            None,
            index_menu,
            None,
            rumps.MenuItem("Settings...", callback=self._open_settings),
            rumps.MenuItem("Dashboard...", callback=self._open_dashboard),
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

        # Start Qt event processing
        from local_rag.gui.qt_bridge import start_qt_timer
        start_qt_timer(self)

    def _start_mcp(self) -> None:
        """Start the MCP server."""
        try:
            self.mcp_service.start(self.config)
            self._mcp_item.title = "MCP Server: Running"
            logger.info("MCP server started")
        except Exception:
            logger.exception("Failed to start MCP server")
            self._mcp_item.title = "MCP Server: Error"

    def _stop_mcp(self) -> None:
        """Stop the MCP server."""
        self.mcp_service.stop()
        self._mcp_item.title = "MCP Server: Stopped"
        logger.info("MCP server stopped")

    def _toggle_mcp(self, sender: rumps.MenuItem) -> None:
        """Toggle MCP server on/off."""
        if self.mcp_service.is_running():
            self._stop_mcp()
        else:
            self._start_mcp()

    def _check_mcp_health(self, _timer) -> None:
        """Check if MCP server is still alive."""
        if self._mcp_item.title.startswith("MCP Server: Running"):
            if not self.mcp_service.is_running():
                self._mcp_item.title = "MCP Server: Stopped"
                logger.warning("MCP server process exited unexpectedly")

    def _update_status(self, _timer) -> None:
        """Update the status line in the menu."""
        try:
            overview = self.status_service.get_overview(self.config)
            count = overview["collection_count"]
            chunks = overview["chunk_count"]
            self._status_item.title = f"{count} collections, {chunks:,} chunks"
        except Exception:
            self._status_item.title = "Status unavailable"

    def _index_all(self, sender: rumps.MenuItem) -> None:
        """Trigger indexing of all collections."""
        self._run_indexing(None)

    def _index_collection(self, collection_name: str) -> None:
        """Trigger indexing of a specific collection."""
        self._run_indexing(collection_name)

    def _run_indexing(self, collection_name: str | None) -> None:
        """Run indexing in a background thread."""
        if self._index_worker and self._index_worker.is_alive():
            rumps.notification(
                title="local-rag",
                subtitle="Indexing",
                message="Indexing is already in progress.",
            )
            return

        label = collection_name or "all collections"
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

        self._index_worker = threading.Thread(target=run, daemon=True)
        self._index_worker.start()

    def _scheduled_reindex(self, _timer) -> None:
        """Run scheduled re-indexing."""
        if self._reindex_first_fire:
            self._reindex_first_fire = False
            return
        logger.info("Running scheduled re-index")
        self._run_indexing(None)

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

    def _open_dashboard(self, sender: rumps.MenuItem) -> None:
        """Open the dashboard window."""
        from local_rag.gui.qt_bridge import show_window
        from local_rag.gui.windows.dashboard_window import DashboardWindow

        if self._dashboard_window and self._dashboard_window.isVisible():
            self._dashboard_window.raise_()
            self._dashboard_window.activateWindow()
            return

        self._dashboard_window = show_window(DashboardWindow)
        self._dashboard_window.destroyed.connect(
            lambda: setattr(self, "_dashboard_window", None)
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

    app = LocalRAGApp()
    app.run()


if __name__ == "__main__":
    main()
