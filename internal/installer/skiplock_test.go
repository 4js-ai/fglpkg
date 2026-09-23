package installer

import (
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// TestInstallAllWithOptionsSkipLock backs the global-tool install (GIS-565):
// Options.SkipLock must suppress the project lock write on the resolve path —
// the write (lockfile.FromPlan(...).Save(projectDir)) that leaked into the
// current directory — while a default install still writes it.
//
// An empty manifest resolves to an empty plan, so this exercises the resolve
// path with no registry or downloads; it needs only genero.Detect(), which is
// skipped when Genero is not installed. Removing the SkipLock guards in
// installer.go makes the SkipLock sub-test fail.
func TestInstallAllWithOptionsSkipLock(t *testing.T) {
	if _, err := genero.Detect(); err != nil {
		t.Skipf("Genero not detected: %v", err)
	}
	m := manifest.New("app", "1.0.0", "", "") // no dependencies -> empty plan, no network

	t.Run("default install writes a lock", func(t *testing.T) {
		i := New(t.TempDir(), "", "", "")
		projectDir := t.TempDir()
		if err := i.InstallAllWithOptions(m, projectDir, true, Options{}); err != nil {
			t.Fatalf("install: %v", err)
		}
		if !lockfile.Exists(projectDir) {
			t.Fatal("a normal resolve-path install must write a lock file")
		}
	})

	t.Run("SkipLock writes no lock", func(t *testing.T) {
		i := New(t.TempDir(), "", "", "")
		projectDir := t.TempDir()
		if err := i.InstallAllWithOptions(m, projectDir, true, Options{SkipLock: true}); err != nil {
			t.Fatalf("install: %v", err)
		}
		if lockfile.Exists(projectDir) {
			t.Fatal("SkipLock must not write a lock file into projectDir (GIS-565)")
		}
	})
}
