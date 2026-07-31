package env

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// envValue extracts the fglpkg-managed value of key from rendered shell lines,
// stripping the prepend wrapper. Substring matching on the joined output is not
// good enough for these tests: a package's store root is a path PREFIX of every
// leaf directory inside it, so `strings.Contains(out, storeRoot)` is true even
// when the store root itself was never emitted.
//
// Renders in the default shell, which is what the Generate* helpers here use.
func envValue(t *testing.T, lines []string, key string) (string, bool) {
	t.Helper()
	line, ok := envLine(t, DefaultShell(), lines, key)
	return line.value, ok
}

// renderedLine is one emitted statement, split into the whole line and the
// fglpkg-managed value inside it.
type renderedLine struct{ full, value string }

// envLine finds key's statement among lines and recovers the value from it.
//
// The wrapper is DERIVED by rendering a sentinel through prependLine, not
// restated here. A hand-copied prefix/suffix pair is how this helper used to
// know the format, and it could silently disagree with the emitter — including
// for any shell nobody remembered to add a branch for. Deriving it also means
// these tests keep working if the checkout itself sits under a path with a space
// in it, which would otherwise flip every value onto the quoted branch.
//
// Two probes are needed because quoting is conditional: the spaced sentinel
// reproduces the quoted form, the inert one the bare form.
//
// The QUOTED probe must be tried first. For sh, quoting only adds characters
// INSIDE the value region, so a quoted line still matches the bare template's
// prefix and suffix — trying the bare probe first would match a quoted line and
// hand back a value with the wrapper's quotes still attached. The quoted
// template's prefix ends in the opening quote, which a bare line cannot match, so
// this order is unambiguous in both directions.
func envLine(t *testing.T, sh Shell, lines []string, key string) (renderedLine, bool) {
	t.Helper()
	sep := pathSeparator()
	for _, probe := range []struct {
		sentinel string
		unescape func(string) string
	}{
		{"FGLPKG SENTINEL", func(s string) string {
			switch sh {
			case ShellPowerShell:
				return strings.ReplaceAll(s, "''", "'")
			case ShellCmd:
				return s // cmd's quotes belong to the wrapper, not the value
			default:
				return strings.ReplaceAll(s, `'\''`, "'")
			}
		}},
		{"FGLPKGSENTINEL", func(s string) string { return s }},
	} {
		tpl := prependLine(sh, key, probe.sentinel, sep)
		i := strings.Index(tpl, probe.sentinel)
		if i < 0 {
			t.Fatalf("prependLine did not embed the sentinel verbatim: %q", tpl)
		}
		prefix, suffix := tpl[:i], tpl[i+len(probe.sentinel):]
		for _, line := range lines {
			if len(line) >= len(prefix)+len(suffix) &&
				strings.HasPrefix(line, prefix) && strings.HasSuffix(line, suffix) {
				value := line[len(prefix) : len(line)-len(suffix)]
				return renderedLine{full: line, value: probe.unescape(value)}, true
			}
		}
	}
	return renderedLine{}, false
}

// envParts returns key's value split into individual path entries.
//
// Splits on pathSeparator() — still correct after --shell, because the separator
// inside a value is a property of the Genero runtime that parses it, not of the
// shell that set it.
func envParts(t *testing.T, lines []string, key string) []string {
	t.Helper()
	value, ok := envValue(t, lines, key)
	if !ok || value == "" {
		return nil
	}
	return strings.Split(value, pathSeparator())
}

// hasPart reports whether want appears as a whole entry (not as a prefix of a
// longer path).
func hasPart(parts []string, want string) bool {
	for _, p := range parts {
		if p == want {
			return true
		}
	}
	return false
}

func abs(t *testing.T, rel string) string {
	t.Helper()
	a, err := filepath.Abs(filepath.FromSlash(rel))
	if err != nil {
		t.Fatalf("abs %s: %v", rel, err)
	}
	return a
}

// mustGenerateLocal renders the local scope, failing the test on error.
func mustGenerateLocal(t *testing.T, g *Generator) []string {
	t.Helper()
	lines, err := g.GenerateLocal()
	if err != nil {
		t.Fatalf("GenerateLocal: %v", err)
	}
	return lines
}

// ─── FGLRESOURCEPATH ──────────────────────────────────────────────────────────

// TestFGLRESOURCEPATHEmitsLeafDirNotStoreRoot: these variables are searched
// non-recursively by basename, so a namespaced package's forms are only found
// when the namespace directory ITSELF is on the path. Emitting the store root —
// the shape FGLLDPATH uses — would never resolve them.
func TestFGLRESOURCEPATHEmitsLeafDirNotStoreRoot(t *testing.T) {
	chdirTemp(t)
	envTestWrite(t, ".fglpkg/packages/poiapi/fglpkg.json",
		`{ "name": "poiapi", "version": "1.0.0", "dependencies": { "fgl": {} } }`)
	envTestWrite(t, ".fglpkg/packages/poiapi/com/fourjs/poiapi/Customer.42f", "FORM")

	parts := envParts(t, mustGenerateLocal(t, New(t.TempDir())), "FGLRESOURCEPATH")
	leaf := abs(t, ".fglpkg/packages/poiapi/com/fourjs/poiapi")
	root := abs(t, ".fglpkg/packages/poiapi")
	if !hasPart(parts, leaf) {
		t.Errorf("FGLRESOURCEPATH %v should contain the namespace dir %q", parts, leaf)
	}
	if hasPart(parts, root) {
		t.Errorf("FGLRESOURCEPATH %v should not contain the bare store root %q (no resources live there)", parts, root)
	}
}

