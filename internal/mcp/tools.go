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
	"github.com/sebastianhutter/local-rag-go/internal/graph"
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

	metadataFilters := make(map[string]string)
	if mf, ok := request.GetArguments()["metadata_filter"].(map[string]any); ok {
		for k, v := range mf {
			metadataFilters[k] = fmt.Sprintf("%v", v)
		}
	}

	filters := &search.Filters{
		Collection:      request.GetString("collection", ""),
		SourceType:      request.GetString("source_type", ""),
		Path:            request.GetString("path", ""),
		DateFrom:        request.GetString("date_from", ""),
		DateTo:          request.GetString("date_to", ""),
		Sender:          request.GetString("sender", ""),
		Author:          request.GetString("author", ""),
		MetadataFilters: metadataFilters,
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
			// source_id is what rag_neighbors takes, so a caller can go from a
			// result to what it is connected to without addressing it by path.
			"source_id":   r.SourceID,
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
			if text, repos := describeCollection(description.String); text != "" {
				entry["description"] = text
			} else if repos > 0 {
				entry["repositories"] = repos
			}
		}
		if lastIndexed.Valid {
			entry["last_indexed"] = lastIndexed.String
		}
		collections = append(collections, entry)
	}

	data, _ := json.MarshalIndent(collections, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

// describeCollection turns a collection's stored description into something fit
// for a client.
//
// The description column is overloaded: system collections keep a human string
// there, but code collections store git watermarks — a JSON map of repository
// path to indexed commit SHA. For a collection with many repositories that runs
// to tens of thousands of characters, and rag_list_collections was returning it
// verbatim, so listing 15 collections cost ~27k tokens of an MCP client's
// context. Watermarks are internal bookkeeping and are summarised instead.
func describeCollection(description string) (text string, repos int) {
	if description == "" {
		return "", 0
	}
	if strings.HasPrefix(description, "{") {
		var watermarks map[string]string
		if err := json.Unmarshal([]byte(description), &watermarks); err == nil {
			seen := map[string]struct{}{}
			for key := range watermarks {
				seen[strings.TrimSuffix(key, ":history")] = struct{}{}
			}
			return "", len(seen)
		}
	}
	return description, 0
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
		// Check if it's a code repository collection
		if repos, ok := cfg.Repositories[collection]; ok {
			result = &indexer.IndexResult{}
			for _, repoPath := range repos {
				r := indexer.IndexGitRepo(conn, cfg, repoPath, collection, false, false, nil)
				result.Merge(r)
			}
		} else if paths, ok := cfg.Projects[collection]; ok {
			result = indexer.IndexProject(conn, cfg, collection, paths, false, nil)
		} else {
			return mcp.NewToolResultError(fmt.Sprintf(
				"unknown collection %q — configure it in config.json under repositories or projects", collection)), nil
		}
	}

	output := map[string]any{
		"collection":  collection,
		"indexed":     result.Indexed,
		"skipped":     result.Skipped,
		"errors":      result.Errors,
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
		if text, repos := describeCollection(description.String); text != "" {
			output["description"] = text
		} else if repos > 0 {
			output["repositories"] = repos
		}
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

	// Markdown docs with a URL in metadata (e.g. Jira/Confluence) → use that URL directly.
	if sourceType == "markdown" {
		if u, ok := metadata["url"].(string); ok && u != "" {
			return u
		}
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

// handleRagNeighbors expands one source into what it is connected to.
func handleRagNeighbors(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	sourceID := int64(request.GetFloat("source_id", 0))
	if sourceID <= 0 {
		return mcp.NewToolResultError("source_id is required (take it from a rag_search result)"), nil
	}

	_, conn, err := openDB()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer conn.Close()

	var path string
	if err := conn.QueryRow("SELECT source_path FROM sources WHERE id = ?", sourceID).Scan(&path); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("no indexed source with source_id %d", sourceID)), nil
	}

	neighbours, err := graph.Expand(conn, []int64{sourceID}, graph.ExpandOptions{
		Rels:    splitList(request.GetString("rel", "")),
		Origins: splitList(request.GetString("origin", "")),
		Hops:    int(request.GetFloat("hops", 0)),
		HubCap:  int(request.GetFloat("hub_cap", 0)),
		Limit:   int(request.GetFloat("limit", 0)),
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("expand failed: %v", err)), nil
	}

	if len(neighbours) == 0 {
		// Distinguishing the two cases matters: one means "nothing to find
		// here", the other means "raise the cap or the graph is not built".
		var edges int
		_ = conn.QueryRow("SELECT COUNT(*) FROM graph_edges").Scan(&edges)
		if edges == 0 {
			return mcp.NewToolResultText(
				"No relation graph has been built yet. Run 'local-rag graph rebuild'."), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf(
			"No neighbours for source %d (%s). Either it has no edges, or it is a hub and "+
				"expansion stopped there -- raise hub_cap to expand through it.", sourceID, path)), nil
	}

	out := make([]map[string]any, 0, len(neighbours))
	for _, n := range neighbours {
		out = append(out, map[string]any{
			"source_id":   n.SourceID,
			"title":       n.Title,
			"collection":  n.Collection,
			"source_path": n.SourcePath,
			"snippet":     n.Snippet,
			"relation":    n.Rel,
			"origin":      n.Origin,
			"hops":        n.Hops,
			"degree":      n.Degree,
			"reached_via": n.ViaID,
		})
	}

	data, _ := json.MarshalIndent(map[string]any{
		"source_id":   sourceID,
		"source_path": path,
		"neighbours":  out,
	}, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

// splitList parses a comma-separated tool argument, tolerating spaces.
func splitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
