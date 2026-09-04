# CLAUDE.md

## Project: local-rag

A fully local, privacy-preserving RAG (Retrieval Augmented Generation) system for macOS. Indexes personal knowledge from multiple sources into a single SQLite database with vector + full-text hybrid search. Exposes search via CLI and an MCP server so Claude Desktop and Claude Code can query it directly.

---

## Quick Start

```bash
# Prerequisites
brew install ollama go
ollama pull bge-m3

# Optional: OCR support for scanned PDFs
brew install tesseract tesseract-lang

# Build
git clone https://github.com/sebastianhutter/local-rag.git
cd local-rag
make build            # binary at bin/local-rag

# Index sources
local-rag index obsidian
local-rag index email
local-rag index calibre
local-rag index rss
local-rag index code acme-tools
local-rag index code                    # all groups
local-rag index code acme-tools --history  # code + commit history
local-rag index project                  # all projects
local-rag index project "Project Alpha"  # specific project

# Search
local-rag search "kubernetes deployment strategy"
local-rag search "invoice from supplier" --collection email
local-rag search "API specification" --collection "Project Alpha"

# Remove entries whose originals are gone
local-rag prune                          # all collections
local-rag prune obsidian                 # one collection

# Run MCP server (for Claude Desktop / Claude Code integration)
local-rag serve
```

---

## Architecture

```mermaid
flowchart LR
    subgraph Sources
        OBS["Obsidian vault<br/>.md .pdf .docx .html .epub .txt"]
        EM["eM Client<br/>SQLite"]
        CAL["Calibre<br/>SQLite"]
        NNW["NetNewsWire<br/>SQLite"]
        GIT["Code repositories<br/>tree-sitter + commits"]
        PRJ["Project docs<br/>any folder"]
    end

    subgraph Indexer
        IDX["Go Indexer<br/>chunking + Ollama embed"]
    end

    subgraph Storage
        DB["rag.db<br/>SQLite + sqlite-vec + FTS5"]
    end

    subgraph Interface
        CLI["CLI"]
        MCP["MCP Server<br/>Claude Desktop / Claude Code"]
    end

    OBS --> IDX
    EM --> IDX
    CAL --> IDX
    NNW --> IDX
    GIT --> IDX
    PRJ --> IDX
    IDX --> DB
    DB --> CLI
    DB --> MCP
```

### Core Concepts

**Collections**: Every indexed source belongs to a collection. System collections ("obsidian", "email", "calibre", "rss") have dedicated parsers. Code repositories are collections of type "code" that contain one or more git repos grouped by org or topic. Project folders create project-type collections. Search can target a specific collection or search across all of them.

**Hybrid search**: Every query runs both vector similarity search (semantic) and FTS5 full-text search (keyword). Results are merged using Reciprocal Rank Fusion (RRF). This ensures that both "what does this mean" and "find the exact phrase" queries work well.

Vector search is two-stage for speed: a fast Hamming-distance KNN over binary-quantized vectors (`vec_documents_bin`) gathers a candidate pool, which is then reranked with the exact float vectors (`vec_documents`). This avoids a full-precision scan of every stored vector on each query. See `docs/hybrid-search-and-rrf.md`.

**Incremental indexing**: Track file hashes, modification times, and watermarks. Only re-embed changed or new content. Use `--force` to re-index everything.

**Relation graph** (see `docs/relation-graph.md`): `graph_edges` records parsed relations between indexed *sources* — Confluence page hierarchy, frontmatter properties, wikilinks and ticket-key mentions — so a search result can be expanded into what it is connected to rather than only what resembles it. Endpoints are sources, not documents: a relation belongs to the file, and `sources.id` survives a re-embed while a document id does not. Every edge carries a `rel` (what it means) and an `origin` (where it came from), and origins are rebuilt independently. Nothing calls a model; `local-rag graph rebuild` is a scan, not an indexing run.

