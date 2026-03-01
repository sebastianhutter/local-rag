"""Abstract base class for indexers."""

import sqlite3
from abc import ABC, abstractmethod
from collections.abc import Callable
from dataclasses import dataclass, field

from local_rag.config import Config


@dataclass
class IndexResult:
    """Summary of an indexing run."""

    indexed: int = 0
    skipped: int = 0
    errors: int = 0
    total_found: int = 0
    error_messages: list[str] = field(default_factory=list)

    def __str__(self) -> str:
        return (
            f"Indexed: {self.indexed}, Skipped: {self.skipped}, "
            f"Errors: {self.errors}, Total found: {self.total_found}"
        )


class BaseIndexer(ABC):
    """Abstract base indexer that all source-specific indexers extend."""

    @abstractmethod
    def index(
        self,
        conn: sqlite3.Connection,
        config: Config,
        force: bool = False,
        progress_callback: Callable[[int, int, str], None] | None = None,
    ) -> IndexResult:
        """Run the indexing process.

        Args:
            conn: SQLite database connection.
            config: Application configuration.
            force: If True, re-index all sources regardless of change detection.
            progress_callback: Optional callback invoked per item with
                (current, total, item_name).

        Returns:
            IndexResult summarizing what happened.
        """
