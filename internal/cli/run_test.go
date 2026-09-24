package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStderr runs fn with os.Stderr redirected to a pipe and returns what was
// written (mirrors captureStdout in info_test.go).
func captureStderr(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	errCh := make(chan error, 1)
	go func() { errCh <- fn() }()
	fnErr := <-errCh
	_ = w.Close()
	os.Stderr = orig
	out, _ := io.ReadAll(r)
	_ = r.Close()
	return string(out), fnErr
}

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

// writeInstalledPkgWithRootBin hand-places an installed package that sets
// `root`, with its bin script under that root — the layout `pack` produces and
// `PublishCopy` records (GIS-569).
func writeInstalledPkgWithRootBin(t *testing.T, projectDir, pkg, cmd, root string) string {
	t.Helper()
	inst := filepath.Join(projectDir, ".fglpkg", "packages", pkg)
	if err := os.MkdirAll(inst, 0755); err != nil {
		t.Fatalf("mkdir installed pkg: %v", err)
	}
	writeRawManifest(t, inst, `{"name":"`+pkg+`","version":"1.0.0","root":"`+root+`","bin":{"`+cmd+`":"scripts/run.sh"}}`+"\n")
	script := filepath.Join(inst, root, "scripts", "run.sh")
	writeScript(t, script)
	return script
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
	writeProjectWithBin(t, dir, true)                     // project bin "greet"
	writeInstalledPkgWithBin(t, dir, "other", "othercmd") // installed bin "othercmd"
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
	writeProjectWithBin(t, dir, true)                  // project "greet"
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
// package is rejected (parity with pack/publish validation). The escaping target
// actually exists, so a dropped escape check would resolve+run it — the test
// asserts the specific "escape" error, not merely that some error occurred.
func TestFindBinCommand_RejectsUnsafeBinPath(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "proj")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	writeRawManifest(t, dir, `{"name":"myproj","version":"1.0.0","bin":{"greet":"../greet.sh"}}`+"\n")
	writeScript(t, filepath.Join(base, "greet.sh")) // the escaping target really exists
	isolateGlobalStore(t)
	chdirTest(t, dir)

	_, _, _, err := findBinCommand("greet")
	if err == nil || !strings.Contains(err.Error(), "escape") {
		t.Fatalf("expected an unsafe-path (escape) error, got: %v", err)
	}
}

// TestRun_NoWarnWhenNoManifest: isProjectDir() is true for a bare .fglpkg/ with
// no manifest (e.g. ~/.fglpkg in $HOME), and a missing manifest must not warn —
// otherwise every `run` from home prints a spurious warning (GIS-566 review).
func TestRun_NoWarnWhenNoManifest(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".fglpkg"), 0755); err != nil {
		t.Fatalf("mkdir .fglpkg: %v", err)
	}
	isolateGlobalStore(t)
	chdirTest(t, dir)

	// run --list (its "No commands available" goes to real stdout — harmless).
	stderr, err := captureStderr(t, cmdRunList)
	if err != nil {
		t.Fatalf("cmdRunList: %v", err)
	}
	if strings.Contains(stderr, "cannot read project") {
		t.Fatalf("a missing manifest must not warn on run --list, got stderr:\n%s", stderr)
	}

	// run <cmd> (findBinCommand returns a not-found error; we only check stderr).
	stderr, _ = captureStderr(t, func() error {
		_, _, _, e := findBinCommand("nosuchtool")
		return e
	})
	if strings.Contains(stderr, "cannot read project") {
		t.Fatalf("a missing manifest must not warn on run <cmd>, got stderr:\n%s", stderr)
	}
}