Measured on a real database: 28,491 edges over 18,597 connected sources — 24,077 ticket mentions, 2,417 wikilinks, 1,745 Confluence parents, 252 typed frontmatter relations. Ticket mentions are the only class that crosses corpora (a mail, a commit and a note all reach the same issue) and cost one regex. Replaying 60 queries taken from real usage, **78% gained at least one document that vector + FTS could not reach at six times the normal `top_k`**.

Expansion cost is bounded by the *degree* of the nodes it starts from, not by the number of nodes, so a hub cap is not a refinement but a precondition: uncapped, expansion returned 24 documents per query instead of 3. The highest-degree sources here are a document enumerating 570 ticket keys and the vault's folder-index notes.

**Traversal**: `graph.Expand` walks out from seed sources, undirected — a page's children are as relevant as its parent. Two rules do the work. A **hub is a destination, not a corridor**: expansion never passes *through* a node whose degree exceeds `hub_cap` (default 25), including a seed, though such a node is still returned when something else reaches it. And ranking is by hops, then **ascending degree**, because degree stands in for specificity: a source two things point at says far more than one four hundred things mention.

Neighbours are collapsed by title before the limit is applied. Without that, automated notification mail sinks the results: one ticket attracts dozens of near-identical machine mails, each with a degree of 1, so rarest-first ranking hands them every slot. 30% of the mention edges in a real database come from tracker notifications.

Traversal returns titles, paths and one-line snippets — never full content. Measured: about 470 characters (~120 tokens) per neighbour, so a median expansion of seven costs roughly **820 tokens against ~3,500 for another `rag_search`** (the median payload over 388 real calls). Four times cheaper, and it reaches documents a second search cannot.

Two further rules keep an expansion small enough for that to hold. `PerSeedLimit` (default 5) stops one prolific seed monopolising the result — a wiki page with thirty children would otherwise contribute thirty siblings-by-proxy and bury what every other seed found. And a hub is not returned either unless `IncludeHubs` is set: a source with hundreds of edges is a table of contents, not an answer. Replaying the 60 mined queries, dropping those two rules raised the candidate count from 356 to 515 while adding navigation notes (`2 Projects.md`, degree 482) and twenty consecutive sprint pages.

Replayed against the real traversal, **78% of those queries gained at least one document unreachable at `top_k` 60**, median 7 per query.

**Pruning**: Indexing removes what indexing cannot see. Before indexing `obsidian`, `code`, `project` or `all`, a prune pass drops sources whose file no longer exists on disk, so deleted and moved files leave search results without a manual step; `--no-prune` skips it. For `obsidian` it also drops sources the vault walk would no longer visit — anything inside a folder named in `obsidian_exclude_folders`, an Obsidian internal directory, or a dot-directory. Without that, adding a folder to the exclude list stranded its documents permanently: the files still exist, so the existence check kept them, while the walk never refreshed them again. Both reasons are logged separately (`kind=file` versus `kind=excluded-folder`), so a mistyped exclude entry shows up as a surprising count rather than a silent deletion. `walkVault` and the prune predicate share `vaultSkipsDir` precisely so they cannot drift. Two deliberate narrowings: a cloud placeholder is never pruned (it exists, and skipping it is temporary), and an unsupported extension is not either — otherwise a change to the parser's extension map would become silent data loss. The standalone `local-rag prune [COLLECTION]` covers every collection type — including email, calibre and rss, which are pruned against their source databases rather than the filesystem. `prune --vectors` is a separate repair path: it deletes embeddings in `vec_documents`/`vec_documents_bin` whose `document_id` no longer resolves, which CASCADE cannot do because the vec0 virtual tables have no foreign keys.

---

## Supported Sources

