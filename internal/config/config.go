// Package config handles loading and saving ~/.local-rag/config.json.
package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
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

	// ExcludeCollections lists collections to skip when a search does not name
	// a collection of its own. Naming one explicitly still searches it, so this
	// demotes a collection out of the default sweep rather than hiding it.
	//
	// It exists because a collection can be large enough to crowd the results
	// without being wrong: an archive of chat transcripts or generated logs can
	// hold most of the documents in the database and take most of the result
	// slots, while the material worth retrieving sits elsewhere. Entries are
	// collection names or types ('system', 'project', 'code'), matching what
	// the collection filter itself accepts.
	ExcludeCollections []string `json:"exclude_collections"`
}

// GraphConfig holds settings for the relation graph.
type GraphConfig struct {
	// MentionExcludeSenders drops ticket-mention edges from mail sent by these
	// senders, matched as a case-insensitive substring of the sender field
	// (so "jira@" covers `Someone (Jira) <jira@example.atlassian.net>`).
	//
	// Tracker notification mail names a ticket without referring to it: the
	// mail exists *because* of the ticket, so an edge between them is a
	// tautology that adds nothing the ticket's own record does not hold. Left
	// in, such mail also wins the traversal's rarest-first ranking outright,
	// because each notification is mentioned by nothing and therefore looks
	// maximally specific. Measured on a real database, 30% of all mention
	// edges came from two notification senders.
	//
	// Empty by default: which senders are machines is a fact about a corpus,
	// not about the software.
	MentionExcludeSenders []string `json:"mention_exclude_senders"`

	// TerraformRegistryPaths maps a module-registry prefix to the paths its
	// modules occupy in indexed repositories, so a registry-style
	// `source = "<host>/<namespace>/<name>/<system>"` resolves to the module it
	// refers to. The module name is appended to each mapped path in turn and
	// matched against the end of an indexed directory path:
	//
	//   "registry.example.com/modules": ["terraform/v2", "terraform/modules"]
	//   source = "registry.example.com/modules/kms/aws"
	//     -> a directory ending in terraform/v2/kms, else terraform/modules/kms
	//
	// Configuration rather than convention, because there is no convention. The
	// module *name* is the only part reliably shared between a registry address
	// and a checkout, and resolving by name alone was measured at 37% unique
	// and 58% ambiguous -- names like "s3" or "backup" match a directory in
	// nearly every repository.
	//
	// A list rather than one path, because one registry's modules routinely
	// live in several repositories, and a module can exist in two of them at
	// once: a v1 and a v2 layout side by side during a migration. Order is
	// therefore meaningful -- the first path that matches wins, so putting the
	// newer layout first expresses which one callers mean.
	//
	// A registry with no entry, and any public registry, is left unresolved
	// rather than guessed at.
	TerraformRegistryPaths map[string][]string `json:"terraform_registry_paths"`
}

// OCRConfig holds settings for optional tesseract-based OCR fallback on scanned PDFs.
type OCRConfig struct {
	Enabled       bool     `json:"enabled"`          // default: false
	Languages     []string `json:"languages"`        // default: ["eng"], joined as "eng+deu" for tesseract
	MaxPages      int      `json:"max_pages"`        // default: 50, skip OCR if PDF exceeds this
	MaxFileSizeMB int      `json:"max_file_size_mb"` // default: 100, skip OCR if file exceeds this
	MinWordCount  int      `json:"min_word_count"`   // default: 10, OCR pages with fewer words than this
}

// GUIConfig holds GUI-specific settings.
type GUIConfig struct {
	AutoStartMCP               bool `json:"auto_start_mcp"`
	MCPPort                    int  `json:"mcp_port"`
	AutoReindex                bool `json:"auto_reindex"`
	AutoReindexIntervalMinutes int  `json:"auto_reindex_interval_minutes"`
	StartOnLogin               bool `json:"start_on_login"`
}

