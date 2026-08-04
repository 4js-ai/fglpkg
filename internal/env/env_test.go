package env

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/classpath"
)

// TestGenerateGWAEmitsFlags verifies --gwa output: one --webcomponent flag
// per COMPONENTTYPE directory under .fglpkg/webcomponents/.
func TestGenerateGWAEmitsFlags(t *testing.T) {
	projectDir := t.TempDir()
	mustMkdir(t, filepath.Join(projectDir, ".fglpkg", "webcomponents", "3DChart"))
	mustMkdir(t, filepath.Join(projectDir, ".fglpkg", "webcomponents", "Heatmap"))

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	g := New(t.TempDir()) // separate global home, empty
	flags, err := g.GenerateGWA()
	if err != nil {
		t.Fatalf("GenerateGWA: %v", err)
	}
	if len(flags) != 2 {
		t.Fatalf("expected 2 --webcomponent flags, got %d: %v", len(flags), flags)
	}
	for _, f := range flags {
		if !strings.HasPrefix(f, "--webcomponent ") {
			t.Errorf("expected --webcomponent prefix, got %q", f)
		}
	}
	joined := strings.Join(flags, "\n")
	if !strings.Contains(joined, "3DChart") || !strings.Contains(joined, "Heatmap") {
		t.Errorf("expected both component names in output: %s", joined)
	}
}

// TestGenerateLocalIncludesFGLIMAGEPATH verifies that the local-scope env
// output prepends the project's .fglpkg/ directory onto FGLIMAGEPATH when
// at least one webcomponent is installed.
func TestGenerateLocalIncludesFGLIMAGEPATH(t *testing.T) {
	projectDir := t.TempDir()
	mustMkdir(t, filepath.Join(projectDir, ".fglpkg", "webcomponents", "MyWidget"))

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	g := New(t.TempDir())
	exports, err := g.GenerateLocal()
	if err != nil {
		t.Fatalf("GenerateLocal: %v", err)
	}
	joined := strings.Join(exports, "\n")
	// envValue rather than a raw "FGLIMAGEPATH=" substring: the question is "was
	// the variable emitted", and the substring form silently misses PowerShell's
	// `$env:FGLIMAGEPATH = ` spelling.
	if _, ok := envValue(t, exports, "FGLIMAGEPATH"); !ok {
		t.Errorf("expected FGLIMAGEPATH export in:\n%s", joined)
	}
	if !strings.Contains(joined, "WEB_COMPONENT_DIRECTORY") {
		t.Errorf("expected GAS hint comment in:\n%s", joined)
	}
}

// TestGenerateLocalSkipsFGLIMAGEPATHWhenEmpty verifies that no FGLIMAGEPATH
// line is emitted when there are no webcomponents installed.
func TestGenerateLocalSkipsFGLIMAGEPATHWhenEmpty(t *testing.T) {
	projectDir := t.TempDir()
	// No webcomponents dir at all.

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	g := New(t.TempDir())
	exports, err := g.GenerateLocal()
	if err != nil {
		t.Fatalf("GenerateLocal: %v", err)
	}
	for _, line := range exports {
		if strings.Contains(line, "FGLIMAGEPATH") {
			t.Errorf("unexpected FGLIMAGEPATH line when no webcomponents installed: %q", line)
		}
	}
}

// TestGenerateGSTIncludesFGLIMAGEPATH verifies the Genero Studio env output
// emits FGLIMAGEPATH (pointing at $(ProjectDir)/.fglpkg) when a webcomponent
// is installed locally (GIS-293).
func TestGenerateGSTIncludesFGLIMAGEPATH(t *testing.T) {
	projectDir := t.TempDir()
	mustMkdir(t, filepath.Join(projectDir, ".fglpkg", "webcomponents", "MyWidget"))

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	g := New(t.TempDir())
	exports, err := g.GenerateGST()
	if err != nil {
		t.Fatalf("GenerateGST: %v", err)
	}
	joined := strings.Join(exports, "\n")
	if !strings.Contains(joined, "FGLIMAGEPATH=$(ProjectDir)/.fglpkg;$(FGLIMAGEPATH)") {
		t.Errorf("expected FGLIMAGEPATH GST line in:\n%s", joined)
	}
}

