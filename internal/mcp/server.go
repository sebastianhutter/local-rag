// Package mcp implements the MCP server for local-rag.
package mcp

import (
	"fmt"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

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
	)

	return s
}

// ServeStdio runs the MCP server over stdin/stdout.
func ServeStdio() error {
	s := CreateServer()
	slog.Info("starting MCP server (stdio)")
	return server.ServeStdio(s)
}

// ServeSSE runs the MCP server over HTTP/SSE on the given port.
func ServeSSE(port int) error {
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
			"combining semantic similarity with keyword matching."),
	mcp.WithString("query",
		mcp.Required(),
		mcp.Description("Search query text (natural language or keywords)")),
	mcp.WithString("collection",
		mcp.Description("Filter by collection name or type ('system', 'project', 'code'). Omit to search all.")),
	mcp.WithNumber("top_k",
		mcp.Description("Number of results to return (default: 10)")),
	mcp.WithString("source_type",
		mcp.Description("Filter by type: 'markdown', 'pdf', 'docx', 'epub', 'html', 'txt', 'email', 'code', 'commit', 'rss'. "+
			"Markdown documents may include frontmatter-derived metadata (e.g. source, issue_key, url for Jira/Confluence docs).")),
	mcp.WithString("date_from",
		mcp.Description("Results after this date (YYYY-MM-DD)")),
	mcp.WithString("date_to",
		mcp.Description("Results before this date (YYYY-MM-DD)")),
	mcp.WithString("sender",
		mcp.Description("Filter by email sender (case-insensitive substring)")),
	mcp.WithString("author",
		mcp.Description("Filter by book author (case-insensitive substring)")),
)

var ragListCollectionsTool = mcp.NewTool("rag_list_collections",
	mcp.WithDescription(
		"List all available collections with source file counts, chunk counts, "+
			"and metadata. Collections of type 'code' represent code groups that "+
			"may contain multiple git repos."),
)

var ragIndexTool = mcp.NewTool("rag_index",
	mcp.WithDescription(
		"Trigger indexing for a collection. For system collections ('obsidian', "+
			"'email', 'calibre', 'rss'), uses configured paths. For code groups, "+
			"indexes all repos in that group. For project collections, a path "+
			"argument is required."),
	mcp.WithString("collection",
		mcp.Required(),
		mcp.Description("Collection name ('obsidian', 'email', 'calibre', 'rss', code group name, or project name)")),
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
