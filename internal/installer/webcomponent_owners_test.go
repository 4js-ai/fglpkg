package installer

import (
	"os"
	"path/filepath"
	"testing"
)

// installWC extracts a webcomponent zip and records its ownership, mirroring
// what installWebcomponent does — the setup shared by the prune tests.
func installWC(t *testing.T, tmp, wcDir, name, comp string, extra map[string]string) {
	t.Helper()
	entries := map[string]string{
		"fglpkg.json":               `{"name":"` + name + `","version":"1.0.0","webcomponents":["` + comp + `"]}`,
		comp + "/" + comp + ".html": "<" + comp + "/>",
	}
	for k, v := range extra {
		entries[k] = v
	}
	z := filepath.Join(tmp, name+".zip")
	writeTestZip(t, z, entries)
	// wcOwnedBy, not nil: installWebcomponent consults the sidecar so a
	// package's own previous files are not mistaken for another package's
	// (GIS-579), and a helper that claims to mirror it has to do the same.
	files, err := extractWebcomponentZip(z, wcDir, []string{comp}, wcOwnedBy(wcDir, name))
	if err != nil {
		t.Fatalf("install %s: %v", name, err)
	}
	if err := recordWCOwnership(wcDir, name, "1.0.0", files); err != nil {
		t.Fatalf("record ownership %s: %v", name, err)
	}
}

// TestPruneWebcomponentsRemovesOwned is the GIS-372 regression: removing a
// webcomponent package prunes its COMPONENTTYPE bundle and namespace tree from
// .fglpkg/webcomponents/ (which `remove` used to leave orphaned), while a
// still-installed package's artifacts survive.
func TestPruneWebcomponentsRemovesOwned(t *testing.T) {
	tmp := t.TempDir()
	wcDir := filepath.Join(tmp, "webcomponents")

	installWC(t, tmp, wcDir, "fjs-map", "Map", map[string]string{"com/fourjs/map/Map.42m": "MAP\n"})
	installWC(t, tmp, wcDir, "fjs-grid", "DataGrid", map[string]string{"com/fourjs/grid/Grid.42m": "GRID\n"})

	i := New(tmp, "", "", "")
	pruned, err := i.pruneWebcomponents(map[string]bool{"fjs-grid": true}) // keep grid, drop map
	if err != nil {
		t.Fatalf("pruneWebcomponents: %v", err)
	}

	for _, p := range []string{"Map/Map.html", "com/fourjs/map/Map.42m"} {
		if _, err := os.Stat(filepath.Join(wcDir, filepath.FromSlash(p))); !os.IsNotExist(err) {
			t.Errorf("expected %s pruned, stat err = %v", p, err)
		}
	}
	// The now-empty namespace dir is pruned too.
	if _, err := os.Stat(filepath.Join(wcDir, "com", "fourjs", "map")); !os.IsNotExist(err) {
		t.Error("expected empty com/fourjs/map to be pruned")
	}
	// The surviving package is untouched.
	for _, p := range []string{"DataGrid/DataGrid.html", "com/fourjs/grid/Grid.42m"} {
		if _, err := os.Stat(filepath.Join(wcDir, filepath.FromSlash(p))); err != nil {
			t.Errorf("expected %s to survive: %v", p, err)
		}
	}
	if len(pruned) != 1 || pruned[0] != "webcomponent fjs-map" {
		t.Errorf("pruned = %v, want [webcomponent fjs-map]", pruned)
	}

	o, err := loadWCOwners(wcDir)
	if err != nil {
		t.Fatalf("loadWCOwners: %v", err)
	}
	if _, ok := o.Packages["fjs-map"]; ok {
		t.Error("sidecar still lists removed package fjs-map")
	}
	if _, ok := o.Packages["fjs-grid"]; !ok {
		t.Error("sidecar dropped surviving package fjs-grid")
	}
}

// TestPruneWebcomponentsCoOwnedSurvives: a file shared (byte-identical, so
// deduped at install) between two packages is only deleted once its last owner
// is removed.
func TestPruneWebcomponentsCoOwnedSurvives(t *testing.T) {
	tmp := t.TempDir()
	wcDir := filepath.Join(tmp, "webcomponents")
	license := "MIT LICENSE\n"

	installWC(t, tmp, wcDir, "a", "a", map[string]string{"docs/LICENSE.txt": license})
	installWC(t, tmp, wcDir, "b", "b", map[string]string{"docs/LICENSE.txt": license})

	licensePath := filepath.Join(wcDir, "docs", "LICENSE.txt")
	i := New(tmp, "", "", "")

	// Remove "a": the shared license is still owned by "b" → must survive.
	if _, err := i.pruneWebcomponents(map[string]bool{"b": true}); err != nil {
		t.Fatalf("prune a: %v", err)
	}
	if _, err := os.Stat(licensePath); err != nil {
		t.Errorf("co-owned LICENSE deleted while b still owns it: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wcDir, "a", "a.html")); !os.IsNotExist(err) {
		t.Error("a's own component was not pruned")
	}

	// Remove "b" too: nothing owns the license now → gone, and the empty
	// sidecar is removed.
	if _, err := i.pruneWebcomponents(map[string]bool{}); err != nil {
		t.Fatalf("prune b: %v", err)
	}
	if _, err := os.Stat(licensePath); !os.IsNotExist(err) {
		t.Error("LICENSE should be gone after its last owner was removed")
	}
	if _, err := os.Stat(wcOwnersPath(wcDir)); !os.IsNotExist(err) {
		t.Error("ownership sidecar should be removed when no package owns anything")
	}
}

// TestPruneWebcomponentsNoSidecar: with no ownership record (e.g. a pre-fix
// install), pruning is a safe no-op rather than an error.
func TestPruneWebcomponentsNoSidecar(t *testing.T) {
	tmp := t.TempDir()
	wcDir := filepath.Join(tmp, "webcomponents")
	if err := os.MkdirAll(wcDir, 0755); err != nil {
		t.Fatal(err)
	}
	i := New(tmp, "", "", "")
	pruned, err := i.pruneWebcomponents(map[string]bool{})
	if err != nil {
		t.Fatalf("pruneWebcomponents with no sidecar: %v", err)
	}
	if len(pruned) != 0 {
		t.Errorf("expected nothing pruned, got %v", pruned)
	}
}

// GIS-579, review round 1. installedWebcomponents feeds LockFile.Validate,
// which SKIPS the version comparison for a nil map — so returning nil for an
// unreadable sidecar would switch drift detection off for exactly the corrupt
// store that most needs it, and the replay would report "Nothing to install"
// over whatever happens to be on disk.
func TestInstalledWebcomponentsIsEmptyNotNilForACorruptSidecar(t *testing.T) {
	inst := New(t.TempDir(), "", "", "")
	if err := os.MkdirAll(inst.webcomponentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wcOwnersPath(inst.webcomponentsDir), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := inst.installedWebcomponents()
	if got == nil {
		t.Fatal("a corrupt sidecar must not read as 'cannot observe the store'")
	}
	if len(got) != 0 {
		t.Errorf("a corrupt sidecar names no installed version, got %v", got)
	}
}

// A missing sidecar is the ordinary case for a project with no web components,
// and must give the same answer.
func TestInstalledWebcomponentsIsEmptyNotNilWithNoSidecar(t *testing.T) {
	inst := New(t.TempDir(), "", "", "")
	if got := inst.installedWebcomponents(); got == nil || len(got) != 0 {
		t.Errorf("want an empty non-nil map with no sidecar, got %#v", got)
	}
}
