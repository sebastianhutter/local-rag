"""PySide6/rumps event loop integration for local-rag.

Provides utilities to run PySide6 windows alongside a rumps menu bar app.
A rumps timer calls QApplication.processEvents() to keep Qt responsive.
"""

import logging

logger = logging.getLogger(__name__)

_qapp = None


def ensure_qapp():
    """Create a QApplication singleton if it doesn't exist.

    Returns:
        The QApplication instance.
    """
    global _qapp
    if _qapp is not None:
        return _qapp

    from PySide6.QtWidgets import QApplication

    existing = QApplication.instance()
    if existing:
        _qapp = existing
        return _qapp

    _qapp = QApplication([])
    _qapp.setApplicationName("local-rag")
    _qapp.setQuitOnLastWindowClosed(False)
    return _qapp


def start_qt_timer(rumps_app) -> None:
    """Set up a rumps timer to process Qt events.

    Calls QApplication.processEvents() at ~60fps (16ms interval) to keep
    PySide6 windows responsive while rumps owns the main thread.

    Args:
        rumps_app: The rumps.App instance.
    """
    import rumps

    def process_qt_events(_):
        app = ensure_qapp()
        app.processEvents()

    timer = rumps.Timer(process_qt_events, 1 / 60)
    timer.start()


def show_window(window_class, *args, **kwargs):
    """Create and show a PySide6 window.

    Ensures QApplication exists before creating the window.

    Args:
        window_class: The QWidget subclass to instantiate.
        *args: Positional arguments for the window constructor.
        **kwargs: Keyword arguments for the window constructor.

    Returns:
        The window instance (kept alive by caller).
    """
    ensure_qapp()
    window = window_class(*args, **kwargs)
    window.show()
    window.raise_()
    window.activateWindow()
    return window