// TestGenerateGSTSkipsFGLIMAGEPATHWhenEmpty verifies no FGLIMAGEPATH line is
// emitted for GST when there are no local webcomponents.
func TestGenerateGSTSkipsFGLIMAGEPATHWhenEmpty(t *testing.T) {
	projectDir := t.TempDir()
	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	g := New(t.TempDir())
	exports, err := g.GenerateGST()
	if err != nil {
		t.Fatalf("GenerateGST: %v", err)
	}
	for _, line := range exports {
		if strings.Contains(line, "FGLIMAGEPATH") {
			t.Errorf("unexpected FGLIMAGEPATH line: %q", line)
		}
	}
}

// TestGenerateGlobalIsGlobalOnly verifies that --global output (GenerateGlobal)
// emits only the global home's packages and never merges in the current
// project's local .fglpkg/ packages (GIS-290).
func TestGenerateGlobalIsGlobalOnly(t *testing.T) {
	globalHome := t.TempDir()
	mustMkdir(t, filepath.Join(globalHome, "packages", "globalpkg"))

	projectDir := t.TempDir()
	mustMkdir(t, filepath.Join(projectDir, ".fglpkg", "packages", "localpkg"))

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	g := New(globalHome)
	exports, err := g.GenerateGlobal()
	if err != nil {
		t.Fatalf("GenerateGlobal: %v", err)
	}
	joined := strings.Join(exports, "\n")
	if !strings.Contains(joined, "globalpkg") {
		t.Errorf("expected global package on FGLLDPATH in:\n%s", joined)
	}
	if strings.Contains(joined, "localpkg") {
		t.Errorf("--global must not merge local project packages, but got:\n%s", joined)
	}
}

// TestGenerateLocalClasspathIsAnchorJarNotEnumeratedJars verifies the
// CLASSPATH export line for a project with multiple jars is the single
// anchor jar path, never the individual jar paths themselves. The anchor is
// laid down by classpath.Sync (what install does); env only references it.
func TestGenerateLocalClasspathIsAnchorJarNotEnumeratedJars(t *testing.T) {
	projectDir := t.TempDir()
	jarsDir := filepath.Join(projectDir, ".fglpkg", "jars")
	mustMkdir(t, jarsDir)
	mustWriteFile(t, filepath.Join(jarsDir, "guava-33.2.1-jre.jar"), "guava")
	mustWriteFile(t, filepath.Join(jarsDir, "jackson-core-2.17.2.jar"), "jackson")
	mustSyncAnchor(t, jarsDir)

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	// Resolve the expected path AFTER chdir (via os.Getwd, matching what
	// filepath.Abs(".") sees internally) rather than from the pre-chdir
	// jarsDir string — on macOS t.TempDir() and the post-chdir cwd can
	// differ by the /var vs /private/var symlink, which would otherwise
	// make an exact-string comparison fail despite both denoting the same
	// real directory.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	wantAnchor := filepath.Join(cwd, ".fglpkg", "jars", classpath.AnchorName)

	g := New(t.TempDir())
	exports, err := g.GenerateLocal()
	if err != nil {
		t.Fatalf("GenerateLocal: %v", err)
	}
	joined := strings.Join(exports, "\n")
	if !strings.Contains(joined, "CLASSPATH="+wantAnchor) {
		t.Errorf("expected CLASSPATH to point at the anchor jar %q, got:\n%s", wantAnchor, joined)
	}
	if strings.Contains(joined, "guava-33.2.1-jre.jar") || strings.Contains(joined, "jackson-core-2.17.2.jar") {
		t.Errorf("CLASSPATH must not enumerate individual jars, got:\n%s", joined)
	}
}

