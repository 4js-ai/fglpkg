package classpath

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustWriteFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

// readManifest opens the jar at path and returns its META-INF/MANIFEST.MF
// content, failing the test if the jar isn't a valid zip or the manifest
// entry is missing.
func readManifest(t *testing.T, path string) string {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open %s as zip: %v", path, err)
	}
	defer r.Close()
	for _, f := range r.File {
		if f.Name == "META-INF/MANIFEST.MF" {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("open manifest entry: %v", err)
			}
			defer rc.Close()
			data, err := io.ReadAll(rc)
			if err != nil {
				t.Fatalf("read manifest entry: %v", err)
			}
			return string(data)
		}
	}
	t.Fatalf("%s has no META-INF/MANIFEST.MF entry", path)
	return ""
}

func TestSyncBuildsValidJarWithClassPath(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "zeta.jar"), "zeta")
	mustWriteFile(t, filepath.Join(dir, "alpha.jar"), "alpha")
	mustWriteFile(t, filepath.Join(dir, "readme.txt"), "not a jar")

	if err := Sync(dir); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	anchor := AnchorPath(dir)
	wantPath := filepath.Join(dir, AnchorName)
	if anchor != wantPath {
		t.Fatalf("anchor path = %q, want %q", anchor, wantPath)
	}

	manifest := readManifest(t, anchor)
	if !strings.Contains(manifest, "Manifest-Version: 1.0\r\n") {
		t.Errorf("manifest missing Manifest-Version header:\n%s", manifest)
	}
	// Sorted, space-separated, basenames only (readme.txt excluded).
	if !strings.Contains(manifest, "Class-Path: alpha.jar zeta.jar") {
		t.Errorf("manifest Class-Path wrong, got:\n%s", manifest)
	}
	if strings.Contains(manifest, "readme.txt") {
		t.Errorf("manifest must not reference non-jar files:\n%s", manifest)
	}
}

func TestSyncEmptyDirWritesNothing(t *testing.T) {
	dir := t.TempDir() // no jars
	if err := Sync(dir); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, AnchorName)); err == nil {
		t.Error("anchor jar should not be created when there are no dependency jars")
	}
	if got := AnchorPath(dir); got != "" {
		t.Errorf("AnchorPath = %q, want \"\"", got)
	}
}

func TestSyncMissingDirIsNoOp(t *testing.T) {
	if err := Sync(filepath.Join(t.TempDir(), "does-not-exist")); err != nil {
		t.Fatalf("Sync on missing dir: %v", err)
	}
}

// TestSyncRemovesAnchorWhenLastJarGone verifies the remove/prune contract:
// once no dependency jars remain, Sync deletes the anchor instead of leaving
// a stale, pointless CLASSPATH entry behind.
func TestSyncRemovesAnchorWhenLastJarGone(t *testing.T) {
	dir := t.TempDir()
	jar := filepath.Join(dir, "dep.jar")
	mustWriteFile(t, jar, "dep")
	if err := Sync(dir); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if AnchorPath(dir) == "" {
		t.Fatal("anchor should exist while dep.jar is present")
	}

	if err := os.Remove(jar); err != nil {
		t.Fatal(err)
	}
	if err := Sync(dir); err != nil {
		t.Fatalf("Sync after removing last jar: %v", err)
	}
	if AnchorPath(dir) != "" {
		t.Error("anchor should be deleted once no dependency jars remain")
	}
}

// TestSyncExcludesItself verifies that a previously-generated anchor jar is
// never listed as one of its own Class-Path dependencies — otherwise every
// regeneration would grow the manifest by referencing itself.
func TestSyncExcludesItself(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "dep.jar"), "dep")

	if err := Sync(dir); err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	if err := Sync(dir); err != nil { // regenerate
		t.Fatalf("second Sync: %v", err)
	}
	manifest := readManifest(t, AnchorPath(dir))
	if strings.Contains(manifest, AnchorName) {
		t.Errorf("manifest must not reference the anchor jar itself:\n%s", manifest)
	}
	if !strings.Contains(manifest, "Class-Path: dep.jar") {
		t.Errorf("manifest should still list the real dependency:\n%s", manifest)
	}
}

