# CLAUDE.md

## Project: local-rag

A fully local, privacy-preserving RAG (Retrieval Augmented Generation) system for macOS. Indexes personal knowledge from multiple sources into a single SQLite database with vector + full-text hybrid search. Exposes search via CLI and an MCP server so Claude Desktop and Claude Code can query it directly.

---

## Quick Start

```bash
# Prerequisites
brew install ollama uv
ollama pull bge-m3

# Setup (uv automatically manages the venv)
cd /path/to/local-rag

# Index sources (uv run auto-creates and manages venv)
uv run local-rag index obsidian ~/Documents/MyVault
uv run local-rag index email
uv run local-rag index project "Project Alpha" ~/Documents/project-alpha-docs/

# Search
uv run local-rag search "kubernetes deployment strategy"
uv run local-rag search "invoice from supplier" --collection email
uv run local-rag search "API specification" --collection "Project Alpha"

# Run MCP server (for Claude Desktop / Claude Code integration)
uv run local-rag serve
```

---

## Architecture

```
Sources                        Indexer                    Storage              Interface
────────                       ───────                    ───────              ─────────
Obsidian (.md) ──┐
                 │
eM Client (SQLite)──┤
                    ├──► Python Indexer ──► rag.db ──► CLI
Project docs ───────┤    (chunking +       (SQLite +     MCP Server
(PDF, DOCX, TXT,   │     Ollama embed)     sqlite-vec    (for Claude)
 MD, HTML, etc.)  ──┘                      + FTS5)
```

### Core Concepts

**Collections**: Every indexed source belongs to a collection. Collections are either system-level ("obsidian", "email") or user-created projects ("Project Alpha", "Client X docs"). Search can target a specific collection or search across all of them.

**Hybrid search**: Every query runs both vector similarity search (semantic) and FTS5 full-text search (keyword). Results are merged using Reciprocal Rank Fusion (RRF). This ensures that both "what does this mean" and "find the exact phrase" queries work well.

**Incremental indexing**: Track file modification times and email watermarks. Only re-embed changed or new content. Full re-index available as fallback.

---

## Tech Stack

| Component    | Choice                     | Notes                                |
|--------------|----------------------------|--------------------------------------|
| Language     | Python 3.13+               |                                      |
| Database     | SQLite + sqlite-vec + FTS5 | Single file, no server               |
| Embeddings   | Ollama + bge-m3 (1024d)    | Fully local, no API keys             |
| PDF parsing  | pymupdf (fitz)             | Best quality for PDF text extraction |
| DOCX parsing | python-docx                | Read Word documents                  |
| HTML to text | beautifulsoup4             | Strip HTML from email bodies         |
| CLI          | click                      |                                      |
| MCP server   | mcp (Python SDK)           | Exposes tools to Claude              |

---

## Database Schema

The database lives at `~/.local-rag/rag.db` by default (configurable).