// TestFGLRESOURCEPATHCoversResourceExtensions: every program-resource kind maps
// to FGLRESOURCEPATH.
func TestFGLRESOURCEPATHCoversResourceExtensions(t *testing.T) {
	// All eight entries of the manual's closed "program resource files" list
	// (c_fgl_EnvVariables_FGLRESOURCEPATH). .iem and .4tm are easy to forget:
	// neither is produced by fglcomp/fglform, so they never show up in a
	// hand-rolled list built by watching a normal build.
	for _, ext := range []string{".42f", ".iem", ".4ad", ".4st", ".4sm", ".4tb", ".4tm", ".42s"} {
		t.Run(ext, func(t *testing.T) {
			chdirTemp(t)
			envTestWrite(t, ".fglpkg/packages/uikit/fglpkg.json",
				`{ "name": "uikit", "version": "1.0.0", "dependencies": { "fgl": {} } }`)
			envTestWrite(t, ".fglpkg/packages/uikit/res/asset"+ext, "X")

			parts := envParts(t, mustGenerateLocal(t, New(t.TempDir())), "FGLRESOURCEPATH")
			want := abs(t, ".fglpkg/packages/uikit/res")
			if !hasPart(parts, want) {
				t.Errorf("%s should land on FGLRESOURCEPATH; got %v", ext, parts)
			}
		})
	}
}

// TestNoResourceVarsWhenOnlyModulesInstalled: a package shipping only compiled
// modules must not produce an empty export for the resource variables.
func TestNoResourceVarsWhenOnlyModulesInstalled(t *testing.T) {
	chdirTemp(t)
	envTestWrite(t, ".fglpkg/packages/dbkit/fglpkg.json",
		`{ "name": "dbkit", "version": "1.0.0", "dependencies": { "fgl": {} } }`)
	envTestWrite(t, ".fglpkg/packages/dbkit/com/fourjs/db/DbConnection.42m", "DB")

	lines := mustGenerateLocal(t, New(t.TempDir()))
	for _, key := range []string{"FGLRESOURCEPATH", "FGLDBPATH", "FGLIMAGEPATH", "FGLPROFILE"} {
		if _, ok := envValue(t, lines, key); ok {
			t.Errorf("%s must not be emitted when the package ships no matching files:\n%s",
				key, strings.Join(lines, "\n"))
		}
	}
}

// TestFGLDBPATHFromSchemaDir: *.sch feeds FGLDBPATH, and only FGLDBPATH.
func TestFGLDBPATHFromSchemaDir(t *testing.T) {
	chdirTemp(t)
	envTestWrite(t, ".fglpkg/packages/dbkit/fglpkg.json",
		`{ "name": "dbkit", "version": "1.0.0", "dependencies": { "fgl": {} } }`)
	envTestWrite(t, ".fglpkg/packages/dbkit/schema/stores.sch", "SCH")

	lines := mustGenerateLocal(t, New(t.TempDir()))
	want := abs(t, ".fglpkg/packages/dbkit/schema")
	if parts := envParts(t, lines, "FGLDBPATH"); !hasPart(parts, want) {
		t.Errorf("FGLDBPATH %v should contain %q", parts, want)
	}
	if _, ok := envValue(t, lines, "FGLRESOURCEPATH"); ok {
		t.Error("a .sch must not also land on FGLRESOURCEPATH")
	}
}

// TestFGLDBPATHCoversSchemaExtensions: a Genero database schema is three files
// (c_fgl_DatabaseSchema_017), and fgldbsch can emit .val/.att alongside the
// .sch. They are legacy, but a package that ships one without a sibling .sch —
// or with the .sch excluded by its `files` globs — must still get its schema
// directory on FGLDBPATH.
func TestFGLDBPATHCoversSchemaExtensions(t *testing.T) {
	for _, ext := range []string{".sch", ".val", ".att"} {
		t.Run(ext, func(t *testing.T) {
			chdirTemp(t)
			envTestWrite(t, ".fglpkg/packages/dbkit/fglpkg.json",
				`{ "name": "dbkit", "version": "1.0.0", "dependencies": { "fgl": {} } }`)
			envTestWrite(t, ".fglpkg/packages/dbkit/schema/stores"+ext, "X")

			parts := envParts(t, mustGenerateLocal(t, New(t.TempDir())), "FGLDBPATH")
			want := abs(t, ".fglpkg/packages/dbkit/schema")
			if !hasPart(parts, want) {
				t.Errorf("%s should land on FGLDBPATH; got %v", ext, parts)
			}
		})
	}
}

// ─── FGLIMAGEPATH ─────────────────────────────────────────────────────────────

