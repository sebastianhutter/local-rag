"""Git-aware repository indexer for local-rag.

Uses git ls-files for file discovery and commit SHAs for incremental indexing.
Parses code files with tree-sitter for structural chunking.
"""

import hashlib
import json
import logging
import sqlite3
import subprocess
from datetime import datetime, timezone
from pathlib import Path

from local_rag.chunker import Chunk, _split_into_windows, _word_count
from local_rag.config import Config
from local_rag.db import get_or_create_collection
from local_rag.embeddings import get_embeddings, serialize_float32
from local_rag.indexers.base import BaseIndexer, IndexResult
from local_rag.parsers.code import (
    CodeDocument,
    get_language,
    is_code_file,
    parse_code_file,
)

logger = logging.getLogger(__name__)

# Files and directories to exclude even if tracked by git
_EXCLUDE_PATTERNS: set[str] = {
    ".DS_Store",
    ".idea/",
    ".vscode/",
    "node_modules/",
    "__pycache__/",
    ".mypy_cache/",
    ".pytest_cache/",
    ".tox/",
    "dist/",
    "build/",
    ".egg-info/",
    "vendor/",
    ".terraform/",
    ".terraform.lock.hcl",
    "go.sum",
    "package-lock.json",
    "yarn.lock",
    "pnpm-lock.yaml",
    "Cargo.lock",
    "poetry.lock",
    "uv.lock",
    "cdk.out/",
}

_WATERMARK_PREFIX = "git:"


def _run_git(repo_path: Path, *args: str) -> subprocess.CompletedProcess[str]:
    """Run a git command in the given repo directory.

    Args:
        repo_path: Path to the git repository.
        *args: Git subcommand and arguments.

    Returns:
        CompletedProcess result.

    Raises:
        subprocess.CalledProcessError: If the git command fails.
    """
    return subprocess.run(
        ["git", "-C", str(repo_path), *args],
        capture_output=True,
        text=True,
        check=True,
    )


def _is_git_repo(repo_path: Path) -> bool:
    """Check if a path is inside a git repository."""
    try:
        _run_git(repo_path, "rev-parse", "--git-dir")
        return True
    except (subprocess.CalledProcessError, FileNotFoundError):
        return False


def _get_head_sha(repo_path: Path) -> str:
    """Get the current HEAD commit SHA."""
    result = _run_git(repo_path, "rev-parse", "HEAD")
    return result.stdout.strip()


def _git_ls_files(repo_path: Path) -> list[str]:
    """List all tracked files in the repo."""
    result = _run_git(repo_path, "ls-files")
    return [line for line in result.stdout.strip().split("\n") if line]


def _git_diff_names(repo_path: Path, from_sha: str, to_sha: str = "HEAD") -> list[str]:
    """Get list of files changed between two commits."""
    result = _run_git(repo_path, "diff", "--name-only", f"{from_sha}..{to_sha}")
    return [line for line in result.stdout.strip().split("\n") if line]


def _commit_exists(repo_path: Path, sha: str) -> bool:
    """Check if a commit SHA exists in the repo."""
    try:
        _run_git(repo_path, "cat-file", "-t", sha)
        return True
    except subprocess.CalledProcessError:
        return False


def _should_exclude(relative_path: str) -> bool:
    """Check if a file should be excluded based on hardcoded patterns."""
    for pattern in _EXCLUDE_PATTERNS:
        if pattern.endswith("/"):
            # Directory pattern
            if f"/{pattern}" in f"/{relative_path}" or relative_path.startswith(
                pattern
            ):
                return True
        else:
            # File pattern
            if relative_path == pattern or relative_path.endswith(f"/{pattern}"):
                return True
    return False


def _file_hash(path: Path) -> str:
    """Compute SHA256 hash of a file's contents."""
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for block in iter(lambda: f.read(8192), b""):
            h.update(block)
    return h.hexdigest()


