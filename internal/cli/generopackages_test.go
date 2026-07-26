package cli

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// TestScanGeneroPackages exercises namespace inference from the staged archive
// layout: a library module's namespace is its archive directory; declared
// programs, flat-root modules, and subdir modules whose ADJACENT source declares
// no PACKAGE are excluded; a subdir module with no adjacent source is still
// namespaced from its directory (the poiapi case — sources compiled from lib/).
func TestScanGeneroPackages(t *testing.T) {
	dir := t.TempDir()
	// Write an on-disk source file and return the .42m path that would sit
	// beside it, for use as the staged map's srcDisk value.
	src := func(rel, content string) string {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
		return strings.TrimSuffix(full, ".4gl") + ".42m"
	}
	// A .42m with no adjacent source on disk (poiapi shape).
	noSrc := func(rel string) string { return filepath.Join(dir, rel) }

	staged := map[string]string{
		// Namespaced by directory; adjacent source confirms PACKAGE.
		"com/fourjs/db/DbConnection.42m": src("com/fourjs/db/DbConnection.4gl", "PACKAGE com.fourjs.db\n"),
		"com/fourjs/db/Query.42m":        src("com/fourjs/db/Query.4gl", "PACKAGE com.fourjs.db\n"), // dedup
		// Namespaced by directory with NO adjacent source (compiled from lib/).
		"com/fourjs/poiapi/PoiApi.42m": noSrc("com/fourjs/poiapi/PoiApi.42m"),
		// Declared program — excluded by the programs list.
		"test/TestConnection.42m": src("test/TestConnection.4gl", "MAIN\nEND MAIN\n"),
		// Flat-root module — no namespace.
		"Flat.42m": src("Flat.4gl", "FUNCTION f() END FUNCTION\n"),
		// Subdir module whose ADJACENT source declares no PACKAGE — excluded.
		"helpers/Util.42m": src("helpers/Util.4gl", "FUNCTION u() END FUNCTION\n"),
		// Not a .42m.
		"README.md": noSrc("README.md"),
	}

	got, err := scanGeneroPackages(staged, []string{"test/TestConnection"})
	if err != nil {
		t.Fatalf("scanGeneroPackages: %v", err)
	}
	want := []string{"com.fourjs.db", "com.fourjs.poiapi"}
	if !equalNamespaceSet(got, want) {
		t.Errorf("namespaces = %v, want %v", got, want)
	}
}

// packAndReadManifest stages the project in dir and returns the parsed
// generoPackages recorded in the shipped fglpkg.json.
func packAndReadGeneroPackages(t *testing.T, dir string) []string {
	t.Helper()
	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	data, _, err := buildPackageZip(m)
	if err != nil {
		t.Fatalf("buildPackageZip: %v", err)
	}
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	for _, f := range r.File {
		if f.Name != manifest.Filename {
			continue
		}
		body, err := readZipEntry(f)
		if err != nil {
			t.Fatalf("read manifest: %v", err)
		}
		var meta struct {
			GeneroPackages []string `json:"generoPackages"`
		}
		if err := json.Unmarshal([]byte(body), &meta); err != nil {
			t.Fatalf("unmarshal staged manifest: %v\n%s", err, body)
		}
		return meta.GeneroPackages
	}
	t.Fatalf("staged manifest not found in zip")
	return nil
}

// TestBuildPackageZipRecordsGeneroPackages covers the PackageB shape: a
// namespaced library module (com.fourjs.db) plus an out-of-namespace program.
// The shipped manifest records only the library namespace.
func TestBuildPackageZipRecordsGeneroPackages(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("fglpkg.json", `{
  "name": "dbconnection",
  "version": "1.0.0",
  "programs": ["test/TestConnection"],
  "dependencies": { "fgl": {} }
}`)
	write("com/fourjs/db/DbConnection.42m", "compiled")
	write("com/fourjs/db/DbConnection.4gl", "PACKAGE com.fourjs.db\nFUNCTION connect() END FUNCTION\n")
	write("test/TestConnection.42m", "compiled")
	write("test/TestConnection.4gl", "IMPORT FGL com.fourjs.db.DbConnection\nMAIN\nEND MAIN\n")

	got := packAndReadGeneroPackages(t, dir)
	want := []string{"com.fourjs.db"}
	if !equalNamespaceSet(got, want) {
		t.Errorf("generoPackages = %v, want %v", got, want)
	}
}

// TestBuildPackageZipFlatHasNoGeneroPackages confirms a package with no
// PACKAGE-declaring module records no generoPackages (omitempty -> absent).
func TestBuildPackageZipFlatHasNoGeneroPackages(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("fglpkg.json", `{
  "name": "flatpkg",
  "version": "1.0.0",
  "dependencies": { "fgl": {} }
}`)
	write("Helper.42m", "compiled")
	write("Helper.4gl", "FUNCTION helper() END FUNCTION\n")

	got := packAndReadGeneroPackages(t, dir)
	if len(got) != 0 {
		t.Errorf("generoPackages = %v, want empty", got)
	}
}

// TestBuildPackageZipAuthorDeclaredGeneroPackagesWins confirms an explicit
// author declaration is preserved even when it differs from what the source
// would compute (the declared set is the override / escape hatch).
func TestBuildPackageZipAuthorDeclaredGeneroPackagesWins(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("fglpkg.json", `{
  "name": "declared",
  "version": "1.0.0",
  "generoPackages": ["custom.declared.ns"],
  "dependencies": { "fgl": {} }
}`)
	// Source would compute com.fourjs.db, but the declaration wins.
	write("com/fourjs/db/DbConnection.42m", "compiled")
	write("com/fourjs/db/DbConnection.4gl", "PACKAGE com.fourjs.db\n")

	got := packAndReadGeneroPackages(t, dir)
	want := []string{"custom.declared.ns"}
	if !equalNamespaceSet(got, want) {
		t.Errorf("generoPackages = %v, want %v (declaration should win)", got, want)
	}
}

// TestBuildPackageZip42mOnlyInfersFromLayout confirms a package shipping only
// compiled .42m with no adjacent .4gl (sources compiled from lib/ or src/ into
// a namespace tree — the poiapi shape) still records the namespace inferred
// from the archive directory.
func TestBuildPackageZip42mOnlyInfersFromLayout(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("fglpkg.json", `{
  "name": "compiledonly",
  "version": "1.0.0",
  "dependencies": { "fgl": {} }
}`)
	write("com/fourjs/db/DbConnection.42m", "compiled") // no sibling .4gl

	got := packAndReadGeneroPackages(t, dir)
	want := []string{"com.fourjs.db"}
	if !equalNamespaceSet(got, want) {
		t.Errorf("generoPackages = %v, want %v (inferred from archive layout)", got, want)
	}
}
