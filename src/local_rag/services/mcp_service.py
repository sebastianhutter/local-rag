"""MCP server lifecycle service for local-rag.

Manages starting, stopping, and health-checking the MCP server subprocess.
Also handles registration with Claude Desktop and Claude Code.
"""

import json
import logging
import signal
import socket
import subprocess
import threading
import time
from pathlib import Path

from local_rag.config import Config

logger = logging.getLogger(__name__)

# Find the project root (directory containing pyproject.toml)
_PROJECT_DIR = Path(__file__).resolve().parent.parent.parent.parent

CLAUDE_DESKTOP_CONFIG = (
    Path.home()
    / "Library"
    / "Application Support"
    / "Claude"
    / "claude_desktop_config.json"
)


def _port_in_use(port: int) -> bool:
    """Check if a TCP port is already in use."""
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        try:
            s.bind(("127.0.0.1", port))
            return False
        except OSError:
            return True


class MCPService:
    """Service for managing the MCP server subprocess lifecycle."""

    def __init__(self) -> None:
        self._process: subprocess.Popen | None = None
        self._stderr_thread: threading.Thread | None = None

    def start(self, config: Config) -> None:
        """Start the MCP server as a subprocess.

        Args:
            config: Application configuration.
        """
        if self.is_running():
            logger.warning("MCP server is already running")
            return

        port = config.gui.mcp_port

        # Check for port conflict before starting
        if _port_in_use(port):
            logger.warning(
                "Port %d is already in use, waiting for it to free up...", port
            )
            # Wait up to 5 seconds for the port to become available
            for _ in range(10):
                time.sleep(0.5)
                if not _port_in_use(port):
                    break
            else:
                logger.error(
                    "Port %d still in use after waiting. "
                    "Another process may be using it.",
                    port,
                )
                return

        cmd = [
            "uv", "run", "--directory", str(_PROJECT_DIR),
            "local-rag", "serve", "--port", str(port),
        ]

        logger.info("Starting MCP server: %s", " ".join(cmd))
        try:
            self._process = subprocess.Popen(
                cmd,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.PIPE,
            )
        except FileNotFoundError:
            logger.error(
                "Cannot start MCP server: 'uv' command not found. "
                "Install it with: brew install uv"
            )
            raise

        # Stream stderr in a background thread so pipe doesn't fill up
        self._stderr_thread = threading.Thread(
            target=self._read_stderr, daemon=True
        )
        self._stderr_thread.start()

        # Give the process a moment to start (or fail)
        time.sleep(1)
        if self._process.poll() is not None:
            logger.error(
                "MCP server exited immediately (exit code %d)",
                self._process.returncode,
            )
            self._process = None
            return

        logger.info("MCP server started (PID %d) on port %d", self._process.pid, port)

    def _read_stderr(self) -> None:
        """Read stderr from the subprocess and log it."""
        proc = self._process
        if not proc or not proc.stderr:
            return
        try:
            for line in proc.stderr:
                text = line.decode("utf-8", errors="replace").rstrip()
                if text:
                    logger.debug("MCP server: %s", text)
        except (ValueError, OSError):
            pass  # process died / pipe closed

    def stop(self) -> None:
        """Stop the MCP server subprocess gracefully."""
        if not self._process:
            return

        pid = self._process.pid
        logger.info("Stopping MCP server (PID %d)", pid)

        try:
            self._process.send_signal(signal.SIGTERM)
            try:
                self._process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                logger.warning("MCP server didn't stop gracefully, killing (PID %d)", pid)
                self._process.kill()
                self._process.wait(timeout=5)
        except OSError as e:
            logger.warning("Error stopping MCP server: %s", e)
        finally:
            self._process = None

    def is_running(self) -> bool:
        """Check if the MCP server subprocess is alive.

        Returns:
            True if the process is running.
        """
        if not self._process:
            return False
        return self._process.poll() is None

    def restart(self, config: Config) -> None:
        """Restart the MCP server.

        Args:
            config: Application configuration.
        """
        self.stop()
        self.start(config)

    def register_claude_desktop(self) -> bool:
        """Register local-rag as an MCP server in Claude Desktop's config.

        Reads/writes ~/Library/Application Support/Claude/claude_desktop_config.json.

        Returns:
            True if registration succeeded.
        """
        try:
            config_path = CLAUDE_DESKTOP_CONFIG
            config_path.parent.mkdir(parents=True, exist_ok=True)

            existing: dict = {}
            if config_path.exists():
                with open(config_path) as f:
                    existing = json.load(f)

            if "mcpServers" not in existing:
                existing["mcpServers"] = {}

            existing["mcpServers"]["local-rag"] = {
                "command": "uv",
                "args": [
                    "run",
                    "--directory",
                    str(_PROJECT_DIR),
                    "local-rag",
                    "serve",
                ],
                "env": {},
            }

            with open(config_path, "w") as f:
                json.dump(existing, f, indent=2)
                f.write("\n")

            logger.info("Registered local-rag with Claude Desktop at %s", config_path)
            return True
        except Exception:
            logger.exception("Failed to register with Claude Desktop")
            return False

    def register_claude_code(self, project_dir: Path) -> bool:
        """Register local-rag as an MCP server in a project's .mcp.json.

        Args:
            project_dir: Directory containing the project.

        Returns:
            True if registration succeeded.
        """
        try:
            mcp_path = project_dir / ".mcp.json"

            existing: dict = {}
            if mcp_path.exists():
                with open(mcp_path) as f:
                    existing = json.load(f)

            if "mcpServers" not in existing:
                existing["mcpServers"] = {}

            existing["mcpServers"]["local-rag"] = {
                "command": "uv",
                "args": [
                    "run",
                    "--directory",
                    str(_PROJECT_DIR),
                    "local-rag",
                    "serve",
                ],
                "env": {},
            }

            with open(mcp_path, "w") as f:
                json.dump(existing, f, indent=2)
                f.write("\n")

            logger.info("Registered local-rag with Claude Code at %s", mcp_path)
            return True
        except Exception:
            logger.exception("Failed to register with Claude Code")
            return False
