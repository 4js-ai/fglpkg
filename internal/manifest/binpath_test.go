package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	slugutil "github.com/4js-mikefolcher/fglpkg/internal/slug"
)

// chdirTest switches to dir for the duration of the test and restores the
// original working directory afterwards. (testing.T.Chdir is Go 1.24+; this
// module targets the version in go.mod, which CI builds with.)
func chdirTest(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

// TestBinScriptPath_HonorsRoot: `bin` paths are relative to `root`, so the
// resolved script must sit under it — the rule `pack` stages from (GIS-569).
func TestBinScriptPath_HonorsRoot(t *testing.T) {
	cases := []struct {
		name string
		root string
		want string
	}{
		{"root unset falls back to the manifest dir", "", filepath.Join("pkg", "scripts", "greet.sh")},
		{"root is prepended", "src", filepath.Join("pkg", "src", "scripts", "greet.sh")},
		{"an explicit dot root is the manifest dir", ".", filepath.Join("pkg", "scripts", "greet.sh")},
		{"a nested root is prepended whole", "build/out", filepath.Join("pkg", "build", "out", "scripts", "greet.sh")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manifest{Name: "p", Version: "1.0.0", Root: tc.root, Bin: map[string]string{"greet": "scripts/greet.sh"}}
			got, err := m.BinScriptPath("pkg", "scripts/greet.sh")
			if err != nil {
				t.Fatalf("BinScriptPath: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBinScriptPath_RejectsUnsafe: an installed manifest arrives from a
// registry, so a bin path that escapes the package (or is absolute) must be
// refused here too, not just at pack time.
func TestBinScriptPath_RejectsUnsafe(t *testing.T) {
	for _, script := range []string{"", "../greet.sh", "/etc/greet.sh", "src/../../greet.sh"} {
		m := &Manifest{Name: "p", Version: "1.0.0", Root: "src"}
		if _, err := m.BinScriptPath("pkg", script); err == nil {
			t.Fatalf("bin script %q should be rejected as unsafe", script)
		}
	}
}

// TestBinScriptPath_RejectsUnsafeRoot: `root` is manifest-supplied too, so an
// escaping or absolute `root` reaches outside the package exactly as an escaping
// bin path does — validating only the script left that route open (PR #87
// review). The error must name `root`, not the script.
func TestBinScriptPath_RejectsUnsafeRoot(t *testing.T) {
	for _, root := range []string{"../../..", "..", "/etc", "src/../.."} {
		m := &Manifest{Name: "p", Version: "1.0.0", Root: root}
		_, err := m.BinScriptPath("pkg", "victim.sh")
		if err == nil {
			t.Fatalf("root %q should be rejected as unsafe", root)
		}
		if !strings.Contains(err.Error(), "root") {
			t.Fatalf("root %q: error should name root, got: %v", root, err)
		}
	}
	// "." and a plain subdirectory stay valid.
	for _, root := range []string{"", ".", "src", "lib/com/x"} {
		m := &Manifest{Name: "p", Version: "1.0.0", Root: root}
		if _, err := m.BinScriptPath("pkg", "greet.sh"); err != nil {
			t.Fatalf("root %q should be accepted, got: %v", root, err)
		}
	}
}

// TestValidate_RejectsUnsafeRoot: the authoring-side twin — `pack`/`lint`/
// `publish` must refuse to ship a manifest whose `root` escapes the package.
func TestValidate_RejectsUnsafeRoot(t *testing.T) {
	for _, root := range []string{"../..", "/etc"} {
		m := New("demo-pkg", "1.0.0", "d", "a")
		m.Root = root
		err := m.Validate()
		if err == nil {
			t.Fatalf("root %q should fail validation", root)
		}
		if !strings.Contains(err.Error(), "root") {
			t.Fatalf("root %q: error should name root, got: %v", root, err)
		}
	}
	m := New("demo-pkg", "1.0.0", "d", "a")
	m.Root = "src"
	if err := m.Validate(); err != nil {
		t.Fatalf("a plain subdirectory root must stay valid: %v", err)
	}
}

// TestNameAvoiding: a DERIVED project name that collides with a package being
// installed is adjusted rather than refused (PR #87 review). A name that does
// not collide is returned untouched.
func TestNameAvoiding(t *testing.T) {
	cases := []struct{ name, avoid, want string }{
		{"poiapi", "poiapi", "poiapi-app"},    // the collision case
		{"poi-api", "poi.api", "poi-api-app"}, // compared canonically (GIS-271)
		{"poiapi", "poi.api", "poiapi"},       // "poi.api" canonicalizes to "poi-api": no collision
		{"poiapi", "fglunit", "poiapi"},       // no collision: untouched
		{"poiapi", "", "poiapi"},              // nothing to avoid
		{"my-proj", "my_proj", "my-proj-app"}, // separator variant still collides
		{"poiapi-app", "poiapi-app", "poiapi-app-app"},
		// A 64-char name cannot carry the suffix (Sanitize truncates it back to
		// the same string), so it falls back rather than return a colliding name.
		{strings.Repeat("a", 64), strings.Repeat("a", 64), FallbackPackageName},
	}
	for _, tc := range cases {
		got := NameAvoiding(tc.name, tc.avoid)
		if got != tc.want {
			t.Errorf("NameAvoiding(%q, %q) = %q, want %q", tc.name, tc.avoid, got, tc.want)
		}
		// Whatever comes back must never collide with what it was told to avoid.
		if tc.avoid != "" && slugutil.Canonical(got) == slugutil.Canonical(tc.avoid) {
			t.Errorf("NameAvoiding(%q, %q) = %q, which still collides", tc.name, tc.avoid, got)
		}
	}
}

// TestDefaultNameForDir: a new manifest is named after the directory it is
// created in, canonicalized to a valid slug — never "." (GIS-568).
func TestDefaultNameForDir(t *testing.T) {
	base := t.TempDir()
	cases := []struct{ dirName, want string }{
		{"newproj", "newproj"},
		{"My Project", "my-project"},                       // spaces and case folded
		{"fgl_ai.sdk", "fgl-ai-sdk"},                       // PEP 503 separator collapsing
		{"--weird--", "weird"},                             // leading/trailing separators trimmed
		{"!!!", FallbackPackageName},                       // nothing usable survives
		{"x", FallbackPackageName},                         // a single character is not a valid slug
		{strings.Repeat("a", 80), strings.Repeat("a", 64)}, // truncated to the limit
	}
	for _, tc := range cases {
		t.Run(tc.dirName, func(t *testing.T) {
			dir := filepath.Join(base, tc.dirName)
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if got := DefaultNameForDir(dir); got != tc.want {
				t.Fatalf("DefaultNameForDir(%q) = %q, want %q", tc.dirName, got, tc.want)
			}
		})
	}
}

// TestLoadOrNew_NamesAfterWorkingDirectory: the "." case that `install <pkg>`
// in a fresh directory actually takes — filepath.Base(".") is ".", which is not
// a legal package name and made `publish` reject the generated manifest
// (GIS-568).
func TestLoadOrNew_NamesAfterWorkingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "newproj")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdirTest(t, dir)

	m, err := LoadOrNew(".")
	if err != nil {
		t.Fatalf("LoadOrNew: %v", err)
	}
	if m.Name != "newproj" {
		t.Fatalf("a new manifest must be named after its directory, got %q", m.Name)
	}
	// The generated name must satisfy the rule `publish` enforces.
	if err := m.Validate(); err != nil {
		t.Fatalf("a generated manifest must be valid: %v", err)
	}
}

// TestLoadOrNew_KeepsExistingName: an existing project's name is never
// rewritten by the fallback.
func TestLoadOrNew_KeepsExistingName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "newproj")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{"name":"already-named","version":"2.0.0"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, Filename), []byte(body), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	chdirTest(t, dir)

	m, err := LoadOrNew(".")
	if err != nil {
		t.Fatalf("LoadOrNew: %v", err)
	}
	if m.Name != "already-named" {
		t.Fatalf("an existing name must be preserved, got %q", m.Name)
	}
}