```sql
-- Collections: namespaces for organizing indexed content
-- System collections: 'obsidian', 'email'
-- User collections: any name, used for project-based grouping
CREATE TABLE collections (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    collection_type TEXT NOT NULL DEFAULT 'project',  -- 'system' or 'project'
    description TEXT,
    created_at TEXT DEFAULT (datetime('now'))
);

-- Sources: individual files or email accounts that have been indexed
CREATE TABLE sources (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    collection_id INTEGER NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    source_type TEXT NOT NULL,           -- 'markdown', 'email', 'pdf', 'docx', 'txt', 'html'
    source_path TEXT NOT NULL,           -- file path or email message ID
    file_hash TEXT,                      -- SHA256 of file content for change detection
    file_modified_at TEXT,               -- filesystem mtime
    last_indexed_at TEXT,
    UNIQUE(collection_id, source_path)
);

-- Documents: chunked content with metadata
CREATE TABLE documents (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id INTEGER NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    collection_id INTEGER NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    chunk_index INTEGER NOT NULL,
    title TEXT,                           -- note title, email subject, PDF filename
    content TEXT NOT NULL,                -- the text chunk
    metadata TEXT,                        -- JSON: tags, sender, dates, heading path, page number, etc.
    created_at TEXT DEFAULT (datetime('now')),
    UNIQUE(source_id, chunk_index)
);

-- Vector index (sqlite-vec virtual table)
CREATE VIRTUAL TABLE vec_documents USING vec0(
    embedding float[768],
    document_id INTEGER
);

-- Full-text search index (FTS5)
CREATE VIRTUAL TABLE documents_fts USING fts5(
    title,
    content,
    content='documents',
    content_rowid='id'
);

-- Triggers to keep FTS in sync with documents table
CREATE TRIGGER documents_ai AFTER INSERT ON documents BEGIN
    INSERT INTO documents_fts(rowid, title, content) VALUES (new.id, new.title, new.content);
END;
CREATE TRIGGER documents_ad AFTER DELETE ON documents BEGIN
    INSERT INTO documents_fts(documents_fts, rowid, title, content) VALUES('delete', old.id, old.title, old.content);
END;
CREATE TRIGGER documents_au AFTER UPDATE ON documents BEGIN
    INSERT INTO documents_fts(documents_fts, rowid, title, content) VALUES('delete', old.id, old.title, old.content);
    INSERT INTO documents_fts(rowid, title, content) VALUES (new.id, new.title, new.content);
END;
```

---

## File Structure

```
local-rag/
├── CLAUDE.md                        # This file
├── pyproject.toml                   # Package config, dependencies, CLI entry point
├── README.md
├── config.example.json              # Example configuration
├── src/
│   └── local_rag/
│       ├── __init__.py
│       ├── cli.py                   # Click CLI entry point
│       ├── config.py                # Load/validate config
│       ├── db.py                    # Database init, connection, migrations
│       ├── embeddings.py            # Ollama embedding helpers
│       ├── chunker.py               # Text chunking strategies (per file type)
│       ├── search.py                # Hybrid search engine (vector + FTS + RRF)
│       ├── parsers/
│       │   ├── __init__.py
│       │   ├── markdown.py          # Obsidian .md parser (frontmatter, wikilinks, tags)
│       │   ├── email.py             # eM Client SQLite reader + email parser
│       │   ├── pdf.py               # PDF text extraction (pymupdf)
│       │   ├── docx.py              # DOCX text extraction (python-docx)
│       │   ├── html.py              # HTML to text (beautifulsoup4)
│       │   └── plaintext.py         # .txt passthrough
│       ├── indexers/
│       │   ├── __init__.py
│       │   ├── base.py              # Abstract base indexer
│       │   ├── obsidian.py          # Walks vault, uses markdown parser
│       │   ├── email_indexer.py     # Copies + reads eM Client DB
│       │   └── project.py           # Walks a folder, dispatches to correct parser by extension
│       └── mcp_server.py            # MCP server exposing search + index tools
├── scripts/
│   └── explore_emclient.py          # One-off: discover eM Client SQLite schema
└── tests/
    ├── test_chunker.py
    ├── test_search.py
    ├── test_parsers.py
    └── fixtures/                    # Sample .md, .pdf, .docx files for tests
```

---

## Implementation Phases

Work through these in order. Each phase should be testable before moving to the next.

### Phase 1: Core Infrastructure

**Goal**: Database + embeddings + chunking work end-to-end with hardcoded test strings.

1. **`db.py`** — Initialize SQLite, load sqlite-vec extension, create schema (all tables above), provide connection helper. Include a `migrate()` function for future schema changes.

2. **`embeddings.py`** — Wrapper around Ollama's Python client.
   - `get_embedding(text: str) -> list[float]` — single text
   - `get_embeddings(texts: list[str]) -> list[list[float]]` — batch, for efficiency
   - Uses `ollama.embed(model=config.embedding_model, input=text)`
   - Serialize to sqlite-vec format with `serialize_float32()`
   - Handle Ollama connection errors gracefully (check if ollama is running)

