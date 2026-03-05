package indexer

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sebastianhutter/local-rag-go/internal/chunker"
	"github.com/sebastianhutter/local-rag-go/internal/config"
	"github.com/sebastianhutter/local-rag-go/internal/embeddings"
	"github.com/sebastianhutter/local-rag-go/internal/parser"
)

// Exclude patterns for files that shouldn't be indexed even if tracked.
var excludePatterns = map[string]bool{
	".DS_Store":           true,
	".terraform.lock.hcl": true,
	"go.sum":              true,
	"package-lock.json":   true,
	"yarn.lock":           true,
	"pnpm-lock.yaml":     true,
	"Cargo.lock":          true,
	"poetry.lock":         true,
	"uv.lock":             true,
}

var excludeDirPatterns = map[string]bool{
	".idea":          true,
	".vscode":        true,
	"node_modules":   true,
	"__pycache__":    true,
	".mypy_cache":    true,
	".pytest_cache":  true,
	".tox":           true,
	"dist":           true,
	"build":          true,
	".egg-info":      true,
	"vendor":         true,
	".terraform":     true,
	"cdk.out":        true,
}

const watermarkPrefix = "git:"

// CommitInfo holds parsed git commit metadata.
type CommitInfo struct {
	SHA         string
	AuthorName  string
	AuthorEmail string
	AuthorDate  string
	Subject     string
}

// FileChange represents a single file change from a git commit.
type FileChange struct {
	FilePath  string
	Additions int
	Deletions int
	IsBinary  bool
}

// IndexGitRepo indexes a git repository using tree-sitter for code parsing.
func IndexGitRepo(conn *sql.DB, cfg *config.Config, repoPath, collectionName string, force, indexHistory bool, progress ProgressCallback) *IndexResult {
	repoPath, _ = filepath.Abs(repoPath)

	if !isGitRepo(repoPath) {
		slog.Error("not a git repository", "path", repoPath)
		return &IndexResult{Errors: 1, ErrorMessages: []string{"not a git repository"}}
	}

	headSHA := getHeadSHA(repoPath)
	slog.Info("git repo", "path", repoPath, "HEAD", headSHA[:12])

	collectionID := getOrCreate(conn, collectionName, "code")

	// Read existing watermarks
	var desc sql.NullString
	conn.QueryRow("SELECT description FROM collections WHERE id = ?", collectionID).Scan(&desc)
	watermarks := parseWatermarks(desc.String)
	repoKey := repoPath
	oldSHA := watermarks[repoKey]

	var filesToIndex []string
	var filesToDelete []string

	if !force && oldSHA != "" {
		if oldSHA == headSHA {
			slog.Info("no new commits since last index", "sha", headSHA[:12])
			if indexHistory {
				return indexGitHistory(conn, cfg, repoPath, collectionID, force, cfg.GitHistoryInMonths)
			}
			return &IndexResult{}
		}

		if commitExists(repoPath, oldSHA) {
			changed := gitDiffNames(repoPath, oldSHA, "HEAD")
			tracked := gitLsFiles(repoPath)
			trackedSet := make(map[string]bool, len(tracked))
			for _, f := range tracked {
				trackedSet[f] = true
			}

			for _, f := range changed {
				if trackedSet[f] {
					filesToIndex = append(filesToIndex, f)
				} else {
					filesToDelete = append(filesToDelete, f)
				}
			}

			slog.Info("incremental index",
				"changed", len(filesToIndex),
				"deleted", len(filesToDelete),
				"since", oldSHA[:12],
			)
		} else {
			slog.Warn("previous watermark commit not found, doing full index", "sha", oldSHA[:12])
			filesToIndex = gitLsFiles(repoPath)
		}
	} else {
		filesToIndex = gitLsFiles(repoPath)
	}

	// Clean up deleted files
	for _, relPath := range filesToDelete {
		sourcePath := filepath.Join(repoPath, relPath)
		deleteSource(conn, collectionID, sourcePath)
	}

	// Filter to supported code files
	var indexable []string
	for _, f := range filesToIndex {
		if shouldIndexFile(f) {
			indexable = append(indexable, f)
		}
	}

	result := &IndexResult{TotalFound: len(indexable)}
	slog.Info("indexing code files", "count", len(indexable), "collection", collectionName)

	for i, relPath := range indexable {
		if progress != nil {
			progress(i+1, len(indexable), relPath)
		}
		indexed, err := indexCodeFile(conn, cfg, repoPath, relPath, collectionID, force)
		if err != nil {
			slog.Error("error indexing", "path", relPath, "err", err)
			result.Errors++
			continue
		}
		if indexed {
			result.Indexed++
		} else {
			result.Skipped++
		}
	}

	// Update watermark
	watermarks[repoKey] = headSHA
	conn.Exec("UPDATE collections SET description = ? WHERE id = ?",
		makeWatermarks(watermarks), collectionID)

	slog.Info("git indexer done", "result", result.String())

	if indexHistory {
		histResult := indexGitHistory(conn, cfg, repoPath, collectionID, force, cfg.GitHistoryInMonths)
		result.Merge(histResult)
	}

	return result
}

