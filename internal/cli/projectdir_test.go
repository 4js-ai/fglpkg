package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// GIS-571: `.fglpkg/` names two unrelated things — a project's local install
// directory, and (as ~/.fglpkg) fglpkg's own home. $HOME therefore counted as a
// project, and every global command silently fell back to project behaviour
// there. These tests pin both halves: fglpkg's own directories are not
// projects, and everything that genuinely is one still is.

// mkFglpkgHome points FGLPKG_HOME at <dir>/.fglpkg and creates it, the shape a
// real $HOME has. Returns the directory that should NOT read as a project.
func mkFglpkgHome(t *testing.T, dir string) string {
	t.Helper()
	home := filepath.Join(dir, ".fglpkg")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", home, err)
	}
	t.Setenv("FGLPKG_HOME", home)
	return dir
}

// TestIsProjectDir_HomeIsNotAProject is the reported bug: standing in $HOME,
// the bare ~/.fglpkg made isProjectDir() true, so `install --global` wrote a
// manifest and lock into $HOME and `remove --global` only edited that
// accidental manifest.
func TestIsProjectDir_HomeIsNotAProject(t *testing.T) {
	t.Setenv("FGLPKG_GLOBAL_DIR", "")
	home := mkFglpkgHome(t, t.TempDir())
	chdirTest(t, home)

	if isProjectDir() {
		t.Fatal("$HOME must not count as a project just because it holds ~/.fglpkg")
	}
}

// The global package root can be somewhere else entirely (FGLPKG_GLOBAL_DIR, or
// $FGLDIR/fglpkg when FGLDIR is bound), and ~/.fglpkg still exists holding
// config and credentials. Comparing only against the ACTIVE store would leave
// $HOME broken for exactly those users, so the home is checked in its own right.
func TestIsProjectDir_HomeIsNotAProjectWhenStoreIsElsewhere(t *testing.T) {
	store := t.TempDir()
	t.Setenv("FGLPKG_GLOBAL_DIR", store)
	home := mkFglpkgHome(t, t.TempDir())
	chdirTest(t, home)

	if isProjectDir() {
		t.Fatal("~/.fglpkg is fglpkg's home even when the package root is elsewhere")
	}
}

// FGLPKG_GLOBAL_DIR can itself name a directory called .fglpkg; standing in its
// parent must not read as a project either.
func TestIsProjectDir_GlobalDirIsNotAProject(t *testing.T) {
	parent := t.TempDir()
	store := filepath.Join(parent, ".fglpkg")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FGLPKG_GLOBAL_DIR", store)
	t.Setenv("FGLPKG_HOME", filepath.Join(t.TempDir(), ".fglpkg"))
	chdirTest(t, parent)

	if isProjectDir() {
		t.Fatal("the directory holding the global store must not count as a project")
	}
}

// The other half of the rule: a real project must still be detected. A local
// install directory that is nobody's home is exactly what it looks like.
func TestIsProjectDir_RealProjectWithOnlyLocalInstallDir(t *testing.T) {
	t.Setenv("FGLPKG_GLOBAL_DIR", "")
	t.Setenv("FGLPKG_HOME", filepath.Join(t.TempDir(), ".fglpkg"))
	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, ".fglpkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	chdirTest(t, proj)

	if !isProjectDir() {
		t.Fatal("a project's own .fglpkg/ must still mark it as a project")
	}
}

// A manifest is unambiguous and decides on its own. This is what keeps the one
// legitimately-ambiguous case working: a user who points FGLPKG_GLOBAL_DIR at a
// real project's .fglpkg/ still has a project.
func TestIsProjectDir_ManifestWinsEvenWhenDirIsTheStore(t *testing.T) {
	proj := t.TempDir()
	store := filepath.Join(proj, ".fglpkg")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "fglpkg.json"),
		[]byte(`{"name":"p","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FGLPKG_GLOBAL_DIR", store)
	chdirTest(t, proj)

	if !isProjectDir() {
		t.Fatal("a manifest must still mark a project even when its .fglpkg IS the store")
	}
}

// A manifest alone, with no install directory yet, is still a project.
func TestIsProjectDir_ManifestOnly(t *testing.T) {
	t.Setenv("FGLPKG_GLOBAL_DIR", "")
	t.Setenv("FGLPKG_HOME", filepath.Join(t.TempDir(), ".fglpkg"))
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "fglpkg.json"),
		[]byte(`{"name":"p","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	chdirTest(t, proj)

	if !isProjectDir() {
		t.Fatal("a manifest alone marks a project")
	}
}

// An empty directory is not a project.
func TestIsProjectDir_EmptyDir(t *testing.T) {
	t.Setenv("FGLPKG_GLOBAL_DIR", "")
	t.Setenv("FGLPKG_HOME", filepath.Join(t.TempDir(), ".fglpkg"))
	chdirTest(t, t.TempDir())

	if isProjectDir() {
		t.Fatal("an empty directory is not a project")
	}
}

// Identity is by os.SameFile, not string comparison, because $HOME and the
// working directory routinely differ by a symlink — /tmp is /private/tmp on
// macOS, and home directories are often symlinked. A textual match would leave
// the bug in place for precisely those users.
func TestIsProjectDir_HomeReachedThroughASymlink(t *testing.T) {
	t.Setenv("FGLPKG_GLOBAL_DIR", "")
	real := t.TempDir()
	home := filepath.Join(real, ".fglpkg")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FGLPKG_HOME", home)

	link := filepath.Join(t.TempDir(), "homelink")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	chdirTest(t, link)

	if isProjectDir() {
		t.Fatal("the home must be recognised through a symlinked path too")
	}
}
