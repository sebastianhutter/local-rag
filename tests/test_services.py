"""Tests for the local_rag.services layer.

Covers ConfigService, StatusService, MCPService, and IndexingService
without importing any GUI frameworks.
"""

import json
import sqlite3
import threading
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

from local_rag.config import Config, GUIConfig
from local_rag.indexers.base import IndexResult
from local_rag.services.config_service import ConfigService
from local_rag.services.indexing_service import IndexingService
from local_rag.services.mcp_service import MCPService
from local_rag.services.status_service import StatusService

# ---------------------------------------------------------------------------
# sqlite extension availability check (mirrors test_search.py pattern)
# ---------------------------------------------------------------------------
_conn = sqlite3.connect(":memory:")
_has_load_extension = hasattr(_conn, "enable_load_extension")
_conn.close()

requires_sqlite_extensions = pytest.mark.skipif(
    not _has_load_extension,
    reason="sqlite3 was not compiled with loadable extension support",
)


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _make_config(tmp_path: Path, **overrides) -> Config:
    """Build a minimal Config rooted in *tmp_path*."""
    defaults = dict(
        db_path=tmp_path / "test.db",
        embedding_dimensions=4,
    )
    defaults.update(overrides)
    return Config(**defaults)


def _init_db_with_data(tmp_path: Path) -> Config:
    """Create a real DB with some rows and return the Config used."""
    import sqlite_vec
    from local_rag.db import get_or_create_collection, init_db

    config = _make_config(tmp_path)
    conn = sqlite3.connect(str(config.db_path))
    conn.enable_load_extension(True)
    sqlite_vec.load(conn)
    conn.enable_load_extension(False)
    conn.execute("PRAGMA foreign_keys=ON")
    conn.row_factory = sqlite3.Row

    init_db(conn, config)

    # Insert two collections with sources and documents
    col1 = get_or_create_collection(conn, "obsidian", "system")
    col2 = get_or_create_collection(conn, "email", "system")

    conn.execute(
        "INSERT INTO sources (collection_id, source_type, source_path, last_indexed_at) "
        "VALUES (?, 'markdown', '/notes/a.md', '2025-06-01T10:00:00')",
        (col1,),
    )
    conn.execute(
        "INSERT INTO sources (collection_id, source_type, source_path, last_indexed_at) "
        "VALUES (?, 'email', 'msg-001', '2025-06-02T12:00:00')",
        (col2,),
    )
    src1 = conn.execute("SELECT id FROM sources WHERE source_path = '/notes/a.md'").fetchone()["id"]
    src2 = conn.execute("SELECT id FROM sources WHERE source_path = 'msg-001'").fetchone()["id"]

    conn.execute(
        "INSERT INTO documents (source_id, collection_id, chunk_index, title, content, metadata) "
        "VALUES (?, ?, 0, 'Note A', 'content a', '{}')",
        (src1, col1),
    )
    conn.execute(
        "INSERT INTO documents (source_id, collection_id, chunk_index, title, content, metadata) "
        "VALUES (?, ?, 0, 'Email 1', 'content b', '{}')",
        (src2, col2),
    )
    conn.execute(
        "INSERT INTO documents (source_id, collection_id, chunk_index, title, content, metadata) "
        "VALUES (?, ?, 1, 'Email 1 chunk 2', 'content c', '{}')",
        (src2, col2),
    )
    conn.commit()
    conn.close()

    return config


# ===================================================================
# ConfigService
# ===================================================================

