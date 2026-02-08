"""Configuration loading and validation for local-rag."""

import json
import logging
from dataclasses import dataclass, field
from pathlib import Path

logger = logging.getLogger(__name__)

DEFAULT_CONFIG_DIR = Path.home() / ".local-rag"
DEFAULT_CONFIG_PATH = DEFAULT_CONFIG_DIR / "config.json"
DEFAULT_DB_PATH = DEFAULT_CONFIG_DIR / "rag.db"


@dataclass
class SearchDefaults:
    """Default search parameters."""

    top_k: int = 10
    rrf_k: int = 60
    vector_weight: float = 0.7
    fts_weight: float = 0.3


@dataclass
class Config:
    """Application configuration."""

    db_path: Path = field(default_factory=lambda: DEFAULT_DB_PATH)
    embedding_model: str = "bge-m3"
    embedding_dimensions: int = 1024
    chunk_size_tokens: int = 500
    chunk_overlap_tokens: int = 50
    obsidian_vaults: list[Path] = field(default_factory=list)
    emclient_db_path: Path = field(
        default_factory=lambda: Path.home() / "Library" / "Application Support" / "eM Client"
    )
    calibre_libraries: list[Path] = field(default_factory=list)
    search_defaults: SearchDefaults = field(default_factory=SearchDefaults)


def _expand_path(p: str | Path) -> Path:
    """Expand ~ and resolve a path."""
    return Path(p).expanduser()


def load_config(path: Path | None = None) -> Config:
    """Load configuration from a JSON file, falling back to defaults.

    Args:
        path: Path to config file. Defaults to ~/.local-rag/config.json.

    Returns:
        Loaded Config instance with all paths expanded.
    """
    config_path = path or DEFAULT_CONFIG_PATH

    # Ensure config directory exists
    DEFAULT_CONFIG_DIR.mkdir(parents=True, exist_ok=True)

    if config_path.exists():
        logger.info("Loading config from %s", config_path)
        with open(config_path) as f:
            data = json.load(f)
    else:
        logger.info("No config file found at %s, using defaults", config_path)
        data = {}

    search_data = data.get("search_defaults", {})
    search_defaults = SearchDefaults(
        top_k=search_data.get("top_k", 10),
        rrf_k=search_data.get("rrf_k", 60),
        vector_weight=search_data.get("vector_weight", 0.7),
        fts_weight=search_data.get("fts_weight", 0.3),
    )

    obsidian_vaults = [_expand_path(v) for v in data.get("obsidian_vaults", [])]
    calibre_libraries = [_expand_path(v) for v in data.get("calibre_libraries", [])]

    config = Config(
        db_path=_expand_path(data.get("db_path", str(DEFAULT_DB_PATH))),
        embedding_model=data.get("embedding_model", "bge-m3"),
        embedding_dimensions=data.get("embedding_dimensions", 1024),
        chunk_size_tokens=data.get("chunk_size_tokens", 500),
        chunk_overlap_tokens=data.get("chunk_overlap_tokens", 50),
        obsidian_vaults=obsidian_vaults,
        emclient_db_path=_expand_path(
            data.get("emclient_db_path", str(Path.home() / "Library" / "Application Support" / "eM Client"))
        ),
        calibre_libraries=calibre_libraries,
        search_defaults=search_defaults,
    )

    return config
