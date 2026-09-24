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

// writeStoreWebcomponent registers a webcomponent-bearing package in the store's
// owners sidecar and writes the files it owns. A PURE webcomponent package has
// no packages/<name> directory at all — its manifest is deliberately not
// extracted — so the sidecar is the only record that it is installed.
func writeStoreWebcomponent(t *testing.T, home, pkg string, files map[string]string) []string {
	t.Helper()
	wcDir := filepath.Join(home, "webcomponents")
	var paths []string
	for rel, body := range files {
		p := filepath.Join(wcDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
		}
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
		paths = append(paths, p)
	}
	o, err := loadWCOwners(wcDir)
	if err != nil {
		t.Fatalf("loadWCOwners: %v", err)
	}
	rels := make([]string, 0, len(files))
	for rel := range files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	o.Packages[pkg] = rels
	if err := saveWCOwners(wcDir, o); err != nil {
		t.Fatalf("saveWCOwners: %v", err)
	}
	return paths
}

// TestRemoveFromStore_KeepsUnrelatedWebcomponents: a webcomponent package has no
// packages/<name> directory, so a keep-set built from packages/ contains none of
// them — and every globally installed web component was pruned on ANY removal
// (PR #88 review, W1).
func TestRemoveFromStore_KeepsUnrelatedWebcomponents(t *testing.T) {
	home := t.TempDir()
	writeStorePkg(t, home, "demo-pkg", `{"name":"demo.pkg","version":"1.0.0"}`)
	wcFiles := writeStoreWebcomponent(t, home, "chart-widget", map[string]string{
		"ChartWidget/ChartWidget.html": "<html>",
		"ChartWidget/chart.js":         "//js",
	})

	res, err := New(home, "", "", "").RemoveFromStore([]string{"demo.pkg"})
	if err != nil {
		t.Fatalf("RemoveFromStore: %v", err)
	}
	for _, p := range res.Pruned {
		if strings.Contains(p, "webcomponent") {
			t.Fatalf("an unrelated web component must not be pruned, got %v", res.Pruned)
		}
	}
	for _, f := range wcFiles {
		assertExists(t, f, true)
	}
}

// TestRemoveFromStore_RemovesWebcomponentByName: a pure webcomponent package is
// installed — its files and its owners entry are there — so it must be
// removable by name. Scanning only packages/ made it invisible (W2).
func TestRemoveFromStore_RemovesWebcomponentByName(t *testing.T) {
	home := t.TempDir()
	keptPkg := writeStorePkg(t, home, "demo-pkg", `{"name":"demo.pkg","version":"1.0.0"}`)
	wcFiles := writeStoreWebcomponent(t, home, "chart-widget", map[string]string{
		"ChartWidget/ChartWidget.html": "<html>",
	})

	res, err := New(home, "", "", "").RemoveFromStore([]string{"chart-widget"})
	if err != nil {
		t.Fatalf("RemoveFromStore: %v", err)
	}
	if len(res.NotFound) != 0 {
		t.Fatalf("an installed web component must not be reported missing, got %v", res.NotFound)
	}
	if len(res.Removed) != 1 || res.Removed[0] != "chart-widget" {
		t.Fatalf("expected chart-widget to be removed, got %v", res.Removed)
	}
	for _, f := range wcFiles {
		assertExists(t, f, false)
	}
	assertExists(t, keptPkg, true) // the unrelated BDL package is untouched
}

// TestRemoveFromStore_RemovesMixedPackageBundles: a package with BOTH a BDL
// directory and webcomponent bundles loses both.
func TestRemoveFromStore_RemovesMixedPackageBundles(t *testing.T) {
	home := t.TempDir()
	pkgDir := writeStorePkg(t, home, "mixed-pkg", `{"name":"mixed.pkg","version":"1.0.0"}`)
	wcFiles := writeStoreWebcomponent(t, home, "mixed-pkg", map[string]string{
		"MixedWidget/MixedWidget.html": "<html>",
	})

	if _, err := New(home, "", "", "").RemoveFromStore([]string{"mixed.pkg"}); err != nil {
		t.Fatalf("RemoveFromStore: %v", err)
	}
	assertExists(t, pkgDir, false)
	for _, f := range wcFiles {
		assertExists(t, f, false)
	}
}

// TestRemoveFromStore_KeepsJarNoInstalledPackageDeclares: the global store also
// holds JARs installed FOR A PROJECT (`install --global` from inside one records
// them in the project's manifest, which the store never sees). Sweeping
// everything "unreferenced" deleted those and broke the project's classpath
// (PR #88 review, J1) — the acceptance criterion GIS-567 words as "does not
// touch any project".
func TestRemoveFromStore_KeepsJarNoInstalledPackageDeclares(t *testing.T) {
	home := t.TempDir()
	writeStorePkg(t, home, "demo-pkg",
		`{"name":"demo.pkg","version":"1.0.0","dependencies":{"java":[{"groupId":"org.x","artifactId":"only-demo","version":"1.0"}]}}`)
	projectJar := writeStoreJar(t, home, "gson-2.10.1.jar") // a project's, not any installed package's
	ownJar := writeStoreJar(t, home, "only-demo-1.0.jar")

	res, err := New(home, "", "", "").RemoveFromStore([]string{"demo.pkg"})
	if err != nil {
		t.Fatalf("RemoveFromStore: %v", err)
	}
	assertExists(t, ownJar, false)    // the removed package's own JAR goes
	assertExists(t, projectJar, true) // somebody else's stays
	for _, p := range res.Pruned {
		if strings.Contains(p, "gson") {
			t.Fatalf("a JAR no installed package declares must not be pruned, got %v", res.Pruned)
		}
	}
}