func indexCodeFile(conn *sql.DB, cfg *config.Config, repoPath, relPath string, collectionID int64, force bool) (bool, error) {
	absPath := filepath.Join(repoPath, relPath)
	if !fileExists(absPath) {
		return false, nil
	}

	fh, err := fileHash(absPath)
	if err != nil {
		return false, err
	}

	if !force && isSourceUnchanged(conn, collectionID, absPath, fh) {
		return false, nil
	}

	language := parser.GetCodeLanguage(relPath)
	if language == "" {
		return false, nil
	}

	doc := parser.ParseCodeFile(absPath, language, relPath)
	if doc == nil || len(doc.Blocks) == 0 {
		return false, nil
	}

	chunks := codeBlocksToChunks(doc, relPath, cfg)
	if len(chunks) == 0 {
		return false, nil
	}

	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Text
	}

	vecs, err := embed(texts, cfg)
	if err != nil {
		return false, fmt.Errorf("embeddings: %w", err)
	}

	info, _ := os.Stat(absPath)
	mtime := ""
	if info != nil {
		mtime = info.ModTime().UTC().Format(time.RFC3339)
	}

	sourceID, err := upsertSource(conn, collectionID, absPath, "code", fh, mtime)
	if err != nil {
		return false, err
	}

	for i, c := range chunks {
		metaJSON, _ := json.Marshal(c.Metadata)
		res, err := conn.Exec(
			"INSERT INTO documents (source_id, collection_id, chunk_index, title, content, metadata) VALUES (?, ?, ?, ?, ?, ?)",
			sourceID, collectionID, c.ChunkIndex, c.Title, c.Text, string(metaJSON),
		)
		if err != nil {
			return false, fmt.Errorf("insert document: %w", err)
		}
		docID, _ := res.LastInsertId()
		vecBytes := embeddings.SerializeFloat32(vecs[i])
		conn.Exec("INSERT INTO vec_documents (embedding, document_id) VALUES (?, ?)", vecBytes, docID)
	}

	slog.Info("indexed code file", "path", relPath, "chunks", len(chunks))
	return true, nil
}

func codeBlocksToChunks(doc *parser.CodeDocument, relPath string, cfg *config.Config) []chunker.Chunk {
	chunkSize := cfg.ChunkSizeTokens
	overlap := cfg.ChunkOverlapTokens
	var chunks []chunker.Chunk
	chunkIdx := 0

	for _, block := range doc.Blocks {
		prefix := fmt.Sprintf("[%s:%d-%d] [%s] [%s: %s]\n",
			relPath, block.StartLine, block.EndLine,
			block.Language, block.SymbolType, block.SymbolName)

		metadata := map[string]any{
			"language":    block.Language,
			"symbol_name": block.SymbolName,
			"symbol_type": block.SymbolType,
			"start_line":  block.StartLine,
			"end_line":    block.EndLine,
			"file_path":   block.FilePath,
		}

		prefixedText := prefix + block.Text
		prefixWC := chunker.WordCount(prefix)

		if chunker.WordCount(prefixedText) <= chunkSize {
			chunks = append(chunks, chunker.Chunk{
				Text:       prefixedText,
				Title:      relPath,
				Metadata:   metadata,
				ChunkIndex: chunkIdx,
			})
			chunkIdx++
		} else {
			available := chunkSize - prefixWC
			if available < 50 {
				available = 50
			}
			windows := chunker.SplitIntoWindows(block.Text, available, overlap)
			for _, w := range windows {
				meta := copyMeta(metadata)
				chunks = append(chunks, chunker.Chunk{
					Text:       prefix + w,
					Title:      relPath,
					Metadata:   meta,
					ChunkIndex: chunkIdx,
				})
				chunkIdx++
			}
		}
	}

	return chunks
}