// TestFGLIMAGEPATHCoversImageExtensions: every format in the manual's supported
// image table (c_fgl_images_resource_spec), plus .ttf — with an
// image-to-font-glyph mapping file a bare image name resolves to a glyph in a
// font file, and the manual requires that font's directory on FGLIMAGEPATH.
func TestFGLIMAGEPATHCoversImageExtensions(t *testing.T) {
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".gif", ".svg", ".bmp", ".ico", ".tiff", ".tif", ".ttf"} {
		t.Run(ext, func(t *testing.T) {
			chdirTemp(t)
			envTestWrite(t, ".fglpkg/packages/icons/fglpkg.json",
				`{ "name": "icons", "version": "1.0.0", "dependencies": { "fgl": {} } }`)
			envTestWrite(t, ".fglpkg/packages/icons/img/logo"+ext, "X")

			parts := envParts(t, mustGenerateLocal(t, New(t.TempDir())), "FGLIMAGEPATH")
			want := abs(t, ".fglpkg/packages/icons/img")
			if !hasPart(parts, want) {
				t.Errorf("%s should land on FGLIMAGEPATH; got %v", ext, parts)
			}
		})
	}
}

// TestFGLIMAGEPATHWebcomponentParentFirstThenImages: the webcomponents parent
// keeps its historical leading position, and the GAS hint continues to describe
// only the webcomponent directory — not the packaged image dirs that now share
// the variable.
func TestFGLIMAGEPATHWebcomponentParentFirstThenImages(t *testing.T) {
	chdirTemp(t)
	mustMkdir(t, filepath.FromSlash(".fglpkg/webcomponents/MyWidget"))
	envTestWrite(t, ".fglpkg/packages/icons/fglpkg.json",
		`{ "name": "icons", "version": "1.0.0", "dependencies": { "fgl": {} } }`)
	envTestWrite(t, ".fglpkg/packages/icons/img/logo.png", "PNG")

	lines := mustGenerateLocal(t, New(t.TempDir()))
	parts := envParts(t, lines, "FGLIMAGEPATH")
	wcParent, imgDir := abs(t, ".fglpkg"), abs(t, ".fglpkg/packages/icons/img")
	if len(parts) != 2 || parts[0] != wcParent || parts[1] != imgDir {
		t.Fatalf("FGLIMAGEPATH should be [webcomponents parent, image dir]; got %v", parts)
	}

	var hint string
	for _, l := range lines {
		if strings.Contains(l, "WEB_COMPONENT_DIRECTORY") {
			hint = l
		}
	}
	if hint == "" {
		t.Fatalf("expected a GAS hint in:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(hint, filepath.Join(wcParent, "webcomponents")) {
		t.Errorf("GAS hint should point at the webcomponents dir: %q", hint)
	}
	if strings.Contains(hint, imgDir) {
		t.Errorf("GAS hint must not advertise a packaged image dir as a webcomponent dir: %q", hint)
	}
}

// TestFGLIMAGEPATHImagesOnlyOmitsGASHint: images alone populate FGLIMAGEPATH,
// but there is no webcomponent to tell GAS about. Gating the hint on a
// non-empty FGLIMAGEPATH (the pre-existing rule) would print a hint pointing at
// a nonexistent <imagedir>/webcomponents.
func TestFGLIMAGEPATHImagesOnlyOmitsGASHint(t *testing.T) {
	chdirTemp(t)
	envTestWrite(t, ".fglpkg/packages/icons/fglpkg.json",
		`{ "name": "icons", "version": "1.0.0", "dependencies": { "fgl": {} } }`)
	envTestWrite(t, ".fglpkg/packages/icons/img/logo.svg", "SVG")

	lines := mustGenerateLocal(t, New(t.TempDir()))
	if parts := envParts(t, lines, "FGLIMAGEPATH"); !hasPart(parts, abs(t, ".fglpkg/packages/icons/img")) {
		t.Errorf("packaged images should populate FGLIMAGEPATH; got %v", parts)
	}
	for _, l := range lines {
		if strings.Contains(l, "WEB_COMPONENT_DIRECTORY") {
			t.Errorf("no webcomponents are installed, so no GAS hint should be printed: %q", l)
		}
	}
}

// ─── the merged-root interaction ──────────────────────────────────────────────

// TestStoreDroppedFromFGLLDPATHStillContributesResources is the key regression
// guard for this feature. The merged root holds only *.42m, so dropping a
// fully-covered package's store dir from FGLLDPATH must not also remove its
// forms, schemas and images — they exist nowhere else. Reusing the FGLLDPATH
// directory list to harvest assets is the natural refactor mistake here.
func TestStoreDroppedFromFGLLDPATHStillContributesResources(t *testing.T) {
	chdirTemp(t)
	envTestWrite(t, ".fglpkg/merged/com/fourjs/db/DbConnection.42m", "DB")
	envTestWrite(t, ".fglpkg/packages/dbkit/fglpkg.json",
		`{ "name": "dbkit", "version": "1.0.0", "generoPackages": ["com.fourjs.db"], "dependencies": { "fgl": {} } }`)
	envTestWrite(t, ".fglpkg/packages/dbkit/com/fourjs/db/DbConnection.42m", "DB")
	envTestWrite(t, ".fglpkg/packages/dbkit/com/fourjs/db/Customer.42f", "FORM")
	envTestWrite(t, ".fglpkg/packages/dbkit/schema/stores.sch", "SCH")

	lines := mustGenerateLocal(t, New(t.TempDir()))
	storeRoot := abs(t, ".fglpkg/packages/dbkit")

	ld := envParts(t, lines, "FGLLDPATH")
	if !hasPart(ld, abs(t, ".fglpkg/merged")) {
		t.Errorf("FGLLDPATH %v should contain the merged root", ld)
	}
	if hasPart(ld, storeRoot) {
		t.Errorf("FGLLDPATH %v should drop a fully-covered package's store dir", ld)
	}
	if parts := envParts(t, lines, "FGLRESOURCEPATH"); !hasPart(parts, abs(t, ".fglpkg/packages/dbkit/com/fourjs/db")) {
		t.Errorf("the dropped store dir must still contribute its form dir to FGLRESOURCEPATH; got %v", parts)
	}
	if parts := envParts(t, lines, "FGLDBPATH"); !hasPart(parts, abs(t, ".fglpkg/packages/dbkit/schema")) {
		t.Errorf("the dropped store dir must still contribute its schema dir to FGLDBPATH; got %v", parts)
	}
}

// ─── ordering and scope ───────────────────────────────────────────────────────

// TestAssetDirsLocalBeforeGlobal: the documented precedence — local scope first,
// so "first match wins" resolves to the project's own copy.
func TestAssetDirsLocalBeforeGlobal(t *testing.T) {
	chdirTemp(t)
	globalHome := t.TempDir()
	envTestWrite(t, ".fglpkg/packages/localpkg/fglpkg.json",
		`{ "name": "localpkg", "version": "1.0.0", "dependencies": { "fgl": {} } }`)
	envTestWrite(t, ".fglpkg/packages/localpkg/forms/Local.42f", "L")
	writeUnder(t, globalHome, "packages/globalpkg/fglpkg.json",
		`{ "name": "globalpkg", "version": "1.0.0", "dependencies": { "fgl": {} } }`)
	writeUnder(t, globalHome, "packages/globalpkg/forms/Global.42f", "G")

	g := New(globalHome)
	lines, err := g.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	parts := envParts(t, lines, "FGLRESOURCEPATH")
	localDir := abs(t, ".fglpkg/packages/localpkg/forms")
	globalDir := filepath.Join(globalHome, "packages", "globalpkg", "forms")
	li, gi := indexOfPart(parts, localDir), indexOfPart(parts, globalDir)
	if li < 0 || gi < 0 {
		t.Fatalf("expected both scopes on FGLRESOURCEPATH; got %v", parts)
	}
	if li > gi {
		t.Errorf("local scope must precede global on FGLRESOURCEPATH; got %v", parts)
	}
}

// TestProfilesGlobalBeforeLocal: FGLPROFILE must be ordered the OPPOSITE way to
// the search-path variables, and this is the regression guard for getting it
// backwards. The search paths are first-match-wins, so local comes first
// (TestAssetDirsLocalBeforeGlobal). FGLPROFILE is not a search path: every
// listed file is loaded and merged, and for a duplicated entry key the last
// file loaded wins. Listing local first would therefore let a globally
// installed package override the project's own copy — the exact inverse of the
// documented precedence, and silent, since both files load without complaint.
func TestProfilesGlobalBeforeLocal(t *testing.T) {
	chdirTemp(t)
	globalHome := t.TempDir()
	envTestWrite(t, ".fglpkg/packages/localpkg/fglpkg.json",
		`{ "name": "localpkg", "version": "1.0.0", "profile": ["profiles/local.4gp"], "dependencies": { "fgl": {} } }`)
	envTestWrite(t, ".fglpkg/packages/localpkg/profiles/local.4gp", "L")
	writeUnder(t, globalHome, "packages/globalpkg/fglpkg.json",
		`{ "name": "globalpkg", "version": "1.0.0", "profile": ["profiles/global.4gp"], "dependencies": { "fgl": {} } }`)
	writeUnder(t, globalHome, "packages/globalpkg/profiles/global.4gp", "G")

	g := New(globalHome)
	lines, err := g.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	parts := envParts(t, lines, "FGLPROFILE")
	localFile := abs(t, ".fglpkg/packages/localpkg/profiles/local.4gp")
	globalFile := filepath.Join(globalHome, "packages", "globalpkg", "profiles", "global.4gp")
	li, gi := indexOfPart(parts, localFile), indexOfPart(parts, globalFile)
	if li < 0 || gi < 0 {
		t.Fatalf("expected both scopes on FGLPROFILE; got %v", parts)
	}
	if gi > li {
		t.Errorf("global profile must be loaded BEFORE the local one so the local wins last-wins merging; got %v", parts)
	}
}

// TestProfilesReverseLexicalWithinScope: the same inversion applies between two
// packages in one scope. Search paths put "alpha" ahead of "zeta" and the first
// match wins, so on FGLPROFILE alpha must come last to win the merge.
func TestProfilesReverseLexicalWithinScope(t *testing.T) {
	chdirTemp(t)
	for _, name := range []string{"alpha", "zeta"} {
		envTestWrite(t, ".fglpkg/packages/"+name+"/fglpkg.json",
			`{ "name": "`+name+`", "version": "1.0.0", "profile": ["app.4gp"], "dependencies": { "fgl": {} } }`)
		envTestWrite(t, ".fglpkg/packages/"+name+"/app.4gp", name)
	}

	parts := envParts(t, mustGenerateLocal(t, New(t.TempDir())), "FGLPROFILE")
	alpha, zeta := abs(t, ".fglpkg/packages/alpha/app.4gp"), abs(t, ".fglpkg/packages/zeta/app.4gp")
	ai, zi := indexOfPart(parts, alpha), indexOfPart(parts, zeta)
	if ai < 0 || zi < 0 {
		t.Fatalf("expected both packages on FGLPROFILE; got %v", parts)
	}
	if zi > ai {
		t.Errorf("zeta must be loaded before alpha so alpha wins last-wins merging; got %v", parts)
	}
}

// TestGenerateGlobalIgnoresLocalAssets: --global emits the global scope only
// (GIS-290), for the new variables as well as FGLLDPATH.
func TestGenerateGlobalIgnoresLocalAssets(t *testing.T) {
	chdirTemp(t)
	globalHome := t.TempDir()
	envTestWrite(t, ".fglpkg/packages/localpkg/fglpkg.json",
		`{ "name": "localpkg", "version": "1.0.0", "dependencies": { "fgl": {} } }`)
	envTestWrite(t, ".fglpkg/packages/localpkg/forms/Local.42f", "L")
	writeUnder(t, globalHome, "packages/globalpkg/fglpkg.json",
		`{ "name": "globalpkg", "version": "1.0.0", "dependencies": { "fgl": {} } }`)
	writeUnder(t, globalHome, "packages/globalpkg/forms/Global.42f", "G")

	g := New(globalHome)
	lines, err := g.GenerateGlobal()
	if err != nil {
		t.Fatalf("GenerateGlobal: %v", err)
	}
	parts := envParts(t, lines, "FGLRESOURCEPATH")
	if !hasPart(parts, filepath.Join(globalHome, "packages", "globalpkg", "forms")) {
		t.Errorf("expected the global resource dir; got %v", parts)
	}
	if hasPart(parts, abs(t, ".fglpkg/packages/localpkg/forms")) {
		t.Errorf("--global must not merge local project resources; got %v", parts)
	}
}

// TestLocalEqualsGlobalHomeNoDuplicates: running inside the directory that IS
// the fglpkg home must not list every path twice.
func TestLocalEqualsGlobalHomeNoDuplicates(t *testing.T) {
	dir := chdirTemp(t)
	envTestWrite(t, ".fglpkg/packages/poiapi/fglpkg.json",
		`{ "name": "poiapi", "version": "1.0.0", "dependencies": { "fgl": {} } }`)
	envTestWrite(t, ".fglpkg/packages/poiapi/forms/Customer.42f", "FORM")

	g := New(filepath.Join(dir, ".fglpkg")) // global home == the local scope
	lines, err := g.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, key := range []string{"FGLLDPATH", "FGLRESOURCEPATH"} {
		parts := envParts(t, lines, key)
		seen := make(map[string]bool, len(parts))
		for _, p := range parts {
			if seen[p] {
				t.Errorf("%s lists %q twice: %v", key, p, parts)
			}
			seen[p] = true
		}
	}
}

// ─── collisions ───────────────────────────────────────────────────────────────

// TestCollisionWarningNamesBothPackagesAndWinner: basename shadowing is made
// loud. The warning names both packages and the directory that actually wins,
// and it never reaches the rendered (stdout) lines.
func TestCollisionWarningNamesBothPackagesAndWinner(t *testing.T) {
	chdirTemp(t)
	for _, pkg := range []string{"alpha", "beta"} {
		envTestWrite(t, ".fglpkg/packages/"+pkg+"/fglpkg.json",
			`{ "name": "`+pkg+`", "version": "1.0.0", "dependencies": { "fgl": {} } }`)
		envTestWrite(t, ".fglpkg/packages/"+pkg+"/forms/Customer.42f", "FORM")
	}

	g := New(t.TempDir())
	lines := mustGenerateLocal(t, g)
	warnings := g.Warnings()
	if len(warnings) != 1 {
		t.Fatalf("expected exactly one collision warning, got %d: %v", len(warnings), warnings)
	}
	w := warnings[0]
	for _, want := range []string{"FGLRESOURCEPATH", "Customer.42f", `"alpha"`, `"beta"`, abs(t, ".fglpkg/packages/alpha/forms")} {
		if !strings.Contains(w, want) {
			t.Errorf("collision warning should mention %q:\n%s", want, w)
		}
	}
	for _, l := range lines {
		if strings.Contains(l, "warning") {
			t.Errorf("diagnostics must never reach stdout (it is eval'd): %q", l)
		}
	}
}

// TestNoCollisionWarningForSamePackageAcrossScopes: the same package installed
// locally and globally is precedence, not a clash — the rule that already
// governs PACKAGE namespaces across scopes.
func TestNoCollisionWarningForSamePackageAcrossScopes(t *testing.T) {
	chdirTemp(t)
	globalHome := t.TempDir()
	manifestJSON := `{ "name": "poiapi", "version": "1.0.0", "dependencies": { "fgl": {} } }`
	envTestWrite(t, ".fglpkg/packages/poiapi/fglpkg.json", manifestJSON)
	envTestWrite(t, ".fglpkg/packages/poiapi/forms/Customer.42f", "FORM")
	writeUnder(t, globalHome, "packages/poiapi/fglpkg.json", manifestJSON)
	writeUnder(t, globalHome, "packages/poiapi/forms/Customer.42f", "FORM")

	g := New(globalHome)
	if _, err := g.Generate(); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if w := g.Warnings(); len(w) != 0 {
		t.Errorf("the same package in two scopes is precedence, not a clash; got: %v", w)
	}
}

// TestCollisionWarningWithinOnePackage: one package shipping the same basename
// in two directories makes the second unreachable — also worth reporting.
func TestCollisionWarningWithinOnePackage(t *testing.T) {
	chdirTemp(t)
	envTestWrite(t, ".fglpkg/packages/uikit/fglpkg.json",
		`{ "name": "uikit", "version": "1.0.0", "dependencies": { "fgl": {} } }`)
	envTestWrite(t, ".fglpkg/packages/uikit/com/acme/Customer.42f", "A")
	envTestWrite(t, ".fglpkg/packages/uikit/forms/Customer.42f", "B")

	g := New(t.TempDir())
	mustGenerateLocal(t, g)
	warnings := g.Warnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0], "two directories") {
		t.Fatalf("expected a within-package shadowing warning, got: %v", warnings)
	}
}

