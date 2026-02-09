"""Git-aware repository indexer for local-rag.

Uses git ls-files for file discovery and commit SHAs for incremental indexing.
Parses code files with tree-sitter for structural chunking.
"""

import hashlib
import json
import logging
import sqlite3
import subprocess
from dataclasses import dataclass
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


@dataclass
class CommitInfo:
    """Parsed git commit metadata."""

    sha: str
    author_name: str
    author_email: str
    author_date: str
    subject: str


@dataclass
class FileChange:
    """A single file change from a git commit."""

    file_path: str
    additions: int
    deletions: int
    is_binary: bool


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


def _get_commits_since(
    repo_path: Path, since_sha: str | None, months: int
) -> list[CommitInfo]:
    """Get commits since a given SHA or within the last N months.

    Args:
        repo_path: Path to the git repository.
        since_sha: If set, only return commits after this SHA.
        months: How many months of history to include.

    Returns:
        List of CommitInfo, oldest first.
    """
    args = [
        "log",
        "--no-merges",
        f"--since={months} months ago",
        "--pretty=format:%H|%an|%ae|%aI|%s",
    ]
    if since_sha:
        args.append(f"{since_sha}..HEAD")

    try:
        result = _run_git(repo_path, *args)
    except subprocess.CalledProcessError as e:
        logger.warning("Failed to get commit log: %s", e)
        return []

    commits: list[CommitInfo] = []
    for line in result.stdout.strip().split("\n"):
        if not line:
            continue
        parts = line.split("|", 4)
        if len(parts) != 5:
            logger.debug("Skipping malformed log line: %s", line)
            continue
        commits.append(
            CommitInfo(
                sha=parts[0],
                author_name=parts[1],
                author_email=parts[2],
                author_date=parts[3],
                subject=parts[4],
            )
        )

    # Reverse so oldest commit is first (git log returns newest first)
    commits.reverse()
    return commits


def _get_commit_file_changes(repo_path: Path, commit_sha: str) -> list[FileChange]:
    """Get the list of files changed in a commit with addition/deletion stats.

    Args:
        repo_path: Path to the git repository.
        commit_sha: The commit SHA to inspect.

    Returns:
        List of FileChange objects.
    """
    try:
        result = _run_git(repo_path, "show", "--numstat", "--format=", commit_sha)
    except subprocess.CalledProcessError as e:
        logger.warning("Failed to get file changes for %s: %s", commit_sha[:12], e)
        return []

    changes: list[FileChange] = []
    for line in result.stdout.strip().split("\n"):
        if not line:
            continue
        parts = line.split("\t", 2)
        if len(parts) != 3:
            continue
        adds_str, dels_str, file_path = parts
        # Binary files show as "-\t-\tfilename"
        is_binary = adds_str == "-" and dels_str == "-"
        changes.append(
            FileChange(
                file_path=file_path,
                additions=0 if is_binary else int(adds_str),
                deletions=0 if is_binary else int(dels_str),
                is_binary=is_binary,
            )
        )
    return changes


def _get_file_diff(repo_path: Path, commit_sha: str, file_path: str) -> str:
    """Get the diff for a specific file in a commit.

    Args:
        repo_path: Path to the git repository.
        commit_sha: The commit SHA.
        file_path: Path of the file within the repo.

    Returns:
        Raw diff text, or empty string on failure.
    """
    try:
        result = _run_git(repo_path, "show", commit_sha, "--", file_path)
        return result.stdout
    except subprocess.CalledProcessError as e:
        logger.warning(
            "Failed to get diff for %s in %s: %s", file_path, commit_sha[:12], e
        )
        return ""


