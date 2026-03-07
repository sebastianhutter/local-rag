// Package config handles loading and saving ~/.local-rag/config.json.
package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

var (
	DefaultConfigDir  = filepath.Join(homeDir(), ".local-rag")
	DefaultConfigPath = filepath.Join(DefaultConfigDir, "config.json")
	DefaultDBPath     = filepath.Join(DefaultConfigDir, "rag.db")
)

// SearchDefaults holds the default search parameters.
type SearchDefaults struct {
	TopK         int     `json:"top_k"`
	RRFK         int     `json:"rrf_k"`
	VectorWeight float64 `json:"vector_weight"`
	FTSWeight    float64 `json:"fts_weight"`
}

// OCRConfig holds settings for optional tesseract-based OCR fallback on scanned PDFs.
type OCRConfig struct {
	Enabled       bool     `json:"enabled"`           // default: false
	Languages     []string `json:"languages"`         // default: ["eng"], joined as "eng+deu" for tesseract
	MaxPages      int      `json:"max_pages"`         // default: 50, skip OCR if PDF exceeds this
	MaxFileSizeMB int      `json:"max_file_size_mb"`  // default: 100, skip OCR if file exceeds this
	MinWordCount  int      `json:"min_word_count"`    // default: 10, OCR pages with fewer words than this
}

// GUIConfig holds GUI-specific settings.
type GUIConfig struct {
	AutoStartMCP             bool `json:"auto_start_mcp"`
	MCPPort                  int  `json:"mcp_port"`
	AutoReindex              bool `json:"auto_reindex"`
	AutoReindexIntervalMinutes int  `json:"auto_reindex_interval_minutes"`
	StartOnLogin             bool `json:"start_on_login"`
}

// Config holds all application configuration.
type Config struct {
	DBPath                    string              `json:"db_path"`
	EmbeddingModel            string              `json:"embedding_model"`
	EmbeddingDimensions       int                 `json:"embedding_dimensions"`
	ChunkSizeTokens           int                 `json:"chunk_size_tokens"`
	ChunkOverlapTokens        int                 `json:"chunk_overlap_tokens"`
	ObsidianVaults            []string            `json:"obsidian_vaults"`
	ObsidianExcludeFolders    []string            `json:"obsidian_exclude_folders"`
	EmclientDBPath            string              `json:"emclient_db_path"`
	CalibreLibraries          []string            `json:"calibre_libraries"`
	NetnewswireDBPath         string              `json:"netnewswire_db_path"`
	Repositories              map[string][]string  `json:"repositories"`
	Projects                  map[string][]string  `json:"projects"`
	DisabledCollections       []string            `json:"disabled_collections"`
	GitHistoryInMonths        int                 `json:"git_history_in_months"`
	GitCommitSubjectBlacklist []string            `json:"git_commit_subject_blacklist"`
	SearchDefaults            SearchDefaults      `json:"search_defaults"`
	OCR                       OCRConfig           `json:"ocr"`
	GUI                       GUIConfig           `json:"gui"`

	// disabledSet is a cached lookup set built from DisabledCollections.
	disabledSet map[string]struct{}
}

// IsCollectionEnabled returns true if the named collection is not disabled.
func (c *Config) IsCollectionEnabled(name string) bool {
	if c.disabledSet == nil {
		c.disabledSet = make(map[string]struct{}, len(c.DisabledCollections))
		for _, n := range c.DisabledCollections {
			c.disabledSet[n] = struct{}{}
		}
	}
	_, disabled := c.disabledSet[name]
	return !disabled
}

// ExpandedDBPath returns the db_path with ~ expanded.
func (c *Config) ExpandedDBPath() string {
	return expandPath(c.DBPath)
}