def _code_blocks_to_chunks(
    doc: CodeDocument, relative_path: str, config: Config
) -> list[Chunk]:
    """Convert CodeDocument blocks into Chunks suitable for embedding.

    Each block gets a context prefix with file path, language, and symbol info.
    Large blocks are split into windows with the prefix preserved.

    Args:
        doc: Parsed code document.
        relative_path: Relative path within the repository.
        config: Application configuration.

    Returns:
        List of Chunk objects.
    """
    chunk_size = config.chunk_size_tokens
    overlap = config.chunk_overlap_tokens
    chunks: list[Chunk] = []
    chunk_idx = 0

    for block in doc.blocks:
        prefix = (
            f"[{relative_path}:{block.start_line}-{block.end_line}] "
            f"[{block.language}] "
            f"[{block.symbol_type}: {block.symbol_name}]\n"
        )

        metadata = {
            "language": block.language,
            "symbol_name": block.symbol_name,
            "symbol_type": block.symbol_type,
            "start_line": block.start_line,
            "end_line": block.end_line,
            "file_path": block.file_path,
        }

        prefixed_text = prefix + block.text
        prefix_word_count = _word_count(prefix)

        if _word_count(prefixed_text) <= chunk_size:
            chunks.append(
                Chunk(
                    text=prefixed_text,
                    title=relative_path,
                    metadata=metadata,
                    chunk_index=chunk_idx,
                )
            )
            chunk_idx += 1
        else:
            # Split the block text (without prefix) into windows,
            # then prepend the prefix to each window
            available = max(chunk_size - prefix_word_count, 50)
            windows = _split_into_windows(block.text, available, overlap)
            for window in windows:
                chunks.append(
                    Chunk(
                        text=prefix + window,
                        title=relative_path,
                        metadata=metadata.copy(),
                        chunk_index=chunk_idx,
                    )
                )
                chunk_idx += 1

    return chunks


def _parse_watermark(description: str | None) -> tuple[str, str] | None:
    """Parse a git watermark from a collection description.

    Format: "git:{repo_path}:{commit_sha}"

    Returns:
        Tuple of (repo_path, commit_sha), or None if not a valid watermark.
    """
    if not description or not description.startswith(_WATERMARK_PREFIX):
        return None
    parts = description[len(_WATERMARK_PREFIX) :].rsplit(":", 1)
    if len(parts) != 2:
        return None
    return parts[0], parts[1]


def _make_watermark(repo_path: Path, commit_sha: str) -> str:
    """Create a watermark string for storing in collection description."""
    return f"{_WATERMARK_PREFIX}{repo_path}:{commit_sha}"