def _commit_to_chunks(
    commit: CommitInfo,
    file_changes: list[FileChange],
    repo_path: Path,
    config: Config,
) -> list[Chunk]:
    """Convert a commit and its file changes into chunks for embedding.

    Each non-binary file change becomes a separate chunk containing the commit
    message and that file's diff. Large diffs are split into windows.

    Args:
        commit: The commit metadata.
        file_changes: List of file changes in the commit.
        repo_path: Path to the repository.
        config: Application configuration.

    Returns:
        List of Chunk objects.
    """
    chunk_size = config.chunk_size_tokens
    overlap = config.chunk_overlap_tokens
    chunks: list[Chunk] = []
    chunk_idx = 0
    repo_name = repo_path.name
    short_sha = commit.sha[:7]
    # Extract date portion from ISO format
    date_str = commit.author_date[:10]

    for fc in file_changes:
        if fc.is_binary:
            continue

        diff_text = _get_file_diff(repo_path, commit.sha, fc.file_path)
        if not diff_text:
            continue

        prefix = (
            f"[{repo_name}/{fc.file_path}] "
            f"[commit: {short_sha}] "
            f"[{date_str}]\n"
        )
        body = f"{commit.subject}\n\n{diff_text}"

        metadata = {
            "commit_sha": commit.sha,
            "commit_sha_short": short_sha,
            "author_name": commit.author_name,
            "author_email": commit.author_email,
            "author_date": commit.author_date,
            "commit_message": commit.subject,
            "file_path": fc.file_path,
            "additions": fc.additions,
            "deletions": fc.deletions,
        }

        prefixed_text = prefix + body
        prefix_word_count = _word_count(prefix)

        if _word_count(prefixed_text) <= chunk_size:
            chunks.append(
                Chunk(
                    text=prefixed_text,
                    title=f"{repo_name}/{fc.file_path}",
                    metadata=metadata,
                    chunk_index=chunk_idx,
                )
            )
            chunk_idx += 1
        else:
            available = max(chunk_size - prefix_word_count, 50)
            windows = _split_into_windows(body, available, overlap)
            for window in windows:
                chunks.append(
                    Chunk(
                        text=prefix + window,
                        title=f"{repo_name}/{fc.file_path}",
                        metadata=metadata.copy(),
                        chunk_index=chunk_idx,
                    )
                )
                chunk_idx += 1

    return chunks


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


def _parse_watermarks(description: str | None) -> dict[str, str]:
    """Parse git watermarks from a collection description.

    Supports two formats:
    - New JSON dict: {"repo_path_1": "sha_1", "repo_path_2": "sha_2"}
    - Legacy single: "git:{repo_path}:{commit_sha}"

    Returns:
        Dict mapping repo path strings to commit SHAs. Empty dict if no watermarks.
    """
    if not description:
        return {}

    # Try JSON format first
    if description.startswith("{"):
        try:
            data = json.loads(description)
            if isinstance(data, dict):
                return data
        except (json.JSONDecodeError, TypeError):
            pass

    # Legacy format: "git:{repo_path}:{commit_sha}"
    if description.startswith(_WATERMARK_PREFIX):
        parts = description[len(_WATERMARK_PREFIX):].rsplit(":", 1)
        if len(parts) == 2:
            return {parts[0]: parts[1]}

    return {}


def _make_watermarks(watermarks: dict[str, str]) -> str:
    """Serialize watermarks dict to JSON for storing in collection description."""
    return json.dumps(watermarks)


