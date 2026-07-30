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

// TestPruneKeepsMixedPackageWebcomponentBundle pins the wantWC contract: the
// ownership sidecar is keyed by package name for ANY webcomponent-bearing
// package, including a *mixed* one (BDL modules under packages/<name>/ plus a
// COMPONENTTYPE bundle routed into webcomponents/). A mixed package's plan
// entry is not IsWebcomponent(), so building wantWC from webcomponent entries
// alone would delete the bundle of a package that is still required.
func TestPruneKeepsMixedPackageWebcomponentBundle(t *testing.T) {
	home := t.TempDir()
	inst := New(home, "", "", "")
	if err := inst.ensureDirs(); err != nil {
		t.Fatal(err)
	}
	// "chart3d" is mixed: a BDL package dir AND an owned webcomponent bundle.
	mkPkgDir(t, inst.packagesDir, "chart3d")
	installWC(t, home, inst.webcomponentsDir, "chart3d", "Chart3D", nil)
	bundle := filepath.Join(inst.webcomponentsDir, "Chart3D", "Chart3D.html")
	if _, err := os.Stat(bundle); err != nil {
		t.Fatalf("setup: bundle not installed: %v", err)
	}

	// A plan that still requires chart3d — as an ordinary (non-webcomponent)
	// package, which is how a mixed package appears.
	plan := &resolver.Plan{Packages: []resolver.ResolvedPackage{{Name: "chart3d"}}}
	pruned, err := inst.pruneToPlan(plan)
	if err != nil {
		t.Fatalf("pruneToPlan: %v", err)
	}
	if _, err := os.Stat(bundle); err != nil {
		t.Errorf("a still-required mixed package's webcomponent bundle was pruned: %v", err)
	}
	if len(pruned) != 0 {
		t.Errorf("pruned = %v, want nothing", pruned)
	}

	// And the same via the lock-driven path: a mixed package is recorded in the
	// lock's `packages` array, not `webcomponents`.
	lf := &lockfile.LockFile{Packages: []lockfile.LockedPackage{{Name: "chart3d", Version: "1.0.0"}}}
	if pruned, err = inst.pruneToLock(lf); err != nil {
		t.Fatalf("pruneToLock: %v", err)
	}
	if _, err := os.Stat(bundle); err != nil {
		t.Errorf("pruneToLock deleted a locked mixed package's bundle: %v", err)
	}
	if len(pruned) != 0 {
		t.Errorf("pruned = %v, want nothing", pruned)
	}
}

// Dropping the mixed package from the graph must still prune its bundle — the
// keep-set widening above must not turn into "never prune webcomponents".
func TestPruneRemovesMixedPackageBundleWhenDropped(t *testing.T) {
	home := t.TempDir()
	inst := New(home, "", "", "")
	if err := inst.ensureDirs(); err != nil {
		t.Fatal(err)
	}
	mkPkgDir(t, inst.packagesDir, "chart3d")
	installWC(t, home, inst.webcomponentsDir, "chart3d", "Chart3D", nil)
	bundle := filepath.Join(inst.webcomponentsDir, "Chart3D", "Chart3D.html")

	pruned, err := inst.pruneToPlan(&resolver.Plan{})
	if err != nil {
		t.Fatalf("pruneToPlan: %v", err)
	}
	if _, err := os.Stat(bundle); !os.IsNotExist(err) {
		t.Error("a dropped mixed package's webcomponent bundle should have been pruned")
	}
	sort.Strings(pruned)
	want := []string{"package chart3d", "webcomponent chart3d"}
	if len(pruned) != len(want) || pruned[0] != want[0] || pruned[1] != want[1] {
		t.Errorf("pruned = %v, want %v", pruned, want)
	}
}

// ─── pruneToLock ─────────────────────────────────────────────────────────────
//
// The install paths that never build a resolver plan (lock valid, nothing or
// only some entries missing) converge against the lock's own contents instead.