| Source | Collection | CLI Command | Data Source |
|--------|------------|-------------|-------------|
| **Obsidian** | `obsidian` | `index obsidian` | Vault directory — all file types (.md, .pdf, .docx, .html, .txt, .epub) |
| **eM Client** | `email` | `index email` | SQLite databases (read-only) — subject, body, sender, recipients, date, folder |
| **Calibre** | `calibre` | `index calibre` | SQLite metadata.db + book files (read-only) — EPUB/PDF content with author, tags, series metadata |
| **NetNewsWire** | `rss` | `index rss` | SQLite databases (read-only) — RSS article title, author, content, feed name |
| **Code Repositories** | repo name | `index code [NAME]` | Git repos grouped by org/topic — paths can be direct repos or parent directories (repos are discovered recursively). Tree-sitter structural parsing + commit history (messages and per-file diffs), respects .gitignore |
| **Project Docs** | user name | `index project [NAME]` | Any folder — files dispatched to correct parser by extension, paths from config |

---

## Tech Stack

| Component    | Choice                     | Notes                                  |
|--------------|----------------------------|----------------------------------------|
| Language     | Go 1.26+                   | CGO required for SQLite                |
| Database     | SQLite + sqlite-vec + FTS5 | Single file, no server                 |
| Embeddings   | Ollama + bge-m3 (1024d)    | Fully local, no API keys               |
| GUI          | Fyne v2 + systray          | macOS menu bar app                     |
| MCP          | mcp-go                     | SSE and stdio transports               |
| PDF          | go-pdfium (WASM/Wazero)    | No CGO needed for PDF                  |
| PDF OCR      | tesseract (optional)       | Fallback for scanned/image-only PDFs   |
| DOCX         | archive/zip + encoding/xml | Word document extraction (.docx, .dotx)|
| Code parsing | go-tree-sitter             | 13 languages; AST split-then-merge (cAST) chunking |
| CLI          | Cobra                      | Subcommands, flags, help               |
| HTML cleanup | golang.org/x/net/html      | Strip tags from email/RSS              |

---

## Database Schema

The database lives at `~/.local-rag/rag.db` by default (configurable).

