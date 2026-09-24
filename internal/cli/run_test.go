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
		writeScript(t, filepath.Join(dir, "scripts", "greet.sh"))
	}
}

// writeInstalledPkgWithBin hand-places an installed package under .fglpkg that
// defines a bin command (no real install needed).
func writeInstalledPkgWithBin(t *testing.T, projectDir, pkg, cmd string) {
	t.Helper()
	inst := filepath.Join(projectDir, ".fglpkg", "packages", pkg)
	if err := os.MkdirAll(inst, 0755); err != nil {
		t.Fatalf("mkdir installed pkg: %v", err)
	}
	writeRawManifest(t, inst, `{"name":"`+pkg+`","version":"1.0.0","bin":{"`+cmd+`":"run.sh"}}`+"\n")
	writeScript(t, filepath.Join(inst, "run.sh"))
}

func writeScript(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho hi\n"), 0755); err != nil {
		t.Fatalf("write script %s: %v", path, err)
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

// TestCmdRunList_ProjectListedBeforeInstalled: project rows are emitted ahead of
// installed (local) rows.
func TestCmdRunList_ProjectListedBeforeInstalled(t *testing.T) {
	dir := t.TempDir()
	writeProjectWithBin(t, dir, true)                        // project bin "greet"
	writeInstalledPkgWithBin(t, dir, "other", "othercmd")    // installed bin "othercmd"
	isolateGlobalStore(t)
	chdirTest(t, dir)

	out, err := captureStdout(t, cmdRunList)
	if err != nil {
		t.Fatalf("cmdRunList: %v", err)
	}
	pi, li := strings.Index(out, "project"), strings.Index(out, "local")
	if pi < 0 || li < 0 || pi > li {
		t.Fatalf("project rows must be listed before installed rows (project@%d local@%d):\n%s", pi, li, out)
	}
}

// TestCmdRunList_MarksShadowed: an installed command the project also defines is
// flagged, so the user can see which one wins.
func TestCmdRunList_MarksShadowed(t *testing.T) {
	dir := t.TempDir()
	writeProjectWithBin(t, dir, true)                 // project "greet"
	writeInstalledPkgWithBin(t, dir, "other", "greet") // installed also defines "greet"
	isolateGlobalStore(t)
	chdirTest(t, dir)

	out, err := captureStdout(t, cmdRunList)
	if err != nil {
		t.Fatalf("cmdRunList: %v", err)
	}
	if !strings.Contains(out, "shadowed by project") {
		t.Fatalf("the installed 'greet' the project also defines should be marked shadowed, got:\n%s", out)
	}
}

// TestFindBinCommand_ProjectFirst: a project's own bin resolves to the script
// under the project root, owned by the project, with source "project" (GIS-566).
func TestFindBinCommand_ProjectFirst(t *testing.T) {
	dir := t.TempDir()
	writeProjectWithBin(t, dir, true)
	isolateGlobalStore(t)
	chdirTest(t, dir)

	script, pkg, source, err := findBinCommand("greet")
	if err != nil {
		t.Fatalf("findBinCommand: %v", err)
	}
	if pkg != "myproj" || source != "project" {
		t.Fatalf("expected project ownership (myproj/project), got %q/%q", pkg, source)
	}
	// t.TempDir may be a symlinked path; match the tail rather than the absolute.
	if !strings.HasSuffix(script, filepath.Join("scripts", "greet.sh")) {
		t.Fatalf("expected the project's script path, got %q", script)
	}
}

// TestFindBinCommand_HonorsRoot: the project's bin script is found under the
// manifest's `root`, matching where `pack` stages it (finding 1).
func TestFindBinCommand_HonorsRoot(t *testing.T) {
	dir := t.TempDir()
	writeRawManifest(t, dir, `{"name":"myproj","version":"1.0.0","root":"src","files":["*.42m"],"bin":{"greet":"scripts/greet.sh"}}`+"\n")
	writeScript(t, filepath.Join(dir, "src", "scripts", "greet.sh"))
	// A decoy at the project root must NOT be picked when root is "src".
	writeScript(t, filepath.Join(dir, "scripts", "greet.sh"))
	isolateGlobalStore(t)
	chdirTest(t, dir)

	script, _, source, err := findBinCommand("greet")
	if err != nil {
		t.Fatalf("findBinCommand: %v", err)
	}
	if source != "project" {
		t.Fatalf("source: %q", source)
	}
	if !strings.HasSuffix(script, filepath.Join("src", "scripts", "greet.sh")) {
		t.Fatalf("expected the script under root 'src', got %q", script)
	}
}

// TestFindBinCommand_RejectsUnsafeBinPath: a bin script path that escapes the
// package is rejected (parity with pack/publish validation).
func TestFindBinCommand_RejectsUnsafeBinPath(t *testing.T) {
	dir := t.TempDir()
	writeRawManifest(t, dir, `{"name":"myproj","version":"1.0.0","bin":{"greet":"../greet.sh"}}`+"\n")
	isolateGlobalStore(t)
	chdirTest(t, dir)

	if _, _, _, err := findBinCommand("greet"); err == nil {
		t.Fatal("expected an error for an unsafe (escaping) bin script path")
	}
}

// TestFindBinCommand_ProjectBinMissingScript: a declared bin whose script is
// absent is a clear error — NOT a silent fall-through to an installed package
// that happens to define the same command.
func TestFindBinCommand_ProjectBinMissingScript(t *testing.T) {
	dir := t.TempDir()
	writeProjectWithBin(t, dir, false)                 // declares "greet", no script
	writeInstalledPkgWithBin(t, dir, "other", "greet") // installed pkg also defines "greet"
	isolateGlobalStore(t)
	chdirTest(t, dir)

	_, _, _, err := findBinCommand("greet")
	if err == nil {
		t.Fatal("a declared-but-missing project script must error, not fall through to the installed package")
	}
	if !strings.Contains(err.Error(), "project declares bin") {
		t.Fatalf("error should name the project bin, got: %v", err)
	}
}

// TestFindBinCommand_ProjectShadowsInstalled: when both the project and an
// installed package declare the same command, the project wins deterministically.
func TestFindBinCommand_ProjectShadowsInstalled(t *testing.T) {
	dir := t.TempDir()
	writeProjectWithBin(t, dir, true)                  // project "greet" (+ script)
	writeInstalledPkgWithBin(t, dir, "other", "greet") // installed "greet"
	isolateGlobalStore(t)
	chdirTest(t, dir)

	_, pkg, source, err := findBinCommand("greet")
	if err != nil {
		t.Fatalf("findBinCommand: %v", err)
	}
	if pkg != "myproj" || source != "project" {
		t.Fatalf("the project's bin must win over the installed package, got %q/%q", pkg, source)
	}
}
