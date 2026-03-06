// Package search implements hybrid vector + FTS5 search with Reciprocal Rank Fusion.
package search

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/sebastianhutter/local-rag-go/internal/config"
	"github.com/sebastianhutter/local-rag-go/internal/embeddings"
)

// SearchResult represents a single search result.
type SearchResult struct {
	Content    string
	Title      string
	Metadata   map[string]any
	Score      float64
	Collection string
	SourcePath string
	SourceType string
}

// Filters holds optional filters for search queries.
type Filters struct {
	Collection      string
	SourceType      string
	DateFrom        string
	DateTo          string
	Sender          string
	Author          string
	MetadataFilters map[string]string
}

func (f *Filters) hasFilters() bool {
	if f == nil {
		return false
	}
	return f.Collection != "" || f.SourceType != "" || f.Sender != "" ||
		f.Author != "" || f.DateFrom != "" || f.DateTo != "" ||
		len(f.MetadataFilters) > 0
}

type rankedResult struct {
	docID int64
	score float64
}

// collectionTypes is the set of valid collection type names.
var collectionTypes = map[string]bool{
	"system":  true,
	"project": true,
	"code":    true,
}

// vectorSearch runs vector similarity search via sqlite-vec.
func vectorSearch(db *sql.DB, queryEmbedding []float32, topK int, filters *Filters) ([]rankedResult, error) {
	queryBlob := embeddings.SerializeFloat32(queryEmbedding)

	candidateLimit := topK * 3
	if filters.hasFilters() {
		candidateLimit = topK * 50
	}

	rows, err := db.Query(
		`SELECT document_id, distance
		 FROM vec_documents
		 WHERE embedding MATCH ?
		 ORDER BY distance
		 LIMIT ?`,
		queryBlob, candidateLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}
	defer rows.Close()

	var results []rankedResult
	for rows.Next() {
		var docID int64
		var distance float64
		if err := rows.Scan(&docID, &distance); err != nil {
			return nil, fmt.Errorf("scan vector result: %w", err)
		}

		if !filters.hasFilters() || passesFilters(db, docID, filters) {
			results = append(results, rankedResult{docID: docID, score: distance})
			if len(results) >= topK {
				break
			}
		}
	}
	return results, rows.Err()
}

// escapeFTSQuery wraps each token in double quotes for safe FTS5 queries.
func escapeFTSQuery(query string) string {
	tokens := strings.Fields(query)
	if len(tokens) == 0 {
		return ""
	}
	quoted := make([]string, len(tokens))
	for i, t := range tokens {
		quoted[i] = `"` + t + `"`
	}
	return strings.Join(quoted, " ")
}

// ftsSearch runs full-text search via FTS5.
func ftsSearch(db *sql.DB, queryText string, topK int, filters *Filters) ([]rankedResult, error) {
	safeQuery := escapeFTSQuery(queryText)
	if safeQuery == "" {
		return nil, nil
	}

	candidateLimit := topK * 3
	if filters.hasFilters() {
		candidateLimit = topK * 50
	}

	rows, err := db.Query(
		`SELECT rowid, rank
		 FROM documents_fts
		 WHERE documents_fts MATCH ?
		 ORDER BY rank
		 LIMIT ?`,
		safeQuery, candidateLimit,
	)
	if err != nil {
		slog.Warn("FTS query failed", "query", safeQuery, "err", err)
		return nil, nil // Non-fatal: return empty results.
	}
	defer rows.Close()

	var results []rankedResult
	for rows.Next() {
		var docID int64
		var rank float64
		if err := rows.Scan(&docID, &rank); err != nil {
			return nil, fmt.Errorf("scan fts result: %w", err)
		}

		if !filters.hasFilters() || passesFilters(db, docID, filters) {
			results = append(results, rankedResult{docID: docID, score: rank})
			if len(results) >= topK {
				break
			}
		}
	}
	return results, rows.Err()
}

