// Package mcp implements the MCP server for local-rag.
package mcp

import (
	"fmt"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/sebastianhutter/local-rag-go/internal/config"
	"github.com/sebastianhutter/local-rag-go/internal/embeddings"
)

// resolveEmbeddingHost picks the embedding Ollama host once at server startup,
// preferring a reachable configured host that serves the model.
func resolveEmbeddingHost() {
	if cfg, err := config.Load(""); err == nil {
		embeddings.ResolveHost(cfg.EmbeddingHosts, cfg.EmbeddingModel)
		embeddings.SetBatchSize(cfg.EmbeddingBatchSize)
		embeddings.SetNumBatch(cfg.EmbeddingNumBatch)
	}
}

// CreateServer builds an MCPServer with all tools registered.
func CreateServer() *server.MCPServer {
	s := server.NewMCPServer(
		"local-rag",
		"1.0.0",
		server.WithInstructions("Local RAG system for searching personal knowledge. "+
			"Indexes Obsidian vaults, emails, ebooks, RSS feeds, code repositories, "+
			"and project documents into a SQLite database with hybrid vector + full-text search."),
	)

	s.AddTools(
		server.ServerTool{Tool: ragSearchTool, Handler: handleRagSearch},
		server.ServerTool{Tool: ragListCollectionsTool, Handler: handleRagListCollections},
		server.ServerTool{Tool: ragIndexTool, Handler: handleRagIndex},
		server.ServerTool{Tool: ragCollectionInfoTool, Handler: handleRagCollectionInfo},
		server.ServerTool{Tool: ragPruneTool, Handler: handleRagPrune},
		server.ServerTool{Tool: ragNeighborsTool, Handler: handleRagNeighbors},
	)

	return s
}

// ServeStdio runs the MCP server over stdin/stdout.
func ServeStdio() error {
	resolveEmbeddingHost()
	s := CreateServer()
	slog.Info("starting MCP server (stdio)")
	return server.ServeStdio(s)
}

// ServeSSE runs the MCP server over HTTP/SSE on the given port.
func ServeSSE(port int) error {
	resolveEmbeddingHost()
	s := CreateServer()
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	slog.Info("starting MCP server (SSE)", "addr", addr)
	sseServer := server.NewSSEServer(s)
	return sseServer.Start(addr)
}

// Tool definitions

var ragSearchTool = mcp.NewTool("rag_search",
	mcp.WithDescription(
		"Search personal knowledge using hybrid vector + full-text search with "+
			"Reciprocal Rank Fusion. Searches across all indexed collections by default, "+
			"combining semantic similarity with keyword matching. "+
			"Supports filtering by arbitrary metadata fields via metadata_filter parameter."),
	mcp.WithString("query",
		mcp.Required(),
		mcp.Description("Search query text (natural language or keywords)")),
	mcp.WithString("collection",
		mcp.Description("Filter by collection name or type ('system', 'project', 'code'). Omit to search all, "+
			"except collections the config excludes from the default sweep — naming one of those "+
			"explicitly still searches it.")),
	mcp.WithNumber("top_k",
		mcp.Description("Number of results to return (default: 10)")),
	mcp.WithString("source_type",
		mcp.Description("Filter by type: 'markdown', 'pdf', 'docx', 'epub', 'html', 'txt', 'email', 'code', 'commit', 'rss'. "+
			"Markdown documents may include frontmatter-derived metadata (e.g. source, issue_key, url for Jira/Confluence docs).")),
	mcp.WithString("path",
		mcp.Description("Filter by source path (case-insensitive substring of the absolute file path). "+
			"Use to scope to a subfolder or repo, e.g. 'backend/services' or 'infrastructure/modules'.")),
	mcp.WithString("date_from",
		mcp.Description("Results after this date (YYYY-MM-DD)")),
	mcp.WithString("date_to",
		mcp.Description("Results before this date (YYYY-MM-DD)")),
	mcp.WithString("sender",
		mcp.Description("Filter by email sender (case-insensitive substring)")),
	mcp.WithString("author",
		mcp.Description("Filter by book author (case-insensitive substring)")),
	mcp.WithObject("metadata_filter",
		mcp.Description("Filter by arbitrary metadata fields. JSON object of key-value string pairs. "+
			"Matches are case-insensitive substring for strings, element-wise for arrays. "+
			"Example: {\"source\": \"jira\", \"issue_key\": \"CB-123\"}")),
)

