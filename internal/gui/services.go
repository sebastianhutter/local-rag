package gui

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"github.com/sebastianhutter/local-rag-go/internal/config"
	"github.com/sebastianhutter/local-rag-go/internal/db"
	"github.com/sebastianhutter/local-rag-go/internal/indexer"
	localMCP "github.com/sebastianhutter/local-rag-go/internal/mcp"
)

// ---------------------------------------------------------------------------
// MCPService — in-process SSE server
// ---------------------------------------------------------------------------

// MCPService manages an in-process MCP SSE server.
type MCPService struct {
	mu        sync.Mutex
	running   bool
	port      int
	sseServer *server.SSEServer
}

// Start creates the MCP server and starts SSE on the given port.
func (s *MCPService) Start(port int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("MCP server already running on port %d", s.port)
	}

	// Check port availability.
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("port %d unavailable: %w", port, err)
	}
	ln.Close()

	mcpServer := localMCP.CreateServer()
	sseServer := server.NewSSEServer(mcpServer)

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	slog.Info("starting MCP server (SSE)", "addr", addr)

	s.sseServer = sseServer
	s.port = port
	s.running = true

	go func() {
		if err := sseServer.Start(addr); err != nil {
			slog.Error("MCP SSE server stopped", "err", err)
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
		}
	}()

	return nil
}

// Stop shuts down the SSE server with a 5-second timeout.
func (s *MCPService) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return
	}

	slog.Info("stopping MCP server")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if s.sseServer != nil {
		if err := s.sseServer.Shutdown(ctx); err != nil {
			slog.Error("MCP shutdown error", "err", err)
		}
		s.sseServer = nil
	}

	s.running = false
	slog.Info("MCP server stopped")
}

// IsRunning returns whether the MCP server is running.
func (s *MCPService) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// Port returns the port the MCP server is running on.
func (s *MCPService) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.port
}

// ---------------------------------------------------------------------------
// IndexingService
// ---------------------------------------------------------------------------

// IndexingService manages background indexing operations.
type IndexingService struct {
	mu             sync.Mutex
	running        bool
	currentLabel   string
	lastCompletion time.Time
}

// IsRunning returns whether an indexing operation is in progress.
func (s *IndexingService) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// CurrentLabel returns the label of the currently running indexer.
func (s *IndexingService) CurrentLabel() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentLabel
}

// LastCompletion returns when the last indexing run completed.
func (s *IndexingService) LastCompletion() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastCompletion
}

func (s *IndexingService) setRunning(label string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return false
	}
	s.running = true
	s.currentLabel = label
	return true
}

func (s *IndexingService) setDone() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
	s.currentLabel = ""
	s.lastCompletion = time.Now()
}

// IndexAll runs all enabled indexers sequentially. Caller should run in a goroutine.
func (s *IndexingService) IndexAll(cfg *config.Config, onComplete func(error)) {
	if !s.setRunning("all") {
		if onComplete != nil {
			onComplete(fmt.Errorf("indexing already in progress"))
		}
		return
	}
	defer func() {
		s.setDone()
		if onComplete != nil {
			onComplete(nil)
		}
	}()

	conn, err := openDB(cfg)
	if err != nil {
		slog.Error("indexAll: open DB failed", "err", err)
		return
	}
	defer conn.Close()

	// Auto-prune obsidian and code collections before indexing
	s.setLabel("pruning")
	indexer.PruneCollection(conn, cfg, "obsidian")
	for groupName := range cfg.Repositories {
		if cfg.IsCollectionEnabled(groupName) {
			indexer.PruneCollection(conn, cfg, groupName)
		}
	}

	collections := []struct {
		label string
		run   func()
	}{
		{"obsidian", func() {
			if cfg.IsCollectionEnabled("obsidian") && len(cfg.ObsidianVaults) > 0 {
				s.setLabel("obsidian")
				indexer.IndexObsidian(conn, cfg, false, nil)
			}
		}},
		{"email", func() {
			if cfg.IsCollectionEnabled("email") {
				s.setLabel("email")
				indexer.IndexEmails(conn, cfg, false, nil)
			}
		}},
		{"calibre", func() {
			if cfg.IsCollectionEnabled("calibre") && len(cfg.CalibreLibraries) > 0 {
				s.setLabel("calibre")
				indexer.IndexCalibre(conn, cfg, false, nil)
			}
		}},
		{"rss", func() {
			if cfg.IsCollectionEnabled("rss") {
				s.setLabel("rss")
				indexer.IndexRSS(conn, cfg, false, nil)
			}
		}},
	}

	for _, c := range collections {
		c.run()
	}

	// Code repositories.
	for groupName, configPaths := range cfg.Repositories {
		if !cfg.IsCollectionEnabled(groupName) {
			continue
		}
		repos := indexer.ResolveRepoPaths(configPaths)
		for _, repoPath := range repos {
			s.setLabel(groupName)
			indexer.IndexGitRepo(conn, cfg, repoPath, groupName, false, true, nil)
		}
	}

	// Project collections from config.
	for projectName, paths := range cfg.Projects {
		if !cfg.IsCollectionEnabled(projectName) {
			continue
		}
		s.setLabel(projectName)
		indexer.IndexProject(conn, cfg, projectName, paths, false, nil)
	}
}

