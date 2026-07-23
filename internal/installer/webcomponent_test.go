package installer

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

// writeTestZip builds a zip at zipPath containing the given name→content
// entries, returning the path.
func writeTestZip(t *testing.T, zipPath string, entries map[string]string) {
	t.Helper()
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	for name, content := range entries {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatalf("zip.Create %s: %v", name, err)
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
}

// TestExtractZipRoutedMixedPackage verifies the mixed-zip routing: a zip
// that contains both BDL files and a COMPONENTTYPE directory gets split —
// the COMPONENTTYPE bundle lands under webcomponentsDir, everything else
// under destDir.
func TestExtractZipRoutedMixedPackage(t *testing.T) {
	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "pkg.zip")
	writeTestZip(t, zipPath, map[string]string{
		"fglpkg.json":          `{"name":"chart-3d","version":"1.0.0","webcomponents":["3DChart"]}`,
		"ChartDemo.42m":        "BDL\n",
		"3DChart/3DChart.html": "<html/>",
		"3DChart/3DChart.js":   "// js",
	})

	destDir := filepath.Join(tmp, "packages", "chart-3d")
	wcDir := filepath.Join(tmp, "webcomponents")

	if _, err := extractZipRouted(zipPath, destDir, wcDir, []string{"3DChart"}); err != nil {
		t.Fatalf("extractZipRouted: %v", err)
	}

	mustExist := []string{
		filepath.Join(destDir, "fglpkg.json"),
		filepath.Join(destDir, "ChartDemo.42m"),
		filepath.Join(wcDir, "3DChart", "3DChart.html"),
		filepath.Join(wcDir, "3DChart", "3DChart.js"),
	}
	for _, p := range mustExist {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s on disk: %v", p, err)
		}
	}

	mustNotExist := []string{
		filepath.Join(destDir, "3DChart", "3DChart.html"), // must NOT leak into packages/
		filepath.Join(wcDir, "ChartDemo.42m"),             // must NOT leak into webcomponents/
		filepath.Join(wcDir, "fglpkg.json"),
	}
	for _, p := range mustNotExist {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("unexpected file at %s — routing leaked", p)
		}
	}
}

// TestExtractZipRoutedPureBDL falls back to the unrouted extraction when
// the manifest declares no webcomponents.
func TestExtractZipRoutedPureBDL(t *testing.T) {
	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "pkg.zip")
	writeTestZip(t, zipPath, map[string]string{
		"fglpkg.json": `{"name":"pure-bdl","version":"1.0.0"}`,
		"Lib.42m":     "BDL\n",
	})

	destDir := filepath.Join(tmp, "packages", "pure-bdl")
	wcDir := filepath.Join(tmp, "webcomponents")

	if _, err := extractZipRouted(zipPath, destDir, wcDir, nil); err != nil {
		t.Fatalf("extractZipRouted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destDir, "Lib.42m")); err != nil {
		t.Errorf("expected BDL file in destDir: %v", err)
	}
}

// TestExtractWebcomponentZipSharedTreeNoClobber is the GIS-298 regression:
// installing a 2nd webcomponent package that shares top-level trees (com/,
// examples/) with the 1st must NOT drop the 1st package's files.
func TestExtractWebcomponentZipSharedTreeNoClobber(t *testing.T) {
	tmp := t.TempDir()
	wcDir := filepath.Join(tmp, "webcomponents")

	mapZip := filepath.Join(tmp, "map.zip")
	writeTestZip(t, mapZip, map[string]string{
		"fglpkg.json":            `{"name":"fjs-map","version":"1.0.0","webcomponents":["Map"]}`,
		"Map/Map.html":           "<map/>",
		"com/fourjs/map/Map.42m": "MAP BDL\n",
		"examples/map_demo.4gl":  "MAIN END MAIN\n",
	})
	gridZip := filepath.Join(tmp, "grid.zip")
	writeTestZip(t, gridZip, map[string]string{
		"fglpkg.json":                  `{"name":"fjs-data-grid","version":"1.0.0","webcomponents":["DataGrid"]}`,
		"DataGrid/DataGrid.html":       "<grid/>",
		"com/fourjs/datagrid/Grid.42m": "GRID BDL\n",
		"examples/grid_demo.4gl":       "MAIN END MAIN\n",
	})

	if _, err := extractWebcomponentZip(mapZip, wcDir, []string{"Map"}); err != nil {
		t.Fatalf("install fjs-map: %v", err)
	}
	if _, err := extractWebcomponentZip(gridZip, wcDir, []string{"DataGrid"}); err != nil {
		t.Fatalf("install fjs-data-grid: %v", err)
	}

	// Both packages' files must survive after the 2nd install.
	for _, p := range []string{
		"Map/Map.html", "com/fourjs/map/Map.42m", "examples/map_demo.4gl",
		"DataGrid/DataGrid.html", "com/fourjs/datagrid/Grid.42m", "examples/grid_demo.4gl",
	} {
		if _, err := os.Stat(filepath.Join(wcDir, filepath.FromSlash(p))); err != nil {
			t.Errorf("expected %s to survive both installs: %v", p, err)
		}
	}
}

