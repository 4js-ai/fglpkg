package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// writeLintProject materializes files under a fresh temp dir and Chdirs into
// it, so the cwd-relative staging walks (buildPackageZip, loadIgnore, …) run
// against the fixture. Returns the project dir.
func writeLintProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	return dir
}

// loadLintReport loads the fixture manifest and runs the full lint pass.
func loadLintReport(t *testing.T) *manifest.Report {
	t.Helper()
	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return lintManifest(m, ".")
}

// warningFields collects the Field of every warning diagnostic.
func warningFields(r *manifest.Report) []string {
	var out []string
	for _, d := range r.Warnings() {
		out = append(out, d.Field)
	}
	return out
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestLintCleanManifest(t *testing.T) {
	writeLintProject(t, map[string]string{
		"fglpkg.json": `{
  "name": "clean",
  "version": "1.0.0",
  "description": "A clean package",
  "license": "MIT",
  "repository": "https://github.com/acme/clean",
  "author": "Acme",
  "dependencies": { "fgl": {} }
}`,
		"Main.42m": "MAIN\nEND MAIN\n",
	})
	r := loadLintReport(t)
	if r.HasErrors() {
		t.Errorf("clean manifest should have no errors, got %+v", r.Errors())
	}
	if len(r.Warnings()) != 0 {
		t.Errorf("clean manifest should have no warnings, got %+v", r.Warnings())
	}
}

func TestLintZeroMatchFilesWarning(t *testing.T) {
	writeLintProject(t, map[string]string{
		"fglpkg.json": `{
  "name": "zm",
  "version": "1.0.0",
  "files": ["*.42m", "*.42x"],
  "dependencies": { "fgl": {} }
}`,
		"Main.42m": "MAIN\nEND MAIN\n",
	})
	r := loadLintReport(t)
	if r.HasErrors() {
		t.Fatalf("expected no errors (a .42m is present), got %+v", r.Errors())
	}
	if !contains(warningFields(r), "files") {
		t.Errorf("expected a zero-match files warning for *.42x, got %+v", r.Warnings())
	}
}

// TestLintEmptyPackageWarning: a package whose archive would hold nothing but
// fglpkg.json and docs is a WARNING, not an error — it stays publishable via
// `fglpkg publish --allow-empty`, so `fglpkg lint` flags it loudly but exits 0
// (GIS-276). This replaces the old BDL-only "no modules" hard error.
func TestLintEmptyPackageWarning(t *testing.T) {
	writeLintProject(t, map[string]string{
		"fglpkg.json": `{
  "name": "empty",
  "version": "1.0.0",
  "files": ["*.42m"],
  "dependencies": { "fgl": {} }
}`,
		"README.md": "# empty\n",
	})
	r := loadLintReport(t)
	if r.HasErrors() {
		t.Fatalf("an asset-less package is a warning, not an error, got errors: %+v", r.Errors())
	}
	var msg string
	for _, d := range r.Warnings() {
		msg += d.Message
	}
	if !strings.Contains(msg, "no assets") {
		t.Errorf("warning should mention no assets, got: %s", msg)
	}
}

// TestLintFlagsDroppedBDLSource pins the GIS-276-review fix: the kind-agnostic
// empty guard cannot see a dropped-modules mistake once any OTHER asset exists,
// so lint must still error when BDL source is present in the tree but none of it
// was staged. The check is keyed on BDL intent in the tree (not package kind),
// so a legitimately BDL-free bin/include package and a pure-webcomponent package
// with a stray example source both stay quiet.
func TestLintFlagsDroppedBDLSource(t *testing.T) {
	const meta = `"description":"d","license":"MIT","repository":"https://github.com/x/y","author":"a"`

	t.Run("bin asset masks dropped BDL source -> error", func(t *testing.T) {
		writeLintProject(t, map[string]string{
			// No root/files: the default *.42m glob matches nothing, so src/Main.4gl
			// (uncompiled source) never stages, yet deploy.sh keeps it non-empty.
			"fglpkg.json":  `{"name":"c.demo","version":"1.0.0",` + meta + `,"bin":{"deploy":"deploy.sh"}}`,
			"src/Main.4gl": "MAIN\nEND MAIN\n",
			"deploy.sh":    "#!/bin/sh\necho hi\n",
		})
		r := loadLintReport(t)
		if !r.HasErrors() {
			t.Fatalf("BDL source present but none staged must error, got %+v", r.Diagnostics)
		}
		var msg string
		for _, d := range r.Errors() {
			msg += d.Message
		}
		if !strings.Contains(msg, "BDL source") {
			t.Errorf("error should name the dropped BDL source, got: %s", msg)
		}
	})

	t.Run("legit bin-only package with no BDL source stays quiet", func(t *testing.T) {
		writeLintProject(t, map[string]string{
			"fglpkg.json": `{"name":"b.demo","version":"1.0.0",` + meta + `,"bin":{"deploy":"deploy.sh"}}`,
			"deploy.sh":   "#!/bin/sh\necho hi\n",
		})
		r := loadLintReport(t)
		if r.HasErrors() {
			t.Errorf("a bin-only package with no BDL source must not error, got %+v", r.Errors())
		}
	})

	t.Run("pure-webcomponent package with a stray example .4gl stays quiet", func(t *testing.T) {
		writeLintProject(t, map[string]string{
			"fglpkg.json":                    `{"name":"w.demo","version":"1.0.0",` + meta + `,"webcomponents":["Chart"]}`,
			"webcomponents/Chart/Chart.html": "<html></html>\n",
			"examples/demo.4gl":              "MAIN\nEND MAIN\n",
		})
		r := loadLintReport(t)
		if r.HasErrors() {
			t.Errorf("a pure-webcomponent package must stay exempt, got %+v", r.Errors())
		}
	})

	t.Run("staged BDL module -> no error", func(t *testing.T) {
		writeLintProject(t, map[string]string{
			"fglpkg.json": `{"name":"ok.demo","version":"1.0.0",` + meta + `,"files":["*.42m"]}`,
			"Main.42m":    "MAIN\nEND MAIN\n",
		})
		r := loadLintReport(t)
		if r.HasErrors() {
			t.Errorf("a package that stages a .42m must not error, got %+v", r.Errors())
		}
	})

	t.Run("ignored BDL source does not count as dropped", func(t *testing.T) {
		writeLintProject(t, map[string]string{
			"fglpkg.json":   `{"name":"i.demo","version":"1.0.0",` + meta + `,"bin":{"deploy":"deploy.sh"}}`,
			"deploy.sh":     "#!/bin/sh\necho hi\n",
			".fglpkgignore": "src/\n",
			"src/Main.4gl":  "MAIN\nEND MAIN\n",
		})
		r := loadLintReport(t)
		if r.HasErrors() {
			t.Errorf("BDL source excluded by .fglpkgignore must not count as dropped, got %+v", r.Errors())
		}
	})
}

func TestLintUnresolvedProgramWarning(t *testing.T) {
	writeLintProject(t, map[string]string{
		"fglpkg.json": `{
  "name": "prog",
  "version": "1.0.0",
  "programs": ["Main", "Missing"],
  "dependencies": { "fgl": {} }
}`,
		"Main.42m": "MAIN\nEND MAIN\n",
	})
	r := loadLintReport(t)
	if r.HasErrors() {
		t.Fatalf("expected no errors, got %+v", r.Errors())
	}
	if !contains(warningFields(r), "programs") {
		t.Errorf("expected an unresolved-program warning for Missing, got %+v", r.Warnings())
	}
}

func TestLintNonexistentRootWarning(t *testing.T) {
	writeLintProject(t, map[string]string{
		"fglpkg.json": `{
  "name": "badroot",
  "version": "1.0.0",
  "root": "nope",
  "dependencies": { "fgl": {} }
}`,
	})
	r := loadLintReport(t)
	if !contains(warningFields(r), "root") {
		t.Errorf("expected a nonexistent-root warning, got %+v", r.Warnings())
	}
	// A missing root that breaks the BDL staging walk must ALSO surface as a
	// blocking error, so `lint` fails and pack/publish refuse — rather than the
	// old behaviour where lint warned, exited 0, and pack then died on its own
	// build. See lintProject: the root check no longer short-circuits the build.
	if !r.HasErrors() {
		t.Errorf("a missing root must be a blocking error, not just a warning; got %+v", r.Diagnostics)
	}
}

// TestLintFriendlyTypeErrorSurfaced verifies the GIS-269 friendly error (a
// scalar where an array is expected) reaches the user through `fglpkg lint`
// with a non-zero exit, not a raw json message.
func TestLintFriendlyTypeErrorSurfaced(t *testing.T) {
	writeLintProject(t, map[string]string{
		"fglpkg.json": `{
  "name": "typeerr",
  "version": "1.0.0",
  "docs": "README.md"
}`,
	})
	out, err := captureStdout(t, func() error { return cmdLint(nil) })
	if err == nil {
		t.Fatal("cmdLint should return a non-nil error for an invalid manifest")
	}
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 {
		t.Errorf("expected ExitError{Code:1}, got %v", err)
	}
	if !strings.Contains(out, "docs") || strings.Contains(out, "cannot unmarshal") {
		t.Errorf("expected a friendly field-named docs error, got:\n%s", out)
	}
}

// TestPackFlagsEmptyPackage confirms `pack --list` no longer REFUSES an
// asset-less package (that is publish's job, via --allow-empty) but flags it
// inline with the listing so the user sees the problem while inspecting the zip
// (GIS-276).
func TestPackFlagsEmptyPackage(t *testing.T) {
	t.Setenv("FGLPKG_GENERO_VERSION", "6.00.01")
	writeLintProject(t, map[string]string{
		"fglpkg.json": `{
  "name": "empty",
  "version": "1.0.0",
  "files": ["*.42m"],
  "dependencies": { "fgl": {} }
}`,
		"README.md": "# empty\n",
	})
	out, err := captureStdout(t, func() error { return cmdPack([]string{"--list"}) })
	if err != nil {
		t.Fatalf("pack --list should succeed and flag (not refuse) an empty package, got: %v", err)
	}
	if !strings.Contains(out, "no assets") {
		t.Errorf("pack --list should flag the empty package on stdout, got:\n%s", out)
	}
}

func TestLintRejectsArguments(t *testing.T) {
	writeLintProject(t, map[string]string{
		"fglpkg.json": `{"name":"x","version":"1.0.0"}`,
	})
	if err := cmdLint([]string{"extra"}); err == nil {
		t.Error("cmdLint should reject positional arguments")
	}
}
