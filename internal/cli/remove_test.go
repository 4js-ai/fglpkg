package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeManifest drops a minimal fglpkg.json declaring the given fgl dependency
// into dir.
func writeManifest(t *testing.T, dir, dep string) {
	t.Helper()
	body := `{"name":"testproj","version":"1.0.0","dependencies":{"fgl":{"` + dep + `":"^1.0.0"}}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "fglpkg.json"), []byte(body), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

// TestCmdRemove_CommaFormRejected covers the comma half of GIS-564: a
// comma-joined argument is one token that matches nothing, so it must be
// rejected up front (before any manifest/installer work) with the
// space-separated form, not reported as a removal.
func TestCmdRemove_CommaFormRejected(t *testing.T) {
	out, err := captureStdout(t, func() error { return cmdRemove([]string{"foo,bar"}) })
	if err == nil {
		t.Fatal("expected an error for the comma form, got nil")
	}
	if !strings.Contains(err.Error(), "spaces") || !strings.Contains(err.Error(), "foo bar") {
		t.Fatalf("error should suggest the space-separated form, got: %v", err)
	}
	if strings.Contains(out, "✓ Removed") {
		t.Fatalf("comma form must not report a removal, got output:\n%s", out)
	}
}

// TestCmdRemove_UnknownNameNoFalseSuccess covers the core of GIS-564: removing a
// package that is not declared warns and fails instead of printing the old
// "✓ Removed <name> (not declared in manifest)" success line — and it leaves the
// manifest untouched (it returns before Save/prune).
func TestCmdRemove_UnknownNameNoFalseSuccess(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "realpkg")
	chdirTest(t, dir)

	out, err := captureStdout(t, func() error { return cmdRemove([]string{"ghost"}) })
	if err == nil {
		t.Fatal("expected a non-nil error when nothing was removed")
	}
	if strings.Contains(out, "✓ Removed") {
		t.Fatalf("an undeclared name must not report success, got output:\n%s", out)
	}
	if !strings.Contains(out, "warning") || !strings.Contains(out, "ghost") {
		t.Fatalf("expected a warning naming the missing package, got output:\n%s", out)
	}
	// The manifest must be unchanged — the real dependency is still declared.
	data, readErr := os.ReadFile(filepath.Join(dir, "fglpkg.json"))
	if readErr != nil {
		t.Fatalf("read manifest: %v", readErr)
	}
	if !strings.Contains(string(data), "realpkg") {
		t.Fatalf("a no-op remove must not rewrite the manifest, got:\n%s", data)
	}
}
