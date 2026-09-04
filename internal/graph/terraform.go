package graph

import (
	"database/sql"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// moduleSourceRE matches a Terraform/OpenTofu `source = "..."` argument. It
// also matches the `source` of a `required_providers` entry, which is a
// provider address rather than a module: those are discarded during resolution
// because a provider is never an indexed directory.
var moduleSourceRE = regexp.MustCompile(`(?m)^\s*source\s*=\s*"([^"]+)"`)

// gitSourceRE pulls the repository path out of a git-addressed module source,
// e.g. git::https://host/group/project.git?ref=1.2.3//subdir. The path is what
// makes this form resolvable without configuration: it is the same path the
// repository occupies in a checkout.
var gitSourceRE = regexp.MustCompile(`^git::(?:[a-z+]+://)?(?:[^@/]+@)?[^/]+/(.+)$`)

// tfFile is one indexed Terraform file.
type tfFile struct {
	ID   int64
	Path string
}

// tfIndex is what the terraform origin needs and nothing else. Kept separate
// from the markdown index because these are code sources, and because loading
// file content is only worth doing when this origin is actually requested.
type tfIndex struct {
	// dirs maps a directory to the .tf files in it. A module is a directory,
	// so this is the set of things a module reference can resolve to.
	dirs map[string][]tfFile
	// callers are the files that contain at least one `source =` argument,
	// with the content needed to read those arguments out.
	callers []tfCaller
}

type tfCaller struct {
	ID      int64
	Path    string
	Content string
}

// loadTerraform reads the Terraform files in every code collection. Content is
// fetched only for chunks that could contain a module reference, which is what
// keeps this to a fraction of the ~39k HCL chunks in a real corpus.
func loadTerraform(conn *sql.DB) (*tfIndex, error) {
	idx := &tfIndex{dirs: make(map[string][]tfFile)}

	rows, err := conn.Query(`
		SELECT s.id, s.source_path
		FROM sources s
		JOIN collections c ON c.id = s.collection_id
		WHERE c.collection_type = 'code' AND lower(s.source_path) LIKE '%.tf'`)
	if err != nil {
		return nil, fmt.Errorf("load terraform files: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var f tfFile
		if err := rows.Scan(&f.ID, &f.Path); err != nil {
			return nil, fmt.Errorf("scan terraform file: %w", err)
		}
		dir := filepath.ToSlash(filepath.Dir(f.Path))
		idx.dirs[dir] = append(idx.dirs[dir], f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Stable order so moduleEntry's fallback does not depend on row order.
	for _, files := range idx.dirs {
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	}

	crows, err := conn.Query(`
		SELECT d.source_id, s.source_path, d.content
		FROM documents d
		JOIN sources s ON s.id = d.source_id
		JOIN collections c ON c.id = d.collection_id
		WHERE c.collection_type = 'code' AND lower(s.source_path) LIKE '%.tf'
		  AND d.content LIKE '%source%=%'`)
	if err != nil {
		return nil, fmt.Errorf("load terraform module references: %w", err)
	}
	defer crows.Close()
	for crows.Next() {
		var c tfCaller
		if err := crows.Scan(&c.ID, &c.Path, &c.Content); err != nil {
			return nil, fmt.Errorf("scan terraform chunk: %w", err)
		}
		idx.callers = append(idx.callers, c)
	}
	if err := crows.Err(); err != nil {
		return nil, err
	}

	slog.Info("graph: loaded terraform files",
		"directories", len(idx.dirs), "chunks_with_a_source_argument", len(idx.callers))
	return idx, nil
}

// edges links a Terraform file to the modules it calls.
//
// Three source forms are resolvable, and they are resolvable for different
// reasons:
//
//   - a relative path, resolved arithmetically against the calling file;
//   - a git address, whose URL contains the repository path, so it matches an
//     indexed directory by path suffix with no assumptions at all;
//   - a registry address, but only for registries the caller has mapped in
//     GraphConfig.TerraformRegistryPaths, because a registry address shares
//     only the module *name* with a checkout and names collide heavily.
//
// A public registry address ("hashicorp/aws", "terraform-aws-modules/vpc/aws")
// is left unresolved: the module is not in the corpus, so there is nothing to
// point at.
func (idx *tfIndex) edges(registryPaths map[string][]string) ([]edge, OriginStats) {
	var st OriginStats
	out := newEdgeSet()

	for _, s := range idx.callers {
		for _, raw := range moduleSourceRE.FindAllStringSubmatch(s.Content, -1) {
			ref := strings.TrimSpace(raw[1])
			if ref == "" {
				continue
			}

			target, kind := idx.resolveModuleSource(ref, s.Path, registryPaths)
			switch kind {
			case notANote:
				// A provider address, a public registry module, or a registry
				// with no mapping: not a failure to resolve something that is
				// present, so not counted as one.
				st.Skipped++
				continue
			case unresolvable:
				st.Unresolved++
				continue
			case resolvedAmbiguously:
				st.Ambiguous++
			}
			if target != s.ID {
				out.add(s.ID, target, RelDependsOn)
			}
		}
	}
	return out.slice(), st
}

// resolveModuleSource maps one `source` value to the source id of the called
// module's entry file.
func (idx *tfIndex) resolveModuleSource(
	ref, fromPath string, registryPaths map[string][]string,
) (int64, resolution) {
	switch {
	case strings.HasPrefix(ref, "./"), strings.HasPrefix(ref, "../"):
		dir := filepath.Clean(filepath.Join(filepath.Dir(fromPath), ref))
		return idx.moduleEntry(dir)

	case strings.HasPrefix(ref, "git::"):
		m := gitSourceRE.FindStringSubmatch(ref)
		if m == nil {
			return 0, notANote
		}
		// Trim the query (?ref=), a //subdir selector, and the .git suffix.
		path := m[1]
		if i := strings.Index(path, "?"); i >= 0 {
			path = path[:i]
		}
		if i := strings.Index(path, "//"); i >= 0 {
			path = path[:i]
		}
		path = strings.TrimSuffix(strings.TrimSuffix(path, "/"), ".git")
		return idx.moduleBySuffix(path)

	default:
		// A registry address: <host>/<namespace>/<name>/<system>. Only the
		// prefixes the caller has mapped are resolvable.
		return idx.resolveRegistry(ref, registryPaths)
	}
}

func (idx *tfIndex) resolveRegistry(ref string, registryPaths map[string][]string) (int64, resolution) {
	// A `//subdir` selector addresses a module nested inside the published
	// one, so it has to survive resolution rather than be trimmed away.
	address, subdir := ref, ""
	if i := strings.Index(ref, "//"); i >= 0 {
		address, subdir = ref[:i], strings.Trim(ref[i+2:], "/")
	}

	parts := strings.Split(strings.TrimPrefix(address, "tfr://"), "/")
	if len(parts) < 4 {
		// Two or three segments is a provider ("hashicorp/aws") or a public
		// registry module; either way it is not in the corpus.
		return 0, notANote
	}
	name := parts[len(parts)-2]

	// Longest matching prefix wins, so a specific namespace can override a
	// broader one.
	prefixes := make([]string, 0, len(registryPaths))
	for prefix := range registryPaths {
		prefixes = append(prefixes, prefix)
	}
	sort.Slice(prefixes, func(i, j int) bool { return len(prefixes[i]) > len(prefixes[j]) })

	for _, prefix := range prefixes {
		if !strings.HasPrefix(address, strings.Trim(prefix, "/")+"/") {
			continue
		}
		// Configured order is the caller's precedence: the same module can
		// exist under two layouts during a migration, and the first listed
		// path is the one they mean.
		for _, base := range registryPaths[prefix] {
			candidate := strings.Trim(base, "/") + "/" + name
			if subdir != "" {
				candidate += "/" + subdir
			}
			if id, res := idx.moduleBySuffix(candidate); res != unresolvable {
				return id, res
			}
		}
		return 0, unresolvable
	}
	return 0, notANote
}

// moduleBySuffix finds the indexed directory whose path ends with the given
// path, then that module's entry file.
func (idx *tfIndex) moduleBySuffix(path string) (int64, resolution) {
	suffix := "/" + strings.Trim(filepath.ToSlash(path), "/")
	var matches []string
	for dir := range idx.dirs {
		if strings.HasSuffix(dir, suffix) {
			matches = append(matches, dir)
		}
	}
	switch len(matches) {
	case 0:
		return 0, unresolvable
	case 1:
		return idx.moduleEntry(matches[0])
	default:
		sort.Strings(matches)
		id, res := idx.moduleEntry(matches[0])
		if res == resolvedExactly {
			res = resolvedAmbiguously
		}
		return id, res
	}
}

// moduleEntry is the source a module edge points at. Endpoints have to be
// sources and a directory is not one, so a module is represented by its entry
// file: main.tf by convention, else the alphabetically first indexed .tf file
// in the directory, which keeps the choice stable across rebuilds.
func (idx *tfIndex) moduleEntry(dir string) (int64, resolution) {
	files := idx.dirs[filepath.ToSlash(dir)]
	if len(files) == 0 {
		return 0, unresolvable
	}
	for _, f := range files {
		if strings.EqualFold(filepath.Base(f.Path), "main.tf") {
			return f.ID, resolvedExactly
		}
	}
	return files[0].ID, resolvedExactly
}