func indexGitHistory(conn *sql.DB, cfg *config.Config, repoPath string, collectionID int64, force bool, months int) *IndexResult {
	repoKey := repoPath
	historyKey := repoKey + ":history"

	var desc sql.NullString
	conn.QueryRow("SELECT description FROM collections WHERE id = ?", collectionID).Scan(&desc)
	watermarks := parseWatermarks(desc.String)

	var sinceSHA string
	if !force {
		sinceSHA = watermarks[historyKey]
	}

	commits := getCommitsSince(repoPath, sinceSHA, months)

	// Filter out blacklisted commits
	if len(cfg.GitCommitSubjectBlacklist) > 0 && len(commits) > 0 {
		var filtered []CommitInfo
		for _, c := range commits {
			blacklisted := false
			for _, prefix := range cfg.GitCommitSubjectBlacklist {
				if strings.HasPrefix(c.Subject, prefix) {
					blacklisted = true
					break
				}
			}
			if !blacklisted {
				filtered = append(filtered, c)
			}
		}
		if diff := len(commits) - len(filtered); diff > 0 {
			slog.Info("filtered commits matching subject blacklist", "count", diff)
		}
		commits = filtered
	}

	if len(commits) == 0 {
		slog.Info("no new commits to index", "repo", repoPath)
		return &IndexResult{}
	}

	slog.Info("indexing commit history", "commits", len(commits), "repo", repoPath, "months", months)

	result := &IndexResult{TotalFound: len(commits)}
	newestSHA := commits[len(commits)-1].SHA

	for i, commit := range commits {
		sourcePath := fmt.Sprintf("git://%s#%s", repoKey, commit.SHA)

		if !force && isSourceExists(conn, collectionID, sourcePath) {
			result.Skipped++
			continue
		}

		fileChanges := getCommitFileChanges(repoPath, commit.SHA)
		if len(fileChanges) == 0 {
			result.Skipped++
			continue
		}

		chunks := commitToChunks(commit, fileChanges, repoPath, cfg)
		if len(chunks) == 0 {
			result.Skipped++
			continue
		}

		texts := make([]string, len(chunks))
		for j, c := range chunks {
			texts[j] = c.Text
		}

		vecs, err := embed(texts, cfg)
		if err != nil {
			slog.Error("error embedding commit", "sha", commit.SHA[:12], "err", err)
			result.Errors++
			continue
		}

		now := time.Now().UTC().Format(time.RFC3339)
		res, err := conn.Exec(
			"INSERT INTO sources (collection_id, source_type, source_path, file_hash, file_modified_at, last_indexed_at) VALUES (?, ?, ?, ?, ?, ?)",
			collectionID, "commit", sourcePath, commit.SHA, commit.AuthorDate, now,
		)
		if err != nil {
			slog.Error("error inserting commit source", "sha", commit.SHA[:12], "err", err)
			result.Errors++
			continue
		}
		sourceID, _ := res.LastInsertId()

		for j, c := range chunks {
			metaJSON, _ := json.Marshal(c.Metadata)
			docRes, err := conn.Exec(
				"INSERT INTO documents (source_id, collection_id, chunk_index, title, content, metadata) VALUES (?, ?, ?, ?, ?, ?)",
				sourceID, collectionID, c.ChunkIndex, c.Title, c.Text, string(metaJSON),
			)
			if err != nil {
				continue
			}
			docID, _ := docRes.LastInsertId()
			vecBytes := embeddings.SerializeFloat32(vecs[j])
			conn.Exec("INSERT INTO vec_documents (embedding, document_id) VALUES (?, ?)", vecBytes, docID)
		}

		result.Indexed++
		slog.Info("indexed commit",
			"num", fmt.Sprintf("%d/%d", i+1, len(commits)),
			"sha", commit.SHA[:7],
			"subject", truncate(commit.Subject, 60),
			"chunks", len(chunks),
		)
	}

	// Update history watermark
	watermarks[historyKey] = newestSHA
	conn.Exec("UPDATE collections SET description = ? WHERE id = ?",
		makeWatermarks(watermarks), collectionID)

	slog.Info("history indexer done", "result", result.String())
	return result
}

func commitToChunks(commit CommitInfo, fileChanges []FileChange, repoPath string, cfg *config.Config) []chunker.Chunk {
	chunkSize := cfg.ChunkSizeTokens
	overlap := cfg.ChunkOverlapTokens
	var chunks []chunker.Chunk
	chunkIdx := 0
	repoName := filepath.Base(repoPath)
	shortSHA := commit.SHA[:7]
	dateStr := commit.AuthorDate[:10]

	for _, fc := range fileChanges {
		if fc.IsBinary {
			continue
		}

		diffText := getFileDiff(repoPath, commit.SHA, fc.FilePath)
		if diffText == "" {
			continue
		}

		prefix := fmt.Sprintf("[%s/%s] [commit: %s] [%s]\n", repoName, fc.FilePath, shortSHA, dateStr)
		body := commit.Subject + "\n\n" + diffText

		metadata := map[string]any{
			"commit_sha":       commit.SHA,
			"commit_sha_short": shortSHA,
			"author_name":      commit.AuthorName,
			"author_email":     commit.AuthorEmail,
			"author_date":      commit.AuthorDate,
			"commit_message":   commit.Subject,
			"file_path":        fc.FilePath,
			"additions":        fc.Additions,
			"deletions":        fc.Deletions,
		}

		prefixedText := prefix + body
		prefixWC := chunker.WordCount(prefix)

		if chunker.WordCount(prefixedText) <= chunkSize {
			chunks = append(chunks, chunker.Chunk{
				Text:       prefixedText,
				Title:      repoName + "/" + fc.FilePath,
				Metadata:   metadata,
				ChunkIndex: chunkIdx,
			})
			chunkIdx++
		} else {
			available := chunkSize - prefixWC
			if available < 50 {
				available = 50
			}
			windows := chunker.SplitIntoWindows(body, available, overlap)
			for _, w := range windows {
				meta := copyMeta(metadata)
				chunks = append(chunks, chunker.Chunk{
					Text:       prefix + w,
					Title:      repoName + "/" + fc.FilePath,
					Metadata:   meta,
					ChunkIndex: chunkIdx,
				})
				chunkIdx++
			}
		}
	}

	return chunks
}

