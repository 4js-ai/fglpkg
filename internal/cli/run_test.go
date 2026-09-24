package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateGlobalStore points the global package root at an empty temp dir so
// run/list don't pick up the developer's real global packages.
func isolateGlobalStore(t *testing.T) {
	t.Helper()
	t.Setenv("FGLPKG_GLOBAL_DIR", t.TempDir())
}

// writeProjectWithBin drops a project manifest declaring one bin command, and
// (when withScript) the script it points at, marked executable.
func writeProjectWithBin(t *testing.T, dir string, withScript bool) {
	t.Helper()
	writeRawManifest(t, dir, `{"name":"myproj","version":"1.0.0","bin":{"greet":"scripts/greet.sh"}}`+"\n")
	if withScript {
		if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0755); err != nil {
			t.Fatalf("mkdir scripts: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "scripts", "greet.sh"), []byte("#!/bin/sh\necho hi\n"), 0755); err != nil {
			t.Fatalf("write script: %v", err)
		}
	}
}

// TestCmdRunList_IncludesProjectBin: `run --list` lists a bin declared in the
// current project's own fglpkg.json, tagged with the "project" source (GIS-566).
func TestCmdRunList_IncludesProjectBin(t *testing.T) {
	dir := t.TempDir()
	writeProjectWithBin(t, dir, true)
	isolateGlobalStore(t)
	chdirTest(t, dir)

	out, err := captureStdout(t, cmdRunList)
	if err != nil {
		t.Fatalf("cmdRunList: %v", err)
	}
	for _, want := range []string{"greet", "project", "scripts/greet.sh", "myproj"} {
		if !strings.Contains(out, want) {
			t.Fatalf("run --list should list the project bin (%q), got:\n%s", want, out)
		}
	}
}

// TestFindBinCommand_ProjectFirst: a project's own bin resolves to the script
// under the project root, owned by the project (GIS-566).
func TestFindBinCommand_ProjectFirst(t *testing.T) {
	dir := t.TempDir()
	writeProjectWithBin(t, dir, true)
	isolateGlobalStore(t)
	chdirTest(t, dir)

	script, pkg, err := findBinCommand("greet")
	if err != nil {
		t.Fatalf("findBinCommand: %v", err)
	}
	if pkg != "myproj" {
		t.Fatalf("expected the project (myproj) to own the command, got %q", pkg)
	}
	// t.TempDir may be a symlinked path; match the tail rather than the absolute.
	if !strings.HasSuffix(script, filepath.Join("scripts", "greet.sh")) {
		t.Fatalf("expected the project's script path, got %q", script)
	}
}

// TestFindBinCommand_ProjectBinMissingScript: a declared bin whose script is
// absent is a clear error, not a silent fall-through to an installed package.
func TestFindBinCommand_ProjectBinMissingScript(t *testing.T) {
	dir := t.TempDir()
	writeProjectWithBin(t, dir, false) // manifest declares the bin, script not created
	isolateGlobalStore(t)
	chdirTest(t, dir)

	if _, _, err := findBinCommand("greet"); err == nil {
		t.Fatal("expected an error when the declared bin script is missing")
	}
}

// TestFindBinCommand_ProjectShadowsInstalled: when both the project and an
// installed package declare the same command, the project wins (project-first
// precedence), deterministically — no ambiguity error.
func TestFindBinCommand_ProjectShadowsInstalled(t *testing.T) {
	dir := t.TempDir()
	writeProjectWithBin(t, dir, true)

	inst := filepath.Join(dir, ".fglpkg", "packages", "other")
	if err := os.MkdirAll(inst, 0755); err != nil {
		t.Fatalf("mkdir installed pkg: %v", err)
	}
	writeRawManifest(t, inst, `{"name":"other","version":"1.0.0","bin":{"greet":"run.sh"}}`+"\n")
	if err := os.WriteFile(filepath.Join(inst, "run.sh"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("write installed script: %v", err)
	}
	isolateGlobalStore(t)
	chdirTest(t, dir)

	_, pkg, err := findBinCommand("greet")
	if err != nil {
		t.Fatalf("findBinCommand: %v", err)
	}
	if pkg != "myproj" {
		t.Fatalf("the project's bin must win over the installed package, got owner %q", pkg)
	}
}