// TestWarningsResetBetweenCalls: Warnings() reflects the most recent call only.
func TestWarningsResetBetweenCalls(t *testing.T) {
	chdirTemp(t)
	for _, pkg := range []string{"alpha", "beta"} {
		envTestWrite(t, ".fglpkg/packages/"+pkg+"/fglpkg.json",
			`{ "name": "`+pkg+`", "version": "1.0.0", "dependencies": { "fgl": {} } }`)
		envTestWrite(t, ".fglpkg/packages/"+pkg+"/forms/Customer.42f", "FORM")
	}
	g := New(t.TempDir())
	mustGenerateLocal(t, g)
	first := len(g.Warnings())
	mustGenerateLocal(t, g)
	if second := len(g.Warnings()); second != first {
		t.Errorf("warnings must not accumulate across calls: %d then %d", first, second)
	}
}

// ─── FGLPROFILE ───────────────────────────────────────────────────────────────

// TestFGLPROFILEEmitsDeclaredFilesInOrder: FGLPROFILE carries FILE paths, in
// declaration order, and is prepended so the user's own value still wins (the
// variable is applied left to right, last definition winning).
func TestFGLPROFILEEmitsDeclaredFilesInOrder(t *testing.T) {
	chdirTemp(t)
	envTestWrite(t, ".fglpkg/packages/poiapi/fglpkg.json",
		`{ "name": "poiapi", "version": "1.0.0", "profile": ["profiles/a.4gp", "profiles/b.4gp"], "dependencies": { "fgl": {} } }`)
	envTestWrite(t, ".fglpkg/packages/poiapi/profiles/a.4gp", "A")
	envTestWrite(t, ".fglpkg/packages/poiapi/profiles/b.4gp", "B")

	lines := mustGenerateLocal(t, New(t.TempDir()))
	parts := envParts(t, lines, "FGLPROFILE")
	wantA, wantB := abs(t, ".fglpkg/packages/poiapi/profiles/a.4gp"), abs(t, ".fglpkg/packages/poiapi/profiles/b.4gp")
	if len(parts) != 2 || parts[0] != wantA || parts[1] != wantB {
		t.Fatalf("FGLPROFILE should list both files in declaration order; got %v", parts)
	}
	// FGLPROFILE is applied left to right with the LAST definition winning, so
	// the package's files must sit ahead of the inherited value — which is what
	// makes a user's own profile still override a package's defaults.
	//
	// Asserted for every shell rather than branching on runtime.GOOS: the
	// ordering is the load-bearing claim and it must hold in all three syntaxes.
	// The inherited-value reference is derived from the emitter (see envLine)
	// instead of spelled out, so no shell can be silently left uncovered.
	for _, sh := range []Shell{ShellSh, ShellPowerShell, ShellCmd} {
		shLines := mustGenerateLocal(t, New(t.TempDir()).WithShell(sh))
		line, ok := envLine(t, sh, shLines, "FGLPROFILE")
		if !ok {
			t.Fatalf("%s: no FGLPROFILE statement in %v", sh, shLines)
		}
		inherited := inheritedRef(t, sh, "FGLPROFILE")
		iValue, iInherited := strings.Index(line.full, wantA), strings.Index(line.full, inherited)
		if iInherited < 0 {
			t.Fatalf("%s: FGLPROFILE line should preserve any inherited value: %q", sh, line.full)
		}
		if iValue < 0 {
			t.Fatalf("%s: FGLPROFILE line should contain the package profile: %q", sh, line.full)
		}
		if iValue > iInherited {
			t.Errorf("%s: package profiles must precede the inherited value: %q", sh, line.full)
		}
	}
}

