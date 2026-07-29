package installer

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

func mkPkgDir(t *testing.T, packagesDir, name string) {
	t.Helper()
	dir := filepath.Join(packagesDir, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file.42m"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
}

func mkJar(t *testing.T, jarsDir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(jarsDir, name), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestPruneToPlanRemovesOrphansKeepsWanted(t *testing.T) {
	home := t.TempDir()
	inst := New(home, "", "", "")
	if err := inst.ensureDirs(); err != nil {
		t.Fatal(err)
	}

	// On disk: a package we keep + one we removed; likewise for JARs.
	mkPkgDir(t, inst.packagesDir, "keeper")
	mkPkgDir(t, inst.packagesDir, "poiapi")
	mkJar(t, inst.jarsDir, "keeper-1.0.0.jar")
	mkJar(t, inst.jarsDir, "poi-5.3.0.jar")

	// The re-resolved plan only knows about "keeper" and its JAR.
	plan := &resolver.Plan{
		Packages: []resolver.ResolvedPackage{{Name: "keeper"}},
		JARs: []manifest.JavaDependency{
			{GroupID: "g", ArtifactID: "keeper", Version: "1.0.0"},
		},
	}

	pruned, err := inst.pruneToPlan(plan)
	if err != nil {
		t.Fatalf("pruneToPlan: %v", err)
	}

	// Orphans gone.
	if _, err := os.Stat(filepath.Join(inst.packagesDir, "poiapi")); !os.IsNotExist(err) {
		t.Error("removed package poiapi should have been pruned")
	}
	if _, err := os.Stat(filepath.Join(inst.jarsDir, "poi-5.3.0.jar")); !os.IsNotExist(err) {
		t.Error("orphaned JAR poi-5.3.0.jar should have been pruned")
	}
	// Wanted retained.
	if _, err := os.Stat(filepath.Join(inst.packagesDir, "keeper")); err != nil {
		t.Errorf("keeper package must be retained: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inst.jarsDir, "keeper-1.0.0.jar")); err != nil {
		t.Errorf("keeper JAR must be retained: %v", err)
	}

	sort.Strings(pruned)
	want := []string{"jar poi-5.3.0.jar", "package poiapi"}
	if len(pruned) != len(want) || pruned[0] != want[0] || pruned[1] != want[1] {
		t.Errorf("pruned = %v, want %v", pruned, want)
	}
}

func TestPruneToPlanNoopWhenEverythingWanted(t *testing.T) {
	home := t.TempDir()
	inst := New(home, "", "", "")
	if err := inst.ensureDirs(); err != nil {
		t.Fatal(err)
	}
	mkPkgDir(t, inst.packagesDir, "keeper")
	mkJar(t, inst.jarsDir, "keeper-1.0.0.jar")

	plan := &resolver.Plan{
		Packages: []resolver.ResolvedPackage{{Name: "keeper"}},
		JARs:     []manifest.JavaDependency{{GroupID: "g", ArtifactID: "keeper", Version: "1.0.0"}},
	}
	pruned, err := inst.pruneToPlan(plan)
	if err != nil {
		t.Fatalf("pruneToPlan: %v", err)
	}
	if len(pruned) != 0 {
		t.Errorf("nothing should be pruned, got %v", pruned)
	}
}

// A webcomponent entry in the plan must not mark a same-named dir under
// packagesDir as wanted — webcomponents live elsewhere — but in practice
// packagesDir holds only BDL/mixed packages, so a stray dir gets pruned.
func TestPruneToPlanIgnoresWebcomponentPlanEntries(t *testing.T) {
	home := t.TempDir()
	inst := New(home, "", "", "")
	if err := inst.ensureDirs(); err != nil {
		t.Fatal(err)
	}
	mkPkgDir(t, inst.packagesDir, "bdlpkg")

	plan := &resolver.Plan{
		Packages: []resolver.ResolvedPackage{
			{Name: "bdlpkg"},
			{Name: "wcpkg", Variant: "webcomponent"},
		},
	}
	if _, err := inst.pruneToPlan(plan); err != nil {
		t.Fatalf("pruneToPlan: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inst.packagesDir, "bdlpkg")); err != nil {
		t.Errorf("BDL package must be retained: %v", err)
	}
}

// TestPruneToPlanKeepsMixedPackageWebcomponents is the #41 regression: a mixed
// BDL+webcomponent package resolves to a genero variant, so IsWebcomponent() is
// false for it even though installBDL records its routed webcomponent files in
// the ownership sidecar. Removing an unrelated package must not take the mixed
// package's still-owned bundle down with it. Drives the real pruneToPlan →
// pruneWebcomponents path against a populated sidecar, which the
// TestPruneWebcomponents* tests bypass by passing wantWC in directly.
func TestPruneToPlanKeepsMixedPackageWebcomponents(t *testing.T) {
	home := t.TempDir()
	inst := New(home, "", "", "")
	if err := inst.ensureDirs(); err != nil {
		t.Fatal(err)
	}

	// The mixed package, installed the way installBDL does it.
	mixedZip := filepath.Join(home, "chart-3d.zip")
	writeTestZip(t, mixedZip, map[string]string{
		"fglpkg.json":          `{"name":"chart-3d","version":"1.0.0","webcomponents":["3DChart"]}`,
		"ChartDemo.42m":        "BDL\n",
		"3DChart/3DChart.html": "<html/>",
	})
	wcInstalled, err := extractZipRouted(mixedZip, filepath.Join(inst.packagesDir, "chart-3d"), inst.webcomponentsDir, []string{"3DChart"})
	if err != nil {
		t.Fatalf("extractZipRouted: %v", err)
	}
	if err := recordWCOwnership(inst.webcomponentsDir, "chart-3d", wcInstalled); err != nil {
		t.Fatalf("recordWCOwnership: %v", err)
	}

	// The unrelated packages being removed: a pure webcomponent and a pure BDL.
	installWC(t, home, inst.webcomponentsDir, "fjs-map", "Map", nil)
	mkPkgDir(t, inst.packagesDir, "bar")

	// The re-resolved plan keeps only the mixed package.
	plan := &resolver.Plan{
		Packages: []resolver.ResolvedPackage{{Name: "chart-3d", Variant: "genero6"}},
	}
	pruned, err := inst.pruneToPlan(plan)
	if err != nil {
		t.Fatalf("pruneToPlan: %v", err)
	}

	// The mixed package survives whole — BDL side and webcomponent side.
	if _, err := os.Stat(filepath.Join(inst.packagesDir, "chart-3d", "ChartDemo.42m")); err != nil {
		t.Errorf("mixed package's BDL files must be retained: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inst.webcomponentsDir, "3DChart", "3DChart.html")); err != nil {
		t.Errorf("still-installed mixed package's webcomponent bundle was deleted: %v", err)
	}
	// The removed packages are gone.
	if _, err := os.Stat(filepath.Join(inst.webcomponentsDir, "Map", "Map.html")); !os.IsNotExist(err) {
		t.Errorf("removed webcomponent package's bundle should be pruned, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(inst.packagesDir, "bar")); !os.IsNotExist(err) {
		t.Error("removed BDL package bar should be pruned")
	}

	for _, p := range pruned {
		if p == "webcomponent chart-3d" {
			t.Errorf("pruned = %v, must not report the still-installed mixed package", pruned)
		}
	}

	o, err := loadWCOwners(inst.webcomponentsDir)
	if err != nil {
		t.Fatalf("loadWCOwners: %v", err)
	}
	if _, ok := o.Packages["chart-3d"]; !ok {
		t.Error("sidecar dropped the still-installed mixed package chart-3d")
	}
	if _, ok := o.Packages["fjs-map"]; ok {
		t.Error("sidecar still lists removed package fjs-map")
	}
}

func writeStubLock(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, lockfile.Filename), []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

// Removing the last dependency empties the graph, so reconcileLock must delete
// fglpkg.lock rather than leave an empty one behind (GIS-273).
func TestReconcileLockDeletesLockWhenGraphEmpty(t *testing.T) {
	dir := t.TempDir()
	writeStubLock(t, dir)

	m := &manifest.Manifest{Name: "proj", Version: "0.1.0"}
	note, err := reconcileLock(&resolver.Plan{}, m, dir)
	if err != nil {
		t.Fatalf("reconcileLock: %v", err)
	}
	if lockfile.Exists(dir) {
		t.Error("an empty graph must delete fglpkg.lock, but it still exists")
	}
	if note == "" {
		t.Error("expected a deletion note for the caller's summary")
	}
}

// A still-populated graph must rewrite (keep) the lock, never delete it, and
// the rewrite must reflect the surviving package.
func TestReconcileLockKeepsLockWhenGraphNonEmpty(t *testing.T) {
	dir := t.TempDir()
	writeStubLock(t, dir)

	m := &manifest.Manifest{Name: "proj", Version: "0.1.0"}
	plan := &resolver.Plan{
		GeneroVersion: genero.MustParse("6.00.01"),
		Packages: []resolver.ResolvedPackage{
			{Name: "keeper", Version: semver.MustParse("1.0.0"), Scope: manifest.ScopeProd},
		},
	}
	note, err := reconcileLock(plan, m, dir)
	if err != nil {
		t.Fatalf("reconcileLock: %v", err)
	}
	if note != "" {
		t.Errorf("no deletion note expected when the lock is kept, got %q", note)
	}
	if !lockfile.Exists(dir) {
		t.Fatal("a non-empty graph must keep fglpkg.lock")
	}
	lf, err := lockfile.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(lf.Packages) != 1 || lf.Packages[0].Name != "keeper" {
		t.Errorf("rewritten lock = %+v, want a single package %q", lf.Packages, "keeper")
	}
}

// reconcileLock must not conjure a lock for a project that never had one, even
// when the graph is empty.
func TestReconcileLockNoopWhenNoLock(t *testing.T) {
	dir := t.TempDir()
	m := &manifest.Manifest{Name: "proj", Version: "0.1.0"}
	note, err := reconcileLock(&resolver.Plan{}, m, dir)
	if err != nil {
		t.Fatalf("reconcileLock: %v", err)
	}
	if lockfile.Exists(dir) {
		t.Error("reconcileLock must not create a lock when none existed")
	}
	if note != "" {
		t.Errorf("no note expected, got %q", note)
	}
}
