# local-rag

A fully local, privacy-preserving RAG (Retrieval Augmented Generation) system for macOS. Indexes personal knowledge from multiple sources into a single SQLite database with hybrid vector + full-text search. Runs as a menu bar app with built-in MCP server so Claude Desktop and Claude Code can query your personal knowledge base directly.

## Supported Sources

| Source           | Collection Type | What Gets Indexed                                                                |
|------------------|-----------------|----------------------------------------------------------------------------------|
| **Obsidian**     | system          | Vault files — `.md`, `.pdf`, `.docx`, `.html`, `.txt`, `.epub`                   |
| **eM Client**    | system          | Emails — subject, body, sender, recipients, date, folder                         |
| **Calibre**      | system          | Ebook metadata + content — EPUB/PDF with author, tags, series                    |
| **NetNewsWire**  | system          | RSS articles — title, author, content, feed name                                 |
| **Code Groups**  | code            | Git repos grouped by org/topic — tree-sitter structural parsing + commit history |
| **Project Docs** | project         | Any folder of documents dispatched to the correct parser by extension            |

## Installation

### From GitHub Releases

Download the latest `.dmg` from [Releases](../../releases), open it, and drag **local-rag** to Applications.

Since the app is not code-signed, bypass Gatekeeper on first launch:

```bash
xattr -cr /Applications/local-rag.app
```

### Build from Source

```bash
# Prerequisites
brew install ollama go
ollama pull bge-m3

# Build
git clone https://github.com/sebastianhutter/local-rag-go.git
cd local-rag-go
make build            # binary at bin/local-rag
make app              # macOS .app bundle at bin/local-rag.app
make dmg              # DMG installer at bin/local-rag.dmg
```

Requires Go 1.24+, CGO enabled (for SQLite), and macOS (for `sips`/`iconutil`/`hdiutil`).

## Quick Start

1. **Configure sources** — edit `~/.local-rag/config.json` or use the Settings GUI:

```json
{
  "obsidian_vaults": ["~/Documents/MyVault"],
  "calibre_libraries": ["~/CalibreLibrary"],
  "code_groups": {
    "my-org": ["~/Repository/my-org/repo1", "~/Repository/my-org/repo2"]
  }
}
```

2. **Index your content:**

```bash
local-rag index obsidian
local-rag index email
local-rag index calibre
local-rag index rss
local-rag index group my-org --history
local-rag index all                        # everything at once
```

3. **Search:**

```bash
local-rag search "kubernetes deployment strategy"
local-rag search "invoice from supplier" --collection email
local-rag search "API specification" --type pdf --top 20
```

## GUI

Launch `local-rag` with no arguments (or `local-rag gui`) to start the menu bar app. There is no Dock icon — it lives entirely in the macOS menu bar.

**Menu bar features:**
- **MCP server toggle** — start/stop the built-in MCP SSE server (default port 31123)
- **Status display** — collection and chunk counts, indexing progress
- **Index menu** — trigger indexing for individual collections or all at once
- **Settings** — configure sources, embedding model, search weights, MCP port, auto-reindex interval, start-on-login
- **Log viewer** — live scrolling log output with auto-scroll toggle
- **Auto-reindex** — periodically re-index all sources on a configurable interval
- **macOS notifications** — notifies when indexing completes or errors occur

## MCP Integration

local-rag exposes 4 MCP tools: `rag_search`, `rag_list_collections`, `rag_collection_info`, and `rag_index`.

### GUI Mode (SSE)

When running as a menu bar app, the MCP server uses SSE transport on `http://127.0.0.1:31123/sse`.

**Claude Code** — add to your project's `.mcp.json`:

```json
{
  "mcpServers": {
    "local-rag": {
      "type": "sse",
      "url": "http://127.0.0.1:31123/sse"
    }
  }
}
```

**Claude Desktop** — add to `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "local-rag": {
      "command": "/path/to/local-rag",
      "args": ["serve"]
    }
  }
}
```

### Standalone Mode (stdio)

For use as a subprocess without the GUI:

```json
{
  "mcpServers": {
    "local-rag": {
      "command": "/path/to/local-rag",
      "args": ["serve"]
    }
  }
}
```

## CLI Reference

### Indexing

```
local-rag index obsidian [--vault/-V PATH]...   Index Obsidian vaults
local-rag index email                            Index eM Client emails
local-rag index calibre [--library/-l PATH]...   Index Calibre ebook libraries
local-rag index rss                              Index NetNewsWire RSS articles
local-rag index group [NAME] [--history]         Index code group(s); omit NAME for all
local-rag index project NAME [PATH...]           Index documents into a named project
local-rag index all                              Index all configured sources
```

All index commands accept `--force` to re-index everything regardless of change detection.

### Searching

```
local-rag search QUERY [flags]

Flags:
  -c, --collection STRING   Filter by collection name
      --type STRING         Filter by source type (markdown, pdf, email, code, ...)
      --from STRING         Filter by email sender
      --author STRING       Filter by book author
      --after YYYY-MM-DD    Only results after this date
      --before YYYY-MM-DD   Only results before this date
      --top INT             Number of results (default: 10)
```

### Collections