// Load reads configuration from the given path (or the default) and returns
// a Config with defaults applied for any missing fields.
func Load(path string) (*Config, error) {
	if path == "" {
		path = DefaultConfigPath
	}

	// Ensure config directory exists.
	if err := os.MkdirAll(DefaultConfigDir, 0o755); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}

	cfg := defaults()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Info("no config file found, using defaults", "path", path)
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	// Parse into a raw map so we can merge selectively.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		slog.Error("failed to parse config, using defaults", "path", path, "err", err)
		return cfg, nil
	}

	// Re-unmarshal into the struct (fills present fields, leaves defaults for absent).
	if err := json.Unmarshal(data, cfg); err != nil {
		slog.Error("failed to decode config, using defaults", "path", path, "err", err)
		return cfg, nil
	}

	// Expand all paths.
	cfg.DBPath = expandPath(cfg.DBPath)
	cfg.EmclientDBPath = expandPath(cfg.EmclientDBPath)
	cfg.NetnewswireDBPath = expandPath(cfg.NetnewswireDBPath)
	for i, v := range cfg.ObsidianVaults {
		cfg.ObsidianVaults[i] = expandPath(v)
	}
	for i, v := range cfg.CalibreLibraries {
		cfg.CalibreLibraries[i] = expandPath(v)
	}
	for name, paths := range cfg.Repositories {
		expanded := make([]string, len(paths))
		for i, p := range paths {
			expanded[i] = expandPath(p)
		}
		cfg.Repositories[name] = expanded
	}
	for name, paths := range cfg.Projects {
		expanded := make([]string, len(paths))
		for i, p := range paths {
			expanded[i] = expandPath(p)
		}
		cfg.Projects[name] = expanded
	}

	// Handle backward compat for auto_reindex.
	if _, ok := raw["gui"]; ok {
		var guiRaw map[string]json.RawMessage
		if err := json.Unmarshal(raw["gui"], &guiRaw); err == nil {
			// Migrate old hours field to minutes.
			if hoursRaw, hasHours := guiRaw["auto_reindex_interval_hours"]; hasHours {
				var hours int
				if json.Unmarshal(hoursRaw, &hours) == nil && hours > 0 {
					cfg.GUI.AutoReindexIntervalMinutes = hours * 60
				}
			}
			if _, hasAutoReindex := guiRaw["auto_reindex"]; !hasAutoReindex {
				// auto_reindex absent: derive from interval
				if cfg.GUI.AutoReindexIntervalMinutes != 60 && cfg.GUI.AutoReindexIntervalMinutes > 0 {
					cfg.GUI.AutoReindex = true
				} else {
					cfg.GUI.AutoReindex = false
				}
			}
		}
	}

	slog.Info("loaded config", "path", path)
	return cfg, nil
}

// Save writes the current configuration to the given path (or the default),
// preserving any unknown keys from the existing file.
func Save(cfg *Config, path string) error {
	if path == "" {
		path = DefaultConfigPath
	}

	// Read existing data to preserve unknown keys.
	existing := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &existing)
	}

	// Overlay current config values.
	existing["db_path"] = cfg.DBPath
	existing["embedding_model"] = cfg.EmbeddingModel
	existing["embedding_dimensions"] = cfg.EmbeddingDimensions
	existing["chunk_size_tokens"] = cfg.ChunkSizeTokens
	existing["chunk_overlap_tokens"] = cfg.ChunkOverlapTokens
	existing["obsidian_vaults"] = cfg.ObsidianVaults
	existing["obsidian_exclude_folders"] = cfg.ObsidianExcludeFolders
	existing["emclient_db_path"] = cfg.EmclientDBPath
	existing["calibre_libraries"] = cfg.CalibreLibraries
	existing["netnewswire_db_path"] = cfg.NetnewswireDBPath
	existing["repositories"] = cfg.Repositories
	existing["projects"] = cfg.Projects
	existing["disabled_collections"] = cfg.DisabledCollections
	existing["git_history_in_months"] = cfg.GitHistoryInMonths
	existing["git_commit_subject_blacklist"] = cfg.GitCommitSubjectBlacklist
	existing["search_defaults"] = cfg.SearchDefaults
	existing["ocr"] = cfg.OCR
	existing["gui"] = cfg.GUI

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	out, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	out = append(out, '\n')

	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	slog.Info("saved config", "path", path)
	return nil
}

// defaults returns a Config with all default values applied.
func defaults() *Config {
	home := homeDir()
	return &Config{
		DBPath:              DefaultDBPath,
		EmbeddingModel:      "bge-m3",
		EmbeddingDimensions: 1024,
		ChunkSizeTokens:     500,
		ChunkOverlapTokens:  50,
		ObsidianVaults:      []string{},
		ObsidianExcludeFolders: []string{},
		EmclientDBPath: filepath.Join(home, "Library", "Application Support", "eM Client"),
		CalibreLibraries: []string{},
		NetnewswireDBPath: filepath.Join(home, "Library", "Containers",
			"com.ranchero.NetNewsWire-Evergreen", "Data", "Library",
			"Application Support", "NetNewsWire", "Accounts"),
		Repositories:              make(map[string][]string),
		Projects:                  make(map[string][]string),
		DisabledCollections:       []string{},
		GitHistoryInMonths:        6,
		GitCommitSubjectBlacklist: []string{},
		SearchDefaults: SearchDefaults{
			TopK:         10,
			RRFK:         60,
			VectorWeight: 0.7,
			FTSWeight:    0.3,
		},
		OCR: OCRConfig{
			Enabled:       false,
			Languages:     []string{"eng"},
			MaxPages:      50,
			MaxFileSizeMB: 100,
			MinWordCount:  10,
		},
		GUI: GUIConfig{
			AutoStartMCP:             true,
			MCPPort:                  31123,
			AutoReindex:              false,
			AutoReindexIntervalMinutes: 60,
			StartOnLogin:             false,
		},
	}
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp"
	}
	return home
}

func expandPath(p string) string {
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(homeDir(), p[2:])
	}
	if p == "~" {
		return homeDir()
	}
	return p
}

// UnexpandPath converts an absolute path back to ~/… form if under the home directory.
func UnexpandPath(p string) string {
	home := homeDir()
	rel, err := filepath.Rel(home, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return p
	}
	return "~/" + rel
}
