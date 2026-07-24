package installer

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
	"github.com/4js-mikefolcher/fglpkg/internal/materialize"
)

// writeStore writes a package store under the installer's packages dir:
// <home>/packages/<name>/ with an fglpkg.json and the given files.
func writeStore(t *testing.T, i *Installer, name, manifestJSON string, files map[string]string) {
	t.Helper()
	pkgDir := filepath.Join(i.PackagesDir(), name)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir store %s: %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "fglpkg.json"), []byte(manifestJSON), 0o644); err != nil {
		t.Fatalf("write store manifest: %v", err)
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

// writeLock writes a minimal project lock naming the given packages.
func writeLock(t *testing.T, projectDir string, names ...string) {
	t.Helper()
	lf := &lockfile.LockFile{Version: 1}
	for _, n := range names {
		lf.Packages = append(lf.Packages, lockfile.LockedPackage{
			Name: n, Version: "1.0.0", DownloadURL: "https://example.test/" + n + ".zip",
			RequiredBy: []string{"<root>"},
		})
	}
	if err := lf.Save(projectDir); err != nil {
		t.Fatalf("save lock: %v", err)
	}
}

func mergedHas(i *Installer, rel string) bool {
	_, err := os.Stat(filepath.Join(i.MergedDir(), filepath.FromSlash(rel)))
	return err == nil
}

// TestSyncMergedRootMaterializesAndRecordsLock is the Phase 4 happy path: after
// packages are on disk, syncMergedRoot builds the merged root AND records each
// package's namespaces + materialized files into the lock.
func TestSyncMergedRootMaterializesAndRecordsLock(t *testing.T) {
	home := t.TempDir()
	projectDir := t.TempDir()
	i := New(home, "", "", "")

	writeStore(t, i, "dbconnection", `{
  "name": "dbconnection", "version": "1.0.0",
  "generoPackages": ["com.fourjs.db"],
  "programs": ["test/TestConnection"],
  "dependencies": { "fgl": {} }
}`, map[string]string{
		"com/fourjs/db/DbConnection.42m": "DB",
		"test/TestConnection.42m":        "PROGRAM",
	})
	writeLock(t, projectDir, "dbconnection")

	if err := i.syncMergedRoot(projectDir, true); err != nil {
		t.Fatalf("syncMergedRoot: %v", err)
	}

	if !mergedHas(i, "com/fourjs/db/DbConnection.42m") {
		t.Error("library module not materialized into merged root")
	}
	if mergedHas(i, "test/TestConnection.42m") {
		t.Error("program should not be materialized")
	}

	lf, err := lockfile.Load(projectDir)
	if err != nil {
		t.Fatalf("reload lock: %v", err)
	}
	p := lf.Packages[0]
	if !stringSlicesEqual(p.GeneroPackages, []string{"com.fourjs.db"}) {
		t.Errorf("lock GeneroPackages = %v, want [com.fourjs.db]", p.GeneroPackages)
	}
	if !stringSlicesEqual(p.Materialized, []string{"com/fourjs/db/DbConnection.42m"}) {
		t.Errorf("lock Materialized = %v", p.Materialized)
	}
}

// TestSyncMergedRootClashAborts confirms a namespace clash is returned (so the
// install path aborts) rather than swallowed.
func TestSyncMergedRootClashAborts(t *testing.T) {
	home := t.TempDir()
	projectDir := t.TempDir()
	i := New(home, "", "", "")

	writeStore(t, i, "alpha", `{
  "name": "alpha", "version": "1.0.0",
  "generoPackages": ["com.dup"], "dependencies": { "fgl": {} }
}`, map[string]string{"com/dup/A.42m": "A"})
	writeStore(t, i, "beta", `{
  "name": "beta", "version": "1.0.0",
  "generoPackages": ["com.dup"], "dependencies": { "fgl": {} }
}`, map[string]string{"com/dup/B.42m": "B"})
	writeLock(t, projectDir, "alpha", "beta")

	err := i.syncMergedRoot(projectDir, true)
	if err == nil {
		t.Fatal("expected a clash error, got nil")
	}
	var clash *materialize.NamespaceClashError
	if !errors.As(err, &clash) {
		t.Fatalf("error is not *NamespaceClashError: %v", err)
	}
}

// TestSyncMergedRootNoLockNoOp confirms recording is skipped (no error, no lock
// conjured) when the project has no lock.
func TestSyncMergedRootNoLockNoOp(t *testing.T) {
	home := t.TempDir()
	projectDir := t.TempDir()
	i := New(home, "", "", "")

	writeStore(t, i, "dbconnection", `{
  "name": "dbconnection", "version": "1.0.0",
  "generoPackages": ["com.fourjs.db"], "dependencies": { "fgl": {} }
}`, map[string]string{"com/fourjs/db/DbConnection.42m": "DB"})

	if err := i.syncMergedRoot(projectDir, true); err != nil {
		t.Fatalf("syncMergedRoot: %v", err)
	}
	if !mergedHas(i, "com/fourjs/db/DbConnection.42m") {
		t.Error("merged root should still be built without a lock")
	}
	if lockfile.Exists(projectDir) {
		t.Error("syncMergedRoot must not conjure a lock file")
	}
}

// TestSyncMergedRootRecordLockFalse confirms the lock is left untouched when
// recordLock is false, even though the merged root is built.
func TestSyncMergedRootRecordLockFalse(t *testing.T) {
	home := t.TempDir()
	projectDir := t.TempDir()
	i := New(home, "", "", "")

	writeStore(t, i, "dbconnection", `{
  "name": "dbconnection", "version": "1.0.0",
  "generoPackages": ["com.fourjs.db"], "dependencies": { "fgl": {} }
}`, map[string]string{"com/fourjs/db/DbConnection.42m": "DB"})
	writeLock(t, projectDir, "dbconnection")

	if err := i.syncMergedRoot(projectDir, false); err != nil {
		t.Fatalf("syncMergedRoot: %v", err)
	}
	lf, _ := lockfile.Load(projectDir)
	if len(lf.Packages[0].GeneroPackages) != 0 || len(lf.Packages[0].Materialized) != 0 {
		t.Errorf("lock should be untouched when recordLock=false, got %v / %v",
			lf.Packages[0].GeneroPackages, lf.Packages[0].Materialized)
	}
}

// TestRebuildMergedRootBestEffort confirms the public best-effort rebuild builds
// the merged root and never panics/fails on an empty scope.
func TestRebuildMergedRootBestEffort(t *testing.T) {
	home := t.TempDir()
	i := New(home, "", "", "")

	// No stores yet — must be a no-op that leaves no merged root.
	i.RebuildMergedRoot()
	if _, err := os.Stat(i.MergedDir()); !os.IsNotExist(err) {
		t.Errorf("empty scope should leave no merged root, stat err = %v", err)
	}

	writeStore(t, i, "strutils", `{
  "name": "strutils", "version": "1.0.0",
  "generoPackages": ["org.util"], "dependencies": { "fgl": {} }
}`, map[string]string{"org/util/Strings.42m": "STR"})
	i.RebuildMergedRoot()
	if !mergedHas(i, "org/util/Strings.42m") {
		t.Error("RebuildMergedRoot should materialize present stores")
	}
}

// TestApplyMaterializationToLockChangeDetection confirms a second call with the
// same result does not rewrite the lock (nil vs empty treated equal).
func TestApplyMaterializationToLockNoChurn(t *testing.T) {
	projectDir := t.TempDir()
	writeLock(t, projectDir, "flatpkg")

	// A result that owns nothing for flatpkg — must not mark the lock changed.
	res := &materialize.Result{
		Owned:      map[string][]string{},
		Namespaces: map[string][]string{},
	}
	before, err := os.ReadFile(filepath.Join(projectDir, lockfile.Filename))
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	if err := applyMaterializationToLock(projectDir, res); err != nil {
		t.Fatalf("applyMaterializationToLock: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(projectDir, lockfile.Filename))
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	if string(before) != string(after) {
		t.Error("lock was rewritten despite no materialization change")
	}
}
