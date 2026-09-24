package installer

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// writeStorePkg hand-places an installed package in a store: a directory with a
// bundled fglpkg.json, which is all the global store has to go on (GIS-567).
func writeStorePkg(t *testing.T, home, dir, body string) string {
	t.Helper()
	pkgDir := filepath.Join(home, "packages", dir)
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", pkgDir, err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "fglpkg.json"), []byte(body+"\n"), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return pkgDir
}

func writeStoreJar(t *testing.T, home, name string) string {
	t.Helper()
	jarsDir := filepath.Join(home, "jars")
	if err := os.MkdirAll(jarsDir, 0755); err != nil {
		t.Fatalf("mkdir jars: %v", err)
	}
	p := filepath.Join(jarsDir, name)
	if err := os.WriteFile(p, []byte("jar"), 0644); err != nil {
		t.Fatalf("write jar: %v", err)
	}
	return p
}

func assertExists(t *testing.T, path string, want bool) {
	t.Helper()
	_, err := os.Stat(path)
	if want && err != nil {
		t.Fatalf("expected to still exist: %s (%v)", path, err)
	}
	if !want && err == nil {
		t.Fatalf("expected to be gone: %s", path)
	}
}

// TestRemoveFromStore_RemovesPackageAndUniquelyOwnedJar: the core of GIS-567 —
// the named package leaves, and a JAR only it declared goes with it.
func TestRemoveFromStore_RemovesPackageAndUniquelyOwnedJar(t *testing.T) {
	home := t.TempDir()
	toolDir := writeStorePkg(t, home, "tool",
		`{"name":"tool","version":"1.0.0","dependencies":{"java":[{"groupId":"org.x","artifactId":"only-tool","version":"1.0"}]}}`)
	keptDir := writeStorePkg(t, home, "other",
		`{"name":"other","version":"1.0.0","dependencies":{"java":[{"groupId":"org.x","artifactId":"shared","version":"2.0"}]}}`)
	onlyTool := writeStoreJar(t, home, "only-tool-1.0.jar")
	shared := writeStoreJar(t, home, "shared-2.0.jar")

	res, err := New(home, "", "", "").RemoveFromStore([]string{"tool"})
	if err != nil {
		t.Fatalf("RemoveFromStore: %v", err)
	}
	if len(res.Removed) != 1 || res.Removed[0] != "tool" {
		t.Fatalf("expected tool to be removed, got %v", res.Removed)
	}
	assertExists(t, toolDir, false)
	assertExists(t, keptDir, true)
	assertExists(t, onlyTool, false) // nothing declares it any more
	assertExists(t, shared, true)    // still declared by "other"
}

// TestRemoveFromStore_KeepsJarAnotherPackageDeclares: a JAR two packages declare
// survives the removal of one of them. The store installs each package's own
// declared JARs, so this is the common case, not an edge one.
func TestRemoveFromStore_KeepsJarAnotherPackageDeclares(t *testing.T) {
	home := t.TempDir()
	writeStorePkg(t, home, "tool",
		`{"name":"tool","version":"1.0.0","dependencies":{"java":[{"groupId":"org.x","artifactId":"shared","version":"2.0"}]}}`)
	writeStorePkg(t, home, "other",
		`{"name":"other","version":"1.0.0","dependencies":{"java":[{"groupId":"org.x","artifactId":"shared","version":"2.0"}]}}`)
	shared := writeStoreJar(t, home, "shared-2.0.jar")

	if _, err := New(home, "", "", "").RemoveFromStore([]string{"tool"}); err != nil {
		t.Fatalf("RemoveFromStore: %v", err)
	}
	assertExists(t, shared, true)
}

// TestRemoveFromStore_ReportsOrphansWithoutDeleting: a package the removed one
// pulled in is REPORTED, never deleted — the store cannot tell a dependency from
// a package the user installed in its own right, so deleting it would throw away
// an explicit install on a guess.
func TestRemoveFromStore_ReportsOrphansWithoutDeleting(t *testing.T) {
	home := t.TempDir()
	writeStorePkg(t, home, "tool",
		`{"name":"tool","version":"1.0.0","dependencies":{"fgl":{"helper":"^1.0.0"}}}`)
	helperDir := writeStorePkg(t, home, "helper", `{"name":"helper","version":"1.0.0"}`)

	res, err := New(home, "", "", "").RemoveFromStore([]string{"tool"})
	if err != nil {
		t.Fatalf("RemoveFromStore: %v", err)
	}
	if len(res.Orphaned) != 1 || res.Orphaned[0] != "helper" {
		t.Fatalf("helper should be reported as orphaned, got %v", res.Orphaned)
	}
	assertExists(t, helperDir, true) // reported, NOT deleted
}

