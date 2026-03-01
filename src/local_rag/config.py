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
class GUIConfig:
    """GUI-specific configuration."""

    auto_start_mcp: bool = True
    mcp_port: int = 31123
    auto_reindex: bool = False
    auto_reindex_interval_hours: int = 6
    start_on_login: bool = False


@dataclass
class Config:
    """Application configuration."""

    db_path: Path = field(default_factory=lambda: DEFAULT_DB_PATH)
    embedding_model: str = "bge-m3"
    embedding_dimensions: int = 1024
    chunk_size_tokens: int = 500
    chunk_overlap_tokens: int = 50
    obsidian_vaults: list[Path] = field(default_factory=list)
    obsidian_exclude_folders: list[str] = field(default_factory=list)
    emclient_db_path: Path = field(
        default_factory=lambda: Path.home()
        / "Library"
        / "Application Support"
        / "eM Client"
    )
    calibre_libraries: list[Path] = field(default_factory=list)
    netnewswire_db_path: Path = field(
        default_factory=lambda: (
            Path.home()
            / "Library"
            / "Containers"
            / "com.ranchero.NetNewsWire-Evergreen"
            / "Data"
            / "Library"
            / "Application Support"
            / "NetNewsWire"
            / "Accounts"
        )
    )
    code_groups: dict[str, list[Path]] = field(default_factory=dict)
    disabled_collections: set[str] = field(default_factory=set)
    git_history_in_months: int = 6
    git_commit_subject_blacklist: list[str] = field(default_factory=list)
    search_defaults: SearchDefaults = field(default_factory=SearchDefaults)
    gui: GUIConfig = field(default_factory=GUIConfig)

    def is_collection_enabled(self, name: str) -> bool:
        """Check if a collection is enabled for indexing.

        Args:
            name: Collection name (e.g., 'obsidian', 'email', 'calibre', 'rss',
                  or any user-created collection name).

        Returns:
            True if the collection is not in the disabled_collections set.
        """
        return name not in self.disabled_collections


def _expand_path(p: str | Path) -> Path:
    """Expand ~ and resolve a path."""
    return Path(p).expanduser()


def _unexpand_path(p: Path) -> str:
    """Convert an absolute path back to ~/... form if under the home directory."""
    try:
        return "~/" + str(p.relative_to(Path.home()))
    except ValueError:
        return str(p)


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
    obsidian_exclude_folders = data.get("obsidian_exclude_folders", [])
    calibre_libraries = [_expand_path(v) for v in data.get("calibre_libraries", [])]
    code_groups: dict[str, list[Path]] = {}
    for group_name, paths in data.get("code_groups", {}).items():
        code_groups[group_name] = [_expand_path(p) for p in paths]
    disabled_collections = set(data.get("disabled_collections", []))

    gui_data = data.get("gui", {})
    # Backward compat: if auto_reindex is absent but interval > 0, derive it
    raw_auto_reindex = gui_data.get("auto_reindex")
    raw_interval = gui_data.get("auto_reindex_interval_hours", 6)
    if raw_auto_reindex is None:
        auto_reindex = raw_interval > 0 if raw_interval != 6 else False
    else:
        auto_reindex = bool(raw_auto_reindex)

    gui_config = GUIConfig(
        auto_start_mcp=gui_data.get("auto_start_mcp", True),
        mcp_port=gui_data.get("mcp_port", 31123),
        auto_reindex=auto_reindex,
        auto_reindex_interval_hours=raw_interval,
        start_on_login=gui_data.get("start_on_login", False),
    )

    config = Config(
        db_path=_expand_path(data.get("db_path", str(DEFAULT_DB_PATH))),
        embedding_model=data.get("embedding_model", "bge-m3"),
        embedding_dimensions=data.get("embedding_dimensions", 1024),
        chunk_size_tokens=data.get("chunk_size_tokens", 500),
        chunk_overlap_tokens=data.get("chunk_overlap_tokens", 50),
        obsidian_vaults=obsidian_vaults,
        obsidian_exclude_folders=obsidian_exclude_folders,
        emclient_db_path=_expand_path(
            data.get(
                "emclient_db_path",
                str(Path.home() / "Library" / "Application Support" / "eM Client"),
            )
        ),
        calibre_libraries=calibre_libraries,
        netnewswire_db_path=_expand_path(
            data.get(
                "netnewswire_db_path",
                str(
                    Path.home()
                    / "Library"
                    / "Containers"
                    / "com.ranchero.NetNewsWire-Evergreen"
                    / "Data"
                    / "Library"
                    / "Application Support"
                    / "NetNewsWire"
                    / "Accounts"
                ),
            )
        ),
        code_groups=code_groups,
        disabled_collections=disabled_collections,
        git_history_in_months=data.get("git_history_in_months", 6),
        git_commit_subject_blacklist=data.get("git_commit_subject_blacklist", []),
        search_defaults=search_defaults,
        gui=gui_config,
    )

    return config


def save_config(config: Config, path: Path | None = None) -> None:
    """Save configuration to a JSON file, preserving unknown keys.

    Reads the existing file first (if present) so that keys not managed
    by this application are kept intact.

    Args:
        config: The Config instance to persist.
        path: Path to config file. Defaults to ~/.local-rag/config.json.
    """
    config_path = path or DEFAULT_CONFIG_PATH

    # Read existing data to preserve unknown keys
    existing: dict = {}
    if config_path.exists():
        with open(config_path) as f:
            existing = json.load(f)

    # Update with current config values (always save fully expanded paths)
    existing["db_path"] = str(config.db_path)
    existing["embedding_model"] = config.embedding_model
    existing["embedding_dimensions"] = config.embedding_dimensions
    existing["chunk_size_tokens"] = config.chunk_size_tokens
    existing["chunk_overlap_tokens"] = config.chunk_overlap_tokens
    existing["obsidian_vaults"] = [str(v) for v in config.obsidian_vaults]
    existing["obsidian_exclude_folders"] = config.obsidian_exclude_folders
    existing["emclient_db_path"] = str(config.emclient_db_path)
    existing["calibre_libraries"] = [str(v) for v in config.calibre_libraries]
    existing["netnewswire_db_path"] = str(config.netnewswire_db_path)
    existing["code_groups"] = {
        name: [str(p) for p in paths]
        for name, paths in config.code_groups.items()
    }
    existing["disabled_collections"] = sorted(config.disabled_collections)
    existing["git_history_in_months"] = config.git_history_in_months
    existing["git_commit_subject_blacklist"] = config.git_commit_subject_blacklist
    existing["search_defaults"] = {
        "top_k": config.search_defaults.top_k,
        "rrf_k": config.search_defaults.rrf_k,
        "vector_weight": config.search_defaults.vector_weight,
        "fts_weight": config.search_defaults.fts_weight,
    }
    existing["gui"] = {
        "auto_start_mcp": config.gui.auto_start_mcp,
        "mcp_port": config.gui.mcp_port,
        "auto_reindex": config.gui.auto_reindex,
        "auto_reindex_interval_hours": config.gui.auto_reindex_interval_hours,
        "start_on_login": config.gui.start_on_login,
    }

    # Ensure directory exists and write
    config_path.parent.mkdir(parents=True, exist_ok=True)
    with open(config_path, "w") as f:
        json.dump(existing, f, indent=2)
        f.write("\n")

    logger.info("Saved config to %s", config_path)
