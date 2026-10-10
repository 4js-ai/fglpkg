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

// GIS-579, review round 1. The ownership skip waved through files another
// package CO-OWNS, which is what dedup produces when two packages ship
// identical bytes at one path (GIS-298). When one of them then ships different
// bytes there, the two genuinely disagree about that file — and skipping the
// conflict check overwrote the other package's copy in silence, undoing the
// guard GIS-298 added.
func TestWebcomponentUpgradeRefusesToRewriteACoOwnedFile(t *testing.T) {
	tmp := t.TempDir()
	wcDir := filepath.Join(tmp, "webcomponents")

	// Both packages ship byte-identical content, so both install and both end
	// up owning docs/shared.md.
	const shared = "shared v1"
	a1 := filepath.Join(tmp, "a1.zip")
	writeTestZip(t, a1, map[string]string{
		"fglpkg.json":    `{"name":"a","version":"1.0.0","webcomponents":["A"]}`,
		"A/A.html":       "<a/>",
		"docs/shared.md": shared,
	})
	b1 := filepath.Join(tmp, "b1.zip")
	writeTestZip(t, b1, map[string]string{
		"fglpkg.json":    `{"name":"b","version":"1.0.0","webcomponents":["B"]}`,
		"B/B.html":       "<b/>",
		"docs/shared.md": shared,
	})
	if err := installWCZip(t, wcDir, "a", "1.0.0", a1, []string{"A"}); err != nil {
		t.Fatalf("install a: %v", err)
	}
	if err := installWCZip(t, wcDir, "b", "1.0.0", b1, []string{"B"}); err != nil {
		t.Fatalf("install b: %v", err)
	}

	// a 2.0.0 changes the co-owned file.
	a2 := filepath.Join(tmp, "a2.zip")
	writeTestZip(t, a2, map[string]string{
		"fglpkg.json":    `{"name":"a","version":"2.0.0","webcomponents":["A"]}`,
		"A/A.html":       "<a2/>",
		"docs/shared.md": "A's NEW text",
	})
	err := installWCZip(t, wcDir, "a", "2.0.0", a2, []string{"A"})
	if err == nil {
		t.Fatal("rewriting a file another package also owns must be refused")
	}
	if !strings.Contains(err.Error(), "refusing to clobber") {
		t.Errorf("want the clobber refusal, got: %v", err)
	}
	got, readErr := os.ReadFile(filepath.Join(wcDir, "docs", "shared.md"))
	if readErr != nil || string(got) != shared {
		t.Errorf("the co-owner's copy was changed underneath it: %q, %v", got, readErr)
	}
}

// The complement, so the fix above cannot be "refuse everything shared": a file
// this package owns ALONE is still freely replaced, which is the whole point of
// consulting the sidecar.
func TestWebcomponentUpgradeStillReplacesAnExclusivelyOwnedFile(t *testing.T) {
	tmp := t.TempDir()
	wcDir := filepath.Join(tmp, "webcomponents")

	v1 := filepath.Join(tmp, "v1.zip")
	writeTestZip(t, v1, map[string]string{
		"fglpkg.json":  `{"name":"a","version":"1.0.0","webcomponents":["A"]}`,
		"A/A.html":     "<a/>",
		"docs/mine.md": "v1",
	})
	// A second package that owns something else entirely, so the sidecar has
	// more than one entry and the exclusivity test is actually exercised.
	other := filepath.Join(tmp, "other.zip")
	writeTestZip(t, other, map[string]string{
		"fglpkg.json":   `{"name":"b","version":"1.0.0","webcomponents":["B"]}`,
		"B/B.html":      "<b/>",
		"docs/other.md": "unrelated",
	})
	if err := installWCZip(t, wcDir, "a", "1.0.0", v1, []string{"A"}); err != nil {
		t.Fatalf("install a: %v", err)
	}
	if err := installWCZip(t, wcDir, "b", "1.0.0", other, []string{"B"}); err != nil {
		t.Fatalf("install b: %v", err)
	}

	v2 := filepath.Join(tmp, "v2.zip")
	writeTestZip(t, v2, map[string]string{
		"fglpkg.json":  `{"name":"a","version":"2.0.0","webcomponents":["A"]}`,
		"A/A.html":     "<a2/>",
		"docs/mine.md": "v2",
	})
	if err := installWCZip(t, wcDir, "a", "2.0.0", v2, []string{"A"}); err != nil {
		t.Fatalf("a file only this package owns must still be replaceable: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(wcDir, "docs", "mine.md"))
	if err != nil || string(got) != "v2" {
		t.Errorf("docs/mine.md = %q (%v), want v2", got, err)
	}
}
