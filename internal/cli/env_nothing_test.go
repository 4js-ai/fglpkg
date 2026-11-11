package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// The subdirectory case is the one that bites: isProjectDir() looks only at
// the current directory, so running from inside a project's src/ falls through
// to the merged default mode, finds nothing local, and used to print a
// plausible-looking export line and exit 0 (GIS-588).
func TestFindProjectAncestorFindsTheProjectAbove(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "fglpkg.json"), []byte(`{"name":"p","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	sub := filepath.Join(root, "src", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, sub)

	got, ok := findProjectAncestor()
	if !ok {
		t.Fatal("findProjectAncestor() found nothing from a project subdirectory")
	}
	if !samePath(t, got, root) {
		t.Errorf("found %q, want the project root %q", got, root)
	}
}

// A project marked only by a local .fglpkg/ install dir counts too.
func TestFindProjectAncestorAcceptsAnFglpkgDir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".fglpkg", "packages"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sub := filepath.Join(root, "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, sub)

	got, ok := findProjectAncestor()
	if !ok {
		t.Fatal("a parent holding .fglpkg/ must count as a project")
	}
	if !samePath(t, got, root) {
		t.Errorf("found %q, want %q", got, root)
	}
}

// With no project anywhere above, the walk must terminate at the filesystem
// root rather than loop.
func TestFindProjectAncestorStopsAtRoot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b", "c")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, dir)

	if got, ok := findProjectAncestor(); ok {
		t.Errorf("found %q where no project exists", got)
	}
}

// The current directory is never its own ancestor — the caller already knows
// it is not a project, so reporting it would be advice that changes nothing.
func TestFindProjectAncestorIgnoresTheCurrentDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "fglpkg.json"), []byte(`{"name":"p","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	chdir(t, root)

	if got, ok := findProjectAncestor(); ok {
		t.Errorf("found %q — the current directory must not be its own ancestor", got)
	}
}

// samePath compares two paths through EvalSymlinks, because macOS resolves
// /tmp to /private/tmp and the raw strings then differ for the same directory.
func samePath(t *testing.T, a, b string) bool {
	t.Helper()
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		ra = a
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		rb = b
	}
	return ra == rb
}