```
local-rag collections list              List all collections with counts
local-rag collections info NAME         Show collection details
local-rag collections delete NAME [-y]  Delete a collection and all its data
local-rag collections export NAME       Export collection metadata as JSON
```

### Other

```
local-rag status            Database stats, collection counts, Ollama status
local-rag serve [--port N]  Start MCP server (stdio, or SSE on given port)
local-rag gui               Start menu bar app (default when no subcommand)
local-rag --version         Print version
local-rag -v, --verbose     Enable debug logging (global flag)
```

## Configuration

Config file: `~/.local-rag/config.json`

| Key                                 | Default                                   | Description                              |
|-------------------------------------|-------------------------------------------|------------------------------------------|
| `db_path`                           | `~/.local-rag/rag.db`                     | SQLite database location                 |
| `embedding_model`                   | `bge-m3`                                  | Ollama embedding model                   |
| `embedding_dimensions`              | `1024`                                    | Embedding vector dimensions              |
| `chunk_size_tokens`                 | `500`                                     | Chunk size in tokens                     |
| `chunk_overlap_tokens`              | `50`                                      | Overlap between chunks                   |
| `obsidian_vaults`                   | `[]`                                      | Paths to Obsidian vaults                 |
| `obsidian_exclude_folders`          | `[]`                                      | Folders to skip in vaults                |
| `emclient_db_path`                  | `~/Library/Application Support/eM Client` | eM Client database path                  |
| `calibre_libraries`                 | `[]`                                      | Paths to Calibre libraries               |
| `netnewswire_db_path`               | *(auto-detected)*                         | NetNewsWire database path                |
| `code_groups`                       | `{}`                                      | Map of group name to repo paths          |
| `disabled_collections`              | `[]`                                      | Collection names to skip during indexing |
| `git_history_in_months`             | `6`                                       | How far back to index commit history     |
| `git_commit_subject_blacklist`      | `[]`                                      | Commit subjects to skip                  |
| `search_defaults.top_k`             | `10`                                      | Default number of search results         |
| `search_defaults.rrf_k`             | `60`                                      | Reciprocal Rank Fusion parameter         |
| `search_defaults.vector_weight`     | `0.7`                                     | Weight for vector similarity             |
| `search_defaults.fts_weight`        | `0.3`                                     | Weight for full-text search              |
| `gui.auto_start_mcp`                | `true`                                    | Start MCP server when GUI launches       |
| `gui.mcp_port`                      | `31123`                                   | SSE server port                          |
| `gui.auto_reindex`                  | `false`                                   | Enable periodic re-indexing              |
| `gui.auto_reindex_interval_minutes` | `60`                                      | Minutes between auto-reindex runs        |
| `gui.start_on_login`                | `false`                                   | Register as login item via launchd       |

## Tech Stack

| Component    | Choice                     | Notes                                  |
|--------------|----------------------------|----------------------------------------|
| Language     | Go 1.24+                   | CGO required for SQLite                |
| Database     | SQLite + sqlite-vec + FTS5 | Single file, no server                 |
| Embeddings   | Ollama + bge-m3 (1024d)    | Fully local, no API keys               |
| GUI          | Fyne v2 + systray          | macOS menu bar app                     |
| MCP          | mcp-go                     | SSE and stdio transports               |
| PDF          | go-pdfium (WASM/Wazero)    | No CGO needed for PDF                  |
| DOCX         | lu4p/cat                   | Word document extraction               |
| Code parsing | go-tree-sitter             | 13 languages with structural splitting |
| CLI          | Cobra                      | Subcommands, flags, help               |
| HTML cleanup | golang.org/x/net/html      | Strip tags from email/RSS              |

### Tree-sitter Languages

Structural splitting (functions, classes, methods): Python, Go, HCL/Terraform, TypeScript, TSX, JavaScript, Rust, Java, C, C++, C#, Ruby, Bash.

Full-file parsing: YAML, TOML, SQL, HTML, CSS, Dockerfile, Markdown.

Plaintext fallback: JSON, TXT, CSV, RST, XML, SCSS, Makefile.

## Building & Development

```bash
make build       # Build binary to bin/local-rag
make test        # Run tests (requires -tags sqlite_fts5)
make test-v      # Verbose tests
make lint        # golangci-lint
make tidy        # go mod tidy
make app         # Build macOS .app bundle
make dmg         # Build DMG installer
make clean       # Remove bin/
```

Pass `VERSION` to inject version at build time:

```bash
make build VERSION=1.2.3
bin/local-rag --version    # local-rag version 1.2.3
```

### Architecture

```
cmd/local-rag/       CLI entry point (Cobra)
internal/
  config/            Configuration loading and defaults
  db/                SQLite + sqlite-vec + FTS5 setup and migrations
  embeddings/        Ollama embedding client
  chunker/           Text chunking strategies
  search/            Hybrid search engine (vector + FTS + RRF)
  parser/            File parsers (markdown, pdf, docx, epub, html, code, ...)
  indexer/           Source indexers (obsidian, email, calibre, rss, git, project)
  mcp/               MCP server (tools, SSE, stdio)
  gui/               Fyne menu bar app, settings, log viewer
scripts/
  build-app.sh       Create macOS .app bundle
  build-dmg.sh       Create DMG installer
```