class GitRepoIndexer(BaseIndexer):
    """Indexes a git repository using tree-sitter for code parsing."""

    def __init__(self, repo_path: Path, collection_name: str) -> None:
        """Initialize the git repo indexer.

        Args:
            repo_path: Path to the git repository root.
            collection_name: Collection name (the code group name).
        """
        self.repo_path = repo_path.resolve()
        self.collection_name = collection_name

    def index(
        self,
        conn: sqlite3.Connection,
        config: Config,
        force: bool = False,
        index_history: bool = False,
    ) -> IndexResult:
        """Index all supported code files in the git repository.

        Args:
            conn: SQLite database connection.
            config: Application configuration.
            force: If True, re-index all files regardless of change detection.
            index_history: If True, also index commit history.

        Returns:
            IndexResult summarizing the indexing run.
        """
        if not _is_git_repo(self.repo_path):
            logger.error("Not a git repository: %s", self.repo_path)
            return IndexResult(errors=1, error_messages=["Not a git repository"])

        head_sha = _get_head_sha(self.repo_path)
        logger.info("Git repo: %s (HEAD: %s)", self.repo_path, head_sha[:12])

        collection_id = get_or_create_collection(conn, self.collection_name, "code")

        # Check for existing watermarks (multi-repo dict)
        row = conn.execute(
            "SELECT description FROM collections WHERE id = ?", (collection_id,)
        ).fetchone()

        watermarks = _parse_watermarks(row["description"] if row else None)
        repo_key = str(self.repo_path)
        old_sha = watermarks.get(repo_key)

        files_to_index: list[str]
        files_to_delete: list[str] = []

        if not force and old_sha:
            if old_sha == head_sha:
                logger.info("No new commits since last index (SHA: %s)", head_sha[:12])
                if index_history:
                    return self._index_history(
                        conn, config, collection_id, force, config.git_history_in_months
                    )
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

        # Update watermark for this repo in the shared dict
        watermarks[repo_key] = head_sha
        conn.execute(
            "UPDATE collections SET description = ? WHERE id = ?",
            (_make_watermarks(watermarks), collection_id),
        )
        conn.commit()

        logger.info(
            "Git indexer done: %d indexed, %d skipped, %d errors out of %d files",
            indexed,
            skipped,
            errors,
            total_found,
        )

        code_result = IndexResult(
            indexed=indexed, skipped=skipped, errors=errors, total_found=total_found
        )

        if index_history:
            history_result = self._index_history(
                conn, config, collection_id, force, config.git_history_in_months
            )
            return IndexResult(
                indexed=code_result.indexed + history_result.indexed,
                skipped=code_result.skipped + history_result.skipped,
                errors=code_result.errors + history_result.errors,
                total_found=code_result.total_found + history_result.total_found,
            )

        return code_result

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

    def _index_history(
        self,
        conn: sqlite3.Connection,
        config: Config,
        collection_id: int,
        force: bool,
        months: int,
    ) -> IndexResult:
        """Index commit history for the repository.

        Each changed file in a commit becomes a separate chunk containing the
        commit message and that file's diff.

        Args:
            conn: SQLite database connection.
            config: Application configuration.
            collection_id: Collection ID to index into.
            force: If True, re-index all history regardless of watermark.
            months: How many months of history to index.

        Returns:
            IndexResult summarizing the history indexing run.
        """
        repo_key = str(self.repo_path)
        history_key = f"{repo_key}:history"

        # Read existing watermarks
        row = conn.execute(
            "SELECT description FROM collections WHERE id = ?", (collection_id,)
        ).fetchone()
        watermarks = _parse_watermarks(row["description"] if row else None)

        since_sha = watermarks.get(history_key) if not force else None

        # If forcing, delete all existing commit sources for this repo
        if force:
            existing_sources = conn.execute(
                "SELECT id FROM sources WHERE collection_id = ? "
                "AND source_type = 'commit' AND source_path LIKE ?",
                (collection_id, f"git://{repo_key}#%"),
            ).fetchall()
            for src in existing_sources:
                source_id = src["id"]
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
                conn.execute(
                    "DELETE FROM documents WHERE source_id = ?", (source_id,)
                )
                conn.execute("DELETE FROM sources WHERE id = ?", (source_id,))
            conn.commit()

        commits = _get_commits_since(self.repo_path, since_sha, months)

        # Filter out blacklisted commits by subject prefix
        if config.git_commit_subject_blacklist and commits:
            before_count = len(commits)
            commits = [
                c
                for c in commits
                if not any(
                    c.subject.startswith(prefix)
                    for prefix in config.git_commit_subject_blacklist
                )
            ]
            filtered = before_count - len(commits)
            if filtered:
                logger.info(
                    "Filtered %d commit(s) matching subject blacklist", filtered
                )

        if not commits:
            logger.info("No new commits to index for %s", self.repo_path)
            return IndexResult(total_found=0, skipped=0)

        logger.info(
            "Indexing %d commit(s) for %s (last %d months)",
            len(commits),
            self.repo_path,
            months,
        )

        indexed = 0
        skipped = 0
        errors = 0
        total_found = len(commits)
        newest_sha = commits[-1].sha

        for i, commit in enumerate(commits, 1):
            try:
                # Check if this commit is already indexed
                source_path = f"git://{repo_key}#{commit.sha}"
                existing = conn.execute(
                    "SELECT id FROM sources WHERE collection_id = ? AND source_path = ?",
                    (collection_id, source_path),
                ).fetchone()
                if existing and not force:
                    skipped += 1
                    continue

                file_changes = _get_commit_file_changes(self.repo_path, commit.sha)
                if not file_changes:
                    skipped += 1
                    continue

                chunks = _commit_to_chunks(
                    commit, file_changes, self.repo_path, config
                )
                if not chunks:
                    skipped += 1
                    continue

                # Generate embeddings
                texts = [c.text for c in chunks]
                embeddings = get_embeddings(texts, config)

                now = datetime.now(timezone.utc).isoformat()

                # Create or update source
                if existing:
                    source_id = existing["id"]
                    old_doc_ids = [
                        r["id"]
                        for r in conn.execute(
                            "SELECT id FROM documents WHERE source_id = ?",
                            (source_id,),
                        ).fetchall()
                    ]
                    if old_doc_ids:
                        placeholders = ",".join("?" * len(old_doc_ids))
                        conn.execute(
                            f"DELETE FROM vec_documents WHERE document_id IN ({placeholders})",
                            old_doc_ids,
                        )
                    conn.execute(
                        "DELETE FROM documents WHERE source_id = ?", (source_id,)
                    )
                    conn.execute(
                        "UPDATE sources SET last_indexed_at = ? WHERE id = ?",
                        (now, source_id),
                    )
                else:
                    cursor = conn.execute(
                        "INSERT INTO sources (collection_id, source_type, source_path, "
                        "file_hash, file_modified_at, last_indexed_at) "
                        "VALUES (?, ?, ?, ?, ?, ?)",
                        (
                            collection_id,
                            "commit",
                            source_path,
                            commit.sha,
                            commit.author_date,
                            now,
                        ),
                    )
                    source_id = cursor.lastrowid

                # Insert documents and embeddings
                for chunk, embedding in zip(chunks, embeddings):
                    metadata_json = (
                        json.dumps(chunk.metadata) if chunk.metadata else None
                    )
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
                        "INSERT INTO vec_documents (embedding, document_id) "
                        "VALUES (?, ?)",
                        (serialize_float32(embedding), doc_id),
                    )

                conn.commit()
                indexed += 1

                logger.info(
                    "Commit %d/%d: %s %s (%d chunks, %d files)",
                    i,
                    total_found,
                    commit.sha[:7],
                    commit.subject[:60],
                    len(chunks),
                    len(file_changes),
                )

            except Exception as e:
                logger.error(
                    "Error indexing commit %s: %s", commit.sha[:12], e
                )
                errors += 1

        # Update history watermark
        watermarks[history_key] = newest_sha
        conn.execute(
            "UPDATE collections SET description = ? WHERE id = ?",
            (_make_watermarks(watermarks), collection_id),
        )
        conn.commit()

        logger.info(
            "History indexer done: %d indexed, %d skipped, %d errors out of %d commits",
            indexed,
            skipped,
            errors,
            total_found,
        )

        return IndexResult(
            indexed=indexed,
            skipped=skipped,
            errors=errors,
            total_found=total_found,
        )