// inheritedRef returns the part of key's emitted statement that references the
// variable's pre-existing value, derived from prependLine rather than restated.
func inheritedRef(t *testing.T, sh Shell, key string) string {
	t.Helper()
	const sentinel = "FGLPKGSENTINEL"
	tpl := prependLine(sh, key, sentinel, pathSeparator())
	i := strings.Index(tpl, sentinel)
	if i < 0 {
		t.Fatalf("prependLine did not embed the sentinel verbatim: %q", tpl)
	}
	return tpl[i+len(sentinel):]
}

// TestFGLPROFILESkipsMissingFile: a declared profile that is not installed is
// dropped (never emitted as a dangling path) and reported.
func TestFGLPROFILESkipsMissingFile(t *testing.T) {
	chdirTemp(t)
	envTestWrite(t, ".fglpkg/packages/poiapi/fglpkg.json",
		`{ "name": "poiapi", "version": "1.0.0", "profile": ["profiles/gone.4gp"], "dependencies": { "fgl": {} } }`)

	g := New(t.TempDir())
	lines := mustGenerateLocal(t, g)
	if _, ok := envValue(t, lines, "FGLPROFILE"); ok {
		t.Errorf("FGLPROFILE must not be emitted for a missing file:\n%s", strings.Join(lines, "\n"))
	}
	if w := g.Warnings(); len(w) != 1 || !strings.Contains(w[0], "gone.4gp") {
		t.Errorf("expected a warning naming the missing profile, got: %v", w)
	}
}

