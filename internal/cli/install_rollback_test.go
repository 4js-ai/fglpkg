package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// GIS-579. The add path declares the dependency and saves the manifest before
// anything is fetched, and the lock is written inside the install. A failed
// install left both files describing a version that was never installed — the
// loud failure is recoverable, the silent drift that follows is not.

func TestSnapshotRestoresBothFilesToTheirPriorContents(t *testing.T) {
	dir := t.TempDir()
	before := map[string]string{
		manifest.Filename: `{"name":"app","version":"1.0.0"}`,
		lockfile.Filename: `{"lockfileVersion":1}`,
	}
	for name, body := range before {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	func() {
		s := snapshotProject(dir, dir)
		// What the add path does before the install runs.
		for name := range before {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(`{"declares":"a package that never installed"}`), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		s.restore()
	}()

	for name, want := range before {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want the pre-install contents %q", name, got, want)
		}
	}
}

// `fglpkg install <pkg>` in an empty directory generates a manifest. A failed
// install must not leave a project behind that nobody asked for, so "restore"
// of a file that did not exist means removing it again.
func TestSnapshotRemovesFilesThatDidNotExistBefore(t *testing.T) {
	dir := t.TempDir()

	func() {
		s := snapshotProject(dir, dir)
		for _, name := range []string{manifest.Filename, lockfile.Filename} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(`{}`), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		s.restore()
	}()

	for _, name := range []string{manifest.Filename, lockfile.Filename} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s survived the restore of a project that had none", name)
		}
	}
}

// A file that exists but cannot be read is left strictly alone. Restoring it
// would mean deleting content that was never captured, which is worse than the
// stale record the restore exists to clean up.
//
// The unreadable file here is a directory, so the check is permission-independent
// and behaves the same if the suite runs as root.
func TestSnapshotLeavesAnUnreadableFileAlone(t *testing.T) {
	dir := t.TempDir()
	lockAsDir := filepath.Join(dir, lockfile.Filename)
	if err := os.MkdirAll(filepath.Join(lockAsDir, "inside"), 0o755); err != nil {
		t.Fatal(err)
	}

	snapshotProject(dir, dir).restore()

	if _, err := os.Stat(filepath.Join(lockAsDir, "inside")); err != nil {
		t.Errorf("an unreadable path was modified by the restore: %v", err)
	}
}

// restore must tolerate being called on a snapshot that was never taken — the
// global-tool install path writes nothing to the project directory and so
// deliberately leaves the snapshot nil.
func TestNilSnapshotRestoreIsANoOp(t *testing.T) {
	var s *projectSnapshot
	s.restore()
}

// GIS-579, review round 1. The restore must also undo the .fglpkg/ scaffolding
// when this run created it: ensureDirs lays packages/, jars/ and webcomponents/
// down before anything is fetched, and isProjectDir counts .fglpkg/ as a
// project marker — so a refused `install <pkg>` in an empty directory left
// behind something that still read as a project.
func TestSnapshotRemovesAStoreThisRunCreated(t *testing.T) {
	dir := t.TempDir()
	s := snapshotProject(dir, dir)
	for _, sub := range []string{"packages", "jars", "webcomponents"} {
		if err := os.MkdirAll(filepath.Join(dir, ".fglpkg", sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s.restore()

	if _, err := os.Stat(filepath.Join(dir, ".fglpkg")); !os.IsNotExist(err) {
		t.Error("the empty store this run created survived the restore")
	}
}

// A store that already existed is never touched, however empty it looks.
func TestSnapshotKeepsAStoreThatAlreadyExisted(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".fglpkg", "packages"), 0o755); err != nil {
		t.Fatal(err)
	}
	snapshotProject(dir, dir).restore()

	if _, err := os.Stat(filepath.Join(dir, ".fglpkg")); err != nil {
		t.Errorf("a pre-existing store was removed: %v", err)
	}
}

// A store holding real files is never removed, even when this run created it.
// A partially-successful install put them there, and deleting a package the
// user may now depend on is a worse surprise than a directory left behind.
func TestSnapshotKeepsAStoreThatHoldsFiles(t *testing.T) {
	dir := t.TempDir()
	s := snapshotProject(dir, dir)
	pkg := filepath.Join(dir, ".fglpkg", "packages", "demo-pkg")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "mod.42m"), []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.restore()

	if _, err := os.Stat(filepath.Join(pkg, "mod.42m")); err != nil {
		t.Errorf("an installed package was deleted by the restore: %v", err)
	}
}
