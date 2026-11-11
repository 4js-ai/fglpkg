package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stageWC writes the ownership sidecar for a scope and creates every file it
// records, so the on-disk check in findWebcomponentDocs passes.
func stageWC(t *testing.T, home string, packages map[string][]string, versions map[string]string) string {
	t.Helper()
	wcDir := writeWCOwners(t, home, packages, versions)
	for _, files := range packages {
		for _, f := range files {
			full := filepath.Join(wcDir, filepath.FromSlash(f))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
		}
	}
	return wcDir
}

// The LOCAL project store is the primary path — a project install is the
// default — and nothing covered it: every other test sets only the global
// store, so deleting the local lookup entirely passed the whole suite.
func TestFindWebcomponentDocsFindsTheLocalProjectStore(t *testing.T) {
	proj := t.TempDir()
	globalHome := t.TempDir()
	localHome := filepath.Join(proj, ".fglpkg")
	stageWC(t, localHome, map[string][]string{"fjs-map": {"docs/LOCAL.md"}}, map[string]string{"fjs-map": "9.9.9"})

	// A global store that holds the same package under a different version, so
	// a pass that reached only the global one would still "succeed" — and be
	// caught by the version.
	stageWC(t, globalHome, map[string][]string{"fjs-map": {"docs/GLOBAL.md"}}, map[string]string{"fjs-map": "1.0.0"})

	t.Setenv("FGLPKG_HOME", globalHome)
	t.Setenv("FGLPKG_GLOBAL_DIR", globalHome)
	chdir(t, proj)

	src, ok := findWebcomponentDocs("fjs-map")
	if !ok {
		t.Fatal("a package installed in the LOCAL project store was not found")
	}
	if src.label != "fjs-map@9.9.9" {
		t.Errorf("label = %q, want fjs-map@9.9.9: the global store won over the project's own install", src.label)
	}
	if len(src.files) != 1 || src.files[0] != "docs/LOCAL.md" {
		t.Errorf("files = %v, want [docs/LOCAL.md]", src.files)
	}
}

// The sidecar is keyed by the name the registry served. A user who types
// another spelling must still resolve, the way a BDL package does (GIS-271).
func TestFindWebcomponentDocsResolvesANonCanonicalSpelling(t *testing.T) {
	home := t.TempDir()
	stageWC(t, home, map[string][]string{"wc-docs": {"docs/README.md"}}, map[string]string{"wc-docs": "1.0.0"})
	t.Setenv("FGLPKG_HOME", home)
	t.Setenv("FGLPKG_GLOBAL_DIR", home)

	for _, spelling := range []string{"WC_Docs", "WC-DOCS", "wc_docs"} {
		src, ok := findWebcomponentDocs(spelling)
		if !ok {
			t.Errorf("docs %s did not resolve the installed wc-docs", spelling)
			continue
		}
		if len(src.files) != 1 {
			t.Errorf("docs %s listed %v, want the one README", spelling, src.files)
		}
	}
}

// A widget bundle ships plain-text resources and bundler output beside its real
// guides. Those padded the listing because .txt counted as documentation
// everywhere. Inside the bundle only a conventional name counts now.
func TestFindWebcomponentDocsLeavesBundleResourcesOut(t *testing.T) {
	home := t.TempDir()
	wcDir := stageWC(t, home, map[string][]string{"fjs-map": {
		"FjsMap/main.js",
		"FjsMap/robots.txt",
		"FjsMap/data/terms.txt",
		"FjsMap/main.js.LICENSE.txt",
		"FjsMap/LICENSE.txt",
		"FjsMap/api-reference.md",
		"docs/notes.txt",
	}}, map[string]string{"fjs-map": "1.0.0"})
	// FjsMap is a bundle only because it holds its own entry point; docs/ is
	// not, and keeps the wide rule.
	if err := os.WriteFile(filepath.Join(wcDir, "FjsMap", "FjsMap.html"), []byte("<html>"), 0o644); err != nil {
		t.Fatalf("write entry point: %v", err)
	}
	t.Setenv("FGLPKG_HOME", home)
	t.Setenv("FGLPKG_GLOBAL_DIR", home)

	src, ok := findWebcomponentDocs("fjs-map")
	if !ok {
		t.Fatal("package did not resolve")
	}
	got := map[string]bool{}
	for _, f := range src.files {
		got[f] = true
	}
	for _, want := range []string{"FjsMap/LICENSE.txt", "FjsMap/api-reference.md", "docs/notes.txt"} {
		if !got[want] {
			t.Errorf("%q is documentation and was dropped from the listing", want)
		}
	}
	for _, unwanted := range []string{"FjsMap/robots.txt", "FjsMap/data/terms.txt", "FjsMap/main.js.LICENSE.txt", "FjsMap/main.js"} {
		if got[unwanted] {
			t.Errorf("%q is a bundle resource and padded the documentation listing", unwanted)
		}
	}
}