class TestConfigService:
    """Tests for ConfigService."""

    def test_load_returns_config(self, tmp_path: Path):
        """load() with a non-existent path returns a Config with defaults."""
        svc = ConfigService()
        cfg = svc.load(tmp_path / "config.json")
        assert isinstance(cfg, Config)
        # Default embedding model
        assert cfg.embedding_model == "bge-m3"

    def test_save_and_load_roundtrip(self, tmp_path: Path):
        """Saving then loading a config preserves values."""
        svc = ConfigService()
        config_path = tmp_path / "config.json"

        original = _make_config(
            tmp_path,
            embedding_model="mxbai-embed-large",
            chunk_size_tokens=300,
            code_groups={"myorg": [Path("/repos/a")]},
        )
        svc.save(original, config_path)
        loaded = svc.load(config_path)

        assert loaded.embedding_model == "mxbai-embed-large"
        assert loaded.chunk_size_tokens == 300
        assert "myorg" in loaded.code_groups

    def test_get_all_collection_names_system_only(self):
        """Without code groups the list contains only system names."""
        svc = ConfigService()
        cfg = Config(db_path=Path("/tmp/test.db"), embedding_dimensions=4)
        names = svc.get_all_collection_names(cfg)
        assert names == ["calibre", "email", "obsidian", "rss"]

    def test_get_all_collection_names_with_code_groups(self):
        """Code group names are merged with system names and sorted."""
        svc = ConfigService()
        cfg = Config(
            db_path=Path("/tmp/test.db"),
            embedding_dimensions=4,
            code_groups={
                "terraform": [Path("/repos/tf")],
                "alpha": [Path("/repos/alpha")],
            },
        )
        names = svc.get_all_collection_names(cfg)
        assert names == ["alpha", "calibre", "email", "obsidian", "rss", "terraform"]

    def test_save_preserves_unknown_keys(self, tmp_path: Path):
        """Unknown keys already in the config file survive a save round-trip."""
        svc = ConfigService()
        config_path = tmp_path / "config.json"

        # Write an initial file with an unknown key
        config_path.write_text(json.dumps({"custom_key": "keep_me"}))

        cfg = _make_config(tmp_path)
        svc.save(cfg, config_path)

        raw = json.loads(config_path.read_text())
        assert raw["custom_key"] == "keep_me"


# ===================================================================
# StatusService
# ===================================================================

class TestStatusService:
    """Tests for StatusService."""

    def test_get_overview_no_db_file(self, tmp_path: Path):
        """When the DB file does not exist, return zero counts."""
        svc = StatusService()
        cfg = _make_config(tmp_path)
        # db_path does not exist yet
        overview = svc.get_overview(cfg)

        assert overview["collection_count"] == 0
        assert overview["source_count"] == 0
        assert overview["chunk_count"] == 0
        assert overview["db_size_mb"] == 0.0
        assert overview["last_indexed"] is None
        assert overview["embedding_model"] == cfg.embedding_model

    @requires_sqlite_extensions
    def test_get_overview_with_data(self, tmp_path: Path):
        """With populated data the overview counts are accurate."""
        cfg = _init_db_with_data(tmp_path)
        svc = StatusService()
        overview = svc.get_overview(cfg)

        assert overview["collection_count"] == 2
        assert overview["source_count"] == 2
        assert overview["chunk_count"] == 3
        assert overview["db_size_mb"] > 0
        assert overview["last_indexed"] == "2025-06-02T12:00:00"

    def test_get_collections_no_db(self, tmp_path: Path):
        """When the DB file does not exist, return an empty list."""
        svc = StatusService()
        cfg = _make_config(tmp_path)
        collections = svc.get_collections(cfg)
        assert collections == []

    @requires_sqlite_extensions
    def test_get_collections_with_data(self, tmp_path: Path):
        """Collections list contains correct per-collection stats."""
        cfg = _init_db_with_data(tmp_path)
        svc = StatusService()
        collections = svc.get_collections(cfg)

        assert len(collections) == 2
        # Sorted by name — email before obsidian
        by_name = {c["name"]: c for c in collections}

        assert by_name["email"]["type"] == "system"
        assert by_name["email"]["source_count"] == 1
        assert by_name["email"]["chunk_count"] == 2
        assert by_name["email"]["last_indexed"] == "2025-06-02T12:00:00"

        assert by_name["obsidian"]["type"] == "system"
        assert by_name["obsidian"]["source_count"] == 1
        assert by_name["obsidian"]["chunk_count"] == 1
        assert by_name["obsidian"]["last_indexed"] == "2025-06-01T10:00:00"

    def test_check_ollama_success(self):
        """check_ollama returns True when ollama.Client().list() succeeds."""
        svc = StatusService()
        mock_client = MagicMock()
        with patch("local_rag.services.status_service.ollama", create=True) as mock_mod:
            mock_mod.Client.return_value = mock_client
            # Patch the import inside check_ollama
            with patch.dict("sys.modules", {"ollama": mock_mod}):
                result = svc.check_ollama()
        assert result is True
        mock_client.list.assert_called_once()

    def test_check_ollama_failure(self):
        """check_ollama returns False when ollama raises."""
        svc = StatusService()
        mock_client = MagicMock()
        mock_client.list.side_effect = ConnectionError("Ollama not running")
        mock_mod = MagicMock()
        mock_mod.Client.return_value = mock_client
        with patch.dict("sys.modules", {"ollama": mock_mod}):
            result = svc.check_ollama()
        assert result is False