// passesFilters checks if a document passes the given filters.
func passesFilters(db *sql.DB, documentID int64, filters *Filters) bool {
	if filters == nil {
		return true
	}

	var metadataStr sql.NullString
	var collectionName, collectionType, sourceType, sourcePath string

	err := db.QueryRow(
		`SELECT d.metadata, c.name, c.collection_type, s.source_type, s.source_path
		 FROM documents d
		 JOIN collections c ON d.collection_id = c.id
		 JOIN sources s ON d.source_id = s.id
		 WHERE d.id = ?`,
		documentID,
	).Scan(&metadataStr, &collectionName, &collectionType, &sourceType, &sourcePath)
	if err != nil {
		return false
	}

	if filters.Collection != "" {
		if collectionTypes[filters.Collection] {
			if collectionType != filters.Collection {
				return false
			}
		} else if collectionName != filters.Collection {
			return false
		}
	}

	if filters.SourceType != "" && sourceType != filters.SourceType {
		return false
	}

	needsMetadata := filters.Sender != "" || filters.Author != "" ||
		filters.DateFrom != "" || filters.DateTo != "" || len(filters.MetadataFilters) > 0

	if needsMetadata {
		var metadata map[string]any
		if metadataStr.Valid {
			_ = json.Unmarshal([]byte(metadataStr.String), &metadata)
		}
		if metadata == nil {
			metadata = make(map[string]any)
		}

		if filters.Sender != "" {
			sender, _ := metadata["sender"].(string)
			if !strings.Contains(strings.ToLower(sender), strings.ToLower(filters.Sender)) {
				return false
			}
		}

		if filters.Author != "" {
			authorLower := strings.ToLower(filters.Author)
			authors, _ := metadata["authors"].([]any)
			found := false
			for _, a := range authors {
				if s, ok := a.(string); ok && strings.Contains(strings.ToLower(s), authorLower) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}

		docDate, _ := metadata["date"].(string)
		if filters.DateFrom != "" && docDate != "" && docDate < filters.DateFrom {
			return false
		}
		if filters.DateTo != "" && docDate != "" && docDate > filters.DateTo {
			return false
		}

		for key, filterVal := range filters.MetadataFilters {
			raw, exists := metadata[key]
			if !exists {
				return false
			}
			filterLower := strings.ToLower(filterVal)
			switch v := raw.(type) {
			case string:
				if !strings.Contains(strings.ToLower(v), filterLower) {
					return false
				}
			case []any:
				found := false
				for _, elem := range v {
					if s, ok := elem.(string); ok && strings.Contains(strings.ToLower(s), filterLower) {
						found = true
						break
					}
				}
				if !found {
					return false
				}
			default:
				if fmt.Sprintf("%v", v) != filterVal {
					return false
				}
			}
		}
	}

	return true
}

// RRFMerge merges two ranked lists using Reciprocal Rank Fusion.
func RRFMerge(vecResults, ftsResults []rankedResult, k int, vectorWeight, ftsWeight float64) []rankedResult {
	scores := make(map[int64]float64)

	for rank, r := range vecResults {
		scores[r.docID] += vectorWeight / float64(k+rank+1)
	}
	for rank, r := range ftsResults {
		scores[r.docID] += ftsWeight / float64(k+rank+1)
	}

	merged := make([]rankedResult, 0, len(scores))
	for docID, score := range scores {
		merged = append(merged, rankedResult{docID: docID, score: score})
	}

	sort.Slice(merged, func(i, j int) bool {
		return merged[i].score > merged[j].score
	})

	return merged
}

// fetchResult loads a SearchResult from the database for a given document ID.
func fetchResult(db *sql.DB, docID int64, score float64) (*SearchResult, error) {
	var content, collectionName, sourcePath, sourceType string
	var title sql.NullString
	var metadataStr sql.NullString

	err := db.QueryRow(
		`SELECT d.content, d.title, d.metadata,
		        c.name, s.source_path, s.source_type
		 FROM documents d
		 JOIN collections c ON d.collection_id = c.id
		 JOIN sources s ON d.source_id = s.id
		 WHERE d.id = ?`,
		docID,
	).Scan(&content, &title, &metadataStr, &collectionName, &sourcePath, &sourceType)
	if err != nil {
		return nil, err
	}

	metadata := make(map[string]any)
	if metadataStr.Valid {
		_ = json.Unmarshal([]byte(metadataStr.String), &metadata)
	}

	titleStr := ""
	if title.Valid {
		titleStr = title.String
	}

	return &SearchResult{
		Content:    content,
		Title:      titleStr,
		Metadata:   metadata,
		Score:      score,
		Collection: collectionName,
		SourcePath: sourcePath,
		SourceType: sourceType,
	}, nil
}

// Search runs hybrid search combining vector similarity and full-text search.
func Search(db *sql.DB, queryEmbedding []float32, queryText string, topK int, filters *Filters, cfg *config.Config) ([]SearchResult, error) {
	vecResults, err := vectorSearch(db, queryEmbedding, topK, filters)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}

	ftsResults, err := ftsSearch(db, queryText, topK, filters)
	if err != nil {
		return nil, fmt.Errorf("fts search: %w", err)
	}

	merged := RRFMerge(
		vecResults,
		ftsResults,
		cfg.SearchDefaults.RRFK,
		cfg.SearchDefaults.VectorWeight,
		cfg.SearchDefaults.FTSWeight,
	)

	var results []SearchResult
	limit := topK
	if limit > len(merged) {
		limit = len(merged)
	}

	for _, r := range merged[:limit] {
		result, err := fetchResult(db, r.docID, r.score)
		if err != nil {
			slog.Warn("failed to fetch result", "doc_id", r.docID, "err", err)
			continue
		}
		results = append(results, *result)
	}

	return results, nil
}

// PerformSearch is a high-level convenience wrapper that handles the full
// config → connection → embedding → search → cleanup flow.
func PerformSearch(ctx context.Context, query string, filters *Filters, topK int) ([]SearchResult, error) {
	cfg, err := config.Load("")
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	dbConn, err := openDB(cfg)
	if err != nil {
		return nil, err
	}
	defer dbConn.Close()

	queryEmbedding, err := embeddings.GetEmbedding(ctx, query, cfg.EmbeddingModel)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	return Search(dbConn, queryEmbedding, query, topK, filters, cfg)
}

func openDB(cfg *config.Config) (*sql.DB, error) {
	dbPath := cfg.ExpandedDBPath()

	// Import db package for Open — but to avoid circular imports we inline the open logic.
	// This is acceptable since PerformSearch is a convenience wrapper.
	dbPkg, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_foreign_keys=ON")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	return dbPkg, nil
}