// IndexCollection runs a single collection's indexer. Caller should run in a goroutine.
func (s *IndexingService) IndexCollection(name string, cfg *config.Config, onComplete func(error)) {
	if !s.setRunning(name) {
		if onComplete != nil {
			onComplete(fmt.Errorf("indexing already in progress"))
		}
		return
	}
	defer func() {
		s.setDone()
		if onComplete != nil {
			onComplete(nil)
		}
	}()

	conn, err := openDB(cfg)
	if err != nil {
		slog.Error("indexCollection: open DB failed", "err", err)
		return
	}
	defer conn.Close()

	// Auto-prune for obsidian, code, and project collections
	if name == "obsidian" {
		indexer.PruneCollection(conn, cfg, name)
	} else if _, isCode := cfg.Repositories[name]; isCode {
		indexer.PruneCollection(conn, cfg, name)
	} else if _, isProject := cfg.Projects[name]; isProject {
		indexer.PruneCollection(conn, cfg, name)
	}

	switch name {
	case "obsidian":
		indexer.IndexObsidian(conn, cfg, false, nil)
	case "email":
		indexer.IndexEmails(conn, cfg, false, nil)
	case "calibre":
		indexer.IndexCalibre(conn, cfg, false, nil)
	case "rss":
		indexer.IndexRSS(conn, cfg, false, nil)
	default:
		// Check if it's a repository collection.
		if configPaths, ok := cfg.Repositories[name]; ok {
			repos := indexer.ResolveRepoPaths(configPaths)
			for _, repoPath := range repos {
				indexer.IndexGitRepo(conn, cfg, repoPath, name, false, true, nil)
			}
			return
		}
		// Check if it's a project.
		if paths, ok := cfg.Projects[name]; ok {
			indexer.IndexProject(conn, cfg, name, paths, false, nil)
			return
		}
	}
}

func (s *IndexingService) setLabel(label string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentLabel = label
}

// ---------------------------------------------------------------------------
// StatusService
// ---------------------------------------------------------------------------

// Overview holds summary statistics.
type Overview struct {
	CollectionCount int
	ChunkCount      int
	DBSizeMB        float64
	LastIndexed     string
	OllamaOnline    bool
}

// CollectionInfo holds per-collection stats.
type CollectionInfo struct {
	Name        string
	Type        string
	ChunkCount  int
	LastIndexed string
	Enabled     bool
}

// StatusService queries DB stats.
type StatusService struct{}

// GetOverview returns summary statistics.
func (s *StatusService) GetOverview(cfg *config.Config) Overview {
	ov := Overview{}
	dbPath := cfg.ExpandedDBPath()

	info, err := os.Stat(dbPath)
	if err != nil {
		return ov
	}
	ov.DBSizeMB = float64(info.Size()) / (1024 * 1024)

	conn, err := db.Open(dbPath)
	if err != nil {
		return ov
	}
	defer conn.Close()

	conn.QueryRow("SELECT COUNT(*) FROM collections").Scan(&ov.CollectionCount)
	conn.QueryRow("SELECT COUNT(*) FROM documents").Scan(&ov.ChunkCount)

	var lastIndexed sql.NullString
	conn.QueryRow("SELECT MAX(last_indexed_at) FROM sources").Scan(&lastIndexed)
	if lastIndexed.Valid {
		ov.LastIndexed = lastIndexed.String
	}

	return ov
}

// GetCollections returns per-collection stats.
func (s *StatusService) GetCollections(cfg *config.Config) []CollectionInfo {
	conn, err := db.Open(cfg.ExpandedDBPath())
	if err != nil {
		return nil
	}
	defer conn.Close()

	rows, err := conn.Query(`
		SELECT c.name, c.collection_type,
		       (SELECT COUNT(*) FROM documents d WHERE d.collection_id = c.id),
		       COALESCE((SELECT MAX(last_indexed_at) FROM sources s WHERE s.collection_id = c.id), '')
		FROM collections c ORDER BY c.name
	`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var collections []CollectionInfo
	for rows.Next() {
		var ci CollectionInfo
		rows.Scan(&ci.Name, &ci.Type, &ci.ChunkCount, &ci.LastIndexed)
		ci.Enabled = cfg.IsCollectionEnabled(ci.Name)
		collections = append(collections, ci)
	}
	return collections
}

// CheckOllama returns true if the Ollama API is reachable.
func (s *StatusService) CheckOllama() bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://127.0.0.1:11434/api/tags")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func openDB(cfg *config.Config) (*sql.DB, error) {
	conn, err := db.Open(cfg.ExpandedDBPath())
	if err != nil {
		return nil, err
	}
	if err := db.InitSchema(conn, cfg.EmbeddingDimensions); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}
