"""MCP server lifecycle service for local-rag.

Manages starting, stopping, and health-checking the MCP server subprocess.
Also handles registration with Claude Desktop and Claude Code.
"""

import json
import logging
import signal
import subprocess
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


class MCPService:
    """Service for managing the MCP server subprocess lifecycle."""

    def __init__(self) -> None:
        self._process: subprocess.Popen | None = None

    def start(self, config: Config) -> None:
        """Start the MCP server as a subprocess.

        Args:
            config: Application configuration.
        """
        if self.is_running():
            logger.warning("MCP server is already running")
            return

        cmd = ["uv", "run", "--directory", str(_PROJECT_DIR), "local-rag", "serve"]
        if config.gui.mcp_transport == "sse" and config.gui.mcp_port:
            cmd.extend(["--port", str(config.gui.mcp_port)])

        logger.info("Starting MCP server: %s", " ".join(cmd))
        self._process = subprocess.Popen(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        logger.info("MCP server started (PID %d)", self._process.pid)

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