// TestRemoveFromStore_OrphanOnlyWhenNothingElseReferencesIt: a dependency two
// packages share is not an orphan when only one of them leaves.
func TestRemoveFromStore_OrphanOnlyWhenNothingElseReferencesIt(t *testing.T) {
	home := t.TempDir()
	writeStorePkg(t, home, "tool", `{"name":"tool","version":"1.0.0","dependencies":{"fgl":{"helper":"^1.0.0"}}}`)
	writeStorePkg(t, home, "other", `{"name":"other","version":"1.0.0","dependencies":{"fgl":{"helper":"^1.0.0"}}}`)
	writeStorePkg(t, home, "helper", `{"name":"helper","version":"1.0.0"}`)

	res, err := New(home, "", "", "").RemoveFromStore([]string{"tool"})
	if err != nil {
		t.Fatalf("RemoveFromStore: %v", err)
	}
	if len(res.Orphaned) != 0 {
		t.Fatalf("helper is still required by other; it must not be reported orphaned, got %v", res.Orphaned)
	}
}

// TestRemoveFromStore_ReportsRemainingDependents: removing something another
// installed package still needs is allowed — the user asked — but must not be
// silent, since the store has no lock that would catch it later.
func TestRemoveFromStore_ReportsRemainingDependents(t *testing.T) {
	home := t.TempDir()
	writeStorePkg(t, home, "tool", `{"name":"tool","version":"1.0.0","dependencies":{"fgl":{"helper":"^1.0.0"}}}`)
	helperDir := writeStorePkg(t, home, "helper", `{"name":"helper","version":"1.0.0"}`)

	res, err := New(home, "", "", "").RemoveFromStore([]string{"helper"})
	if err != nil {
		t.Fatalf("RemoveFromStore: %v", err)
	}
	assertExists(t, helperDir, false) // the explicit request still wins
	deps := res.StillRequiredBy["helper"]
	if len(deps) != 1 || deps[0] != "tool" {
		t.Fatalf("expected tool to be reported as a remaining dependent, got %v", deps)
	}
}

// TestRemoveFromStore_NotFoundIsNotSuccess: a name that was never installed is
// reported as such and removes nothing (the GIS-564 no-false-success rule).
func TestRemoveFromStore_NotFoundIsNotSuccess(t *testing.T) {
	home := t.TempDir()
	keptDir := writeStorePkg(t, home, "tool", `{"name":"tool","version":"1.0.0"}`)

	res, err := New(home, "", "", "").RemoveFromStore([]string{"nosuchpkg"})
	if err != nil {
		t.Fatalf("RemoveFromStore: %v", err)
	}
	if len(res.Removed) != 0 {
		t.Fatalf("nothing should have been removed, got %v", res.Removed)
	}
	if len(res.NotFound) != 1 || res.NotFound[0] != "nosuchpkg" {
		t.Fatalf("the missing name should be reported, got %v", res.NotFound)
	}
	assertExists(t, keptDir, true)
}

// TestRemoveFromStore_MatchesCanonically: the store directory is the canonical
// slug, so `remove demo.pkg` must find `demo-pkg` (GIS-271).
func TestRemoveFromStore_MatchesCanonically(t *testing.T) {
	home := t.TempDir()
	pkgDir := writeStorePkg(t, home, "demo-pkg", `{"name":"demo.pkg","version":"1.0.0"}`)

	res, err := New(home, "", "", "").RemoveFromStore([]string{"demo.pkg"})
	if err != nil {
		t.Fatalf("RemoveFromStore: %v", err)
	}
	if len(res.Removed) != 1 || res.Removed[0] != "demo-pkg" {
		t.Fatalf("expected demo-pkg to be removed, got %v (not found: %v)", res.Removed, res.NotFound)
	}
	assertExists(t, pkgDir, false)
}

// TestRemoveFromStore_UnreadableManifestIsStillRemovable: a package whose
// manifest is missing or corrupt occupies a directory and must still be
// uninstallable by name — otherwise a half-extracted package is stuck forever.
func TestRemoveFromStore_UnreadableManifestIsStillRemovable(t *testing.T) {
	home := t.TempDir()
	pkgDir := filepath.Join(home, "packages", "broken")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "fglpkg.json"), []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}

	res, err := New(home, "", "", "").RemoveFromStore([]string{"broken"})
	if err != nil {
		t.Fatalf("RemoveFromStore: %v", err)
	}
	if len(res.Removed) != 1 {
		t.Fatalf("a package with an unreadable manifest must still be removable, got %v / notfound %v", res.Removed, res.NotFound)
	}
	assertExists(t, pkgDir, false)
}