3. **`chunker.py`** — Text splitting strategies.
   - `chunk_markdown(text: str, title: str) -> list[Chunk]` — split on headings, preserve heading path as context prefix, ~500 token target, 50 token overlap
   - `chunk_email(subject: str, body: str) -> list[Chunk]` — one chunk if short, paragraph-split if long, strip signatures/quoted replies
   - `chunk_plain(text: str, title: str) -> list[Chunk]` — fixed-size token windows with overlap
   - `Chunk` is a dataclass: `text: str, title: str, metadata: dict, chunk_index: int`

4. **`config.py`** — Load from `~/.local-rag/config.json` with sensible defaults.

5. **`search.py`** — Hybrid search engine.
   - `search(query, top_k=10, collection=None, filters=None) -> list[SearchResult]`
   - Vector search via sqlite-vec `MATCH`
   - Full-text search via FTS5 `MATCH`
   - Combine with RRF (k=60)
   - Apply optional filters: collection name, source_type, date range, metadata JSON queries
   - `SearchResult`: `content, title, metadata, score, collection, source_path`

**Test**: Create DB, embed a few strings, insert them, search, verify results make sense.

### Phase 2: Obsidian Indexer

**Goal**: Index one or more Obsidian vaults into the "obsidian" collection.

1. **`parsers/markdown.py`**
   - Parse YAML frontmatter (between `---` delimiters) → extract tags, aliases, dates
   - Convert wikilinks `[[target|display]]` → keep both target and display text as searchable content
   - Strip `![[embeds]]` notation but note the reference
   - Strip Dataview queries (`\`\`\`dataview ... \`\`\``)
   - Detect inline `#tags`
   - Return structured data: `title, body_text, frontmatter_dict, tags, links`

2. **`indexers/obsidian.py`**
   - Accept vault paths from config or CLI args
   - Create/get the "obsidian" system collection
   - Walk vault recursively, skip `.obsidian/`, `.trash/`, hidden files
   - For each `.md` file:
     - Compute SHA256 hash of file content
     - Check `sources` table — if hash matches, skip (no changes)
     - Parse with markdown parser
     - Chunk with markdown chunker
     - Embed all chunks via Ollama
     - Delete old documents for this source (if re-indexing) and insert new ones
     - Update sources table
   - Log progress: files found, skipped (unchanged), indexed, errors

**Test**: Point at a small vault or create test fixtures. Verify documents appear in DB, search returns relevant results.

### Phase 3: eM Client Email Indexer

**Goal**: Index emails from eM Client into the "email" collection.