```sql
-- Collections: namespaces for organizing indexed content
-- System collections: 'obsidian', 'email', 'calibre', 'rss'
-- User collections: any name, used for project-based grouping
CREATE TABLE collections (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    collection_type TEXT NOT NULL DEFAULT 'project',  -- 'system', 'project', or 'code'
    description TEXT,
    paths TEXT,                               -- JSON array of source paths (used by project collections)
    created_at TEXT DEFAULT (datetime('now'))
);

-- Sources: individual files or email accounts that have been indexed
CREATE TABLE sources (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    collection_id INTEGER NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    source_type TEXT NOT NULL,           -- 'markdown', 'email', 'pdf', 'docx', 'txt', 'html', 'epub', 'code', 'rss'
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

-- Vector index (sqlite-vec virtual table) — exact float embeddings, used for reranking
CREATE VIRTUAL TABLE vec_documents USING vec0(
    embedding float[1024],
    document_id INTEGER
);

-- Binary-quantized mirror of vec_documents (1 bit/dim, ~32x smaller). Shares each
-- row's rowid with vec_documents so the exact float vector can be fetched by rowid.
-- Vector search runs a fast Hamming-distance KNN here to gather candidates, then
-- reranks them with the exact float vectors above. Kept in sync on insert/delete.
CREATE VIRTUAL TABLE vec_documents_bin USING vec0(
    embedding bit[1024],
    document_id INTEGER
);

-- Speeds up per-collection COUNT/aggregation (collections list/info) and
-- collection-scoped deletes; without it those queries full-scan documents.
CREATE INDEX idx_documents_collection_id ON documents(collection_id);

-- Full-text search index (FTS5)
CREATE VIRTUAL TABLE documents_fts USING fts5(
    title,
    content,
    content='documents',
    content_rowid='id'
);

-- Parsed relations between sources, for expanding a result into what it is
-- connected to. Endpoints are sources (a relation belongs to the file, and
-- sources.id survives a re-embed). Unlike the vec0 tables this one takes
-- foreign keys, so prune and collection-delete clean up edges for free.
-- rel = what the relation means ('links_to', 'child_of', 'mentions', or a
-- frontmatter property name such as 'related' or 'parent').
-- origin = where it came from ('wikilink', 'frontmatter', 'confluence',
-- 'ticket-regex'), so one class can be rebuilt or discarded alone.
CREATE TABLE graph_edges (
    src_source_id INTEGER NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    dst_source_id INTEGER NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    rel           TEXT NOT NULL,
    origin        TEXT NOT NULL,
    PRIMARY KEY (src_source_id, dst_source_id, rel)
);
CREATE INDEX idx_graph_edges_dst ON graph_edges(dst_source_id);
CREATE INDEX idx_graph_edges_origin ON graph_edges(origin);

-- Key/value store for schema bookkeeping: 'schema_version' drives db.Migrate,
-- 'binary_backfill_done' marks vec_documents_bin as fully populated so the
-- one-time backfill is not re-checked on every open.
CREATE TABLE meta (
    key TEXT PRIMARY KEY,
    value TEXT
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

**Schema changes.** Every statement above is `CREATE ... IF NOT EXISTS`, so `InitSchema` is idempotent and adding a new table, index or virtual table there is enough — existing databases pick it up on the next open. `db.Migrate` handles only what a re-run of `InitSchema` cannot fix: data that must be rewritten (`collection_type` reclassification), columns added to an existing table (`ALTER TABLE`), and one-time backfills. Bump `SchemaVersion` and add a `if current < N` block when a change needs that; otherwise `InitSchema` alone is the migration path.

---

## File Structure

```
local-rag/
├── CLAUDE.md                        # This file
├── Makefile                         # Build targets (build, test, lint, app, dmg)
├── README.md
├── go.mod / go.sum                  # Go module dependencies
├── cmd/
│   └── local-rag/
│       ├── main.go                  # Cobra root command, global flags, version
│       ├── cmd_index.go             # index (obsidian/email/calibre/rss/code/project/all)
│       ├── cmd_search.go            # search
│       ├── cmd_collections.go       # collections list/info/delete/export/paths
│       ├── cmd_prune.go             # prune, prune --vectors
│       ├── cmd_graph.go             # graph rebuild, graph stats
│       ├── cmd_neighbors.go         # neighbors
│       ├── cmd_status.go            # status
│       ├── cmd_serve.go             # serve (stdio / SSE)
│       └── cmd_gui.go               # gui
├── configs/
│   └── config.example.json          # Annotated configuration template
├── .github/
│   └── workflows/release.yml        # Tagged release build
├── docs/
│   ├── architecture.md              # System architecture overview
│   ├── emclient-schema.md           # eM Client SQLite schema documentation
│   ├── hybrid-search-and-rrf.md     # How hybrid search and RRF work
│   ├── relation-graph.md            # Why the graph exists, the four edge origins, traversal rules
│   └── ollama-and-embeddings.md     # Ollama setup and embedding models
├── internal/
│   ├── config/                      # Configuration loading and defaults
│   ├── db/                          # SQLite + sqlite-vec + FTS5 setup, migrations, orphan/prune queries
│   ├── embeddings/                  # Ollama embedding client + host resolution
│   ├── chunker/                     # Text chunking strategies (per file type)
│   ├── search/                      # Hybrid search engine (vector + FTS + RRF)
│   ├── graph/                       # Relation graph: edge derivation per origin, traversal
│   ├── parser/                      # File parsers (markdown, pdf, docx, epub, html, code, rss, email, calibre)
│   ├── indexer/                     # Source indexers (obsidian, email, calibre, rss, git, project),
│   │                                #   shared batching (batch.go), pruning (prune.go)
│   ├── mcp/                         # MCP server (tools, SSE, stdio)
│   └── gui/                         # Fyne menu bar app, settings, log viewer
└── scripts/
    ├── build-app.sh                 # Create macOS .app bundle
    └── build-dmg.sh                 # Create DMG installer
```

---

## CLI Commands

```bash
# Indexing
local-rag index obsidian [--vault/-V PATH]...    # Index Obsidian vaults (from config or args)
local-rag index email                             # Index eM Client emails
local-rag index calibre [--library/-l PATH]...   # Index Calibre ebook libraries
local-rag index rss                               # Index NetNewsWire RSS articles
local-rag index code [NAME] [--history]          # Index repository collection(s), --history for commit history
local-rag index project [NAME]                    # Index project(s) from config
local-rag index all                               # Index all configured sources at once