// Config holds all application configuration.
type Config struct {
	DBPath                    string              `json:"db_path"`
	EmbeddingModel            string              `json:"embedding_model"`
	EmbeddingDimensions       int                 `json:"embedding_dimensions"`
	EmbeddingHosts            []string            `json:"embedding_hosts"`
	EmbeddingBatchSize        int                 `json:"embedding_batch_size"`
	EmbeddingWorkers          int                 `json:"embedding_workers"`
	EmbeddingNumBatch         int                 `json:"embedding_num_batch"`
	ChunkSizeTokens           int                 `json:"chunk_size_tokens"`
	ChunkOverlapTokens        int                 `json:"chunk_overlap_tokens"`
	ObsidianVaults            []string            `json:"obsidian_vaults"`
	ObsidianExcludeFolders    []string            `json:"obsidian_exclude_folders"`
	EmclientDBPath            string              `json:"emclient_db_path"`
	CalibreLibraries          []string            `json:"calibre_libraries"`
	NetnewswireDBPath         string              `json:"netnewswire_db_path"`
	Repositories              map[string][]string `json:"repositories"`
	Projects                  map[string][]string `json:"projects"`
	DisabledCollections       []string            `json:"disabled_collections"`
	SkipCloudPlaceholders     bool                `json:"skip_cloud_placeholders"`
	GitHistoryInMonths        int                 `json:"git_history_in_months"`
	GitCommitSubjectBlacklist []string            `json:"git_commit_subject_blacklist"`
	SearchDefaults            SearchDefaults      `json:"search_defaults"`
	Graph                     GraphConfig         `json:"graph"`
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

// systemCollections are the reserved names owned by the built-in indexers.
var systemCollections = []string{"obsidian", "email", "calibre", "rss"}

// NameConflict is a collection name claimed by more than one source.
type NameConflict struct {
	Name  string
	Kinds []string // e.g. ["repositories", "projects"]
}

func (c NameConflict) String() string {
	return fmt.Sprintf("%q is configured under %s", c.Name, strings.Join(c.Kinds, " and "))
}

// CollectionNameConflicts reports names claimed by more than one source.
//
// Collection names are unique in the database, so a name used by both a
// repository and a project resolves to a single collection and merges two
// unrelated corpora — indexing both looks like it works while the collections
// list shows one entry. Renaming one of them is the fix.
func (c *Config) CollectionNameConflicts() []NameConflict {
	kinds := map[string][]string{}
	add := func(name, kind string) {
		for _, k := range kinds[name] {
			if k == kind {
				return
			}
		}
		kinds[name] = append(kinds[name], kind)
	}

	for name := range c.Repositories {
		add(name, "repositories")
	}
	for name := range c.Projects {
		add(name, "projects")
	}
	for _, name := range systemCollections {
		if _, isRepo := c.Repositories[name]; isRepo {
			add(name, "system collections")
		}
		if _, isProject := c.Projects[name]; isProject {
			add(name, "system collections")
		}
	}

	var conflicts []NameConflict
	for name, ks := range kinds {
		if len(ks) > 1 {
			sort.Strings(ks)
			conflicts = append(conflicts, NameConflict{Name: name, Kinds: ks})
		}
	}
	sort.Slice(conflicts, func(i, j int) bool { return conflicts[i].Name < conflicts[j].Name })
	return conflicts
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

	for _, conflict := range cfg.CollectionNameConflicts() {
		slog.Warn("collection name conflict: indexing will fail until one is renamed",
			"conflict", conflict.String())
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
	existing["embedding_hosts"] = cfg.EmbeddingHosts
	existing["embedding_batch_size"] = cfg.EmbeddingBatchSize
	existing["embedding_workers"] = cfg.EmbeddingWorkers
	existing["embedding_num_batch"] = cfg.EmbeddingNumBatch
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
	existing["skip_cloud_placeholders"] = cfg.SkipCloudPlaceholders
	existing["git_history_in_months"] = cfg.GitHistoryInMonths
	existing["git_commit_subject_blacklist"] = cfg.GitCommitSubjectBlacklist
	existing["search_defaults"] = cfg.SearchDefaults
	existing["graph"] = cfg.Graph
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
		DBPath:                 DefaultDBPath,
		EmbeddingModel:         "bge-m3",
		EmbeddingDimensions:    1024,
		EmbeddingBatchSize:     32,
		EmbeddingWorkers:       4,
		EmbeddingNumBatch:      0,
		ChunkSizeTokens:        500,
		ChunkOverlapTokens:     50,
		ObsidianVaults:         []string{},
		ObsidianExcludeFolders: []string{},
		EmclientDBPath:         filepath.Join(home, "Library", "Application Support", "eM Client"),
		CalibreLibraries:       []string{},
		NetnewswireDBPath: filepath.Join(home, "Library", "Containers",
			"com.ranchero.NetNewsWire-Evergreen", "Data", "Library",
			"Application Support", "NetNewsWire", "Accounts"),
		Repositories:              make(map[string][]string),
		Projects:                  make(map[string][]string),
		DisabledCollections:       []string{},
		SkipCloudPlaceholders:     true,
		GitHistoryInMonths:        6,
		GitCommitSubjectBlacklist: []string{},
		SearchDefaults: SearchDefaults{
			TopK:               10,
			RRFK:               60,
			VectorWeight:       0.7,
			FTSWeight:          0.3,
			ExcludeCollections: []string{},
		},
		Graph: GraphConfig{
			MentionExcludeSenders:  []string{},
			TerraformRegistryPaths: map[string][]string{},
		},
		OCR: OCRConfig{
			Enabled:       false,
			Languages:     []string{"eng"},
			MaxPages:      50,
			MaxFileSizeMB: 100,
			MinWordCount:  10,
		},
		GUI: GUIConfig{
			AutoStartMCP:               true,
			MCPPort:                    31123,
			AutoReindex:                false,
			AutoReindexIntervalMinutes: 60,
			StartOnLogin:               false,
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
