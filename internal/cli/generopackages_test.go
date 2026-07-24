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

// TestScanGeneroPackages exercises the per-module classification directly:
// PACKAGE-declaring library modules contribute their (deduped) namespace,
// declared programs and no-PACKAGE flat modules do not, and a .42m with no
// sibling .4gl is counted as a library module but not parsed.
func TestScanGeneroPackages(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) string {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
		return full
	}
	// scanGeneroPackages reads the sibling .4gl of each staged .42m, so only
	// the .4gl sources need to exist on disk.
	lib := write("com/fourjs/db/DbConnection.4gl", "PACKAGE com.fourjs.db\nFUNCTION connect() END FUNCTION\n")
	lib2 := write("com/fourjs/db/Query.4gl", "PACKAGE com.fourjs.db\n")          // same namespace -> dedup
	other := write("org/util/Strings.4gl", "-- header\nPACKAGE org.util\n")      // distinct namespace
	prog := write("test/TestConnection.4gl", "MAIN\n  DISPLAY \"hi\"\nEND MAIN") // declared program: skipped
	flat := write("Legacy.4gl", "FUNCTION helper() END FUNCTION\n")              // library, no PACKAGE

	m42 := func(gl string) string { return strings.TrimSuffix(gl, ".4gl") + ".42m" }
	staged := map[string]string{
		"com/fourjs/db/DbConnection.42m": m42(lib),
		"com/fourjs/db/Query.42m":        m42(lib2),
		"org/util/Strings.42m":           m42(other),
		"test/TestConnection.42m":        m42(prog),
		"Legacy.42m":                     m42(flat),
		"README.md":                      filepath.Join(dir, "README.md"),                // not a .42m
		"missing/OnlyCompiled.42m":       filepath.Join(dir, "missing/OnlyCompiled.42m"), // no sibling .4gl
	}

	scan, err := scanGeneroPackages(staged, []string{"test/TestConnection"})
	if err != nil {
		t.Fatalf("scanGeneroPackages: %v", err)
	}
	wantNS := []string{"com.fourjs.db", "org.util"}
	if !equalNamespaceSet(scan.namespaces, wantNS) {
		t.Errorf("namespaces = %v, want %v", scan.namespaces, wantNS)
	}
	// DbConnection, Query, Strings, Legacy, OnlyCompiled — the declared program
	// TestConnection is skipped, README.md is not a .42m.
	if scan.libModules != 5 {
		t.Errorf("libModules = %d, want 5", scan.libModules)
	}
	// All but OnlyCompiled (no sibling .4gl) have parseable source.
	if scan.parsedSource != 4 {
		t.Errorf("parsedSource = %d, want 4", scan.parsedSource)
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

// TestBuildPackageZip42mOnlyHasNoGeneroPackages confirms a package shipping
// only compiled .42m (no .4gl to parse) records nothing rather than guessing.
func TestBuildPackageZip42mOnlyHasNoGeneroPackages(t *testing.T) {
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
	if len(got) != 0 {
		t.Errorf("generoPackages = %v, want empty (no source to determine namespaces)", got)
	}
}
