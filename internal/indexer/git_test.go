package indexer

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
)

func TestShouldExclude(t *testing.T) {
	tests := []struct {
		path    string
		exclude bool
	}{
		{"main.go", false},
		{"src/app.py", false},
		{".DS_Store", true},
		{"go.sum", true},
		{"package-lock.json", true},
		{"node_modules/foo/bar.js", true},
		{"__pycache__/mod.pyc", true},
		{".terraform/providers/aws.tf", true},
		{"vendor/lib/file.go", true},
		{"src/main.rs", false},
		{".terraform.lock.hcl", true},
		{"uv.lock", true},
	}
	for _, tt := range tests {
		if got := shouldExclude(tt.path); got != tt.exclude {
			t.Errorf("shouldExclude(%q) = %v, want %v", tt.path, got, tt.exclude)
		}
	}
}

func TestParseWatermarks(t *testing.T) {
	// Empty string
	wm := parseWatermarks("")
	if len(wm) != 0 {
		t.Errorf("expected empty map, got %v", wm)
	}

	// JSON format
	wm = parseWatermarks(`{"/path/repo":"abc123","/path/repo:history":"def456"}`)
	if wm["/path/repo"] != "abc123" {
		t.Errorf("expected 'abc123', got %q", wm["/path/repo"])
	}
	if wm["/path/repo:history"] != "def456" {
		t.Errorf("expected 'def456', got %q", wm["/path/repo:history"])
	}

	// Legacy format
	wm = parseWatermarks("git:/path/repo:abc123")
	if wm["/path/repo"] != "abc123" {
		t.Errorf("expected 'abc123', got %q", wm["/path/repo"])
	}

	// Invalid
	wm = parseWatermarks("random text")
	if len(wm) != 0 {
		t.Errorf("expected empty map for invalid input, got %v", wm)
	}
}

func TestMakeWatermarks(t *testing.T) {
	wm := map[string]string{
		"/path/repo": "abc123",
	}
	result := makeWatermarks(wm)
	if result == "" {
		t.Fatal("expected non-empty result")
	}

	// Parse it back
	parsed := parseWatermarks(result)
	if parsed["/path/repo"] != "abc123" {
		t.Errorf("round-trip failed: got %q", parsed["/path/repo"])
	}
}

func TestShouldIndexFile(t *testing.T) {
	tests := []struct {
		path  string
		index bool
	}{
		{"main.go", true},
		{"app.py", true},
		{"script.js", true},
		{"infra.tf", true},
		{"README.md", true},   // .md is in CodeExtensionMap (markdown via tree-sitter)
		{"go.sum", false},     // excluded
		{".DS_Store", false},  // excluded
		{"Makefile", true},
	}
	for _, tt := range tests {
		if got := shouldIndexFile(tt.path); got != tt.index {
			t.Errorf("shouldIndexFile(%q) = %v, want %v", tt.path, got, tt.index)
		}
	}
}

func TestIsGitRepo(t *testing.T) {
	// Current repo should be a git repo (we're inside local-rag-go)
	if !isGitRepo(".") {
		t.Error("expected current directory to be a git repo")
	}

	tmpDir := t.TempDir()
	if isGitRepo(tmpDir) {
		t.Error("temp dir should not be a git repo")
	}
}

// gitInit runs git init in the given directory.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", dir)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init %s: %v\n%s", dir, err, out)
	}
}

func TestDiscoverGitRepos_SingleRepo(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)

	repos, err := DiscoverGitRepos(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 || repos[0] != dir {
		t.Errorf("expected [%s], got %v", dir, repos)
	}
}

func TestDiscoverGitRepos_ParentDir(t *testing.T) {
	parent := t.TempDir()
	repoA := filepath.Join(parent, "repo-a")
	repoB := filepath.Join(parent, "repo-b")
	os.MkdirAll(repoA, 0o755)
	os.MkdirAll(repoB, 0o755)
	gitInit(t, repoA)
	gitInit(t, repoB)

	repos, err := DiscoverGitRepos(parent)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(repos)
	if len(repos) != 2 {
		t.Fatalf("expected 2 repos, got %d: %v", len(repos), repos)
	}
	if repos[0] != repoA || repos[1] != repoB {
		t.Errorf("expected [%s, %s], got %v", repoA, repoB, repos)
	}
}

func TestDiscoverGitRepos_NestedStopsAtGit(t *testing.T) {
	parent := t.TempDir()
	outer := filepath.Join(parent, "outer")
	inner := filepath.Join(outer, "sub", "inner")
	os.MkdirAll(inner, 0o755)
	gitInit(t, outer)
	gitInit(t, inner) // simulates a submodule

	repos, err := DiscoverGitRepos(parent)
	if err != nil {
		t.Fatal(err)
	}
	// Should only find outer, not inner
	if len(repos) != 1 || repos[0] != outer {
		t.Errorf("expected [%s], got %v", outer, repos)
	}
}

func TestDiscoverGitRepos_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	repos, err := DiscoverGitRepos(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 0 {
		t.Errorf("expected empty, got %v", repos)
	}
}

func TestDiscoverGitRepos_NotADir(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file.txt")
	os.WriteFile(f, []byte("hello"), 0o644)

	_, err := DiscoverGitRepos(f)
	if err == nil {
		t.Error("expected error for non-directory path")
	}
}

func TestResolveRepoPaths_Mixed(t *testing.T) {
	parent := t.TempDir()
	repoA := filepath.Join(parent, "repo-a")
	repoB := filepath.Join(parent, "repo-b")
	repoC := filepath.Join(t.TempDir(), "standalone")
	os.MkdirAll(repoA, 0o755)
	os.MkdirAll(repoB, 0o755)
	os.MkdirAll(repoC, 0o755)
	gitInit(t, repoA)
	gitInit(t, repoB)
	gitInit(t, repoC)

	// Mix: one parent dir + one direct repo
	resolved := ResolveRepoPaths([]string{parent, repoC})
	if len(resolved) != 3 {
		t.Fatalf("expected 3 repos, got %d: %v", len(resolved), resolved)
	}
}

func TestResolveRepoPaths_Dedup(t *testing.T) {
	parent := t.TempDir()
	repo := filepath.Join(parent, "repo")
	os.MkdirAll(repo, 0o755)
	gitInit(t, repo)

	// Both the parent (discovers repo) and repo itself should deduplicate
	resolved := ResolveRepoPaths([]string{parent, repo})
	if len(resolved) != 1 {
		t.Errorf("expected 1 deduplicated repo, got %d: %v", len(resolved), resolved)
	}
}