// Without the entry point the directory is an ancillary tree, not a bundle, so
// the wide rule still applies to it. This is what keeps the narrowing from
// reaching a package that merely happens to use a capitalised folder name.
func TestFindWebcomponentDocsOnlyNarrowsRealBundles(t *testing.T) {
	home := t.TempDir()
	stageWC(t, home, map[string][]string{"fjs-map": {"Guides/intro.txt"}}, map[string]string{"fjs-map": "1.0.0"})
	t.Setenv("FGLPKG_HOME", home)
	t.Setenv("FGLPKG_GLOBAL_DIR", home)

	src, _ := findWebcomponentDocs("fjs-map")
	if len(src.files) != 1 {
		t.Errorf("files = %v, want [Guides/intro.txt]: Guides/ has no Guides.html, so it is not a component bundle", src.files)
	}
}

// fglpkg's OWN directory must never be read as a project above the cwd, or the
// "but %s is a project" hint would send a user to ~/.fglpkg.
func TestFindProjectAncestorIgnoresFglpkgsOwnDir(t *testing.T) {
	home := t.TempDir()
	fglpkgHome := filepath.Join(home, ".fglpkg")
	if err := os.MkdirAll(filepath.Join(fglpkgHome, "packages"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A directory UNDER the global store: walking up from here reaches home,
	// which holds .fglpkg — fglpkg's own, not a project's.
	deep := filepath.Join(fglpkgHome, "packages", "somepkg")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("FGLPKG_HOME", fglpkgHome)
	t.Setenv("FGLPKG_GLOBAL_DIR", fglpkgHome)
	chdir(t, deep)

	if dir, ok := findProjectAncestor(); ok && sameDir(t, dir, home) {
		t.Errorf("findProjectAncestor() named %q — that is fglpkg's own home, not a project", dir)
	}
}

func sameDir(t *testing.T, a, b string) bool {
	t.Helper()
	fa, err1 := os.Stat(a)
	fb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(fa, fb)
}

// A web component whose only documentation sits at the ZIP ROOT (the usual
// README.md and LICENSE) has nothing on disk: extractWebcomponentZip skips
// every root entry, so the ownership sidecar never records them either.
//
// The message must not say the package "ships no documentation files" — it may
// well ship them — but name the limit that put them out of reach, which is the
// only thing that tells the author how to fix it.
func TestDocsWebcomponentWithNoFilesOnDiskNamesTheLimit(t *testing.T) {
	home := t.TempDir()
	// The sidecar records only what was extracted: the bundle, no docs.
	stageWC(t, home, map[string][]string{"wc-rootdocs": {"FjsMap/main.js"}}, map[string]string{"wc-rootdocs": "1.0.0"})
	t.Setenv("FGLPKG_HOME", home)
	t.Setenv("FGLPKG_GLOBAL_DIR", home)
	chdir(t, t.TempDir())

	out, err := captureStdout(t, func() error { return cmdDocs([]string{"wc-rootdocs"}) })
	if err != nil {
		t.Fatalf("cmdDocs = %v, want no error", err)
	}
	if strings.Contains(out, "ships no documentation files") {
		t.Errorf("the message blames the author for a limit of the install layout:\n%s", out)
	}
	for _, want := range []string{"kept on disk", "zip-root files", "not extracted"} {
		if !strings.Contains(out, want) {
			t.Errorf("the message does not name the limit (%q missing):\n%s", want, out)
		}
	}
}