**Database access**: Open eM Client's `mail_data.dat` in **SQLite read-only mode** using URI syntax:
```python
sqlite3.connect("file:/path/to/mail_data.dat?mode=ro", uri=True)
```
This takes only a shared lock (won't block eM Client) and prevents any accidental writes. If eM Client holds an exclusive lock (e.g. during compaction), the open will fail — catch this and retry with a clear message.

1. **`scripts/explore_emclient.py`** — Run this FIRST to understand the actual schema.
   - Open `mail_data.dat` in read-only mode
   - `SELECT name FROM sqlite_master WHERE type='table'`
   - For each table: `PRAGMA table_info(table_name)`
   - Print sample rows from mail-related tables
   - Save findings to `docs/emclient-schema.md`
   - This script is exploratory — its output informs the parser implementation

2. **`parsers/email.py`**
   - Open eM Client SQLite database in read-only mode
   - Query emails: the `partHeader` column likely contains email headers as a BLOB
   - Parse headers to extract: From, To, Cc, Subject, Date, Message-ID
   - Extract body text (handle plain text and HTML variants)
   - Convert HTML to plain text using BeautifulSoup
   - Strip quoted reply chains (lines starting with `>`, `On ... wrote:` blocks)
   - Strip common email signatures
   - Return: `subject, body_text, sender, recipients, date, folder, message_id`

3. **`indexers/email_indexer.py`**
   - Locate eM Client DB from config or auto-detect
   - Open database in read-only mode (retry on lock with backoff)
   - Create/get the "email" system collection
   - Read emails via parser
   - For each email:
     - Check if message_id already indexed (skip if so)
     - Chunk with email chunker
     - Embed and insert
   - Track watermark (latest indexed message ID or date) in sources table

**Test**: Run explorer against live DB (read-only), then index a subset.

### Phase 4: Project Document Indexer

**Goal**: Index arbitrary document folders (PDF, DOCX, TXT, HTML, MD) into named project collections.

1. **`parsers/pdf.py`**
   - Use `pymupdf` (fitz) to extract text page by page
   - Return list of `(page_number, text)` tuples
   - Handle OCR-only PDFs gracefully (warn that text extraction yielded no content)

2. **`parsers/docx.py`**
   - Use `python-docx` to extract paragraphs
   - Preserve heading structure for chunking context
   - Extract text from tables as well

3. **`parsers/html.py`**
   - Use BeautifulSoup to extract text
   - Preserve heading structure

4. **`parsers/plaintext.py`**
   - Read file, return content. Simple.

5. **`indexers/project.py`**
   - Accept: collection name + one or more file/folder paths
   - Create the project collection if it doesn't exist
   - Walk folders recursively
   - Dispatch to the correct parser based on file extension:
     - `.md` → markdown parser
     - `.pdf` → pdf parser
     - `.docx` → docx parser
     - `.html`, `.htm` → html parser
     - `.txt`, `.csv`, `.json`, `.yaml`, `.yml` → plaintext parser
   - Chunk, embed, insert — same pipeline as other indexers
   - Support incremental updates via file hash comparison

**Test**: Create a project collection with a few test PDFs and Word docs. Search across it.

### Phase 5: CLI

**Goal**: Unified command-line interface for all operations.

```bash
# Indexing
local-rag index obsidian [--vault PATH]...       # Index Obsidian vaults (from config or args)
local-rag index email                             # Index eM Client emails
local-rag index project "Name" PATH [PATH]...    # Index docs into a named project

# Searching
local-rag search "query text"                     # Search all collections
local-rag search "query" --collection obsidian    # Search specific collection
local-rag search "query" --collection "Project A" # Search a project
local-rag search "query" --type pdf               # Filter by source type
local-rag search "query" --from "sender@mail.com" # Filter by email sender
local-rag search "query" --after 2025-01-01       # Filter by date
local-rag search "query" --top 20                 # Number of results

# Collection management
local-rag collections list                        # List all collections with doc counts
local-rag collections info "Project A"            # Show details of a collection
local-rag collections delete "Project A"          # Delete a project collection and all its data

# Status
local-rag status                                  # Overall stats: collections, doc counts, DB size, last index times

# MCP server
local-rag serve                                   # Start MCP server (stdio transport)
local-rag serve --port 8080                       # Start with HTTP/SSE transport
```

Use the `click` library. Register `local-rag` as a console script entry point in `pyproject.toml`.

### Phase 6: MCP Server

**Goal**: Expose the RAG as an MCP server so Claude Desktop and Claude Code can query it.

1. **`mcp_server.py`** — Implement using the `mcp` Python SDK.

   **Tools to expose:**

   - `rag_search(query: str, collection?: str, top_k?: int, source_type?: str, date_from?: str, date_to?: str) -> list[SearchResult]`
     - The primary tool. Returns ranked results with content, title, metadata, score, collection name, source path.
     - When collection is omitted, searches across all collections.

   - `rag_list_collections() -> list[CollectionInfo]`
     - Returns all collections with name, type, document count, last indexed timestamp.
     - Useful for Claude to know what's available before searching.

   - `rag_index(collection: str, path?: str) -> IndexResult`
     - Trigger indexing. For "obsidian" and "email", uses configured paths. For projects, requires a path.
     - Returns count of documents indexed/updated/skipped.

   - `rag_collection_info(collection: str) -> CollectionDetail`
     - Detailed info about a collection: source count, document count, source types breakdown, sample titles.

2. **Transport**: Use stdio transport by default (standard for Claude Code). Optionally support SSE for network access.

3. **Registration**:

   For **Claude Code**, add to the project's `.mcp.json`:
   ```json
   {
     "mcpServers": {
       "local-rag": {
         "command": "uv",
         "args": ["run", "--directory", "/path/to/local-rag", "local-rag", "serve"],
         "env": {}
       }
     }
   }
   ```

   For **Claude Desktop**, add to `~/Library/Application Support/Claude/claude_desktop_config.json`:
   ```json
   {
     "mcpServers": {
       "local-rag": {
         "command": "uv",
         "args": ["run", "--directory", "/path/to/local-rag", "local-rag", "serve"],
         "env": {}
       }
     }
   }
   ```

   For **claude.ai** (this chat interface), MCP servers from Claude Desktop are not directly available. The MCP connection here comes from browser extensions or Anthropic's own connectors. You would use the CLI or Claude Code for RAG access, or potentially expose it via a remote MCP server with authentication.

---

## Configuration

Config file location: `~/.local-rag/config.json`

```json
{
  "db_path": "~/.local-rag/rag.db",
  "embedding_model": "bge-m3",
  "embedding_dimensions": 1024,
  "chunk_size_tokens": 500,
  "chunk_overlap_tokens": 50,
  "obsidian_vaults": [
    "~/Documents/MyVault"
  ],
  "emclient_db_path": "~/Library/Application Support/eM Client",
  "search_defaults": {
    "top_k": 10,
    "rrf_k": 60,
    "vector_weight": 0.7,
    "fts_weight": 0.3
  }
}
```

Create a `config.example.json` in the repo root with placeholder paths and comments.

---

## Key Constraints & Rules

- **Everything runs locally.** No cloud APIs, no API keys, no data leaves the machine.
- **Embedding model must be configurable.** Default to `bge-m3` (1024) but support switching to `mxbai-embed-large` (1024d) or others. If the model changes, all existing embeddings must be regenerated (warn the user).
- **Incremental indexing by default.** Use file hashes (SHA256) for document files and message IDs for email. Provide `--force` flag to re-index everything.
- **Collection isolation.** Project collections are independent. Deleting a collection removes all its sources, documents, and embeddings cleanly (CASCADE).
- **Graceful error handling.** If Ollama is not running, print a clear error. If a PDF has no extractable text, warn and skip. Never crash mid-index — log errors and continue.
- **Search always returns source attribution.** Every result includes the collection name, source file path, and chunk context so the user can trace back to the original document.

---

## Coding Standards

- Type hints on all function signatures
- Dataclasses for structured data (Chunk, SearchResult, CollectionInfo, etc.)
- Docstrings on public functions
- No global state — pass db connections and config explicitly
- Use `logging` module, not print statements
- Tests for parsers and search logic (chunker edge cases, RRF merging, etc.)

---

## References

- sqlite-vec: https://github.com/asg017/sqlite-vec
- sqlite-rag CLI (for inspiration): https://github.com/sqliteai/sqlite-rag
- Ollama embedding docs: https://ollama.com/blog/embedding-models
- MCP Python SDK: https://github.com/modelcontextprotocol/python-sdk
- MCP specification: https://modelcontextprotocol.io
- eM Client stores data as SQLite: https://www.emclient.com/email-data-loss
- eM Client forensic schema analysis: https://github.com/SecurityAura/Aura-Research/blob/main/DFIR/BEC/eM%20Client/eMClient.md
- TDS SQLite RAG tutorial: https://towardsdatascience.com/retrieval-augmented-generation-in-sqlite/
- Private RAG stack example: https://github.com/LLM-Implementation/private-rag-embeddinggemma
- Inferable SQLite + Ollama RAG: https://www.inferable.ai/blog/posts/sqlite-rag