# ===================================================================
# MCPService
# ===================================================================

class TestMCPService:
    """Tests for MCPService."""

    def test_is_running_initially_false(self):
        """A fresh MCPService reports not running."""
        svc = MCPService()
        assert svc.is_running() is False

    def test_start_launches_process(self):
        """start() spawns a subprocess and is_running() returns True."""
        svc = MCPService()
        cfg = Config(db_path=Path("/tmp/test.db"), embedding_dimensions=4)

        mock_proc = MagicMock()
        mock_proc.poll.return_value = None  # process alive
        mock_proc.pid = 12345

        with patch("local_rag.services.mcp_service.subprocess.Popen", return_value=mock_proc) as mock_popen:
            svc.start(cfg)

        assert svc.is_running() is True
        mock_popen.assert_called_once()
        # Verify command includes "uv run ..."
        args, kwargs = mock_popen.call_args
        cmd = args[0]
        assert cmd[0] == "uv"
        assert "serve" in cmd

    def test_start_with_sse_transport(self):
        """start() includes --port flag when transport is SSE."""
        svc = MCPService()
        cfg = Config(
            db_path=Path("/tmp/test.db"),
            embedding_dimensions=4,
            gui=GUIConfig(mcp_transport="sse", mcp_port=9999),
        )

        mock_proc = MagicMock()
        mock_proc.poll.return_value = None
        mock_proc.pid = 12345

        with patch("local_rag.services.mcp_service.subprocess.Popen", return_value=mock_proc) as mock_popen:
            svc.start(cfg)

        cmd = mock_popen.call_args[0][0]
        assert "--port" in cmd
        assert "9999" in cmd

    def test_start_skips_if_already_running(self):
        """start() does nothing when the process is already running."""
        svc = MCPService()
        cfg = Config(db_path=Path("/tmp/test.db"), embedding_dimensions=4)

        mock_proc = MagicMock()
        mock_proc.poll.return_value = None
        mock_proc.pid = 12345

        with patch("local_rag.services.mcp_service.subprocess.Popen", return_value=mock_proc) as mock_popen:
            svc.start(cfg)
            # Second start should not spawn another process
            svc.start(cfg)

        assert mock_popen.call_count == 1

    def test_stop_terminates_process(self):
        """stop() sends SIGTERM and waits on the process."""
        svc = MCPService()
        cfg = Config(db_path=Path("/tmp/test.db"), embedding_dimensions=4)

        mock_proc = MagicMock()
        mock_proc.poll.return_value = None
        mock_proc.pid = 12345

        with patch("local_rag.services.mcp_service.subprocess.Popen", return_value=mock_proc):
            svc.start(cfg)

        svc.stop()

        mock_proc.send_signal.assert_called_once()
        mock_proc.wait.assert_called_once_with(timeout=5)
        assert svc.is_running() is False

    def test_stop_when_not_running(self):
        """stop() is a no-op when there is no process."""
        svc = MCPService()
        # Should not raise
        svc.stop()
        assert svc.is_running() is False

    def test_register_claude_desktop_creates_config(self, tmp_path: Path):
        """register_claude_desktop writes the correct JSON structure."""
        config_path = tmp_path / "Claude" / "claude_desktop_config.json"

        with patch.object(MCPService, "__init__", lambda self: None):
            svc = MCPService()

        with patch("local_rag.services.mcp_service.CLAUDE_DESKTOP_CONFIG", config_path):
            result = svc.register_claude_desktop()

        assert result is True
        assert config_path.exists()

        data = json.loads(config_path.read_text())
        assert "mcpServers" in data
        assert "local-rag" in data["mcpServers"]
        entry = data["mcpServers"]["local-rag"]
        assert entry["command"] == "uv"
        assert "serve" in entry["args"]
        assert "env" in entry

    def test_register_claude_desktop_preserves_existing_keys(self, tmp_path: Path):
        """Existing mcpServers entries survive registration."""
        config_path = tmp_path / "Claude" / "claude_desktop_config.json"
        config_path.parent.mkdir(parents=True)
        config_path.write_text(json.dumps({
            "mcpServers": {
                "other-tool": {"command": "other", "args": []}
            },
            "someOtherKey": True,
        }))

        with patch("local_rag.services.mcp_service.CLAUDE_DESKTOP_CONFIG", config_path):
            svc = MCPService()
            result = svc.register_claude_desktop()

        assert result is True
        data = json.loads(config_path.read_text())
        assert "other-tool" in data["mcpServers"]
        assert "local-rag" in data["mcpServers"]
        assert data["someOtherKey"] is True

    def test_register_claude_code_creates_mcp_json(self, tmp_path: Path):
        """register_claude_code writes .mcp.json in the given project dir."""
        svc = MCPService()
        result = svc.register_claude_code(tmp_path)

        assert result is True
        mcp_path = tmp_path / ".mcp.json"
        assert mcp_path.exists()

        data = json.loads(mcp_path.read_text())
        assert "mcpServers" in data
        assert "local-rag" in data["mcpServers"]
        entry = data["mcpServers"]["local-rag"]
        assert entry["command"] == "uv"
        assert "serve" in entry["args"]

    def test_register_claude_code_preserves_existing_keys(self, tmp_path: Path):
        """Existing entries in .mcp.json are preserved."""
        mcp_path = tmp_path / ".mcp.json"
        mcp_path.write_text(json.dumps({
            "mcpServers": {
                "existing": {"command": "x", "args": []}
            },
            "extraKey": 42,
        }))

        svc = MCPService()
        result = svc.register_claude_code(tmp_path)

        assert result is True
        data = json.loads(mcp_path.read_text())
        assert "existing" in data["mcpServers"]
        assert "local-rag" in data["mcpServers"]
        assert data["extraKey"] == 42

    def test_restart_stops_then_starts(self):
        """restart() calls stop() then start()."""
        svc = MCPService()
        cfg = Config(db_path=Path("/tmp/test.db"), embedding_dimensions=4)

        with patch.object(svc, "stop") as mock_stop, \
             patch.object(svc, "start") as mock_start:
            svc.restart(cfg)

        mock_stop.assert_called_once()
        mock_start.assert_called_once_with(cfg)