# All index commands support --force to re-index everything, --no-prune to skip the
# automatic prune pass that runs for obsidian/code/project/all, and --no-graph to skip
# the relation-graph rebuild that runs after any index

# Pruning
local-rag prune [COLLECTION] [-y]                 # Drop sources whose originals are gone; omit NAME for all
local-rag prune --vectors [-y]                    # Drop orphaned embeddings (no surviving document)

# Searching
local-rag search "query text"                     # Search all collections
local-rag search "query" --collection obsidian    # Search specific collection
local-rag search "query" --collection "Project A" # Search a project
local-rag search "query" --type pdf               # Filter by source type
local-rag search "query" --path infra/modules      # Filter by source path (substring of the file path)
local-rag search "query" --collection code --path backend/services  # Scope to a subfolder/repo
local-rag search "query" --from "sender@mail.com" # Filter by email sender
local-rag search "query" --author "Author Name"   # Filter by book author
local-rag search "query" --after 2025-01-01       # Filter by date
local-rag search "query" --meta source=jira       # Filter by metadata field
local-rag search "query" --meta issue_key=PROJ-123  # Filter by specific metadata value
local-rag search "query" --top 20                 # Number of results

# Collection management
local-rag collections list                       # List all collections with counts
local-rag collections info NAME                  # Show collection details
local-rag collections delete NAME [-y]           # Delete a collection and all its data
local-rag collections export NAME                # Export collection metadata as JSON
local-rag collections paths list NAME            # List configured paths for a collection
local-rag collections paths add NAME PATH...     # Add paths to a collection in config
local-rag collections paths remove NAME PATH...  # Remove paths from a collection in config
local-rag collections paths update NAME \        # Rewrite path prefixes in-place
  --old-prefix OLD --new-prefix NEW              # (config paths + source paths in DB)

# Relation graph
local-rag graph rebuild                  # Derive all edge origins and replace the stored set
local-rag graph rebuild --origin wikilink,confluence   # Rebuild only these origins
local-rag graph stats                    # Edge counts by origin and relation, plus the top hubs
local-rag neighbors SOURCE               # What a source is connected to (id or path substring)
local-rag neighbors 130057 --rel related --hops 2 --hub-cap 40 --top 10

# Status and GUI
local-rag status                        # Overall stats: collections, doc counts, DB size, Ollama status
local-rag gui                           # Start menu bar app (default when no subcommand)
local-rag --version                     # Print version
local-rag -v, --verbose                 # Debug logging (global flag)

# MCP server
local-rag serve                         # Start MCP server (stdio transport)
local-rag serve --port 31123            # Start with HTTP/SSE transport

