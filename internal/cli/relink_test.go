package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// writeRelinkStore writes an installed package store under
// <cwd>/.fglpkg/packages/<name>/ with a manifest and files.
func writeRelinkStore(t *testing.T, name, manifestJSON string, files map[string]string) {
	t.Helper()
	pkgDir := filepath.Join(".fglpkg", "packages", name)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir store: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "fglpkg.json"), []byte(manifestJSON), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	for rel, content := range files {
		full := filepath.Join(pkgDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}

func chdirTempRelink(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	orig, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
}

// TestCmdRelinkLocalBuildsMergedRoot: `relink --local` materializes the merged
// root from an installed store.
func TestCmdRelinkLocalBuildsMergedRoot(t *testing.T) {
	chdirTempRelink(t)
	writeRelinkStore(t, "dbconnection",
		`{ "name": "dbconnection", "version": "1.0.0", "generoPackages": ["com.fourjs.db"], "dependencies": { "fgl": {} } }`,
		map[string]string{"com/fourjs/db/DbConnection.42m": "DB"})

	if err := cmdRelink([]string{"--local"}); err != nil {
		t.Fatalf("cmdRelink: %v", err)
	}
	if _, err := os.Stat(filepath.Join(".fglpkg", "merged", "com", "fourjs", "db", "DbConnection.42m")); err != nil {
		t.Errorf("merged root not built: %v", err)
	}
}

// TestCmdRelinkIsIdempotent: a second relink over an existing merged root
// succeeds and leaves it intact.
func TestCmdRelinkIsIdempotent(t *testing.T) {
	chdirTempRelink(t)
	writeRelinkStore(t, "strutils",
		`{ "name": "strutils", "version": "1.0.0", "generoPackages": ["org.util"], "dependencies": { "fgl": {} } }`,
		map[string]string{"org/util/Strings.42m": "S"})

	if err := cmdRelink([]string{"--local"}); err != nil {
		t.Fatalf("relink #1: %v", err)
	}
	if err := cmdRelink([]string{"--local"}); err != nil {
		t.Fatalf("relink #2: %v", err)
	}
	if _, err := os.Stat(filepath.Join(".fglpkg", "merged", "org", "util", "Strings.42m")); err != nil {
		t.Errorf("merged file missing after second relink: %v", err)
	}
}

// TestCmdRelinkClashFails: two packages declaring the same namespace make
// relink fail loudly.
func TestCmdRelinkClashFails(t *testing.T) {
	chdirTempRelink(t)
	writeRelinkStore(t, "alpha",
		`{ "name": "alpha", "version": "1.0.0", "generoPackages": ["com.dup"], "dependencies": { "fgl": {} } }`,
		map[string]string{"com/dup/A.42m": "A"})
	writeRelinkStore(t, "beta",
		`{ "name": "beta", "version": "1.0.0", "generoPackages": ["com.dup"], "dependencies": { "fgl": {} } }`,
		map[string]string{"com/dup/B.42m": "B"})

	if err := cmdRelink([]string{"--local"}); err == nil {
		t.Fatal("expected relink to fail on a namespace clash, got nil")
	}
}

// TestCmdRelinkRejectsArgsAndConflictingScopes covers argument validation.
func TestCmdRelinkRejectsArgsAndConflictingScopes(t *testing.T) {
	chdirTempRelink(t)
	if err := cmdRelink([]string{"extra"}); err == nil {
		t.Error("expected error for a positional argument")
	}
	if err := cmdRelink([]string{"--local", "--global"}); err == nil {
		t.Error("expected error for --local + --global together")
	}
}
