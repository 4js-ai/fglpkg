package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// GIS-579. A widget that ships anything outside its declared COMPONENTTYPE
// dirs — a BDL wrapper, docs, examples — reinstalls those paths on every
// version. extractWebcomponentZip's conflict check had no way to tell them from
// another package's files, so it refused every such upgrade, naming only the
// package being installed as the conflicting one.
//
// The ownership sidecar is exactly the record needed, and recordWCOwnership had
// been writing it all along; nothing read it but `remove`.

// installWCZip runs one extraction the way installWebcomponent does, consulting
// the sidecar for what pkg already owns and recording what it installed.
func installWCZip(t *testing.T, wcDir, pkg, version, zipPath string, componentTypes []string) error {
	t.Helper()
	installed, err := extractWebcomponentZip(zipPath, wcDir, componentTypes, wcOwnedBy(wcDir, pkg))
	if err != nil {
		return err
	}
	return recordWCOwnership(wcDir, pkg, version, installed)
}

func TestWebcomponentUpgradeReplacesThePackagesOwnSharedFiles(t *testing.T) {
	tmp := t.TempDir()
	wcDir := filepath.Join(tmp, "webcomponents")

	v1 := filepath.Join(tmp, "v1.zip")
	writeTestZip(t, v1, map[string]string{
		"fglpkg.json":      `{"name":"map","version":"1.0.0","webcomponents":["Map"]}`,
		"Map/Map.html":     "<v1/>",
		"com/acme/Map.4gl": "v1 wrapper",
	})
	v2 := filepath.Join(tmp, "v2.zip")
	writeTestZip(t, v2, map[string]string{
		"fglpkg.json":      `{"name":"map","version":"1.2.0","webcomponents":["Map"]}`,
		"Map/Map.html":     "<v2/>",
		"com/acme/Map.4gl": "v2 wrapper, different",
	})

	if err := installWCZip(t, wcDir, "map", "1.0.0", v1, []string{"Map"}); err != nil {
		t.Fatalf("install v1: %v", err)
	}
	if err := installWCZip(t, wcDir, "map", "1.2.0", v2, []string{"Map"}); err != nil {
		t.Fatalf("upgrade to v2 must not report a clash on the package's own file: %v", err)
	}

	for path, want := range map[string]string{
		filepath.Join(wcDir, "com", "acme", "Map.4gl"): "v2 wrapper, different",
		filepath.Join(wcDir, "Map", "Map.html"):        "<v2/>",
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
}

// The guard must survive the fix. A file another package owns is still a clash,
// and the sidecar lookup must be keyed on the package being installed — reading
// the union of all owners would wave every clash through.
func TestWebcomponentClashWithAnotherPackageIsStillRefused(t *testing.T) {
	tmp := t.TempDir()
	wcDir := filepath.Join(tmp, "webcomponents")

	first := filepath.Join(tmp, "first.zip")
	writeTestZip(t, first, map[string]string{
		"fglpkg.json":      `{"name":"map","version":"1.0.0","webcomponents":["Map"]}`,
		"Map/Map.html":     "<map/>",
		"com/acme/Map.4gl": "map's wrapper",
	})
	second := filepath.Join(tmp, "second.zip")
	writeTestZip(t, second, map[string]string{
		"fglpkg.json":      `{"name":"grid","version":"1.0.0","webcomponents":["Grid"]}`,
		"Grid/Grid.html":   "<grid/>",
		"com/acme/Map.4gl": "grid's impostor",
	})

	if err := installWCZip(t, wcDir, "map", "1.0.0", first, []string{"Map"}); err != nil {
		t.Fatalf("install map: %v", err)
	}
	err := installWCZip(t, wcDir, "grid", "1.0.0", second, []string{"Grid"})
	if err == nil {
		t.Fatal("another package's clashing file must still be refused")
	}
	if !strings.Contains(err.Error(), "refusing to clobber") {
		t.Errorf("want the clobber refusal, got: %v", err)
	}
	// The refused install must have changed nothing.
	got, readErr := os.ReadFile(filepath.Join(wcDir, "com", "acme", "Map.4gl"))
	if readErr != nil || string(got) != "map's wrapper" {
		t.Errorf("the owner's file was touched by a refused install: %q, %v", got, readErr)
	}
	if _, err := os.Stat(filepath.Join(wcDir, "Grid", "Grid.html")); err == nil {
		t.Error("the refused package partially wrote its own bundle")
	}
}

// A file the new version no longer ships must go. Nothing else ever looks at it
// again, it stays on FGLIMAGEPATH, and `remove` cannot prune it once the
// ownership record stops naming it.
func TestWebcomponentUpgradeRemovesFilesTheNewVersionDropped(t *testing.T) {
	tmp := t.TempDir()
	wcDir := filepath.Join(tmp, "webcomponents")

	v1 := filepath.Join(tmp, "v1.zip")
	writeTestZip(t, v1, map[string]string{
		"fglpkg.json":        `{"name":"map","version":"1.0.0","webcomponents":["Map"]}`,
		"Map/Map.html":       "<v1/>",
		"com/acme/Map.4gl":   "wrapper",
		"examples/demo.4gl":  "a demo the next version drops",
		"examples/extra.4gl": "another",
	})
	v2 := filepath.Join(tmp, "v2.zip")
	writeTestZip(t, v2, map[string]string{
		"fglpkg.json":      `{"name":"map","version":"1.2.0","webcomponents":["Map"]}`,
		"Map/Map.html":     "<v2/>",
		"com/acme/Map.4gl": "wrapper",
	})

	if err := installWCZip(t, wcDir, "map", "1.0.0", v1, []string{"Map"}); err != nil {
		t.Fatalf("install v1: %v", err)
	}
	if err := installWCZip(t, wcDir, "map", "1.2.0", v2, []string{"Map"}); err != nil {
		t.Fatalf("upgrade to v2: %v", err)
	}

	for _, gone := range []string{"examples/demo.4gl", "examples/extra.4gl"} {
		if _, err := os.Stat(filepath.Join(wcDir, filepath.FromSlash(gone))); err == nil {
			t.Errorf("%s survived an upgrade that no longer ships it", gone)
		}
	}
	// The emptied directory goes with them.
	if _, err := os.Stat(filepath.Join(wcDir, "examples")); err == nil {
		t.Error("the emptied examples/ directory was left behind")
	}
	// What the new version does ship is untouched.
	if _, err := os.Stat(filepath.Join(wcDir, "com", "acme", "Map.4gl")); err != nil {
		t.Errorf("a file both versions ship was deleted: %v", err)
	}
}

// Dedup (GIS-298) can leave one file listed under several packages. A version
// that drops such a file must not delete it out from under the others.
func TestWebcomponentUpgradeKeepsAFileAnotherPackageAlsoOwns(t *testing.T) {
	tmp := t.TempDir()
	wcDir := filepath.Join(tmp, "webcomponents")

	shared := map[string]string{
		"fglpkg.json":     `{"name":"map","version":"1.0.0","webcomponents":["Map"]}`,
		"Map/Map.html":    "<map/>",
		"common/util.4gl": "identical in both packages",
	}
	v1 := filepath.Join(tmp, "v1.zip")
	writeTestZip(t, v1, shared)
	if err := installWCZip(t, wcDir, "map", "1.0.0", v1, []string{"Map"}); err != nil {
		t.Fatalf("install map: %v", err)
	}

	// A second package ships the SAME bytes at that path, so it installs
	// cleanly and ends up co-owning it.
	other := filepath.Join(tmp, "other.zip")
	writeTestZip(t, other, map[string]string{
		"fglpkg.json":     `{"name":"grid","version":"1.0.0","webcomponents":["Grid"]}`,
		"Grid/Grid.html":  "<grid/>",
		"common/util.4gl": "identical in both packages",
	})
	if err := installWCZip(t, wcDir, "grid", "1.0.0", other, []string{"Grid"}); err != nil {
		t.Fatalf("install grid: %v", err)
	}

	// map 1.2.0 drops common/util.4gl; grid still has it.
	v2 := filepath.Join(tmp, "v2.zip")
	writeTestZip(t, v2, map[string]string{
		"fglpkg.json":  `{"name":"map","version":"1.2.0","webcomponents":["Map"]}`,
		"Map/Map.html": "<map2/>",
	})
	if err := installWCZip(t, wcDir, "map", "1.2.0", v2, []string{"Map"}); err != nil {
		t.Fatalf("upgrade map: %v", err)
	}

	if _, err := os.Stat(filepath.Join(wcDir, "common", "util.4gl")); err != nil {
		t.Errorf("a file grid still owns was deleted with map's old version: %v", err)
	}
}
