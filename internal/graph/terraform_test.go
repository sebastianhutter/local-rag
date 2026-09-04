package graph

import (
	"database/sql"
	"reflect"
	"sort"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// setupTFDB adds the collections table the terraform loader joins on.
func setupTFDB(t *testing.T) (*sql.DB, int64) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE collections (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, collection_type TEXT);
		CREATE TABLE sources (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			collection_id INTEGER NOT NULL,
			source_type TEXT NOT NULL,
			source_path TEXT NOT NULL);
		CREATE TABLE documents (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_id INTEGER NOT NULL,
			collection_id INTEGER NOT NULL,
			chunk_index INTEGER NOT NULL,
			content TEXT NOT NULL DEFAULT '',
			metadata TEXT);
		CREATE TABLE graph_edges (
			src_source_id INTEGER NOT NULL,
			dst_source_id INTEGER NOT NULL,
			rel TEXT NOT NULL, origin TEXT NOT NULL,
			PRIMARY KEY (src_source_id, dst_source_id, rel));
		INSERT INTO collections (name, collection_type) VALUES ('infra','code');`); err != nil {
		t.Fatal(err)
	}
	return db, 1
}

// addTF inserts a .tf file with content.
func addTF(t *testing.T, db *sql.DB, coll int64, path, content string) int64 {
	t.Helper()
	res, err := db.Exec(
		"INSERT INTO sources (collection_id, source_type, source_path) VALUES (?, 'code', ?)", coll, path)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if _, err := db.Exec(
		"INSERT INTO documents (source_id, collection_id, chunk_index, content) VALUES (?, ?, 0, ?)",
		id, coll, content); err != nil {
		t.Fatal(err)
	}
	return id
}

func tfEdges(t *testing.T, db *sql.DB) []storedEdge {
	t.Helper()
	rows, err := db.Query("SELECT src_source_id, dst_source_id, rel, origin FROM graph_edges")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []storedEdge
	for rows.Next() {
		var e storedEdge
		if err := rows.Scan(&e.Src, &e.Dst, &e.Rel, &e.Origin); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Src != out[j].Src {
			return out[i].Src < out[j].Src
		}
		return out[i].Dst < out[j].Dst
	})
	return out
}

// A relative source is resolved arithmetically against the calling file.
func TestTerraformRelativeSource(t *testing.T) {
	db, coll := setupTFDB(t)
	mod := addTF(t, db, coll, "/repo/modules/kms/main.tf", "resource \"aws_kms_key\" \"k\" {}")
	root := addTF(t, db, coll, "/repo/live/prod/main.tf",
		"module \"kms\" {\n  source = \"../../modules/kms\"\n}")

	if _, err := Rebuild(db, RebuildOptions{Origins: []string{OriginTerraform}}); err != nil {
		t.Fatal(err)
	}
	want := []storedEdge{{Src: root, Dst: mod, Rel: RelDependsOn, Origin: OriginTerraform}}
	if got := tfEdges(t, db); !reflect.DeepEqual(got, want) {
		t.Errorf("edges = %+v, want %+v", got, want)
	}
}

// A git address carries the repository path, so it needs no configuration.
func TestTerraformGitSource(t *testing.T) {
	db, coll := setupTFDB(t)
	mod := addTF(t, db, coll, "/checkout/terraform/aws/modules/s3/main.tf", "# s3")
	root := addTF(t, db, coll, "/repo/live/main.tf",
		"module \"s3\" {\n  source = \"git::https://git.example.com/terraform/aws/modules/s3.git?ref=2.8.1\"\n}")

	if _, err := Rebuild(db, RebuildOptions{Origins: []string{OriginTerraform}}); err != nil {
		t.Fatal(err)
	}
	want := []storedEdge{{Src: root, Dst: mod, Rel: RelDependsOn, Origin: OriginTerraform}}
	if got := tfEdges(t, db); !reflect.DeepEqual(got, want) {
		t.Errorf("edges = %+v, want %+v", got, want)
	}
}

// A registry address resolves only through configuration.
func TestTerraformRegistrySource(t *testing.T) {
	db, coll := setupTFDB(t)
	mod := addTF(t, db, coll, "/checkout/terraform/modules/vpc/main.tf", "# vpc")
	root := addTF(t, db, coll, "/repo/live/main.tf",
		"module \"vpc\" {\n  source = \"registry.example.com/infra/vpc/aws\"\n}")

	// No mapping: skipped, not counted as a failure to resolve.
	stats, err := Rebuild(db, RebuildOptions{Origins: []string{OriginTerraform}})
	if err != nil {
		t.Fatal(err)
	}
	if got := tfEdges(t, db); len(got) != 0 {
		t.Errorf("edges = %+v, want none without a mapping", got)
	}
	if st := stats.ByOrigin[OriginTerraform]; st.Skipped != 1 || st.Unresolved != 0 {
		t.Errorf("Skipped=%d Unresolved=%d, want 1 and 0", st.Skipped, st.Unresolved)
	}

	// With a mapping it resolves.
	if _, err := Rebuild(db, RebuildOptions{
		Origins: []string{OriginTerraform},
		TerraformRegistryPaths: map[string][]string{
			"registry.example.com/infra": {"terraform/modules"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	want := []storedEdge{{Src: root, Dst: mod, Rel: RelDependsOn, Origin: OriginTerraform}}
	if got := tfEdges(t, db); !reflect.DeepEqual(got, want) {
		t.Errorf("edges = %+v, want %+v", got, want)
	}
}

// The same module can exist under two layouts during a migration. Configured
// order is the caller's precedence, so the first listed path wins.
func TestTerraformRegistryPathOrderIsPrecedence(t *testing.T) {
	db, coll := setupTFDB(t)
	v2 := addTF(t, db, coll, "/checkout/terraform/v2/backup/main.tf", "# v2")
	v1 := addTF(t, db, coll, "/checkout/terraform/aws/modules/backup/main.tf", "# v1")
	root := addTF(t, db, coll, "/repo/live/main.tf",
		"module \"backup\" {\n  source = \"registry.example.com/reg/backup/aws\"\n}")

	for _, tc := range []struct {
		name  string
		paths []string
		want  int64
	}{
		{"v2 first", []string{"terraform/v2", "terraform/aws/modules"}, v2},
		{"v1 first", []string{"terraform/aws/modules", "terraform/v2"}, v1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Rebuild(db, RebuildOptions{
				Origins:                []string{OriginTerraform},
				TerraformRegistryPaths: map[string][]string{"registry.example.com/reg": tc.paths},
			}); err != nil {
				t.Fatal(err)
			}
			want := []storedEdge{{Src: root, Dst: tc.want, Rel: RelDependsOn, Origin: OriginTerraform}}
			if got := tfEdges(t, db); !reflect.DeepEqual(got, want) {
				t.Errorf("edges = %+v, want %+v", got, want)
			}
		})
	}
}

// A //subdir selector addresses a module nested inside the published one and
// must survive resolution rather than collapse to the parent.
func TestTerraformRegistrySubdir(t *testing.T) {
	db, coll := setupTFDB(t)
	parent := addTF(t, db, coll, "/checkout/tf/eks/main.tf", "# eks")
	nested := addTF(t, db, coll, "/checkout/tf/eks/modules/addons/main.tf", "# addons")
	root := addTF(t, db, coll, "/repo/live/main.tf",
		"module \"addons\" {\n  source = \"registry.example.com/reg/eks/aws//modules/addons\"\n}")

	if _, err := Rebuild(db, RebuildOptions{
		Origins:                []string{OriginTerraform},
		TerraformRegistryPaths: map[string][]string{"registry.example.com/reg": {"tf"}},
	}); err != nil {
		t.Fatal(err)
	}
	got := tfEdges(t, db)
	want := []storedEdge{{Src: root, Dst: nested, Rel: RelDependsOn, Origin: OriginTerraform}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("edges = %+v, want the nested module %d not the parent %d", got, nested, parent)
	}
}

// A provider address and a public registry module are not modules in the
// corpus, and must not be counted as failures to resolve.
func TestTerraformSkipsProvidersAndPublicRegistry(t *testing.T) {
	db, coll := setupTFDB(t)
	addTF(t, db, coll, "/repo/live/versions.tf", `
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}
module "vpc" {
  source = "terraform-aws-modules/vpc/aws"
}`)

	stats, err := Rebuild(db, RebuildOptions{Origins: []string{OriginTerraform}})
	if err != nil {
		t.Fatal(err)
	}
	if got := tfEdges(t, db); len(got) != 0 {
		t.Errorf("edges = %+v, want none", got)
	}
	st := stats.ByOrigin[OriginTerraform]
	if st.Unresolved != 0 {
		t.Errorf("Unresolved = %d, want 0 -- neither is a module in the corpus", st.Unresolved)
	}
	if st.Skipped != 2 {
		t.Errorf("Skipped = %d, want 2", st.Skipped)
	}
}

// A module reference to a directory that is not indexed is a genuine
// resolution failure and is counted as one.
func TestTerraformUnresolvedRelative(t *testing.T) {
	db, coll := setupTFDB(t)
	addTF(t, db, coll, "/repo/live/main.tf", "module \"x\" {\n  source = \"../../not/checked/out\"\n}")
	stats, err := Rebuild(db, RebuildOptions{Origins: []string{OriginTerraform}})
	if err != nil {
		t.Fatal(err)
	}
	if st := stats.ByOrigin[OriginTerraform]; st.Unresolved != 1 {
		t.Errorf("Unresolved = %d, want 1", st.Unresolved)
	}
}

// An edge points at the module's entry file, since endpoints must be sources
// and a directory is not one. main.tf wins; otherwise the choice is stable.
func TestTerraformModuleEntryFile(t *testing.T) {
	db, coll := setupTFDB(t)
	addTF(t, db, coll, "/repo/modules/kms/outputs.tf", "# outputs")
	main := addTF(t, db, coll, "/repo/modules/kms/main.tf", "# main")
	addTF(t, db, coll, "/repo/modules/kms/variables.tf", "# vars")
	root := addTF(t, db, coll, "/repo/live/main.tf", "module \"kms\" {\n  source = \"../modules/kms\"\n}")

	if _, err := Rebuild(db, RebuildOptions{Origins: []string{OriginTerraform}}); err != nil {
		t.Fatal(err)
	}
	got := tfEdges(t, db)
	if len(got) != 1 || got[0].Dst != main {
		t.Errorf("edge points at %+v, want main.tf (%d)", got, main)
	}
	_ = root
}

// Without main.tf the entry file is the alphabetically first .tf, so two
// rebuilds of unchanged data agree.
func TestTerraformModuleEntryFallbackIsStable(t *testing.T) {
	db, coll := setupTFDB(t)
	addTF(t, db, coll, "/repo/modules/kms/zz.tf", "# z")
	first := addTF(t, db, coll, "/repo/modules/kms/aa.tf", "# a")
	addTF(t, db, coll, "/repo/live/main.tf", "module \"kms\" {\n  source = \"../modules/kms\"\n}")

	if _, err := Rebuild(db, RebuildOptions{Origins: []string{OriginTerraform}}); err != nil {
		t.Fatal(err)
	}
	got := tfEdges(t, db)
	if len(got) != 1 || got[0].Dst != first {
		t.Errorf("edge points at %+v, want the alphabetically first file (%d)", got, first)
	}
}
