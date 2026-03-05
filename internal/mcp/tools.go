package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/sebastianhutter/local-rag-go/internal/config"
	"github.com/sebastianhutter/local-rag-go/internal/db"
	"github.com/sebastianhutter/local-rag-go/internal/embeddings"
	"github.com/sebastianhutter/local-rag-go/internal/indexer"
	"github.com/sebastianhutter/local-rag-go/internal/search"
)

func openDB() (*config.Config, *sql.DB, error) {
	cfg, err := config.Load("")
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}
	conn, err := db.Open(cfg.ExpandedDBPath())
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}
	if err := db.InitSchema(conn, cfg.EmbeddingDimensions); err != nil {
		conn.Close()
		return nil, nil, err
	}
	return cfg, conn, nil
}

// --- rag_search ---

func handleRagSearch(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := request.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError("query parameter is required"), nil
	}

	cfg, conn, err := openDB()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer conn.Close()

	topK := request.GetInt("top_k", 10)

	filters := &search.Filters{
		Collection: request.GetString("collection", ""),
		SourceType: request.GetString("source_type", ""),
		DateFrom:   request.GetString("date_from", ""),
		DateTo:     request.GetString("date_to", ""),
		Sender:     request.GetString("sender", ""),
		Author:     request.GetString("author", ""),
	}

	queryEmbedding, err := embeddings.GetEmbedding(ctx, query, cfg.EmbeddingModel)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to embed query (is Ollama running?): %v", err)), nil
	}

	results, err := search.Search(conn, queryEmbedding, query, topK, filters, cfg)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
	}

	var output []map[string]any
	for _, r := range results {
		entry := map[string]any{
			"title":       r.Title,
			"content":     r.Content,
			"collection":  r.Collection,
			"source_type": r.SourceType,
			"source_path": r.SourcePath,
			"source_uri":  buildSourceURI(r.SourcePath, r.SourceType, r.Collection, r.Metadata, cfg),
			"score":       fmt.Sprintf("%.4f", r.Score),
			"metadata":    r.Metadata,
		}
		output = append(output, entry)
	}

	data, _ := json.MarshalIndent(output, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

// --- rag_list_collections ---

func handleRagListCollections(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	_, conn, err := openDB()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer conn.Close()

	rows, err := conn.Query(`
		SELECT c.name, c.collection_type, c.description, c.created_at,
		       (SELECT COUNT(*) FROM sources s WHERE s.collection_id = c.id),
		       (SELECT COUNT(*) FROM documents d WHERE d.collection_id = c.id),
		       (SELECT MAX(s.last_indexed_at) FROM sources s WHERE s.collection_id = c.id)
		FROM collections c ORDER BY c.name
	`)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("query collections: %v", err)), nil
	}
	defer rows.Close()

	var collections []map[string]any
	for rows.Next() {
		var name, collType, createdAt string
		var description, lastIndexed sql.NullString
		var sourceCount, chunkCount int
		rows.Scan(&name, &collType, &description, &createdAt, &sourceCount, &chunkCount, &lastIndexed)

		entry := map[string]any{
			"name":         name,
			"type":         collType,
			"source_count": sourceCount,
			"chunk_count":  chunkCount,
			"created_at":   createdAt,
		}
		if description.Valid {
			entry["description"] = description.String
		}
		if lastIndexed.Valid {
			entry["last_indexed"] = lastIndexed.String
		}
		collections = append(collections, entry)
	}

	data, _ := json.MarshalIndent(collections, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

// --- rag_index ---

func handleRagIndex(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	collection, err := request.RequireString("collection")
	if err != nil {
		return mcp.NewToolResultError("collection parameter is required"), nil
	}

	cfg, conn, err := openDB()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer conn.Close()

	if !cfg.IsCollectionEnabled(collection) {
		return mcp.NewToolResultError(fmt.Sprintf("collection %q is disabled in config", collection)), nil
	}

	var result *indexer.IndexResult

	switch collection {
	case "obsidian":
		result = indexer.IndexObsidian(conn, cfg, false, nil)
	case "email":
		result = indexer.IndexEmails(conn, cfg, false, nil)
	case "calibre":
		result = indexer.IndexCalibre(conn, cfg, false, nil)
	case "rss":
		result = indexer.IndexRSS(conn, cfg, false, nil)
	default:
		// Check if it's a code group
		if repos, ok := cfg.CodeGroups[collection]; ok {
			result = &indexer.IndexResult{}
			for _, repoPath := range repos {
				r := indexer.IndexGitRepo(conn, cfg, repoPath, collection, false, false, nil)
				result.Merge(r)
			}
		} else {
			// Assume it's a project collection
			path := request.GetString("path", "")
			if path == "" {
				// Try to load paths from DB
				var pathsJSON sql.NullString
				conn.QueryRow("SELECT paths FROM collections WHERE name = ?", collection).Scan(&pathsJSON)
				if pathsJSON.Valid && pathsJSON.String != "" {
					var paths []string
					if json.Unmarshal([]byte(pathsJSON.String), &paths) == nil && len(paths) > 0 {
						result = indexer.IndexProject(conn, cfg, collection, paths, false, nil)
					}
				}
				if result == nil {
					return mcp.NewToolResultError(fmt.Sprintf(
						"unknown collection %q — provide a 'path' argument for project collections", collection)), nil
				}
			} else {
				result = indexer.IndexProject(conn, cfg, collection, []string{path}, false, nil)
			}
		}
	}

	output := map[string]any{
		"collection": collection,
		"indexed":    result.Indexed,
		"skipped":    result.Skipped,
		"errors":     result.Errors,
		"total_found": result.TotalFound,
	}
	data, _ := json.MarshalIndent(output, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

// --- rag_prune ---

func handleRagPrune(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cfg, conn, err := openDB()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer conn.Close()

	collection := request.GetString("collection", "")

	var result *indexer.PruneResult
	if collection != "" {
		result = indexer.PruneCollection(conn, cfg, collection)
	} else {
		result = indexer.PruneAll(conn, cfg)
	}

	output := map[string]any{
		"pruned":  result.Pruned,
		"checked": result.Checked,
		"errors":  result.Errors,
	}
	if collection != "" {
		output["collection"] = collection
	} else {
		output["collection"] = "all"
	}
	if len(result.ErrorMessages) > 0 {
		output["error_messages"] = result.ErrorMessages
	}

	data, _ := json.MarshalIndent(output, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

// --- rag_collection_info ---

func handleRagCollectionInfo(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	collection, err := request.RequireString("collection")
	if err != nil {
		return mcp.NewToolResultError("collection parameter is required"), nil
	}

	_, conn, err := openDB()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer conn.Close()

	var id int64
	var collType, createdAt string
	var description sql.NullString
	err = conn.QueryRow("SELECT id, collection_type, created_at, description FROM collections WHERE name = ?", collection).
		Scan(&id, &collType, &createdAt, &description)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("collection %q not found", collection)), nil
	}

	var sourceCount, chunkCount int
	conn.QueryRow("SELECT COUNT(*) FROM sources WHERE collection_id = ?", id).Scan(&sourceCount)
	conn.QueryRow("SELECT COUNT(*) FROM documents WHERE collection_id = ?", id).Scan(&chunkCount)

	var lastIndexed sql.NullString
	conn.QueryRow("SELECT MAX(last_indexed_at) FROM sources WHERE collection_id = ?", id).Scan(&lastIndexed)

	// Source type breakdown
	sourceTypes := map[string]int{}
	typeRows, err := conn.Query("SELECT source_type, COUNT(*) FROM sources WHERE collection_id = ? GROUP BY source_type", id)
	if err == nil {
		defer typeRows.Close()
		for typeRows.Next() {
			var st string
			var cnt int
			typeRows.Scan(&st, &cnt)
			sourceTypes[st] = cnt
		}
	}

	// Sample titles
	var sampleTitles []string
	titleRows, err := conn.Query("SELECT DISTINCT title FROM documents WHERE collection_id = ? AND title IS NOT NULL LIMIT 10", id)
	if err == nil {
		defer titleRows.Close()
		for titleRows.Next() {
			var t string
			titleRows.Scan(&t)
			sampleTitles = append(sampleTitles, t)
		}
	}

	output := map[string]any{
		"name":          collection,
		"type":          collType,
		"created_at":    createdAt,
		"source_count":  sourceCount,
		"chunk_count":   chunkCount,
		"source_types":  sourceTypes,
		"sample_titles": sampleTitles,
	}
	if description.Valid {
		output["description"] = description.String
	}
	if lastIndexed.Valid {
		output["last_indexed"] = lastIndexed.String
	}

	data, _ := json.MarshalIndent(output, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

// --- Source URI helpers ---

func buildSourceURI(sourcePath, sourceType, collection string, metadata map[string]any, cfg *config.Config) any {
	if sourceType == "rss" {
		if u, ok := metadata["url"].(string); ok && u != "" {
			return u
		}
		return nil
	}

	if sourceType == "email" || sourceType == "commit" {
		return nil
	}

	if strings.HasPrefix(sourcePath, "calibre://") || strings.HasPrefix(sourcePath, "git://") {
		return nil
	}

	// Check if it's in an Obsidian vault
	for _, vault := range cfg.ObsidianVaults {
		if strings.HasPrefix(sourcePath, vault) {
			uri := buildObsidianURI(sourcePath, vault)
			if uri != "" {
				return uri
			}
		}
	}

	// Code files → vscode URI
	if sourceType == "code" {
		startLine := 1
		if sl, ok := metadata["start_line"].(float64); ok {
			startLine = int(sl)
		}
		return fmt.Sprintf("vscode://file%s:%d", sourcePath, startLine)
	}

	// Default: file URI
	return "file://" + sourcePath
}

func buildObsidianURI(sourcePath, vaultPath string) string {
	vaultName := filepath.Base(vaultPath)
	relPath, err := filepath.Rel(vaultPath, sourcePath)
	if err != nil {
		slog.Warn("failed to compute relative path", "source", sourcePath, "vault", vaultPath)
		return ""
	}
	return fmt.Sprintf("obsidian://open?vault=%s&file=%s",
		url.QueryEscape(vaultName),
		url.QueryEscape(relPath))
}
