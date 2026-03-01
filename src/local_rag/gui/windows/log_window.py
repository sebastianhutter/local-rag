"""Log viewer window for local-rag GUI."""

import logging
from collections import deque

from PySide6.QtCore import QObject, Qt, Signal, Slot
from PySide6.QtGui import QFont, QTextCursor
from PySide6.QtWidgets import (
    QHBoxLayout,
    QPlainTextEdit,
    QPushButton,
    QVBoxLayout,
    QWidget,
)


class LogSignalEmitter(QObject):
    """Thread-safe bridge: emits a Qt signal when a log record arrives."""

    log_received = Signal(str)


class GUILogHandler(logging.Handler):
    """Logging handler that captures records for the GUI log viewer.

    Keeps a ring buffer of recent messages so the window can display
    history even if opened after logs were emitted. Emits a Qt signal
    for real-time streaming.
    """

    MAX_BUFFER = 5000

    def __init__(self) -> None:
        super().__init__()
        self.buffer: deque[str] = deque(maxlen=self.MAX_BUFFER)
        self.emitter = LogSignalEmitter()
        self.setFormatter(
            logging.Formatter(
                "%(asctime)s [%(levelname)s] %(name)s: %(message)s",
                datefmt="%H:%M:%S",
            )
        )

    def emit(self, record: logging.LogRecord) -> None:
        try:
            msg = self.format(record)
            self.buffer.append(msg)
            self.emitter.log_received.emit(msg)
        except Exception:
            self.handleError(record)


class LogWindow(QWidget):
    """Window displaying application log output."""

    def __init__(self, handler: GUILogHandler, parent: QWidget | None = None) -> None:
        super().__init__(parent)
        self.setWindowTitle("local-rag Logs")
        self.resize(800, 500)
        self.setWindowFlags(Qt.WindowType.Window)

        self._handler = handler
        self._auto_scroll = True

        self._build_ui()
        self._load_history()

        # Connect signal for real-time streaming
        self._handler.emitter.log_received.connect(self._append_line)

    def _build_ui(self) -> None:
        """Build the log viewer layout."""
        layout = QVBoxLayout(self)

        self._text = QPlainTextEdit()
        self._text.setReadOnly(True)
        self._text.setFont(QFont("Menlo", 11))
        self._text.setMaximumBlockCount(GUILogHandler.MAX_BUFFER)
        self._text.setLineWrapMode(QPlainTextEdit.LineWrapMode.NoWrap)
        layout.addWidget(self._text)

        buttons = QHBoxLayout()

        self._scroll_btn = QPushButton("Auto-scroll: On")
        self._scroll_btn.setCheckable(True)
        self._scroll_btn.setChecked(True)
        self._scroll_btn.toggled.connect(self._toggle_auto_scroll)
        buttons.addWidget(self._scroll_btn)

        clear_btn = QPushButton("Clear")
        clear_btn.clicked.connect(self._text.clear)
        buttons.addWidget(clear_btn)

        buttons.addStretch()
        layout.addLayout(buttons)

    def _load_history(self) -> None:
        """Load buffered log lines into the text widget."""
        if self._handler.buffer:
            self._text.setPlainText("\n".join(self._handler.buffer))
            if self._auto_scroll:
                self._text.moveCursor(QTextCursor.MoveOperation.End)

    @Slot(str)
    def _append_line(self, line: str) -> None:
        """Append a log line and optionally scroll to bottom."""
        self._text.appendPlainText(line)
        if self._auto_scroll:
            self._text.moveCursor(QTextCursor.MoveOperation.End)

    def _toggle_auto_scroll(self, checked: bool) -> None:
        """Toggle auto-scroll behavior."""
        self._auto_scroll = checked
        self._scroll_btn.setText(f"Auto-scroll: {'On' if checked else 'Off'}")
        if checked:
            self._text.moveCursor(QTextCursor.MoveOperation.End)
