package cli

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// TestBuildPackageZipContents verifies the zip builder includes the
// expected files for a representative manifest (default patterns + docs
// + bin). buildPackageZip reads from the current working directory, so
// the test Chdirs into a temp project.
func TestBuildPackageZipContents(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("fglpkg.json", `{
  "name": "packtest",
  "version": "1.0.0",
  "dependencies": { "fgl": {} },
  "docs": ["README.md"],
  "bin": { "migrate": "scripts/migrate.sh" }
}`)
	write("Main.42m", "MAIN\nEND MAIN\n")
	write("pkg/Util.42m", "FUNCTION helper() END FUNCTION\n")
	write("README.md", "# Packtest\n")
	write("scripts/migrate.sh", "#!/bin/sh\necho migrate\n")
	// A file that should be excluded (not matching any pattern).
	write("notes.txt", "scratch notes\n")

	// buildPackageZip walks the current directory, so swap cwd.
	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	data, sum, err := buildPackageZip(m)
	if err != nil {
		t.Fatalf("buildPackageZip: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("zip is empty")
	}
	if len(sum) != 64 {
		t.Errorf("SHA256 hex digest should be 64 chars, got %d", len(sum))
	}

	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	got := map[string]bool{}
	for _, f := range r.File {
		got[f.Name] = true
	}

	wantIncluded := []string{
		"fglpkg.json",
		"Main.42m",
		"pkg/Util.42m",
		"README.md",
		"scripts/migrate.sh",
	}
	for _, name := range wantIncluded {
		if !got[name] {
			t.Errorf("expected %q in zip, got entries: %v", name, keys(got))
		}
	}
	if got["notes.txt"] {
		t.Errorf("notes.txt should not be in zip (no matching pattern)")
	}
}

// TestBuildPackageZipExcludesLocalCacheAndStripsDevDeps reproduces the
// ifx-to-pgs leak: a project that installs a devDependency locally
// (.fglpkg/packages/<dep>/...) and declares a docs pattern whose name
// happens to collide with a file inside that dep would ship the dep's
// docs and advertise its own devDependencies block in the published
// manifest. The fix is two-fold: walk past .fglpkg/, and strip
// devDependencies from the manifest copy that goes into the zip.
func TestBuildPackageZipExcludesLocalCacheAndStripsDevDeps(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("fglpkg.json", `{
  "name": "leaktest",
  "version": "1.0.0",
  "root": "src",
  "files": ["*.42m"],
  "docs": ["USERGUIDE.md"],
  "dependencies": { "fgl": {} },
  "devDependencies": { "fgl": { "fglunit": "^0.1.0" } }
}`)
	write("USERGUIDE.md", "# leaktest user guide\n")
	write("src/Main.42m", "MAIN END MAIN\n")
	// The local package cache holds an installed devDependency. Both its
	// USERGUIDE.md and its compiled modules must stay out of the zip.
	write(".fglpkg/packages/fglunit/USERGUIDE.md", "# fglunit docs (must not ship)\n")
	write(".fglpkg/packages/fglunit/com/fourjs/fglunit/FglUnit.42m", "MAIN END MAIN\n")

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	data, _, err := buildPackageZip(m)
	if err != nil {
		t.Fatalf("buildPackageZip: %v", err)
	}

	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	got := map[string]string{}
	for _, f := range r.File {
		body, err := readZipEntry(f)
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		got[f.Name] = body
	}

	for name := range got {
		if filepath.HasPrefix(name, ".fglpkg/") || filepath.ToSlash(name) == ".fglpkg" {
			t.Errorf("local cache entry leaked into zip: %s", name)
		}
	}
	if _, ok := got["USERGUIDE.md"]; !ok {
		t.Errorf("expected root USERGUIDE.md in zip; got %v", keys(boolKeys(got)))
	}

	mfRaw, ok := got["fglpkg.json"]
	if !ok {
		t.Fatal("manifest missing from zip")
	}
	if strings.Contains(mfRaw, "devDependencies") {
		t.Errorf("publishable manifest should not advertise devDependencies; got:\n%s", mfRaw)
	}
	if !strings.Contains(mfRaw, `"name": "leaktest"`) {
		t.Errorf("publishable manifest looks malformed:\n%s", mfRaw)
	}
}

// readZipEntry reads a zip file entry to a string for assertion.
func readZipEntry(f *zip.File) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(rc); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// boolKeys converts a map[string]string into a map[string]bool so the
// existing keys() helper can be reused for diagnostics.
func boolKeys(m map[string]string) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

func TestListZipEntriesSortedAndSized(t *testing.T) {
	// Build a tiny zip in-memory.
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	mustWrite := func(name, body string) {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
		if _, err := fw.Write([]byte(body)); err != nil {
			t.Fatalf("Write %s: %v", name, err)
		}
	}
	mustWrite("zeta.txt", "zz")
	mustWrite("alpha.txt", "aaa")
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	entries, err := listZipEntries(buf.Bytes())
	if err != nil {
		t.Fatalf("listZipEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].name != "alpha.txt" || entries[1].name != "zeta.txt" {
		t.Errorf("entries not sorted: %+v", entries)
	}
	if entries[0].size != 3 || entries[1].size != 2 {
		t.Errorf("sizes wrong: %+v", entries)
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestPackRejectsInvalidVersion covers GIS-374: pack must apply the same strict
// version check as publish, so a non-semver version fails up front instead of
// being packed into a bogus artifact.
func TestPackRejectsInvalidVersion(t *testing.T) {
	dir := t.TempDir()
	manifestJSON := `{"name":"demo.pkg","version":"not-a-semver","genero":">=3.20","license":"MIT","repository":"https://example.com/x/y","author":"t","files":["*.42m"]}`
	if err := os.WriteFile(filepath.Join(dir, manifest.Filename), []byte(manifestJSON), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mod.42m"), []byte("stub"), 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	err := cmdPack([]string{"--list"})
	if err == nil {
		t.Fatal("expected pack to reject an invalid version, got nil")
	}
	if !strings.Contains(err.Error(), "not valid semver") {
		t.Errorf("error = %q, want it to mention 'not valid semver'", err.Error())
	}
}
