package materialize

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// scope creates a fresh Scope rooted at a temp dir with packages/ + merged/.
func newScope(t *testing.T) Scope {
	t.Helper()
	root := t.TempDir()
	return Scope{
		PackagesDir: filepath.Join(root, "packages"),
		MergedDir:   filepath.Join(root, "merged"),
	}
}

// writePkg writes a package store dir <PackagesDir>/<name>/ with the given
// manifest JSON and files (relative path -> content).
func writePkg(t *testing.T, scope Scope, name, manifestJSON string, files map[string]string) {
	t.Helper()
	pkgDir := filepath.Join(scope.PackagesDir, name)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", pkgDir, err)
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

func mergedExists(scope Scope, rel string) bool {
	_, err := os.Stat(filepath.Join(scope.MergedDir, filepath.FromSlash(rel)))
	return err == nil
}

func mergedContent(t *testing.T, scope Scope, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(scope.MergedDir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read merged %s: %v", rel, err)
	}
	return string(data)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRebuildTwoDistinctNamespaces is the happy path: two packages with
// distinct namespaces materialize into one merged root; the out-of-namespace
// program is excluded.
func TestRebuildTwoDistinctNamespaces(t *testing.T) {
	scope := newScope(t)
	writePkg(t, scope, "dbconnection", `{
  "name": "dbconnection", "version": "1.0.0",
  "generoPackages": ["com.fourjs.db"],
  "programs": ["test/TestConnection"],
  "dependencies": { "fgl": {} }
}`, map[string]string{
		"com/fourjs/db/DbConnection.42m": "DB",
		"com/fourjs/db/Query.42m":        "QUERY",
		"test/TestConnection.42m":        "PROGRAM", // not in a declared namespace dir
	})
	writePkg(t, scope, "strutils", `{
  "name": "strutils", "version": "1.0.0",
  "generoPackages": ["org.util"],
  "dependencies": { "fgl": {} }
}`, map[string]string{
		"org/util/Strings.42m": "STR",
	})

	res, err := Rebuild(scope)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	for _, rel := range []string{"com/fourjs/db/DbConnection.42m", "com/fourjs/db/Query.42m", "org/util/Strings.42m"} {
		if !mergedExists(scope, rel) {
			t.Errorf("expected %s in merged root", rel)
		}
	}
	if mergedExists(scope, "test/TestConnection.42m") {
		t.Error("program TestConnection.42m must not be merged")
	}
	if got := mergedContent(t, scope, "com/fourjs/db/DbConnection.42m"); got != "DB" {
		t.Errorf("merged content = %q, want DB", got)
	}

	wantOwned := []string{"com/fourjs/db/DbConnection.42m", "com/fourjs/db/Query.42m"}
	if !equalStrings(res.Owned["dbconnection"], wantOwned) {
		t.Errorf("Owned[dbconnection] = %v, want %v", res.Owned["dbconnection"], wantOwned)
	}
	if !equalStrings(res.Owned["strutils"], []string{"org/util/Strings.42m"}) {
		t.Errorf("Owned[strutils] = %v", res.Owned["strutils"])
	}
	if !equalStrings(res.Namespaces["dbconnection"], []string{"com.fourjs.db"}) {
		t.Errorf("Namespaces[dbconnection] = %v", res.Namespaces["dbconnection"])
	}
	if len(res.Inferred) != 0 {
		t.Errorf("Inferred = %v, want none", res.Inferred)
	}
}

// TestRebuildNamespaceClash: two packages declaring the same namespace is a
// hard error naming both, and the pre-existing merged root is left intact.
func TestRebuildNamespaceClash(t *testing.T) {
	scope := newScope(t)
	writePkg(t, scope, "alpha", `{
  "name": "alpha", "version": "1.0.0",
  "generoPackages": ["com.dup"],
  "dependencies": { "fgl": {} }
}`, map[string]string{"com/dup/A.42m": "A"})
	writePkg(t, scope, "beta", `{
  "name": "beta", "version": "1.0.0",
  "generoPackages": ["com.dup"],
  "dependencies": { "fgl": {} }
}`, map[string]string{"com/dup/B.42m": "B"})

	_, err := Rebuild(scope)
	if err == nil {
		t.Fatal("expected a namespace clash error, got nil")
	}
	var clash *NamespaceClashError
	if !errors.As(err, &clash) {
		t.Fatalf("error is not *NamespaceClashError: %v", err)
	}
	if clash.Namespace != "com.dup" {
		t.Errorf("clash namespace = %q, want com.dup", clash.Namespace)
	}
	// alpha is planned before beta (sorted), so PackageA=alpha, PackageB=beta.
	if clash.PackageA != "alpha" || clash.PackageB != "beta" {
		t.Errorf("clash packages = %q/%q, want alpha/beta", clash.PackageA, clash.PackageB)
	}
	// The merged root must not have been created/mutated by a failed rebuild.
	if _, statErr := os.Stat(scope.MergedDir); !os.IsNotExist(statErr) {
		t.Errorf("merged root should be untouched on clash, stat err = %v", statErr)
	}
}

// TestRebuildIdempotent: running twice yields the same result and a valid tree.
func TestRebuildIdempotent(t *testing.T) {
	scope := newScope(t)
	writePkg(t, scope, "dbconnection", `{
  "name": "dbconnection", "version": "1.0.0",
  "generoPackages": ["com.fourjs.db"],
  "dependencies": { "fgl": {} }
}`, map[string]string{"com/fourjs/db/DbConnection.42m": "DB"})

	first, err := Rebuild(scope)
	if err != nil {
		t.Fatalf("Rebuild #1: %v", err)
	}
	second, err := Rebuild(scope)
	if err != nil {
		t.Fatalf("Rebuild #2: %v", err)
	}
	if !equalStrings(first.Owned["dbconnection"], second.Owned["dbconnection"]) {
		t.Errorf("owned differs between runs: %v vs %v", first.Owned, second.Owned)
	}
	if !mergedExists(scope, "com/fourjs/db/DbConnection.42m") {
		t.Error("merged file missing after second rebuild")
	}
}

// TestRebuildRemovesStaleEntries: a package removed from the store disappears
// from the merged root on the next rebuild.
func TestRebuildRemovesStaleEntries(t *testing.T) {
	scope := newScope(t)
	writePkg(t, scope, "keep", `{
  "name": "keep", "version": "1.0.0",
  "generoPackages": ["com.keep"],
  "dependencies": { "fgl": {} }
}`, map[string]string{"com/keep/K.42m": "K"})
	writePkg(t, scope, "drop", `{
  "name": "drop", "version": "1.0.0",
  "generoPackages": ["com.drop"],
  "dependencies": { "fgl": {} }
}`, map[string]string{"com/drop/D.42m": "D"})

	if _, err := Rebuild(scope); err != nil {
		t.Fatalf("Rebuild #1: %v", err)
	}
	if !mergedExists(scope, "com/drop/D.42m") {
		t.Fatal("setup: drop should be merged initially")
	}

	if err := os.RemoveAll(filepath.Join(scope.PackagesDir, "drop")); err != nil {
		t.Fatalf("remove drop store: %v", err)
	}
	res, err := Rebuild(scope)
	if err != nil {
		t.Fatalf("Rebuild #2: %v", err)
	}
	if mergedExists(scope, "com/drop/D.42m") {
		t.Error("stale drop entry survived rebuild")
	}
	if !mergedExists(scope, "com/keep/K.42m") {
		t.Error("keep entry should still be merged")
	}
	if _, ok := res.Owned["drop"]; ok {
		t.Error("Owned should not include the removed package")
	}
}

// TestRebuildInferenceFallback: a legacy package with no generoPackages infers
// namespaces from the tree; flat root .42m and declared programs are excluded,
// and the package is reported as inferred.
func TestRebuildInferenceFallback(t *testing.T) {
	scope := newScope(t)
	writePkg(t, scope, "legacy", `{
  "name": "legacy", "version": "1.0.0",
  "programs": ["test/TestConnection"],
  "dependencies": { "fgl": {} }
}`, map[string]string{
		"com/acme/lib/Helper.42m": "HELPER", // -> inferred namespace com.acme.lib
		"test/TestConnection.42m": "PROG",   // declared program: excluded
		"Root.42m":                "ROOT",   // flat root: excluded
	})

	res, err := Rebuild(scope)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if !mergedExists(scope, "com/acme/lib/Helper.42m") {
		t.Error("inferred library module should be merged")
	}
	if mergedExists(scope, "test/TestConnection.42m") {
		t.Error("declared program should not be merged")
	}
	if mergedExists(scope, "Root.42m") {
		t.Error("flat root module should not be merged")
	}
	if !equalStrings(res.Namespaces["legacy"], []string{"com.acme.lib"}) {
		t.Errorf("inferred namespaces = %v, want [com.acme.lib]", res.Namespaces["legacy"])
	}
	if !equalStrings(res.Inferred, []string{"legacy"}) {
		t.Errorf("Inferred = %v, want [legacy]", res.Inferred)
	}
}

// TestRebuildNestedNamespacesNoClash: a parent namespace in one package and a
// child namespace in another are distinct — no clash, no filesystem overlap.
func TestRebuildNestedNamespacesNoClash(t *testing.T) {
	scope := newScope(t)
	writePkg(t, scope, "core", `{
  "name": "core", "version": "1.0.0",
  "generoPackages": ["com.fourjs.db"],
  "dependencies": { "fgl": {} }
}`, map[string]string{"com/fourjs/db/Core.42m": "CORE"})
	writePkg(t, scope, "plugins", `{
  "name": "plugins", "version": "1.0.0",
  "generoPackages": ["com.fourjs.db.plugins"],
  "dependencies": { "fgl": {} }
}`, map[string]string{"com/fourjs/db/plugins/Plug.42m": "PLUG"})

	res, err := Rebuild(scope)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if !mergedExists(scope, "com/fourjs/db/Core.42m") || !mergedExists(scope, "com/fourjs/db/plugins/Plug.42m") {
		t.Error("both parent and child namespace modules should be merged")
	}
	// core owns only the parent module, not the child package's file.
	if !equalStrings(res.Owned["core"], []string{"com/fourjs/db/Core.42m"}) {
		t.Errorf("Owned[core] = %v, want [com/fourjs/db/Core.42m]", res.Owned["core"])
	}
	if !equalStrings(res.Owned["plugins"], []string{"com/fourjs/db/plugins/Plug.42m"}) {
		t.Errorf("Owned[plugins] = %v", res.Owned["plugins"])
	}
}

// TestRebuildEmptyPackagesDir: a missing packages dir is not an error; any
// stale merged root is cleared.
func TestRebuildEmptyPackagesDir(t *testing.T) {
	scope := newScope(t)
	// Pre-seed a stale merged root to confirm it is cleared.
	if err := os.MkdirAll(filepath.Join(scope.MergedDir, "stale"), 0o755); err != nil {
		t.Fatalf("seed stale: %v", err)
	}
	res, err := Rebuild(scope)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if len(res.Owned) != 0 {
		t.Errorf("Owned = %v, want empty", res.Owned)
	}
	if _, statErr := os.Stat(scope.MergedDir); !os.IsNotExist(statErr) {
		t.Errorf("stale merged root should be cleared, stat err = %v", statErr)
	}
}

// TestCopyFile verifies the copy fallback used when hard-linking is impossible.
func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.42m")
	dst := filepath.Join(dir, "sub", "dst.42m")
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile: %v", err)
	}
	data, err := os.ReadFile(dst)
	if err != nil || string(data) != "payload" {
		t.Errorf("copyFile produced %q (err %v), want payload", data, err)
	}
}

// TestLinkOrCopyContent verifies linkOrCopy yields a readable destination with
// the source content (whether via hard link or copy fallback).
func TestLinkOrCopyContent(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.42m")
	dst := filepath.Join(dir, "dst.42m")
	if err := os.WriteFile(src, []byte("linked"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := linkOrCopy(src, dst); err != nil {
		t.Fatalf("linkOrCopy: %v", err)
	}
	data, err := os.ReadFile(dst)
	if err != nil || string(data) != "linked" {
		t.Errorf("linkOrCopy produced %q (err %v), want linked", data, err)
	}
	// Calling again over an existing dst must succeed (idempotent).
	if err := linkOrCopy(src, dst); err != nil {
		t.Fatalf("linkOrCopy (re-run): %v", err)
	}
}