// TestGenerateClasspathMergesLocalAndGlobalAnchors verifies Generate()
// (the merged local+global scope used by `fglpkg bdl` etc.) emits both
// anchor jars — local first — when both scopes have jars, never the
// individual jar paths from either.
func TestGenerateClasspathMergesLocalAndGlobalAnchors(t *testing.T) {
	globalHome := t.TempDir()
	mustMkdir(t, filepath.Join(globalHome, "jars"))
	mustWriteFile(t, filepath.Join(globalHome, "jars", "global-dep.jar"), "g")
	mustSyncAnchor(t, filepath.Join(globalHome, "jars"))

	projectDir := t.TempDir()
	localJars := filepath.Join(projectDir, ".fglpkg", "jars")
	mustMkdir(t, localJars)
	mustWriteFile(t, filepath.Join(localJars, "local-dep.jar"), "l")
	mustSyncAnchor(t, localJars)

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	// See the symlink-resolution note in
	// TestGenerateLocalClasspathIsAnchorJarNotEnumeratedJars: resolve the
	// local anchor's expected path via the post-chdir cwd, not the
	// pre-chdir projectDir/localJars strings.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	localAnchor := filepath.Join(cwd, ".fglpkg", "jars", classpath.AnchorName)
	globalAnchor := filepath.Join(globalHome, "jars", classpath.AnchorName)

	g := New(globalHome)
	exports, err := g.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	joined := strings.Join(exports, "\n")
	if !strings.Contains(joined, "CLASSPATH="+localAnchor+string(os.PathListSeparator)+globalAnchor) {
		t.Errorf("expected CLASSPATH=<local anchor>%c<global anchor>, got:\n%s", os.PathListSeparator, joined)
	}
	if strings.Contains(joined, "local-dep.jar") || strings.Contains(joined, "global-dep.jar") {
		t.Errorf("CLASSPATH must not enumerate individual jars, got:\n%s", joined)
	}
}

// TestGenerateGlobalClasspathIsAnchorJar mirrors the local-scope anchor
// test for GenerateGlobal (--global).
func TestGenerateGlobalClasspathIsAnchorJar(t *testing.T) {
	globalHome := t.TempDir()
	jarsDir := filepath.Join(globalHome, "jars")
	mustMkdir(t, jarsDir)
	mustWriteFile(t, filepath.Join(jarsDir, "guava.jar"), "guava")
	mustSyncAnchor(t, jarsDir)

	g := New(globalHome)
	exports, err := g.GenerateGlobal()
	if err != nil {
		t.Fatalf("GenerateGlobal: %v", err)
	}
	joined := strings.Join(exports, "\n")
	if !strings.Contains(joined, "CLASSPATH="+filepath.Join(jarsDir, classpath.AnchorName)) {
		t.Errorf("expected CLASSPATH to point at the anchor jar, got:\n%s", joined)
	}
	if strings.Contains(joined, "guava.jar") {
		t.Errorf("CLASSPATH must not enumerate individual jars, got:\n%s", joined)
	}
}

// TestGenerateGSTClasspathIsAnchorJar verifies the Genero Studio CLASSPATH
// line references the anchor jar via its $(ProjectDir)-relative form.
func TestGenerateGSTClasspathIsAnchorJar(t *testing.T) {
	projectDir := t.TempDir()
	jarsDir := filepath.Join(projectDir, ".fglpkg", "jars")
	mustMkdir(t, jarsDir)
	mustWriteFile(t, filepath.Join(jarsDir, "guava.jar"), "guava")
	mustSyncAnchor(t, jarsDir)

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	g := New(t.TempDir())
	exports, err := g.GenerateGST()
	if err != nil {
		t.Fatalf("GenerateGST: %v", err)
	}
	joined := strings.Join(exports, "\n")
	want := "CLASSPATH=$(ProjectDir)/.fglpkg/jars/" + classpath.AnchorName + ";$(CLASSPATH)"
	if !strings.Contains(joined, want) {
		t.Errorf("expected %q in:\n%s", want, joined)
	}
	if strings.Contains(joined, "guava.jar;") {
		t.Errorf("CLASSPATH must not enumerate individual jars, got:\n%s", joined)
	}
}

// TestGenerateLocalClasspathEmptyWhenNoJars verifies no CLASSPATH line is
// emitted (and no anchor jar created) when a project has no Java deps.
func TestGenerateLocalClasspathEmptyWhenNoJars(t *testing.T) {
	projectDir := t.TempDir()
	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	g := New(t.TempDir())
	exports, err := g.GenerateLocal()
	if err != nil {
		t.Fatalf("GenerateLocal: %v", err)
	}
	for _, line := range exports {
		if strings.Contains(line, "CLASSPATH") {
			t.Errorf("unexpected CLASSPATH line with no jars present: %q", line)
		}
	}
}