// TestCmdRunList_WarnsOnMalformedManifest: an unreadable/invalid manifest is
// still surfaced (the guard suppresses only a *missing* file).
func TestCmdRunList_WarnsOnMalformedManifest(t *testing.T) {
	dir := t.TempDir()
	writeRawManifest(t, dir, `{"name":"x","version":"1.0.0",}`+"\n") // trailing comma -> parse error
	isolateGlobalStore(t)
	chdirTest(t, dir)

	stderr, err := captureStderr(t, cmdRunList)
	if err != nil {
		t.Fatalf("cmdRunList: %v", err)
	}
	if !strings.Contains(stderr, "cannot read project") {
		t.Fatalf("a malformed manifest should warn, got stderr:\n%s", stderr)
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

// TestFindBinCommand_InstalledHonorsRoot: an installed package that sets `root`
// keeps its bin script under that root, so resolution must join it — without
// this, `run --list` advertised the command while `run <cmd>` reported it "not
// found in any installed package" (GIS-569).
func TestFindBinCommand_InstalledHonorsRoot(t *testing.T) {
	dir := t.TempDir()
	writeRawManifest(t, dir, `{"name":"myproj","version":"1.0.0"}`+"\n") // project declares no bin
	want := writeInstalledPkgWithRootBin(t, dir, "tool", "dothing", "src")
	// A decoy at the package root must NOT be picked when root is "src".
	writeScript(t, filepath.Join(dir, ".fglpkg", "packages", "tool", "scripts", "run.sh"))
	isolateGlobalStore(t)
	chdirTest(t, dir)

	script, pkg, source, err := findBinCommand("dothing")
	if err != nil {
		t.Fatalf("findBinCommand: %v", err)
	}
	if pkg != "tool" || source != "installed" {
		t.Fatalf("expected the installed package to own the command, got %q/%q", pkg, source)
	}
	if !strings.HasSuffix(script, filepath.Join("src", "scripts", "run.sh")) {
		t.Fatalf("expected the script under the package root 'src', got %q (want suffix of %s)", script, want)
	}
}

// TestFindBinCommand_InstalledRejectsUnsafeBinPath: an installed manifest comes
// from a registry, so a bin path that escapes the package is skipped — the
// escaping target exists, so a dropped check would resolve and run it.
func TestFindBinCommand_InstalledRejectsUnsafeBinPath(t *testing.T) {
	dir := t.TempDir()
	writeRawManifest(t, dir, `{"name":"myproj","version":"1.0.0"}`+"\n")
	inst := filepath.Join(dir, ".fglpkg", "packages", "tool")
	if err := os.MkdirAll(inst, 0755); err != nil {
		t.Fatalf("mkdir installed pkg: %v", err)
	}
	writeRawManifest(t, inst, `{"name":"tool","version":"1.0.0","bin":{"dothing":"../../../outside.sh"}}`+"\n")
	writeScript(t, filepath.Join(dir, "outside.sh")) // the escaping target really exists
	isolateGlobalStore(t)
	chdirTest(t, dir)

	script, _, _, err := findBinCommand("dothing")
	if err == nil {
		t.Fatalf("an escaping installed bin path must not resolve, got script %q", script)
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("the package should be skipped, leaving a plain not-found error, got: %v", err)
	}
}

// TestFindBinCommand_InstalledRejectsUnsafeRoot: the run-side twin of the
// installer test — an installed manifest whose `root` escapes the package must
// not resolve to a file outside it, even though its `bin` path is itself
// harmless (PR #87 review). The escaping target exists and is executable, so a
// dropped check would resolve and run it.
func TestFindBinCommand_InstalledRejectsUnsafeRoot(t *testing.T) {
	dir := t.TempDir()
	writeRawManifest(t, dir, `{"name":"myproj","version":"1.0.0"}`+"\n")
	inst := filepath.Join(dir, ".fglpkg", "packages", "demo-pkg")
	if err := os.MkdirAll(inst, 0755); err != nil {
		t.Fatalf("mkdir installed pkg: %v", err)
	}
	// root escapes back to the consumer's project directory; the bin path itself
	// is an innocent "victim.sh".
	writeRawManifest(t, inst, `{"name":"demo-pkg","version":"1.0.0","root":"../../..","bin":{"greet":"victim.sh"}}`+"\n")
	writeScript(t, filepath.Join(dir, "victim.sh")) // exists, and is executable
	isolateGlobalStore(t)
	chdirTest(t, dir)

	script, _, _, err := findBinCommand("greet")
	if err == nil {
		t.Fatalf("an escaping root must not resolve, got script %q", script)
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("the package should be skipped, leaving a plain not-found error, got: %v", err)
	}
}
