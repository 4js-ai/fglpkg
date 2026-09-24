package installer

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

func TestMakeBinScriptsExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod not applicable on Windows")
	}

	pkgDir := t.TempDir()

	// Create the script file.
	scriptDir := filepath.Join(pkgDir, "scripts")
	if err := os.MkdirAll(scriptDir, 0755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(scriptDir, "run.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\necho hello\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := &manifest.Manifest{
		Name:    "testpkg",
		Version: "1.0.0",
		Bin:     map[string]string{"run": "scripts/run.sh"},
	}

	if err := makeBinScriptsExecutable(pkgDir, m); err != nil {
		t.Fatalf("makeBinScriptsExecutable: %v", err)
	}

	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	mode := info.Mode()
	if mode&0111 == 0 {
		t.Errorf("expected executable bits set, got mode %o", mode)
	}
}

func TestMakeBinScriptsExecutableMissingFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod not applicable on Windows")
	}

	pkgDir := t.TempDir()

	m := &manifest.Manifest{
		Name:    "testpkg",
		Version: "1.0.0",
		Bin:     map[string]string{"missing": "scripts/missing.sh"},
	}

	err := makeBinScriptsExecutable(pkgDir, m)
	if err == nil {
		t.Fatal("expected error for missing script file")
	}
}

func TestMakeBinScriptsExecutableMultiple(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod not applicable on Windows")
	}

	pkgDir := t.TempDir()
	scriptDir := filepath.Join(pkgDir, "scripts")
	if err := os.MkdirAll(scriptDir, 0755); err != nil {
		t.Fatal(err)
	}

	scripts := []string{"a.sh", "b.py"}
	for _, name := range scripts {
		if err := os.WriteFile(filepath.Join(scriptDir, name), []byte("#!/bin/bash\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	m := &manifest.Manifest{
		Name:    "testpkg",
		Version: "1.0.0",
		Bin: map[string]string{
			"cmd-a": "scripts/a.sh",
			"cmd-b": "scripts/b.py",
		},
	}

	if err := makeBinScriptsExecutable(pkgDir, m); err != nil {
		t.Fatalf("makeBinScriptsExecutable: %v", err)
	}

	for _, name := range scripts {
		info, err := os.Stat(filepath.Join(scriptDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&0111 == 0 {
			t.Errorf("%s: expected executable bits set, got mode %o", name, info.Mode())
		}
	}
}

// TestMakeBinScriptsExecutableHonorsRoot: `bin` paths are relative to the
// package's `root`, so a package published with root set keeps its script at
// <pkgDir>/<root>/<script>. Resolving without root missed it entirely and failed
// the whole install with "cannot set bin script permissions" (GIS-569).
func TestMakeBinScriptsExecutableHonorsRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod not applicable on Windows")
	}

	pkgDir := t.TempDir()
	// The script lives under root, exactly where `pack` stages it.
	scriptPath := filepath.Join(pkgDir, "src", "scripts", "run.sh")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho hi\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := &manifest.Manifest{
		Name:    "testpkg",
		Version: "1.0.0",
		Root:    "src",
		Bin:     map[string]string{"run": "scripts/run.sh"},
	}

	if err := makeBinScriptsExecutable(pkgDir, m); err != nil {
		t.Fatalf("makeBinScriptsExecutable: %v", err)
	}
	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0111 == 0 {
		t.Errorf("expected the script under root to be executable, got mode %o", info.Mode())
	}
}

// TestMakeBinScriptsExecutableRejectsUnsafePath: a bin path that escapes the
// package directory is refused rather than chmod-ed — an installed manifest
// comes from a registry and is not inherently trusted.
func TestMakeBinScriptsExecutableRejectsUnsafePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod not applicable on Windows")
	}

	base := t.TempDir()
	pkgDir := filepath.Join(base, "pkg")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	// The escaping target really exists, so a dropped check would chmod it.
	outside := filepath.Join(base, "outside.sh")
	if err := os.WriteFile(outside, []byte("#!/bin/sh\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := &manifest.Manifest{
		Name:    "testpkg",
		Version: "1.0.0",
		Bin:     map[string]string{"run": "../outside.sh"},
	}

	err := makeBinScriptsExecutable(pkgDir, m)
	if err == nil || !strings.Contains(err.Error(), "escape") {
		t.Fatalf("expected an escape error for a bin path outside the package, got: %v", err)
	}
	info, statErr := os.Stat(outside)
	if statErr != nil {
		t.Fatal(statErr)
	}
	if info.Mode()&0111 != 0 {
		t.Errorf("a file outside the package must not be made executable, got mode %o", info.Mode())
	}
}