// TestRemoveFromStore_EmptyStore: removing from a store that does not exist yet
// reports the name as not-found rather than failing.
func TestRemoveFromStore_EmptyStore(t *testing.T) {
	res, err := New(t.TempDir(), "", "", "").RemoveFromStore([]string{"tool"})
	if err != nil {
		t.Fatalf("RemoveFromStore on an empty store: %v", err)
	}
	if len(res.Removed) != 0 || len(res.NotFound) != 1 {
		t.Fatalf("expected a clean not-found, got removed=%v notfound=%v", res.Removed, res.NotFound)
	}
}

// TestRemoveFromStore_MultiplePackages: several names in one call, with the
// keep-set computed once over everything that survives.
func TestRemoveFromStore_MultiplePackages(t *testing.T) {
	home := t.TempDir()
	writeStorePkg(t, home, "a", `{"name":"a","version":"1.0.0","dependencies":{"java":[{"groupId":"g","artifactId":"ja","version":"1"}]}}`)
	writeStorePkg(t, home, "b", `{"name":"b","version":"1.0.0","dependencies":{"java":[{"groupId":"g","artifactId":"jb","version":"1"}]}}`)
	keptDir := writeStorePkg(t, home, "c", `{"name":"c","version":"1.0.0","dependencies":{"java":[{"groupId":"g","artifactId":"jc","version":"1"}]}}`)
	ja := writeStoreJar(t, home, "ja-1.jar")
	jb := writeStoreJar(t, home, "jb-1.jar")
	jc := writeStoreJar(t, home, "jc-1.jar")

	res, err := New(home, "", "", "").RemoveFromStore([]string{"a", "b"})
	if err != nil {
		t.Fatalf("RemoveFromStore: %v", err)
	}
	sort.Strings(res.Removed)
	if strings.Join(res.Removed, ",") != "a,b" {
		t.Fatalf("expected a and b removed, got %v", res.Removed)
	}
	assertExists(t, ja, false)
	assertExists(t, jb, false)
	assertExists(t, jc, true)
	assertExists(t, keptDir, true)
}

// TestRemoveFromStore_LeavesOtherStoresAlone: the removal must stay inside the
// store it was pointed at. A project's .fglpkg/ sharing the same parent
// directory is a different scope entirely (the reason ReconcileAfterRemove
// refuses to prune a global home at all).
func TestRemoveFromStore_LeavesOtherStoresAlone(t *testing.T) {
	base := t.TempDir()
	globalHome := filepath.Join(base, "global")
	projectHome := filepath.Join(base, "proj", ".fglpkg")

	writeStorePkg(t, globalHome, "tool", `{"name":"tool","version":"1.0.0"}`)
	projectCopy := writeStorePkg(t, projectHome, "tool", `{"name":"tool","version":"1.0.0"}`)
	projectJar := writeStoreJar(t, projectHome, "orphan-1.jar")

	if _, err := New(globalHome, "", "", "").RemoveFromStore([]string{"tool"}); err != nil {
		t.Fatalf("RemoveFromStore: %v", err)
	}
	// The project's own copy — and even a JAR nothing there declares — is
	// untouched: it is not this store's business.
	assertExists(t, projectCopy, true)
	assertExists(t, projectJar, true)
}

// TestRemoveFromStore_MatchesNonCanonicalStoreDir: the store directory is
// normally already the canonical slug, so canonicalizing the REQUESTED name is
// what makes `remove demo.pkg` work. Both sides are canonicalized, which also
// covers a directory left behind by an older fglpkg that stored a raw name —
// without this it could never be removed by its canonical name.
func TestRemoveFromStore_MatchesNonCanonicalStoreDir(t *testing.T) {
	home := t.TempDir()
	pkgDir := writeStorePkg(t, home, "Demo.Pkg", `{"name":"Demo.Pkg","version":"1.0.0"}`)

	res, err := New(home, "", "", "").RemoveFromStore([]string{"demo-pkg"})
	if err != nil {
		t.Fatalf("RemoveFromStore: %v", err)
	}
	if len(res.Removed) != 1 || res.Removed[0] != "Demo.Pkg" {
		t.Fatalf("a non-canonical store directory should still be removable by its canonical name, got removed=%v notfound=%v", res.Removed, res.NotFound)
	}
	assertExists(t, pkgDir, false)
}
