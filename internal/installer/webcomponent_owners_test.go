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
	files, err := extractWebcomponentZip(z, wcDir, []string{comp})
	if err != nil {
		t.Fatalf("install %s: %v", name, err)
	}
	if err := recordWCOwnership(wcDir, name, files); err != nil {
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