// TestFGLPROFILEIgnoresEscapingPath: manifest.Load does not validate, so a
// hand-edited or hostile installed manifest can declare a path outside the
// store. env must refuse to put it on FGLPROFILE.
func TestFGLPROFILEIgnoresEscapingPath(t *testing.T) {
	chdirTemp(t)
	envTestWrite(t, ".fglpkg/packages/evil/fglpkg.json",
		`{ "name": "evil", "version": "1.0.0", "profile": ["../../../etc/passwd"], "dependencies": { "fgl": {} } }`)

	g := New(t.TempDir())
	lines := mustGenerateLocal(t, g)
	if _, ok := envValue(t, lines, "FGLPROFILE"); ok {
		t.Errorf("an escaping profile path must not be emitted:\n%s", strings.Join(lines, "\n"))
	}
	if w := g.Warnings(); len(w) != 1 || !strings.Contains(w[0], "escapes the package directory") {
		t.Errorf("expected an escape warning, got: %v", w)
	}
}

// ─── Genero Studio output ─────────────────────────────────────────────────────

// TestGenerateGSTEmitsNewVarsProjectDirRelative: the new variables use the same
// $(ProjectDir) templating as FGLLDPATH.
func TestGenerateGSTEmitsNewVarsProjectDirRelative(t *testing.T) {
	chdirTemp(t)
	envTestWrite(t, ".fglpkg/packages/poiapi/fglpkg.json",
		`{ "name": "poiapi", "version": "1.0.0", "profile": ["profiles/a.4gp"], "dependencies": { "fgl": {} } }`)
	envTestWrite(t, ".fglpkg/packages/poiapi/com/fourjs/poiapi/Customer.42f", "FORM")
	envTestWrite(t, ".fglpkg/packages/poiapi/schema/stores.sch", "SCH")
	envTestWrite(t, ".fglpkg/packages/poiapi/img/logo.png", "PNG")
	envTestWrite(t, ".fglpkg/packages/poiapi/profiles/a.4gp", "A")

	lines, err := New(t.TempDir()).GenerateGST()
	if err != nil {
		t.Fatalf("GenerateGST: %v", err)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"FGLRESOURCEPATH=$(ProjectDir)/.fglpkg/packages/poiapi/com/fourjs/poiapi;$(FGLRESOURCEPATH)",
		"FGLDBPATH=$(ProjectDir)/.fglpkg/packages/poiapi/schema;$(FGLDBPATH)",
		"FGLIMAGEPATH=$(ProjectDir)/.fglpkg/packages/poiapi/img;$(FGLIMAGEPATH)",
		"FGLPROFILE=$(ProjectDir)/.fglpkg/packages/poiapi/profiles/a.4gp;$(FGLPROFILE)",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing GST line %q in:\n%s", want, joined)
		}
	}
}

