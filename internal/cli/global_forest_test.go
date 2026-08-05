package cli

// Unit tests for the global dependency forest (GIS — `fglpkg list --global`).
// buildGlobalForest is pure: every input is a value, so the whole forest shape
// is asserted here without touching a filesystem or a lock file. The rendering
// is shared with the single-root tree (writeTree), so these tests exercise the
// forest's own logic — roots, parentage, per-declarer JAR versions, dedup, and
// cycle termination — rather than re-testing the renderer.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

func gjar(group, artifact, version string) manifest.JavaDependency {
	return manifest.JavaDependency{GroupID: group, ArtifactID: artifact, Version: version}
}

// fdeps builds production-scope FGL dependency edges (the common case in these
// tests). Optional edges are written out as fglDep literals where they matter.
func fdeps(names ...string) []fglDep {
	out := make([]fglDep, len(names))
	for i, n := range names {
		out[i] = fglDep{name: n}
	}
	return out
}

// gjars wraps JavaDependency values as production-scope JAR edges.
func gjars(js ...manifest.JavaDependency) []jarDep {
	out := make([]jarDep, len(js))
	for i, j := range js {
		out[i] = jarDep{dep: j}
	}
	return out
}

// renderForest is the whole forest pipeline under test: bundled-manifest
// metadata -> text, through the same writeTree the local tree uses.
func renderForest(pkgs []globalPkg, maxDepth int) string {
	var buf bytes.Buffer
	writeTree(&buf, buildGlobalForest(pkgs, maxDepth), "Global packages — /store")
	return buf.String()
}

// Two packages that depend on nothing installed are both forest roots, each
// carrying its own declared JARs.
func TestBuildGlobalForestIndependentRoots(t *testing.T) {
	pkgs := []globalPkg{
		{name: "beta", version: "2.0.0", jars: gjars(gjar("g", "b", "1"))},
		{name: "alpha", version: "1.0.0", jars: gjars(gjar("g", "a", "1"))},
	}
	got := renderForest(pkgs, 0)
	for _, want := range []string{"alpha@1.0.0", "beta@2.0.0", "g:a  1", "g:b  1", "2 packages, 2 JARs."} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// Roots are alphabetical regardless of input order.
	if strings.Index(got, "alpha@1.0.0") > strings.Index(got, "beta@2.0.0") {
		t.Errorf("roots should be alphabetical, got:\n%s", got)
	}
}

// A package named as an FGL dep of another installed package nests under it and
// is not itself a root.
func TestBuildGlobalForestNestsInstalledDep(t *testing.T) {
	pkgs := []globalPkg{
		{name: "app", version: "1.0.0", fglDeps: fdeps("lib")},
		{name: "lib", version: "2.0.0", jars: gjars(gjar("g", "x", "1"))},
	}
	got := renderForest(pkgs, 0)
	// app is the sole root; lib hangs beneath it (indented), not at column 0.
	if !strings.Contains(got, "└─ app@1.0.0") {
		t.Errorf("app should be the sole top-level root, got:\n%s", got)
	}
	if !strings.Contains(got, "   └─ lib@2.0.0") {
		t.Errorf("lib should be nested under app, got:\n%s", got)
	}
	if strings.Count(got, "lib@2.0.0") != 1 {
		t.Errorf("lib should appear exactly once, got:\n%s", got)
	}
	if !strings.Contains(got, "2 packages, 1 JAR.") {
		t.Errorf("counts wrong, got:\n%s", got)
	}
}