# Six MCP tools: rag_search, rag_neighbors, rag_list_collections, rag_collection_info,
#   rag_index, rag_prune
# rag_search with metadata_filter: {"source": "jira"} filters by frontmatter fields
# rag_search also accepts a "path" param: a case-insensitive substring of the
# source path to scope results to a subfolder or repo (e.g. "backend/services")
# rag_search's "collection" param takes a name OR a type ('system', 'project', 'code')
# rag_search results carry "source_id", which rag_neighbors takes -- so an agent can
#   search, then follow a result outwards through the relation graph
# rag_neighbors(source_id, rel?, origin?, hops?, hub_cap?, limit?) returns titles, paths
#   and one-line snippets, never full content
# Collections in search_defaults.exclude_collections are skipped when "collection"
# is omitted; naming one explicitly still searches it
```

---

## Configuration

Config file location: `~/.local-rag/config.json`

```json
{
  "db_path": "~/.local-rag/rag.db",
  "embedding_model": "bge-m3",
  "embedding_dimensions": 1024,
  "embedding_hosts": [
    "http://192.168.30.90:11434",
    "http://127.0.0.1:11434"
  ],
  "embedding_batch_size": 32,
  "embedding_workers": 4,
  "embedding_num_batch": 0,
  "chunk_size_tokens": 500,
  "chunk_overlap_tokens": 50,
  "obsidian_vaults": [
    "~/Documents/MyVault"
  ],
  "obsidian_exclude_folders": [
    "_Inbox",
    "_Templates"
  ],
  "emclient_db_path": "~/Library/Application Support/eM Client",
  "calibre_libraries": [
    "~/CalibreLibrary"
  ],
  "netnewswire_db_path": "~/Library/Containers/com.ranchero.NetNewsWire-Evergreen/Data/Library/Application Support/NetNewsWire/Accounts",
  "repositories": {
    "my-org": ["~/Repository/my-org"],
    "terraform": ["~/Repository/my-org/tf-infra", "~/Repository/other-org/tf-modules"]
  },
  "projects": {
    "client-docs": ["~/Documents/client-project/specs", "~/Documents/client-project/notes"],
    "research": ["~/Documents/research-papers"]
  },
  "disabled_collections": [],
  "skip_cloud_placeholders": true,
  "git_history_in_months": 6,
  "git_commit_subject_blacklist": [
    "Automated show, episode and transcript sync"
  ],
  "search_defaults": {
    "top_k": 10,
    "rrf_k": 60,
    "vector_weight": 0.7,
    "fts_weight": 0.3,
    "exclude_collections": []
  },
  "graph": {
    "mention_exclude_senders": ["jira@", "noreply@"]
  },
  "ocr": {
    "enabled": false,
    "languages": ["eng"],
    "max_pages": 50,
    "max_file_size_mb": 100,
    "min_word_count": 10
  }
}
```

**`embedding_hosts`** (optional): an ordered list of Ollama endpoints. At startup (index/search/serve/GUI) the first host that is reachable **and already serves `embedding_model`** is selected and exported as `OLLAMA_HOST`; if none qualify it falls back to Ollama's default (localhost). An `OLLAMA_HOST` already set in the environment overrides the list. This lets a fast remote/GPU Ollama be used when available (e.g. for a heavy reindex) and transparently fall back to local otherwise. **All listed hosts must serve the same embedding model** (identical weights) or vectors will be inconsistent with the existing corpus. `index code` / `index all` process collections in sorted (deterministic) order.

**`embedding_batch_size`** (optional, default `32`): number of texts sent per Ollama embedding request. Larger batches keep a GPU host better fed — throughput scales up to ~128 (diminishing returns beyond) — at the cost of more memory per request, so a small/CPU/memory-constrained host may prefer a lower value. All of `embedding_model`, `embedding_hosts`, and `embedding_batch_size` are editable in the menu-bar app under **Settings → General** (embedding batch size field + the *Ollama Hosts* card).

**`embedding_workers`** (optional, default `4`): how many embedding requests are in flight at once. A single request leaves a remote host idle between round trips; several keep a GPU fed. Database writes stay on one goroutine — SQLite takes no concurrent writers — so this only parallelises the network-bound part. Raise it for a fast remote host, set it to `1` to serialise. Editable under **Settings → General**.

**All** indexers share one batching path (`internal/indexer/batch.go`): every source reduces its unit of work to an `indexItem` (identity, chunks, metadata), and the batcher groups *several items* into a single embedding request rather than sending one request per item, so `embedding_batch_size` is actually filled. Previously each file, article, email, book and commit paid its own network round trip — dominant cost when Ollama is remote. Measured against a remote GPU host: 200 RSS articles went from ~85s over 200 requests to ~2.8s over 2 (**~30x**).

Items are built lazily as batches fill, so memory is bounded to `embedding_workers × embedding_batch_size` chunks, and expensive extraction (PDF text, OCR, tree-sitter, `git show`) only runs for items that are actually going to be re-embedded — an unchanged file is never opened. Writes stay on one goroutine because SQLite takes no concurrent writers.

**`embedding_num_batch`** (optional, default `0` = server default): sent to Ollama as the `num_batch` model option when non-zero. llama.cpp must fit an entire embedding input into one *physical batch*, so an input longer than this is rejected with `input (N tokens) is too large to process` — even though Ollama has already truncated it to the model's context. With bge-m3's 8192-token context and Ollama's default physical batch of 2048, any chunk over ~2048 tokens fails. Setting this to the context length makes everything the model accepts also processable. Measured throughput is unchanged by this option (53-56 texts/sec at 0, 2048, 4096 and 8192), but Ollama applies it at model load, so a non-zero value reloads the model once. Left at `0` by default; raise it only if the log shows inputs being rejected. Editable under **Settings → General**.

Note that `chunk_size_tokens` counts whitespace-separated **words**, not model tokens, so dense content (code, minified data, non-English text) can produce chunks several times larger in tokens than the setting suggests. A batch whose embedding request fails is retried one item at a time, so a single unembeddable item costs only itself instead of discarding everything batched with it.

`--force` clears the collection (or, for code, the repository) up front rather than purging batch by batch. Deleting from the vec0 vector tables filters on an un-indexed column and full-scans them, which at ~800k vectors costs ~6.6s per 512-item wave — comparable to the embedding it accompanies. Clearing once costs ~9s for the whole run and drops the per-wave write to ~80ms.

**`search_defaults.exclude_collections`** (optional, default `[]`): collection names or types skipped when a search does not name a collection of its own. Naming one explicitly still searches it, so this demotes a collection out of the default sweep rather than hiding it — the exclusion is applied in `search.Search`, so CLI, MCP and GUI all inherit it without knowing it exists.

It exists because a collection can be large enough to crowd the results without being wrong. Measured on a real database: an archive of Claude session transcripts (`Notes/_Claude Sessions/`) was 261 notes — 11% of Obsidian sources — but 311,014 documents: 91% of the Obsidian collection and 40% of the entire database, at a median 612 chunks per note against 7 for an ordinary note. Across 60 queries replayed from real usage it took **50% of all top-10 result slots**. That alone does not make those results wrong — for a question the transcript actually discussed, it may be the best source — but excluding it from the default sweep roughly doubled what graph-style neighbour expansion could reach (120 → 259 candidate documents), because a transcript chunk occupies a seed slot without connecting to anything. The archive stays searchable with `--collection claude-sessions`; it just stops competing for every query. Editable under **Settings → Search**.

Note that exclusion counts as a filter, so it widens the vector candidate pool and the FTS candidate limit exactly as the other filters do — a collection can be excluded without the result count collapsing.

**`graph.mention_exclude_senders`** (optional, default `[]`): senders whose mail contributes no ticket-mention edges, matched as a case-insensitive substring of the sender field — `"jira@"` covers `Someone (Jira) <jira@example.atlassian.net>` without needing the display name. Editable under **Settings → Graph**; takes effect on the next `graph rebuild`.

Tracker notification mail names a ticket without referring to it: the mail exists *because* of the ticket, so an edge between them is a tautology that adds nothing the ticket's own record holds. Worse, it wins traversal outright — ranking prefers a low-degree neighbour as more specific, and nothing ever mentions a notification, so every notification looks maximally specific. Measured on a real database, **7,323 of 24,077 mention edges (30%) came from two notification senders**, and expanding a ticket returned five machine mails before any human reference.

The exclusion applies to mentions only. A wikilink in a source from an excluded sender is still an edge, because who sent something says nothing about what it links to.

**`skip_cloud_placeholders`** (optional, default `true`): skip files that exist only in the cloud. macOS marks on-demand files from OneDrive, iCloud Drive, Google Drive and Synology Drive with the `SF_DATALESS` flag — the name, size and mtime are local but the data is not. Opening one makes macOS download it from the provider first, so indexing a mostly-online folder is bounded by network speed and materialises the files on disk (a OneDrive shared library can be hundreds of GB). Placeholders are stat-ed but never opened, so a skipped file costs nothing; a per-path warning reports how many were skipped. Set to `false` to download and index them anyway, or mark the folders *Always Keep on This Device* in Finder. Editable under **Settings → General** (*Cloud Storage* card). Pruning is unaffected — a placeholder still exists on disk, so previously indexed content is not removed.

Indexing skips unchanged files by comparing the stored `file_modified_at` against the filesystem before hashing. The content hash still decides whether a file is re-embedded, but an untouched file is never opened — without this, every run re-read every file, which on cloud storage meant re-downloading anything the provider had evicted.

---

## MCP Server Registration

### GUI Mode (SSE) — recommended for Claude Code

When the menu bar app is running, its built-in MCP server uses SSE on `http://127.0.0.1:31123/sse`.