// TestGenerateGSTEmitsNoCommentLines: Genero Studio parses this output as a
// strict VAR=value list, so neither the GAS hint nor a collision may appear —
// even when both are triggered.
func TestGenerateGSTEmitsNoCommentLines(t *testing.T) {
	chdirTemp(t)
	mustMkdir(t, filepath.FromSlash(".fglpkg/webcomponents/MyWidget"))
	for _, pkg := range []string{"alpha", "beta"} {
		envTestWrite(t, ".fglpkg/packages/"+pkg+"/fglpkg.json",
			`{ "name": "`+pkg+`", "version": "1.0.0", "dependencies": { "fgl": {} } }`)
		envTestWrite(t, ".fglpkg/packages/"+pkg+"/forms/Customer.42f", "FORM")
	}

	// Asked for in every shell, because --shell must not reach GST at all: it is
	// a Genero Studio file format, not shell syntax. The comment markers are
	// taken from commentPrefix so a shell added later cannot sneak one in.
	for _, sh := range []Shell{ShellSh, ShellPowerShell, ShellCmd} {
		g := New(t.TempDir()).WithShell(sh)
		lines, err := g.GenerateGST()
		if err != nil {
			t.Fatalf("%s: GenerateGST: %v", sh, err)
		}
		if len(g.Warnings()) == 0 {
			t.Fatalf("%s: expected the collision to be reported out-of-band", sh)
		}
		for _, l := range lines {
			if strings.HasPrefix(l, commentPrefix(sh)) || strings.Contains(l, "WEB_COMPONENT_DIRECTORY") {
				t.Errorf("%s: GST output must contain no comment lines: %q", sh, l)
			}
		}
	}
}