// TestSyncRefreshesOnJarSetChange verifies the anchor is regenerated (not
// left stale) when the jar set in dir changes between calls — the
// install/update/remove contract.
func TestSyncRefreshesOnJarSetChange(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "one.jar"), "one")

	if err := Sync(dir); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if m := readManifest(t, AnchorPath(dir)); !strings.Contains(m, "Class-Path: one.jar") {
		t.Fatalf("expected one.jar only, got:\n%s", m)
	}

	mustWriteFile(t, filepath.Join(dir, "two.jar"), "two")
	if err := Sync(dir); err != nil {
		t.Fatalf("Sync (after add): %v", err)
	}
	if m := readManifest(t, AnchorPath(dir)); !strings.Contains(m, "Class-Path: one.jar two.jar") {
		t.Fatalf("expected both jars after refresh, got:\n%s", m)
	}
}

// TestSyncSkipsWriteWhenCurrent verifies the byte-compare fast path: a Sync
// against an unchanged jar set leaves the anchor file untouched (same mtime),
// so repeated installs don't churn a file that fglrun may be reading.
func TestSyncSkipsWriteWhenCurrent(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "dep.jar"), "dep")
	if err := Sync(dir); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	anchor := AnchorPath(dir)
	before, err := os.Stat(anchor)
	if err != nil {
		t.Fatal(err)
	}
	// Backdate the anchor so an (unwanted) rewrite would visibly change mtime
	// even on filesystems with coarse timestamps.
	old := before.ModTime().Add(-1 * 60 * 1e9)
	if err := os.Chtimes(anchor, old, old); err != nil {
		t.Fatal(err)
	}

	if err := Sync(dir); err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	after, err := os.Stat(anchor)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(old) {
		t.Error("Sync rewrote an already-current anchor (mtime changed)")
	}
}

func TestHasDependencyJars(t *testing.T) {
	dir := t.TempDir()
	if HasDependencyJars(dir) {
		t.Error("empty dir should have no dependency jars")
	}
	mustWriteFile(t, filepath.Join(dir, AnchorName), "anchor only")
	if HasDependencyJars(dir) {
		t.Error("the anchor itself must not count as a dependency jar")
	}
	mustWriteFile(t, filepath.Join(dir, "dep.jar"), "dep")
	if !HasDependencyJars(dir) {
		t.Error("dep.jar should count as a dependency jar")
	}
}

// TestWrapManifestHeaderRoundTrips verifies the 72-byte-per-line wrapping
// (JAR manifest spec) produces lines that never exceed the limit and, when
// unwrapped (continuation prefix space + CRLF removed), reconstruct the
// exact original value -- both for a short value (no wrapping needed) and
// a long one (many jar names, forcing several continuation lines).
func TestWrapManifestHeaderRoundTrips(t *testing.T) {
	short := wrapManifestHeader("Class-Path", "a.jar b.jar")
	assertLinesWithinLimit(t, short)
	assertUnwrapsTo(t, short, "Class-Path", "a.jar b.jar")

	var names []string
	for i := 0; i < 40; i++ {
		names = append(names, "some-long-dependency-artifact-name-"+strings.Repeat("x", i%5)+".jar")
	}
	value := strings.Join(names, " ")
	long := wrapManifestHeader("Class-Path", value)
	assertLinesWithinLimit(t, long)
	assertUnwrapsTo(t, long, "Class-Path", value)
}

func assertLinesWithinLimit(t *testing.T, wrapped string) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSuffix(wrapped, "\r\n"), "\r\n") {
		if len(line)+2 > 72 { // +2 for the \r\n this Split stripped
			t.Errorf("line exceeds 72 bytes including CRLF: %q (%d bytes + CRLF)", line, len(line))
		}
	}
}

func assertUnwrapsTo(t *testing.T, wrapped, name, want string) {
	t.Helper()
	var b strings.Builder
	for i, line := range strings.Split(strings.TrimSuffix(wrapped, "\r\n"), "\r\n") {
		if i == 0 {
			b.WriteString(strings.TrimPrefix(line, name+": "))
			continue
		}
		if !strings.HasPrefix(line, " ") {
			t.Fatalf("continuation line missing leading space: %q", line)
		}
		b.WriteString(strings.TrimPrefix(line, " "))
	}
	if got := b.String(); got != want {
		t.Errorf("unwrapped value = %q, want %q", got, want)
	}
}

// TestBuildClasspathManifestIsValidZipEntry is a belt-and-suspenders check
// that buildClasspathManifest's output, once written into a zip archive,
// survives a normal zip round-trip byte-for-byte.
func TestBuildClasspathManifestIsValidZipEntry(t *testing.T) {
	want := buildClasspathManifest([]string{"a.jar", "b.jar"})

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("META-INF/MANIFEST.MF")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(want); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	rc, err := zr.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("manifest bytes changed across a zip round-trip:\nwant %q\ngot  %q", want, got)
	}
}