class GitRepoIndexer(BaseIndexer):
    """Indexes a git repository using tree-sitter for code parsing."""

    def __init__(self, repo_path: Path, collection_name: str | None = None) -> None:
        """Initialize the git repo indexer.

        Args:
            repo_path: Path to the git repository root.
            collection_name: Optional collection name. Defaults to the repo directory name.
        """
        self.repo_path = repo_path.resolve()
        self.collection_name = collection_name or (
            f"git-{self.repo_path.parent.name}-{self.repo_path.name}"
        )

    def index(
        self, conn: sqlite3.Connection, config: Config, force: bool = False
    ) -> IndexResult:
        """Index all supported code files in the git repository.

        Args:
            conn: SQLite database connection.
            config: Application configuration.
            force: If True, re-index all files regardless of change detection.

        Returns:
            IndexResult summarizing the indexing run.
        """
        if not _is_git_repo(self.repo_path):
            logger.error("Not a git repository: %s", self.repo_path)
            return IndexResult(errors=1, error_messages=["Not a git repository"])

        head_sha = _get_head_sha(self.repo_path)
        logger.info("Git repo: %s (HEAD: %s)", self.repo_path, head_sha[:12])

        collection_id = get_or_create_collection(conn, self.collection_name, "project")

        # Check for existing watermark
        row = conn.execute(
            "SELECT description FROM collections WHERE id = ?", (collection_id,)
        ).fetchone()

        watermark = _parse_watermark(row["description"] if row else None)

        files_to_index: list[str]
        files_to_delete: list[str] = []

        if not force and watermark:
            old_repo_path, old_sha = watermark
            if old_sha == head_sha:
                logger.info("No new commits since last index (SHA: %s)", head_sha[:12])
                return IndexResult(total_found=0, skipped=0)

            if _commit_exists(self.repo_path, old_sha):
                # Incremental: get changed files
                changed = set(_git_diff_names(self.repo_path, old_sha, head_sha))
                tracked = set(_git_ls_files(self.repo_path))

                # Files that were changed and are still tracked
                files_to_index = sorted(changed & tracked)
                # Files that were changed but are no longer tracked (deleted)
                files_to_delete = sorted(changed - tracked)

                logger.info(
                    "Incremental index: %d changed, %d deleted since %s",
                    len(files_to_index),
                    len(files_to_delete),
                    old_sha[:12],
                )
            else:
                logger.warning(
                    "Previous watermark commit %s not found, doing full index",
                    old_sha[:12],
                )
                files_to_index = _git_ls_files(self.repo_path)
        else:
            files_to_index = _git_ls_files(self.repo_path)

        # Clean up deleted files from DB
        for rel_path in files_to_delete:
            source_path = str(self.repo_path / rel_path)
            self._delete_source(conn, collection_id, source_path)

        # Filter to supported code files
        indexable = [f for f in files_to_index if self._should_index(f)]

        total_found = len(indexable)
        indexed = 0
        skipped = 0
        errors = 0

        logger.info(
            "Indexing %d files (of %d total changed/tracked) in '%s'",
            total_found,
            len(files_to_index),
            self.collection_name,
        )

        for rel_path in indexable:
            try:
                was_indexed = self._index_file(
                    conn, config, rel_path, collection_id, force
                )
                if was_indexed:
                    indexed += 1
                else:
                    skipped += 1
            except Exception as e:
                logger.error("Error indexing %s: %s", rel_path, e)
                errors += 1

        # Update watermark
        watermark_str = _make_watermark(self.repo_path, head_sha)
        conn.execute(
            "UPDATE collections SET description = ? WHERE id = ?",
            (watermark_str, collection_id),
        )
        conn.commit()

        logger.info(
            "Git indexer done: %d indexed, %d skipped, %d errors out of %d files",
            indexed,
            skipped,
            errors,
            total_found,
        )

        return IndexResult(
            indexed=indexed, skipped=skipped, errors=errors, total_found=total_found
        )

    def _should_index(self, relative_path: str) -> bool:
        """Check if a file should be indexed based on extension and exclusion patterns."""
        if _should_exclude(relative_path):
            return False
        path = Path(relative_path)
        return is_code_file(path)

    def _delete_source(
        self, conn: sqlite3.Connection, collection_id: int, source_path: str
    ) -> None:
        """Delete a source and its documents/embeddings from the database."""
        existing = conn.execute(
            "SELECT id FROM sources WHERE collection_id = ? AND source_path = ?",
            (collection_id, source_path),
        ).fetchone()

        if not existing:
            return

        source_id = existing["id"]
        old_doc_ids = [
            r["id"]
            for r in conn.execute(
                "SELECT id FROM documents WHERE source_id = ?", (source_id,)
            ).fetchall()
        ]
        if old_doc_ids:
            placeholders = ",".join("?" * len(old_doc_ids))
            conn.execute(
                f"DELETE FROM vec_documents WHERE document_id IN ({placeholders})",
                old_doc_ids,
            )
        conn.execute("DELETE FROM documents WHERE source_id = ?", (source_id,))
        conn.execute("DELETE FROM sources WHERE id = ?", (source_id,))
        conn.commit()
        logger.debug("Deleted source: %s", source_path)

    def _index_file(
        self,
        conn: sqlite3.Connection,
        config: Config,
        relative_path: str,
        collection_id: int,
        force: bool,
    ) -> bool:
        """Index a single file from the repository.

        Args:
            conn: SQLite database connection.
            config: Application configuration.
            relative_path: Path relative to the repository root.
            collection_id: Collection ID to index into.
            force: If True, re-index regardless of change detection.

        Returns:
            True if the file was indexed, False if skipped (unchanged).
        """
        file_path = self.repo_path / relative_path
        if not file_path.exists():
            logger.warning("File not found: %s", file_path)
            return False

        source_path = str(file_path.resolve())
        file_h = _file_hash(file_path)

        # Check if already indexed with same hash
        if not force:
            row = conn.execute(
                "SELECT id, file_hash FROM sources "
                "WHERE collection_id = ? AND source_path = ?",
                (collection_id, source_path),
            ).fetchone()
            if row and row["file_hash"] == file_h:
                logger.debug("Unchanged, skipping: %s", relative_path)
                return False

        # Parse code file
        language = get_language(file_path)
        if not language:
            logger.debug("No language detected for %s, skipping", relative_path)
            return False

        doc = parse_code_file(file_path, language, relative_path)
        if not doc or not doc.blocks:
            logger.warning("No content extracted from %s, skipping", relative_path)
            return False

        # Convert blocks to chunks
        chunks = _code_blocks_to_chunks(doc, relative_path, config)
        if not chunks:
            return False

        # Generate embeddings
        texts = [c.text for c in chunks]
        embeddings = get_embeddings(texts, config)

        now = datetime.now(timezone.utc).isoformat()
        mtime = datetime.fromtimestamp(
            file_path.stat().st_mtime, tz=timezone.utc
        ).isoformat()

        # Delete old data for this source if it exists
        existing = conn.execute(
            "SELECT id FROM sources WHERE collection_id = ? AND source_path = ?",
            (collection_id, source_path),
        ).fetchone()

        if existing:
            source_id = existing["id"]
            old_doc_ids = [
                r["id"]
                for r in conn.execute(
                    "SELECT id FROM documents WHERE source_id = ?", (source_id,)
                ).fetchall()
            ]
            if old_doc_ids:
                placeholders = ",".join("?" * len(old_doc_ids))
                conn.execute(
                    f"DELETE FROM vec_documents WHERE document_id IN ({placeholders})",
                    old_doc_ids,
                )
            conn.execute("DELETE FROM documents WHERE source_id = ?", (source_id,))
            conn.execute(
                "UPDATE sources SET file_hash = ?, file_modified_at = ?, "
                "last_indexed_at = ?, source_type = ? WHERE id = ?",
                (file_h, mtime, now, "code", source_id),
            )
        else:
            cursor = conn.execute(
                "INSERT INTO sources (collection_id, source_type, source_path, "
                "file_hash, file_modified_at, last_indexed_at) "
                "VALUES (?, ?, ?, ?, ?, ?)",
                (collection_id, "code", source_path, file_h, mtime, now),
            )
            source_id = cursor.lastrowid

        # Insert new documents and embeddings
        for chunk, embedding in zip(chunks, embeddings):
            metadata_json = json.dumps(chunk.metadata) if chunk.metadata else None
            cursor = conn.execute(
                "INSERT INTO documents (source_id, collection_id, chunk_index, "
                "title, content, metadata) VALUES (?, ?, ?, ?, ?, ?)",
                (
                    source_id,
                    collection_id,
                    chunk.chunk_index,
                    chunk.title,
                    chunk.text,
                    metadata_json,
                ),
            )
            doc_id = cursor.lastrowid
            conn.execute(
                "INSERT INTO vec_documents (embedding, document_id) VALUES (?, ?)",
                (serialize_float32(embedding), doc_id),
            )

        conn.commit()
        logger.info("Indexed %s (%d chunks)", relative_path, len(chunks))
        return True