// TestGenerateGSTIgnoresShell: --shell selects shell syntax, and GST output is
// not shell syntax. The CLI rejects the combination, but the library must be
// inert about it too, since GST values land verbatim in a user's project
// settings and a stray quote character would be stored there.
func TestGenerateGSTIgnoresShell(t *testing.T) {
	chdirTemp(t)
	envTestWrite(t, ".fglpkg/packages/poiapi/fglpkg.json",
		`{ "name": "poiapi", "version": "1.0.0", "dependencies": { "fgl": {} } }`)
	envTestWrite(t, ".fglpkg/packages/poiapi/forms/Customer.42f", "FORM")

	var want []string
	for _, sh := range []Shell{ShellSh, ShellPowerShell, ShellCmd} {
		lines, err := New(t.TempDir()).WithShell(sh).GenerateGST()
		if err != nil {
			t.Fatalf("%s: GenerateGST: %v", sh, err)
		}
		if want == nil {
			want = lines
			continue
		}
		if strings.Join(lines, "\n") != strings.Join(want, "\n") {
			t.Errorf("--shell %s changed GST output:\n got: %v\nwant: %v", sh, lines, want)
		}
	}
}

// ─── caching and raw values ───────────────────────────────────────────────────

// TestScanWalksEachStoreDirOnce proves the per-invocation cache: several
// variables need answers about the same tree, but the tree is walked once.
func TestScanWalksEachStoreDirOnce(t *testing.T) {
	chdirTemp(t)
	envTestWrite(t, ".fglpkg/packages/poiapi/fglpkg.json",
		`{ "name": "poiapi", "version": "1.0.0", "dependencies": { "fgl": {} } }`)
	envTestWrite(t, ".fglpkg/packages/poiapi/forms/Customer.42f", "FORM")

	s := newScanner()
	pkgDir, mergedDir := abs(t, ".fglpkg/packages/poiapi"), abs(t, ".fglpkg/merged")
	first := s.scan(pkgDir, mergedDir)
	second := s.scan(pkgDir, mergedDir)
	if s.walks != 1 {
		t.Errorf("expected 1 walk for a repeated scan of the same store dir, got %d", s.walks)
	}
	if first != second {
		t.Error("expected the cached *storeScan to be returned")
	}
}

// TestRawEnvMatchesGenerateValues: the child-process environment and the shell
// output are computed from the same plan, so they cannot drift.
func TestRawEnvMatchesGenerateValues(t *testing.T) {
	chdirTemp(t)
	envTestWrite(t, ".fglpkg/packages/poiapi/fglpkg.json",
		`{ "name": "poiapi", "version": "1.0.0", "profile": ["profiles/a.4gp"], "dependencies": { "fgl": {} } }`)
	envTestWrite(t, ".fglpkg/packages/poiapi/com/fourjs/poiapi/Customer.42f", "FORM")
	envTestWrite(t, ".fglpkg/packages/poiapi/schema/stores.sch", "SCH")
	envTestWrite(t, ".fglpkg/packages/poiapi/profiles/a.4gp", "A")

	g := New(t.TempDir())
	lines, err := g.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	raw, err := g.RawEnv()
	if err != nil {
		t.Fatalf("RawEnv: %v", err)
	}
	for _, key := range RawEnvOrder {
		want, ok := envValue(t, lines, key)
		if !ok {
			if _, present := raw[key]; present {
				t.Errorf("RawEnv has %s but Generate did not emit it", key)
			}
			continue
		}
		if raw[key] != want {
			t.Errorf("%s: RawEnv %q != Generate %q", key, raw[key], want)
		}
	}
}

// BenchmarkGenerateLocalLargeStore records the cost of the per-invocation store
// walk, which `fglpkg env` pays on every shell startup.
func BenchmarkGenerateLocalLargeStore(b *testing.B) {
	dir := b.TempDir()
	orig, _ := os.Getwd()
	b.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		b.Fatalf("chdir: %v", err)
	}
	pkgDir := filepath.Join(".fglpkg", "packages", "big")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		b.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "fglpkg.json"),
		[]byte(`{ "name": "big", "version": "1.0.0", "dependencies": { "fgl": {} } }`), 0o644); err != nil {
		b.Fatalf("write manifest: %v", err)
	}
	for i := 0; i < 100; i++ {
		sub := filepath.Join(pkgDir, "com", "acme", fmt.Sprintf("ns%02d", i))
		if err := os.MkdirAll(sub, 0o755); err != nil {
			b.Fatalf("mkdir: %v", err)
		}
		for j := 0; j < 10; j++ {
			name := fmt.Sprintf("Form%02d_%02d.42f", i, j)
			if err := os.WriteFile(filepath.Join(sub, name), []byte("F"), 0o644); err != nil {
				b.Fatalf("write: %v", err)
			}
		}
	}

	g := New(b.TempDir())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.GenerateLocal(); err != nil {
			b.Fatalf("GenerateLocal: %v", err)
		}
	}
}

// ─── shared fixtures ──────────────────────────────────────────────────────────

// writeUnder writes content at base/rel, creating parents. Used for the global
// home, which (unlike the local scope) is not the working directory.
func writeUnder(t *testing.T, base, rel, content string) {
	t.Helper()
	full := filepath.Join(base, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// indexOfPart returns the position of want among parts, or -1.
func indexOfPart(parts []string, want string) int {
	for i, p := range parts {
		if p == want {
			return i
		}
	}
	return -1
}