// The global store installs each package's own declared JARs, so the same
// coordinate at two versions under two packages must show both — no dedup.
func TestBuildGlobalForestSameCoordDistinctVersions(t *testing.T) {
	pkgs := []globalPkg{
		{name: "alpha", version: "1", jars: gjars(gjar("org", "log4j-api", "2.17.1"))},
		{name: "beta", version: "1", jars: gjars(gjar("org", "log4j-api", "2.26.1"))},
	}
	got := renderForest(pkgs, 0)
	if strings.Contains(got, "(*)") {
		t.Errorf("different versions are not repeats, got:\n%s", got)
	}
	for _, want := range []string{"org:log4j-api  2.17.1", "org:log4j-api  2.26.1", "2 packages, 2 JARs."} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// The same coordinate AND version declared by two packages is one JAR: the
// second occurrence collapses to a (*) leaf and is not double-counted.
func TestBuildGlobalForestSameCoordSameVersionCollapses(t *testing.T) {
	pkgs := []globalPkg{
		{name: "alpha", version: "1", jars: gjars(gjar("org", "x", "1.0"))},
		{name: "beta", version: "1", jars: gjars(gjar("org", "x", "1.0"))},
	}
	got := renderForest(pkgs, 0)
	if strings.Count(got, "org:x") != 2 {
		t.Errorf("JAR should appear under both packages, got:\n%s", got)
	}
	if !strings.Contains(got, "org:x  1.0 (*)") {
		t.Errorf("repeat occurrence should carry the (*) marker, got:\n%s", got)
	}
	if !strings.Contains(got, "2 packages, 1 JAR.") {
		t.Errorf("repeat must not be double-counted, got:\n%s", got)
	}
	if !strings.Contains(got, repeatLegend) {
		t.Errorf("legend should print when something collapsed, got:\n%s", got)
	}
}

// A hand-broken store with a package cycle must terminate, not recurse forever.
// With every package required by another there is no root, so the fallback makes
// each a root and the (*) dedup closes the cycle.
func TestBuildGlobalForestCycleTerminates(t *testing.T) {
	pkgs := []globalPkg{
		{name: "a", version: "1", fglDeps: fdeps("b")},
		{name: "b", version: "1", fglDeps: fdeps("a")},
	}
	got := renderForest(pkgs, 0) // completing at all is half the assertion
	if !strings.Contains(got, "(*)") {
		t.Errorf("cycle should be broken with a (*) leaf, got:\n%s", got)
	}
	if !strings.Contains(got, "2 packages, 0 JARs.") {
		t.Errorf("each package should be counted exactly once, got:\n%s", got)
	}
}

// A dependency on a package that is not installed is ignored: the forest shows
// what is on disk, not what a manifest merely declares.
func TestBuildGlobalForestUninstalledDepIgnored(t *testing.T) {
	pkgs := []globalPkg{
		{name: "app", version: "1", fglDeps: fdeps("missing")},
	}
	got := renderForest(pkgs, 0)
	if strings.Contains(got, "missing") {
		t.Errorf("an uninstalled dep must not appear, got:\n%s", got)
	}
	if !strings.Contains(got, "1 package, 0 JARs.") {
		t.Errorf("app should be the only node, got:\n%s", got)
	}
}

// FGL-package children are listed before JAR leaves at every level, matching the
// single-root tree's ordering — even when a JAR sorts alphabetically first.
func TestBuildGlobalForestPackagesBeforeJars(t *testing.T) {
	pkgs := []globalPkg{
		{name: "app", version: "1", fglDeps: fdeps("zlib"), jars: gjars(gjar("aaa", "aaa", "1"))},
		{name: "zlib", version: "1"},
	}
	got := renderForest(pkgs, 0)
	// Presence first, so a dropped child cannot pass the ordering check vacuously
	// (strings.Index returns -1 for a missing substring).
	if !strings.Contains(got, "zlib@1") || !strings.Contains(got, "aaa:aaa") {
		t.Fatalf("both the package child and the JAR leaf must appear, got:\n%s", got)
	}
	if strings.Index(got, "zlib@1") > strings.Index(got, "aaa:aaa") {
		t.Errorf("FGL package child should precede the JAR leaf, got:\n%s", got)
	}
}

// A dependency cycle that coexists with an independent root must not swallow the
// cyclic packages: they are reachable from no root, so buildGlobalForest promotes
// each still-unvisited package to a root of its own rather than dropping it.
func TestBuildGlobalForestIsolatedCycleStillShown(t *testing.T) {
	pkgs := []globalPkg{
		{name: "a", version: "1", fglDeps: fdeps("b")},
		{name: "b", version: "1", fglDeps: fdeps("a")},
		{name: "c", version: "1"},
	}
	got := renderForest(pkgs, 0)
	for _, want := range []string{"a@1", "b@1", "c@1"} {
		if !strings.Contains(got, want) {
			t.Errorf("installed package %q must appear even inside an isolated cycle, got:\n%s", want, got)
		}
	}
	// All three installed packages are counted, none silently dropped.
	if !strings.Contains(got, "3 packages, 0 JARs.") {
		t.Errorf("every installed package should be counted exactly once, got:\n%s", got)
	}
}

// maxDepth caps the printed depth: the top-level roots are depth 1, so --depth 1
// shows only them and their transitive nodes are truncated.
func TestBuildGlobalForestMaxDepth(t *testing.T) {
	pkgs := []globalPkg{
		{name: "app", version: "1", fglDeps: fdeps("lib")},
		{name: "lib", version: "1", jars: gjars(gjar("g", "x", "1"))},
	}
	full := renderForest(pkgs, 0)
	if !strings.Contains(full, "lib@1") || !strings.Contains(full, "g:x  1") {
		t.Errorf("unlimited depth should show the whole forest, got:\n%s", full)
	}
	shallow := renderForest(pkgs, 1)
	if strings.Contains(shallow, "lib@1") {
		t.Errorf("--depth 1 should truncate below the roots, got:\n%s", shallow)
	}
	if !strings.Contains(shallow, "1 package, 0 JARs.") {
		t.Errorf("only the root should be counted at depth 1, got:\n%s", shallow)
	}
}

// installedFGLDeps unions the prod and optional FGL scopes and de-duplicates,
// tagging each with its scope; production wins when a name is in both buckets,
// and dev deps are excluded.
func TestInstalledFGLDeps(t *testing.T) {
	m := &manifest.Manifest{}
	m.Dependencies.FGL = map[string]string{"b": "1", "a": "1"}
	m.OptionalDependencies.FGL = map[string]string{"b": "2", "c": "1"}
	m.DevDependencies.FGL = map[string]string{"devonly": "1"}

	got := installedFGLDeps(m)
	// a,b are production (untagged); c is optional; b appears in both → production
	// wins; devonly is excluded.
	want := []fglDep{{name: "a"}, {name: "b"}, {name: "c", scope: "optional"}}
	if len(got) != len(want) {
		t.Fatalf("installedFGLDeps = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("installedFGLDeps = %+v, want %+v", got, want)
		}
	}
}

// The forest tags an optionally-declared dependency (package or JAR) with
// (optional), just like the local tree; a production dependency stays untagged,
// and a root — which has no incoming edge — never carries a scope.
func TestBuildGlobalForestOptionalScopeTag(t *testing.T) {
	pkgs := []globalPkg{
		{name: "app", version: "1.0.0",
			fglDeps: []fglDep{{name: "plugin", scope: "optional"}, {name: "core"}},
			jars:    []jarDep{{dep: gjar("g", "x", "1"), scope: "optional"}}},
		{name: "plugin", version: "2.0.0"},
		{name: "core", version: "1.5.0"},
	}
	got := renderForest(pkgs, 0)
	if !strings.Contains(got, "plugin@2.0.0 (optional)") {
		t.Errorf("an optional FGL dependency should be tagged (optional), got:\n%s", got)
	}
	if !strings.Contains(got, "g:x  1 (optional)") {
		t.Errorf("an optional JAR dependency should be tagged (optional), got:\n%s", got)
	}
	if !strings.Contains(got, "core@1.5.0") || strings.Contains(got, "core@1.5.0 (optional)") {
		t.Errorf("a production FGL dependency must stay untagged, got:\n%s", got)
	}
	if strings.Contains(got, "app@1.0.0 (") {
		t.Errorf("a root package has no incoming edge and must not carry a scope, got:\n%s", got)
	}
}
