"""Configuration service for local-rag.

Provides a high-level interface for loading, saving, and querying configuration.
"""

import logging
from pathlib import Path

from local_rag.config import Config, load_config, save_config

logger = logging.getLogger(__name__)


class ConfigService:
    """Service for managing application configuration."""

    def load(self, path: Path | None = None) -> Config:
        """Load configuration from disk.

        Args:
            path: Optional config file path. Defaults to ~/.local-rag/config.json.

        Returns:
            Loaded Config instance.
        """
        return load_config(path)

    def save(self, config: Config, path: Path | None = None) -> None:
        """Save configuration to disk, preserving unknown keys.

        Args:
            config: Config instance to save.
            path: Optional config file path.
        """
        save_config(config, path)

    def get_all_collection_names(self, config: Config) -> list[str]:
        """Get all known collection names from config.

        Returns system collection names plus code group names.

        Args:
            config: Application configuration.

        Returns:
            Sorted list of collection names.
        """
        names: set[str] = {"obsidian", "email", "calibre", "rss"}
        names.update(config.code_groups.keys())
        return sorted(names)