// Git helper functions

func runGit(repoPath string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", repoPath}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func isGitRepo(repoPath string) bool {
	_, err := runGit(repoPath, "rev-parse", "--git-dir")
	return err == nil
}

func getHeadSHA(repoPath string) string {
	out, err := runGit(repoPath, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func gitLsFiles(repoPath string) []string {
	out, err := runGit(repoPath, "ls-files")
	if err != nil {
		return nil
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

func gitDiffNames(repoPath, fromSHA, toSHA string) []string {
	out, err := runGit(repoPath, "diff", "--name-only", fromSHA+".."+toSHA)
	if err != nil {
		return nil
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

func commitExists(repoPath, sha string) bool {
	_, err := runGit(repoPath, "cat-file", "-t", sha)
	return err == nil
}

func getCommitsSince(repoPath, sinceSHA string, months int) []CommitInfo {
	args := []string{
		"log", "--no-merges",
		fmt.Sprintf("--since=%d months ago", months),
		"--pretty=format:%H|%an|%ae|%aI|%s",
	}
	if sinceSHA != "" {
		args = append(args, sinceSHA+"..HEAD")
	}

	out, err := runGit(repoPath, args...)
	if err != nil {
		slog.Warn("failed to get commit log", "err", err)
		return nil
	}

	var commits []CommitInfo
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 5)
		if len(parts) != 5 {
			continue
		}
		commits = append(commits, CommitInfo{
			SHA:         parts[0],
			AuthorName:  parts[1],
			AuthorEmail: parts[2],
			AuthorDate:  parts[3],
			Subject:     parts[4],
		})
	}

	// Reverse so oldest is first
	for i, j := 0, len(commits)-1; i < j; i, j = i+1, j-1 {
		commits[i], commits[j] = commits[j], commits[i]
	}
	return commits
}

func getCommitFileChanges(repoPath, commitSHA string) []FileChange {
	out, err := runGit(repoPath, "show", "--numstat", "--format=", commitSHA)
	if err != nil {
		return nil
	}

	var changes []FileChange
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		isBinary := parts[0] == "-" && parts[1] == "-"
		adds := 0
		dels := 0
		if !isBinary {
			adds, _ = strconv.Atoi(parts[0])
			dels, _ = strconv.Atoi(parts[1])
		}
		changes = append(changes, FileChange{
			FilePath:  parts[2],
			Additions: adds,
			Deletions: dels,
			IsBinary:  isBinary,
		})
	}
	return changes
}

func getFileDiff(repoPath, commitSHA, filePath string) string {
	out, err := runGit(repoPath, "show", commitSHA, "--", filePath)
	if err != nil {
		return ""
	}
	return out
}

func shouldIndexFile(relPath string) bool {
	if shouldExclude(relPath) {
		return false
	}
	return parser.IsCodeFile(relPath)
}

func shouldExclude(relPath string) bool {
	base := filepath.Base(relPath)
	if excludePatterns[base] {
		return true
	}
	parts := strings.Split(relPath, "/")
	for _, part := range parts {
		if excludeDirPatterns[part] {
			return true
		}
	}
	return false
}

func parseWatermarks(description string) map[string]string {
	if description == "" {
		return map[string]string{}
	}
	if strings.HasPrefix(description, "{") {
		var data map[string]string
		if err := json.Unmarshal([]byte(description), &data); err == nil {
			return data
		}
	}
	if strings.HasPrefix(description, watermarkPrefix) {
		rest := description[len(watermarkPrefix):]
		if idx := strings.LastIndex(rest, ":"); idx >= 0 {
			return map[string]string{rest[:idx]: rest[idx+1:]}
		}
	}
	return map[string]string{}
}

func makeWatermarks(watermarks map[string]string) string {
	b, _ := json.Marshal(watermarks)
	return string(b)
}