// TestExtractWebcomponentZipDedupIdentical: a byte-identical shared file
// installed by two packages is a dedup, not a conflict.
func TestExtractWebcomponentZipDedupIdentical(t *testing.T) {
	tmp := t.TempDir()
	wcDir := filepath.Join(tmp, "webcomponents")
	shared := "SHARED LICENSE TEXT\n"

	for _, name := range []string{"a", "b"} {
		z := filepath.Join(tmp, name+".zip")
		writeTestZip(t, z, map[string]string{
			"fglpkg.json":      `{"name":"` + name + `","version":"1.0.0","webcomponents":["` + name + `"]}`,
			name + "/x.html":   "<" + name + "/>",
			"docs/LICENSE.txt": shared,
		})
		if _, err := extractWebcomponentZip(z, wcDir, []string{name}); err != nil {
			t.Fatalf("install %s: %v", name, err)
		}
	}

	got, err := os.ReadFile(filepath.Join(wcDir, "docs", "LICENSE.txt"))
	if err != nil {
		t.Fatalf("read shared file: %v", err)
	}
	if string(got) != shared {
		t.Errorf("shared file content = %q, want %q", got, shared)
	}
}

// TestExtractWebcomponentZipConflictErrors: a shared file with DIFFERENT
// content aborts the install (non-nil error) and leaves the first package's
// file intact — no silent clobber, no partial write of the 2nd package.
func TestExtractWebcomponentZipConflictErrors(t *testing.T) {
	tmp := t.TempDir()
	wcDir := filepath.Join(tmp, "webcomponents")

	first := filepath.Join(tmp, "first.zip")
	writeTestZip(t, first, map[string]string{
		"fglpkg.json":    `{"name":"first","version":"1.0.0","webcomponents":["First"]}`,
		"First/x.html":   "<first/>",
		"docs/README.md": "FIRST\n",
	})
	second := filepath.Join(tmp, "second.zip")
	writeTestZip(t, second, map[string]string{
		"fglpkg.json":    `{"name":"second","version":"1.0.0","webcomponents":["Second"]}`,
		"Second/y.html":  "<second/>",
		"docs/README.md": "SECOND\n", // same path, different content → conflict
	})

	if _, err := extractWebcomponentZip(first, wcDir, []string{"First"}); err != nil {
		t.Fatalf("install first: %v", err)
	}
	_, err := extractWebcomponentZip(second, wcDir, []string{"Second"})
	if err == nil {
		t.Fatal("expected a conflict error installing second, got nil")
	}
	// The first package's file must be untouched.
	got, readErr := os.ReadFile(filepath.Join(wcDir, "docs", "README.md"))
	if readErr != nil {
		t.Fatalf("read README after conflict: %v", readErr)
	}
	if string(got) != "FIRST\n" {
		t.Errorf("README clobbered despite conflict: got %q", got)
	}
	// The second package must not have partially written its own bundle.
	if _, err := os.Stat(filepath.Join(wcDir, "Second", "y.html")); err == nil {
		t.Error("second package partially written despite aborted install")
	}
}

// TestExtractWebcomponentZipReinstallCleansOwned: reinstalling the same
// package clears its own COMPONENTTYPE dir so a file dropped in the new
// version does not linger.
func TestExtractWebcomponentZipReinstallCleansOwned(t *testing.T) {
	tmp := t.TempDir()
	wcDir := filepath.Join(tmp, "webcomponents")

	v1 := filepath.Join(tmp, "v1.zip")
	writeTestZip(t, v1, map[string]string{
		"fglpkg.json": `{"name":"w","version":"1.0.0","webcomponents":["W"]}`,
		"W/old.html":  "old",
		"W/W.html":    "<w/>",
	})
	v2 := filepath.Join(tmp, "v2.zip")
	writeTestZip(t, v2, map[string]string{
		"fglpkg.json": `{"name":"w","version":"2.0.0","webcomponents":["W"]}`,
		"W/W.html":    "<w2/>",
	})

	if _, err := extractWebcomponentZip(v1, wcDir, []string{"W"}); err != nil {
		t.Fatalf("install v1: %v", err)
	}
	if _, err := extractWebcomponentZip(v2, wcDir, []string{"W"}); err != nil {
		t.Fatalf("reinstall v2: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wcDir, "W", "old.html")); err == nil {
		t.Error("stale W/old.html survived reinstall — owned dir not cleaned")
	}
	if _, err := os.Stat(filepath.Join(wcDir, "W", "W.html")); err != nil {
		t.Errorf("W/W.html missing after reinstall: %v", err)
	}
}

// TestReadWebcomponentsFromZip pulls the webcomponents list out of the
// manifest inside a zip without extracting anything else.
func TestReadWebcomponentsFromZip(t *testing.T) {
	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "pkg.zip")
	writeTestZip(t, zipPath, map[string]string{
		"fglpkg.json": `{"name":"m","version":"1.0.0","webcomponents":["A","B"]}`,
	})
	got, err := readWebcomponentsFromZip(zipPath)
	if err != nil {
		t.Fatalf("readWebcomponentsFromZip: %v", err)
	}
	if len(got) != 2 || got[0] != "A" || got[1] != "B" {
		t.Errorf("unexpected webcomponents list: %v", got)
	}
}
