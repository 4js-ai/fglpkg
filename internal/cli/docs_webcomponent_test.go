package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLooksLikeDoc(t *testing.T) {
	cases := []struct {
		path     string
		inBundle bool
		want     bool
		why      string
	}{
		// The files GIS-581 reports as unreachable.
		{"docs/fjs-map/USERGUIDE.md", false, true, ""},
		{"docs/fjs-map/README.md", false, true, ""},
		{"docs/fjs-map/CHANGELOG.md", false, true, ""},
		{"LICENSE", false, true, ""},
		{"docs/NOTICE", false, true, ""},
		{"guide.txt", false, true, ""},
		{"manual.rst", false, true, ""},
		{"manual.adoc", false, true, ""},
		{"README", false, true, ""},
		{"readme.MD", false, true, ""},
		// Pins the EXTENSION rule, not the stem list: every other .md row
		// above also matches by its conventional stem (README, USERGUIDE,
		// CHANGELOG), so dropping ".md" from docMarkupExtensions would leave
		// them all passing. This is the guide's own example file.
		{"docs/api-reference.md", false, true, "pins the .md extension"},
		// A widget's own assets are not documentation, and must not pad the
		// listing — the sidecar records them alongside the docs.
		{"FjsMap/index.html", true, false, ""},
		{"FjsMap/main.js", true, false, ""},
		{"FjsMap/style.css", true, false, ""},
		{"FjsMap/icon.png", true, false, ""},
		{"FjsMap/data.json", true, false, ""},
		{"wcsettings/FjsMap.xml", false, false, ""},
		// Plain .txt INSIDE a component bundle is a resource or bundler
		// output, not a guide, and padded the listing beside the real docs.
		{"FjsMap/robots.txt", true, false, "a bundle resource"},
		{"FjsMap/data/terms.txt", true, false, "a bundle resource, nested"},
		{"FjsMap/main.js.LICENSE.txt", true, false, "bundler output, not the licence"},
		// ... but a conventional name inside the bundle still counts, and
		// markup inside the bundle is always a guide.
		{"FjsMap/LICENSE.txt", true, true, "conventional stem wins inside a bundle"},
		{"FjsMap/README.txt", true, true, "conventional stem wins inside a bundle"},
		{"FjsMap/api-reference.md", true, true, "markup is a guide wherever it sits"},
		// Outside a bundle the plain-text rule is unchanged: nothing there is
		// generated, and erring wide costs only a line.
		{"docs/robots.txt", false, true, "outside a bundle .txt still counts"},
	}
	for _, tc := range cases {
		if got := looksLikeDoc(tc.path, tc.inBundle); got != tc.want {
			t.Errorf("looksLikeDoc(%q, inComponentDir=%v) = %v, want %v %s",
				tc.path, tc.inBundle, got, tc.want, tc.why)
		}
	}
}

// writeWCOwners writes the ownership sidecar the installer maintains. It lives
// NEXT TO webcomponents/, not inside it, so Genero's FGLIMAGEPATH discovery
// never mistakes it for a COMPONENTTYPE dir.
func writeWCOwners(t *testing.T, home string, packages map[string][]string, versions map[string]string) string {
	t.Helper()
	wcDir := filepath.Join(home, "webcomponents")
	if err := os.MkdirAll(wcDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := map[string]any{"packages": packages}
	if versions != nil {
		body["versions"] = versions
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "webcomponent-owners.json"), data, 0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	return wcDir
}

func TestFindWebcomponentDocs(t *testing.T) {
	home := t.TempDir()
	wcDir := writeWCOwners(t,
		home,
		map[string][]string{"fjs-map": {
			"docs/fjs-map/USERGUIDE.md",
			"docs/fjs-map/README.md",
			"FjsMap/index.html",
			"FjsMap/main.js",
		}},
		map[string]string{"fjs-map": "1.2.0"},
	)
	// The sidecar records what was written; only what is really on disk may
	// be listed, or the listing promises a file that then fails to open.
	for _, f := range []string{"docs/fjs-map/USERGUIDE.md", "docs/fjs-map/README.md", "FjsMap/index.html", "FjsMap/main.js"} {
		full := filepath.Join(wcDir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	t.Setenv("FGLPKG_HOME", home)
	t.Setenv("FGLPKG_GLOBAL_DIR", home)

	src, ok := findWebcomponentDocs("fjs-map")
	if !ok {
		t.Fatal("findWebcomponentDocs() reported the package as unknown; GIS-581 is not fixed")
	}
	if len(src.files) != 2 {
		t.Fatalf("got %d doc files %v, want exactly the 2 markdown files", len(src.files), src.files)
	}
	if src.label != "fjs-map@1.2.0" {
		t.Errorf("label = %q, want fjs-map@1.2.0", src.label)
	}
	for _, f := range src.files {
		if looksLikeDoc(f, false) {
			continue
		}
		t.Errorf("non-doc file %q leaked into the listing", f)
	}
}

// A sidecar written before the Versions field existed names no version. The
// package must still resolve — it is installed either way.
func TestFindWebcomponentDocsWithoutVersion(t *testing.T) {
	home := t.TempDir()
	wcDir := writeWCOwners(t, home, map[string][]string{"fjs-map": {"docs/README.md"}}, nil)
	full := filepath.Join(wcDir, "docs", "README.md")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("FGLPKG_HOME", home)
	t.Setenv("FGLPKG_GLOBAL_DIR", home)

	src, ok := findWebcomponentDocs("fjs-map")
	if !ok {
		t.Fatal("a versionless sidecar entry must still resolve")
	}
	if src.label != "fjs-map" {
		t.Errorf("label = %q, want the bare name when no version is recorded", src.label)
	}
}

// A file the sidecar records but that is no longer on disk must not be listed:
// the listing would otherwise offer a file that fails to open.
func TestFindWebcomponentDocsSkipsMissingFiles(t *testing.T) {
	home := t.TempDir()
	writeWCOwners(t, home, map[string][]string{"fjs-map": {"docs/GONE.md"}}, nil)
	t.Setenv("FGLPKG_HOME", home)
	t.Setenv("FGLPKG_GLOBAL_DIR", home)

	src, ok := findWebcomponentDocs("fjs-map")
	if !ok {
		t.Fatal("the package is recorded, so it must resolve")
	}
	if len(src.files) != 0 {
		t.Errorf("a recorded-but-absent file was listed: %v", src.files)
	}
}

func TestFindWebcomponentDocsUnknownPackage(t *testing.T) {
	home := t.TempDir()
	writeWCOwners(t, home, map[string][]string{"fjs-map": {"docs/README.md"}}, nil)
	t.Setenv("FGLPKG_HOME", home)
	t.Setenv("FGLPKG_GLOBAL_DIR", home)

	if _, ok := findWebcomponentDocs("not-installed"); ok {
		t.Error("an unrecorded package must not resolve")
	}
}

func TestDedupeStrings(t *testing.T) {
	got := dedupeStrings([]string{"a", "", "b", "a", "b", "c"})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