# ===================================================================
# IndexingService
# ===================================================================

class TestIndexingService:
    """Tests for IndexingService."""

    @requires_sqlite_extensions
    def test_index_disabled_collection_returns_empty(self, tmp_path: Path):
        """Indexing a disabled collection returns an empty IndexResult."""
        cfg = _make_config(tmp_path, disabled_collections={"obsidian"})
        svc = IndexingService()
        result = svc.index_collection("obsidian", cfg)

        assert isinstance(result, IndexResult)
        assert result.indexed == 0
        assert result.skipped == 0
        assert result.errors == 0

    @requires_sqlite_extensions
    def test_get_indexable_collections_basic(self, tmp_path: Path):
        """_get_indexable_collections returns system + code group names."""
        import sqlite_vec
        from local_rag.db import init_db

        cfg = _make_config(
            tmp_path,
            obsidian_vaults=[Path("/vault")],
            calibre_libraries=[Path("/calibre")],
            code_groups={"myorg": [Path("/repos/myorg")]},
        )
        # emclient and netnewswire paths point to nonexistent dirs so they
        # should NOT appear in the indexable list.
        cfg.emclient_db_path = tmp_path / "nonexistent_emclient"
        cfg.netnewswire_db_path = tmp_path / "nonexistent_nnw"

        conn = sqlite3.connect(str(cfg.db_path))
        conn.enable_load_extension(True)
        sqlite_vec.load(conn)
        conn.enable_load_extension(False)
        conn.execute("PRAGMA foreign_keys=ON")
        conn.row_factory = sqlite3.Row
        init_db(conn, cfg)

        svc = IndexingService()
        collections = svc._get_indexable_collections(cfg, conn)
        conn.close()

        assert "obsidian" in collections
        assert "calibre" in collections
        assert "myorg" in collections
        # email and rss should be absent (paths don't exist)
        assert "email" not in collections
        assert "rss" not in collections

    @requires_sqlite_extensions
    def test_get_indexable_collections_includes_email_when_path_exists(self, tmp_path: Path):
        """email appears when emclient_db_path exists."""
        import sqlite_vec
        from local_rag.db import init_db

        emclient_path = tmp_path / "eM Client"
        emclient_path.mkdir()

        cfg = _make_config(tmp_path, obsidian_vaults=[])
        cfg.emclient_db_path = emclient_path
        cfg.netnewswire_db_path = tmp_path / "nonexistent_nnw"

        conn = sqlite3.connect(str(cfg.db_path))
        conn.enable_load_extension(True)
        sqlite_vec.load(conn)
        conn.enable_load_extension(False)
        conn.execute("PRAGMA foreign_keys=ON")
        conn.row_factory = sqlite3.Row
        init_db(conn, cfg)

        svc = IndexingService()
        collections = svc._get_indexable_collections(cfg, conn)
        conn.close()

        assert "email" in collections

    @requires_sqlite_extensions
    def test_get_indexable_collections_includes_project_from_db(self, tmp_path: Path):
        """Project collections stored in the DB are included."""
        import sqlite_vec
        from local_rag.db import get_or_create_collection, init_db

        cfg = _make_config(tmp_path, obsidian_vaults=[])
        cfg.emclient_db_path = tmp_path / "nonexistent_emclient"
        cfg.netnewswire_db_path = tmp_path / "nonexistent_nnw"

        conn = sqlite3.connect(str(cfg.db_path))
        conn.enable_load_extension(True)
        sqlite_vec.load(conn)
        conn.enable_load_extension(False)
        conn.execute("PRAGMA foreign_keys=ON")
        conn.row_factory = sqlite3.Row
        init_db(conn, cfg)

        # Insert a project collection with paths
        get_or_create_collection(
            conn, "My Project", "project", paths=["/docs/project"]
        )

        svc = IndexingService()
        collections = svc._get_indexable_collections(cfg, conn)
        conn.close()

        assert "My Project" in collections

    @requires_sqlite_extensions
    def test_index_all_with_cancel_event_stops_early(self, tmp_path: Path):
        """Setting the cancel_event prevents processing further collections."""
        cfg = _make_config(
            tmp_path,
            obsidian_vaults=[Path("/vault")],
            code_groups={"org1": [Path("/r1")], "org2": [Path("/r2")]},
        )
        cfg.emclient_db_path = tmp_path / "nonexistent_emclient"
        cfg.netnewswire_db_path = tmp_path / "nonexistent_nnw"

        cancel = threading.Event()
        cancel.set()  # pre-cancelled

        svc = IndexingService()

        # _run_indexer would fail if it were called, because the paths
        # don't exist and no mocking is done.  The cancel event should
        # prevent it from being reached.
        with patch.object(svc, "_run_indexer") as mock_run:
            results = svc.index_all(cfg, cancel_event=cancel)

        # No indexer should have been called because cancel was set
        mock_run.assert_not_called()
        assert results == {}

    @requires_sqlite_extensions
    def test_index_all_skips_disabled_collections(self, tmp_path: Path):
        """Disabled collections are skipped during index_all."""
        cfg = _make_config(
            tmp_path,
            obsidian_vaults=[Path("/vault")],
            disabled_collections={"obsidian"},
        )
        cfg.emclient_db_path = tmp_path / "nonexistent_emclient"
        cfg.netnewswire_db_path = tmp_path / "nonexistent_nnw"

        svc = IndexingService()

        with patch.object(svc, "_run_indexer") as mock_run:
            results = svc.index_all(cfg)

        # obsidian is disabled, so _run_indexer should not have been called
        mock_run.assert_not_called()

    @requires_sqlite_extensions
    def test_index_all_handles_indexer_error(self, tmp_path: Path):
        """If _run_indexer raises, the error is captured in the result."""
        cfg = _make_config(
            tmp_path,
            obsidian_vaults=[Path("/vault")],
        )
        cfg.emclient_db_path = tmp_path / "nonexistent_emclient"
        cfg.netnewswire_db_path = tmp_path / "nonexistent_nnw"

        svc = IndexingService()

        with patch.object(
            svc, "_run_indexer", side_effect=RuntimeError("boom")
        ):
            results = svc.index_all(cfg)

        assert "obsidian" in results
        assert results["obsidian"].errors == 1
        assert "boom" in results["obsidian"].error_messages[0]

    def test_index_result_defaults(self):
        """IndexResult defaults to all zeros."""
        r = IndexResult()
        assert r.indexed == 0
        assert r.skipped == 0
        assert r.errors == 0
        assert r.total_found == 0
        assert r.error_messages == []

    def test_index_result_str(self):
        """IndexResult __str__ includes counts."""
        r = IndexResult(indexed=5, skipped=2, errors=1, total_found=8)
        s = str(r)
        assert "5" in s
        assert "2" in s
        assert "1" in s
        assert "8" in s