// TestRemoveFromStore_KeepsJarWhenAKeptManifestIsUnreadable: a remaining package
// whose manifest cannot be read has unknown JAR requirements, so a candidate
// that looks unreferenced only because of that gap is kept and reported. A
// leftover JAR costs disk; a deleted one breaks a package that is still there.
func TestRemoveFromStore_KeepsJarWhenAKeptManifestIsUnreadable(t *testing.T) {
	home := t.TempDir()
	writeStorePkg(t, home, "demo-pkg",
		`{"name":"demo.pkg","version":"1.0.0","dependencies":{"java":[{"groupId":"org.x","artifactId":"shared","version":"2.0"}]}}`)
	writeStorePkg(t, home, "broken", `{not json`) // still installed; requirements unknown
	shared := writeStoreJar(t, home, "shared-2.0.jar")

	res, err := New(home, "", "", "").RemoveFromStore([]string{"demo.pkg"})
	if err != nil {
		t.Fatalf("RemoveFromStore: %v", err)
	}
	assertExists(t, shared, true)
	if len(res.KeptJars) != 1 || res.KeptJars[0] != "shared-2.0.jar" {
		t.Fatalf("the withheld JAR should be reported, got %v", res.KeptJars)
	}
	if len(res.Pruned) != 0 {
		t.Fatalf("nothing should have been pruned, got %v", res.Pruned)
	}
}

// TestRemoveFromStore_NonCanonicalDirGraphLinesUp: dependency names in a
// manifest are canonicalized, so a legacy store directory that is not itself
// canonical must still line up in the graph — otherwise its dependents and
// orphans silently go unreported (PR #88 review nit).
func TestRemoveFromStore_NonCanonicalDirGraphLinesUp(t *testing.T) {
	home := t.TempDir()
	// "Demo_Pkg" canonicalizes to "demo-pkg", which is how tool declares it.
	writeStorePkg(t, home, "Demo_Pkg", `{"name":"Demo_Pkg","version":"1.0.0"}`)
	writeStorePkg(t, home, "tool", `{"name":"tool","version":"1.0.0","dependencies":{"fgl":{"demo.pkg":"^1.0.0"}}}`)

	// Removing the non-canonical package must warn that tool still needs it.
	res, err := New(home, "", "", "").RemoveFromStore([]string{"demo-pkg"})
	if err != nil {
		t.Fatalf("RemoveFromStore: %v", err)
	}
	if deps := res.StillRequiredBy["Demo_Pkg"]; len(deps) != 1 || deps[0] != "tool" {
		t.Fatalf("expected tool reported as a remaining dependent of Demo_Pkg, got %v", res.StillRequiredBy)
	}

	// And the reverse: removing tool must report the non-canonical package as an
	// orphan it pulled in.
	home2 := t.TempDir()
	writeStorePkg(t, home2, "Demo_Pkg", `{"name":"Demo_Pkg","version":"1.0.0"}`)
	writeStorePkg(t, home2, "tool", `{"name":"tool","version":"1.0.0","dependencies":{"fgl":{"demo.pkg":"^1.0.0"}}}`)
	res2, err := New(home2, "", "", "").RemoveFromStore([]string{"tool"})
	if err != nil {
		t.Fatalf("RemoveFromStore: %v", err)
	}
	if len(res2.Orphaned) != 1 || res2.Orphaned[0] != "Demo_Pkg" {
		t.Fatalf("expected Demo_Pkg reported as orphaned, got %v", res2.Orphaned)
	}
}

// TestRemoveFromStore_KeepsWebcomponentWithNonCanonicalOwner: the webcomponent
// keep-set must be keyed by the owners sidecar's OWN package names, not by the
// canonical key the scan uses internally. With a non-canonical owner the two
// differ, and a canonical keep-set silently prunes a web component that should
// survive.
func TestRemoveFromStore_KeepsWebcomponentWithNonCanonicalOwner(t *testing.T) {
	home := t.TempDir()
	writeStorePkg(t, home, "demo-pkg", `{"name":"demo.pkg","version":"1.0.0"}`)
	wcFiles := writeStoreWebcomponent(t, home, "Chart_Widget", map[string]string{
		"ChartWidget/ChartWidget.html": "<html>",
	})

	res, err := New(home, "", "", "").RemoveFromStore([]string{"demo.pkg"})
	if err != nil {
		t.Fatalf("RemoveFromStore: %v", err)
	}
	for _, p := range res.Pruned {
		if strings.Contains(p, "webcomponent") {
			t.Fatalf("an unrelated web component must survive whatever its owner name looks like, got %v", res.Pruned)
		}
	}
	for _, f := range wcFiles {
		assertExists(t, f, true)
	}
}