// rag_neighbors answers a question hybrid search structurally cannot: not
// "what resembles this query" but "what is this document connected to".
var ragNeighborsTool = mcp.NewTool("rag_neighbors",
	mcp.WithDescription(
		"Walk the relation graph out from an indexed source and return what it is connected to: "+
			"linked notes, a wiki page's parent and children, the tickets a document mentions, and "+
			"whatever mentions it. Use it after rag_search to follow a result outwards -- it finds "+
			"documents that are related but share no wording with the query, which vector and "+
			"full-text search cannot reach. Returns titles, paths and one-line snippets rather than "+
			"full content, so it costs a fraction of another search. Requires 'local-rag graph "+
			"rebuild' to have been run."),
	mcp.WithNumber("source_id",
		mcp.Required(),
		mcp.Description("The source_id of an indexed file, as returned in rag_search results")),
	mcp.WithString("rel",
		mcp.Description("Only follow these relations, comma-separated. Common ones: 'links_to' "+
			"(a wikilink), 'child_of' (wiki page hierarchy), 'mentions' (a ticket key), plus "+
			"frontmatter property names such as 'related' or 'parent'. Omit to follow all.")),
	mcp.WithString("origin",
		mcp.Description("Only follow edges derived this way, comma-separated: 'confluence', "+
			"'frontmatter', 'wikilink', 'ticket-regex'. Omit to follow all.")),
	mcp.WithNumber("hops",
		mcp.Description("Traversal depth, 1 or 2 (default 1). Two hops over a densely "+
			"cross-referenced corpus returns a lot; prefer 1 and follow up on what looks useful.")),
	mcp.WithNumber("hub_cap",
		mcp.Description("Do not expand through a source with more edges than this (default 25). "+
			"A hub is a fine destination and a poor route: a ticket 400 documents mention says "+
			"nothing about which of them belong together. Raise it to expand through one anyway.")),
	mcp.WithNumber("limit",
		mcp.Description("Maximum neighbours to return (default 20)")),
)

var ragListCollectionsTool = mcp.NewTool("rag_list_collections",
	mcp.WithDescription(
		"List all available collections with source file counts, chunk counts, "+
			"and metadata. Collections of type 'code' represent repository collections that "+
			"may contain multiple git repos."),
)

var ragIndexTool = mcp.NewTool("rag_index",
	mcp.WithDescription(
		"Trigger indexing for a collection. For system collections ('obsidian', "+
			"'email', 'calibre', 'rss'), uses configured paths. For repository collections, "+
			"indexes all repos in that collection. For project collections, uses "+
			"configured paths."),
	mcp.WithString("collection",
		mcp.Required(),
		mcp.Description("Collection name ('obsidian', 'email', 'calibre', 'rss', repository collection name, or project name)")),
	mcp.WithString("path",
		mcp.Description("Path to index (required for project collections)")),
)

var ragPruneTool = mcp.NewTool("rag_prune",
	mcp.WithDescription(
		"Remove stale indexed entries whose originals no longer exist. "+
			"Prunes deleted files from Obsidian/projects, removed emails from eM Client, "+
			"purged RSS articles from NetNewsWire, removed books from Calibre, "+
			"and deleted code files from repositories."),
	mcp.WithString("collection",
		mcp.Description("Collection name to prune. Omit to prune all collections.")),
)

var ragCollectionInfoTool = mcp.NewTool("rag_collection_info",
	mcp.WithDescription(
		"Get detailed information about a specific collection. Returns source count, "+
			"chunk count, source type breakdown, last indexed timestamp, and a sample "+
			"of document titles."),
	mcp.WithString("collection",
		mcp.Required(),
		mcp.Description("The collection name. Use rag_list_collections() to discover available names.")),
)