// TestEnvNeverWritesAnchorAndWarnsWhenMissing pins the write/read split: with
// jars on disk but no anchor (a pre-anchor install, or the anchor deleted by
// hand), env must NOT create the anchor — env/bdl are read→stdout commands,
// often eval'd from shell profiles — and must instead emit no CLASSPATH line
// plus a warning telling the user to run `fglpkg install`.
func TestEnvNeverWritesAnchorAndWarnsWhenMissing(t *testing.T) {
	projectDir := t.TempDir()
	jarsDir := filepath.Join(projectDir, ".fglpkg", "jars")
	mustMkdir(t, jarsDir)
	mustWriteFile(t, filepath.Join(jarsDir, "dep.jar"), "dep")
	// Deliberately NO classpath.Sync here.

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	g := New(t.TempDir())
	exports, err := g.GenerateLocal()
	if err != nil {
		t.Fatalf("GenerateLocal: %v", err)
	}
	for _, line := range exports {
		if strings.Contains(line, "CLASSPATH") {
			t.Errorf("no CLASSPATH line expected when the anchor is missing, got: %q", line)
		}
	}
	if _, err := os.Stat(filepath.Join(jarsDir, classpath.AnchorName)); err == nil {
		t.Error("env must never write the anchor jar to disk")
	}
	warned := false
	for _, w := range g.Warnings() {
		if strings.Contains(w, classpath.AnchorName) && strings.Contains(w, "fglpkg install") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("expected a missing-anchor warning pointing at 'fglpkg install', got: %q", g.Warnings())
	}
}

// TestRawEnvClasspathIsAnchorJar covers the raw (unquoted, programmatic) env
// path used by `fglpkg bdl`: RawEnv and BuildJavaClasspath must expose the same
// single .classpath.jar anchor as the shell/GST renderers, never the individual
// jar paths. Regression guard for the raw variant, which the shell/GST/merged
// anchor tests don't exercise.
func TestRawEnvClasspathIsAnchorJar(t *testing.T) {
	projectDir := t.TempDir()
	jarsDir := filepath.Join(projectDir, ".fglpkg", "jars")
	mustMkdir(t, jarsDir)
	mustWriteFile(t, filepath.Join(jarsDir, "guava-33.2.1-jre.jar"), "guava")
	mustWriteFile(t, filepath.Join(jarsDir, "jackson-core-2.17.2.jar"), "jackson")
	mustSyncAnchor(t, jarsDir)

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	// Resolve the expected anchor AFTER chdir (macOS /var vs /private/var symlink).
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	wantAnchor := filepath.Join(cwd, ".fglpkg", "jars", classpath.AnchorName)

	// Empty global home so only the local scope contributes to CLASSPATH.
	g := New(t.TempDir())
	raw, err := g.RawEnv()
	if err != nil {
		t.Fatalf("RawEnv: %v", err)
	}
	if got := raw["CLASSPATH"]; got != wantAnchor {
		t.Errorf("RawEnv CLASSPATH = %q, want the anchor %q", got, wantAnchor)
	}
	if strings.Contains(raw["CLASSPATH"], "guava-33.2.1-jre.jar") ||
		strings.Contains(raw["CLASSPATH"], "jackson-core-2.17.2.jar") {
		t.Errorf("RawEnv CLASSPATH must not enumerate individual jars, got: %q", raw["CLASSPATH"])
	}

	// BuildJavaClasspath (the value `fglpkg bdl` sets directly) must agree.
	cp, err := g.BuildJavaClasspath()
	if err != nil {
		t.Fatalf("BuildJavaClasspath: %v", err)
	}
	if cp != wantAnchor {
		t.Errorf("BuildJavaClasspath = %q, want the anchor %q", cp, wantAnchor)
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
}

func mustWriteFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

// mustSyncAnchor lays down <dir>/.classpath.jar the way install does — env
// tests never write the anchor themselves, mirroring the production split.
func mustSyncAnchor(t *testing.T, dir string) {
	t.Helper()
	if err := classpath.Sync(dir); err != nil {
		t.Fatalf("classpath.Sync(%s): %v", dir, err)
	}
}
