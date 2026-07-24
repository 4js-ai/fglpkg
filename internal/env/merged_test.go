package env

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// envTestWrite writes content to rel (relative to cwd), creating parents.
func envTestWrite(t *testing.T, rel, content string) {
	t.Helper()
	full := filepath.FromSlash(rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// chdirTemp switches cwd to a fresh temp dir for the duration of the test.
func chdirTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	return dir
}

const materializedManifest = `{ "name": "%s", "version": "1.0.0", "generoPackages": ["%s"], "dependencies": { "fgl": {} } }`
const legacyManifest = `{ "name": "%s", "version": "1.0.0", "dependencies": { "fgl": {} } }`

// TestBuildFGLLDPATHEmitsMergedRootForMaterializedPackage: a package that
// recorded generoPackages is represented by the single merged-root entry, not a
// per-package store entry.
func TestBuildFGLLDPATHEmitsMergedRootForMaterializedPackage(t *testing.T) {
	chdirTemp(t)
	envTestWrite(t, ".fglpkg/merged/com/fourjs/db/DbConnection.42m", "DB")
	envTestWrite(t, ".fglpkg/packages/dbconnection/fglpkg.json",
		`{ "name": "dbconnection", "version": "1.0.0", "generoPackages": ["com.fourjs.db"], "dependencies": { "fgl": {} } }`)

	g := New(t.TempDir()) // empty global home
	got, err := g.BuildFGLLDPATH()
	if err != nil {
		t.Fatalf("BuildFGLLDPATH: %v", err)
	}

	mergedAbs, _ := filepath.Abs(filepath.Join(".fglpkg", "merged"))
	storeAbs, _ := filepath.Abs(filepath.Join(".fglpkg", "packages", "dbconnection"))
	if !strings.Contains(got, mergedAbs) {
		t.Errorf("FGLLDPATH %q should contain merged root %q", got, mergedAbs)
	}
	if strings.Contains(got, storeAbs) {
		t.Errorf("FGLLDPATH %q should NOT contain the store dir for a materialized package", got)
	}
}

// TestBuildFGLLDPATHKeepsPerPackageForLegacy: a package with no recorded
// generoPackages keeps its historical per-package entry.
func TestBuildFGLLDPATHKeepsPerPackageForLegacy(t *testing.T) {
	chdirTemp(t)
	envTestWrite(t, ".fglpkg/packages/legacy/fglpkg.json",
		`{ "name": "legacy", "version": "1.0.0", "dependencies": { "fgl": {} } }`)
	envTestWrite(t, ".fglpkg/packages/legacy/com/acme/Lib.42m", "LIB")

	g := New(t.TempDir())
	got, err := g.BuildFGLLDPATH()
	if err != nil {
		t.Fatalf("BuildFGLLDPATH: %v", err)
	}
	storeAbs, _ := filepath.Abs(filepath.Join(".fglpkg", "packages", "legacy"))
	if !strings.Contains(got, storeAbs) {
		t.Errorf("FGLLDPATH %q should contain the legacy per-package store %q", got, storeAbs)
	}
}

// TestBuildFGLLDPATHMixed: merged root + a materialized package (covered) + a
// legacy package (per-package), with the merged root ordered first.
func TestBuildFGLLDPATHMixed(t *testing.T) {
	chdirTemp(t)
	envTestWrite(t, ".fglpkg/merged/com/fourjs/db/DbConnection.42m", "DB")
	envTestWrite(t, ".fglpkg/packages/dbconnection/fglpkg.json",
		`{ "name": "dbconnection", "version": "1.0.0", "generoPackages": ["com.fourjs.db"], "dependencies": { "fgl": {} } }`)
	envTestWrite(t, ".fglpkg/packages/legacy/fglpkg.json",
		`{ "name": "legacy", "version": "1.0.0", "dependencies": { "fgl": {} } }`)

	g := New(t.TempDir())
	got, err := g.BuildFGLLDPATH()
	if err != nil {
		t.Fatalf("BuildFGLLDPATH: %v", err)
	}
	mergedAbs, _ := filepath.Abs(filepath.Join(".fglpkg", "merged"))
	legacyAbs, _ := filepath.Abs(filepath.Join(".fglpkg", "packages", "legacy"))
	dbAbs, _ := filepath.Abs(filepath.Join(".fglpkg", "packages", "dbconnection"))

	if strings.Contains(got, dbAbs) {
		t.Errorf("materialized package store must not appear: %q", got)
	}
	iMerged := strings.Index(got, mergedAbs)
	iLegacy := strings.Index(got, legacyAbs)
	if iMerged < 0 || iLegacy < 0 {
		t.Fatalf("expected both merged (%q) and legacy (%q) in %q", mergedAbs, legacyAbs, got)
	}
	if iMerged > iLegacy {
		t.Errorf("merged root should precede the legacy per-package entry: %q", got)
	}
}

// TestBuildFGLLDPATHSkipsEmptyMergedRoot: an empty merged dir is not emitted.
func TestBuildFGLLDPATHSkipsEmptyMergedRoot(t *testing.T) {
	chdirTemp(t)
	if err := os.MkdirAll(filepath.Join(".fglpkg", "merged"), 0o755); err != nil {
		t.Fatalf("mkdir merged: %v", err)
	}
	envTestWrite(t, ".fglpkg/packages/legacy/fglpkg.json",
		`{ "name": "legacy", "version": "1.0.0", "dependencies": { "fgl": {} } }`)

	g := New(t.TempDir())
	got, err := g.BuildFGLLDPATH()
	if err != nil {
		t.Fatalf("BuildFGLLDPATH: %v", err)
	}
	mergedAbs, _ := filepath.Abs(filepath.Join(".fglpkg", "merged"))
	if strings.Contains(got, mergedAbs) {
		t.Errorf("empty merged root should not be emitted: %q", got)
	}
}

// TestGenerateGSTEmitsMergedRoot: GST output uses the $(ProjectDir)-relative
// merged path for a materialized package.
func TestGenerateGSTEmitsMergedRoot(t *testing.T) {
	chdirTemp(t)
	envTestWrite(t, ".fglpkg/merged/com/fourjs/db/DbConnection.42m", "DB")
	envTestWrite(t, ".fglpkg/packages/dbconnection/fglpkg.json",
		`{ "name": "dbconnection", "version": "1.0.0", "generoPackages": ["com.fourjs.db"], "dependencies": { "fgl": {} } }`)

	g := New(t.TempDir())
	lines, err := g.GenerateGST()
	if err != nil {
		t.Fatalf("GenerateGST: %v", err)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "$(ProjectDir)/.fglpkg/merged") {
		t.Errorf("GST FGLLDPATH should reference the merged root:\n%s", joined)
	}
	if strings.Contains(joined, "$(ProjectDir)/.fglpkg/packages/dbconnection") {
		t.Errorf("GST FGLLDPATH should not reference a materialized package store:\n%s", joined)
	}
}

// TestGenerateGlobalEmitsMergedRoot: the global-only view emits the global
// merged root for a materialized global package.
func TestGenerateGlobalEmitsMergedRoot(t *testing.T) {
	globalHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(globalHome, "merged", "org", "util"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(globalHome, "merged", "org", "util", "Strings.42m"), []byte("S"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	pkgDir := filepath.Join(globalHome, "packages", "strutils")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "fglpkg.json"),
		[]byte(`{ "name": "strutils", "version": "1.0.0", "generoPackages": ["org.util"], "dependencies": { "fgl": {} } }`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	g := New(globalHome)
	lines, err := g.GenerateGlobal()
	if err != nil {
		t.Fatalf("GenerateGlobal: %v", err)
	}
	joined := strings.Join(lines, "\n")
	mergedAbs := filepath.Join(globalHome, "merged")
	if !strings.Contains(joined, mergedAbs) {
		t.Errorf("global FGLLDPATH should contain the global merged root %q:\n%s", mergedAbs, joined)
	}
	if strings.Contains(joined, pkgDir) {
		t.Errorf("global FGLLDPATH should not contain a materialized package store:\n%s", joined)
	}
}