func TestPruneToLockRemovesPackagesAbsentFromLock(t *testing.T) {
	home := t.TempDir()
	inst := New(home, "", "", "")
	if err := inst.ensureDirs(); err != nil {
		t.Fatal(err)
	}
	mkPkgDir(t, inst.packagesDir, "keeper")
	mkPkgDir(t, inst.packagesDir, "orphan")

	lf := &lockfile.LockFile{Packages: []lockfile.LockedPackage{{Name: "keeper", Version: "1.0.0"}}}

	pruned, err := inst.pruneToLock(lf)
	if err != nil {
		t.Fatalf("pruneToLock: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inst.packagesDir, "orphan")); !os.IsNotExist(err) {
		t.Error("a package the lock does not name should have been pruned")
	}
	if _, err := os.Stat(filepath.Join(inst.packagesDir, "keeper")); err != nil {
		t.Errorf("a locked package must be retained: %v", err)
	}
	if len(pruned) != 1 || pruned[0] != "package orphan" {
		t.Errorf("pruned = %v, want [package orphan]", pruned)
	}
}

// TestPruneToLockLeavesJARsAlone is the deliberate asymmetry with pruneToPlan: a
// LockedJAR records coordinates but not the manifest's optional `jar` filename
// override, so its on-disk name can only be guessed — and a wrong guess would
// delete a JAR that is genuinely required. Orphaned JARs wait for a re-resolve,
// which is the only moment one can actually become orphaned.
func TestPruneToLockLeavesJARsAlone(t *testing.T) {
	home := t.TempDir()
	inst := New(home, "", "", "")
	if err := inst.ensureDirs(); err != nil {
		t.Fatal(err)
	}
	mkJar(t, inst.jarsDir, "custom-name.jar") // a `jar` override the lock cannot reproduce

	lf := &lockfile.LockFile{JARs: []lockfile.LockedJAR{
		{Key: "g:a", GroupID: "g", ArtifactID: "a", Version: "1.0.0"},
	}}
	pruned, err := inst.pruneToLock(lf)
	if err != nil {
		t.Fatalf("pruneToLock: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inst.jarsDir, "custom-name.jar")); err != nil {
		t.Errorf("pruneToLock must not delete JARs: %v", err)
	}
	if len(pruned) != 0 {
		t.Errorf("pruned = %v, want nothing", pruned)
	}
}

func TestPruneToLockNoopWhenStoreMatches(t *testing.T) {
	home := t.TempDir()
	inst := New(home, "", "", "")
	if err := inst.ensureDirs(); err != nil {
		t.Fatal(err)
	}
	mkPkgDir(t, inst.packagesDir, "keeper")

	lf := &lockfile.LockFile{Packages: []lockfile.LockedPackage{{Name: "keeper", Version: "1.0.0"}}}
	pruned, err := inst.pruneToLock(lf)
	if err != nil {
		t.Fatalf("pruneToLock: %v", err)
	}
	if len(pruned) != 0 {
		t.Errorf("nothing should be pruned, got %v", pruned)
	}
}

// pruneTo with a nil wantJar skips the JAR sweep; an empty non-nil one prunes
// every JAR. The distinction is what keeps pruneToLock and pruneToPlan honest.
func TestPruneToEmptyNonNilJarSetPrunesAllJARs(t *testing.T) {
	home := t.TempDir()
	inst := New(home, "", "", "")
	if err := inst.ensureDirs(); err != nil {
		t.Fatal(err)
	}
	mkJar(t, inst.jarsDir, "gone-1.0.0.jar")

	pruned, err := inst.pruneTo(map[string]bool{}, map[string]bool{}, map[string]bool{})
	if err != nil {
		t.Fatalf("pruneTo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inst.jarsDir, "gone-1.0.0.jar")); !os.IsNotExist(err) {
		t.Error("an empty (non-nil) wantJar should prune every JAR")
	}
	if len(pruned) != 1 || pruned[0] != "jar gone-1.0.0.jar" {
		t.Errorf("pruned = %v, want [jar gone-1.0.0.jar]", pruned)
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
	note, err := reconcileLock(&resolver.Plan{}, m, dir, "")
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
	note, err := reconcileLock(plan, m, dir, "")
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
	note, err := reconcileLock(&resolver.Plan{}, m, dir, "")
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