Add to the project's `.mcp.json`:
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

### Standalone Mode (stdio) — for Claude Desktop

For **Claude Desktop**, add to `~/Library/Application Support/Claude/claude_desktop_config.json`:
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

---

## Key Constraints & Rules

- **This repository is public — no names from the indexed corpus.** Code, tests, doc comments, `docs/` pages and commit messages must not contain customer or project names, real ticket keys, note titles, people, internal hostnames or mail addresses. The temptation is specific to this project: development means measuring against a live personal database, and the obvious example to reach for when writing a comment or a fixture is the one just measured. Translate before writing it down — `PROJ-42`, `acme-tools`, `example.atlassian.net`, a generic note title. Aggregate measurements are welcome and are what makes the documentation useful; the identities behind them are not.
- **Everything runs locally.** No cloud APIs, no API keys, no data leaves the machine.
- **Embedding model must be configurable.** Default to `bge-m3` (1024) but support switching to `mxbai-embed-large` (1024d) or others. If the model changes, all existing embeddings must be regenerated (warn the user).
- **Incremental indexing by default.** Use file hashes (SHA256) for document files, message IDs for email, and watermarks for date-based sources. Provide `--force` flag to re-index everything.
- **Collection isolation.** Collections are independent. Deleting a collection removes all its sources, documents, and embeddings cleanly (CASCADE).
- **Collection names are unique across all source types.** A name used by both `repositories` and `projects` (or shadowing a system collection) would resolve to one row and merge two unrelated corpora into a single collection — indexing both appears to succeed while the collections list shows one entry. Config loading logs a warning, and indexing that name fails with a clear message; other collections are unaffected. Rename one of them, then index under the new name.
- **Graceful error handling.** If Ollama is not running, print a clear error. If a PDF has no extractable text, warn and skip. Never crash mid-index — log errors and continue.
- **Search always returns source attribution.** Every result includes the collection name, source file path, and chunk context so the user can trace back to the original document.
- **Read-only access to external databases.** eM Client, Calibre, and NetNewsWire databases are always opened in SQLite read-only mode to prevent accidental writes.
- **Collections can be disabled.** Add collection names to `disabled_collections` in config to stop indexing without deleting existing data. Works with any collection name: system collections (`obsidian`, `email`, `calibre`, `rss`) or user-created ones (repository collection names, project names).

---

## Coding Standards

- Exported types and functions have Go doc comments
- Structs for structured data (Chunk, SearchResult, etc.) — no untyped maps for public API
- Error values returned, not panics; wrap errors with `fmt.Errorf("...: %w", err)`
- No global state — pass `*sql.DB` and `*config.Config` explicitly through call stack
- Use `log/slog` for structured logging, not `fmt.Print`
- Build with `make build` and test with `make test` (both require `-tags sqlite_fts5`)
- Tests live in `_test.go` files alongside the code they test

---

## References

- sqlite-vec: https://github.com/asg017/sqlite-vec
- Ollama embedding docs: https://ollama.com/blog/embedding-models
- mcp-go: https://github.com/mark3labs/mcp-go
- MCP specification: https://modelcontextprotocol.io
- go-pdfium: https://github.com/klippa-app/go-pdfium
- go-tree-sitter: https://github.com/smacker/go-tree-sitter
- eM Client forensic schema analysis: https://github.com/SecurityAura/Aura-Research/blob/main/DFIR/BEC/eM%20Client/eMClient.md
