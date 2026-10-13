package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/config"
	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	"github.com/4js-mikefolcher/fglpkg/internal/provider"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
)

func TestParseSearchArgs(t *testing.T) {
	cases := []struct {
		name         string
		args         []string
		wantTerm     string
		wantAll      bool
		wantGenero   string
		wantRegistry string
		wantErr      string
	}{
		{"keyword", []string{"foo"}, "foo", false, "", "", ""},
		{"all", []string{"--all"}, "", true, "", "", ""},
		{"no args errors", nil, "", false, "", "", "usage:"},
		{"all + term conflict", []string{"--all", "foo"}, "", false, "", "", "mutually exclusive"},
		{"two terms errors", []string{"foo", "bar"}, "", false, "", "", "extra argument"},
		{"genero flag separate value", []string{"--genero", "4.01", "foo"}, "foo", false, "4.01", "", ""},
		{"genero flag equals form", []string{"--genero=4.01", "foo"}, "foo", false, "4.01", "", ""},
		{"genero with all", []string{"--all", "--genero", "3.20"}, "", true, "3.20", "", ""},
		{"genero missing value", []string{"--genero"}, "", false, "", "", "requires a version"},
		{"genero empty equals form", []string{"--genero="}, "", false, "", "", "requires a version"},
		{"registry separate value", []string{"--registry", "acme", "foo"}, "foo", false, "", "acme", ""},
		{"registry equals form", []string{"foo", "--registry=acme"}, "foo", false, "", "acme", ""},
		{"registry with all", []string{"--all", "--registry", "acme"}, "", true, "", "acme", ""},
		{"registry missing value", []string{"foo", "--registry"}, "", false, "", "", "requires a value"},
		{"registry empty equals form", []string{"foo", "--registry="}, "", false, "", "", "requires a value"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			term, all, genero, registry, err := parseSearchArgs(c.args)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if term != c.wantTerm || all != c.wantAll || genero != c.wantGenero || registry != c.wantRegistry {
				t.Errorf("term=%q all=%v genero=%q registry=%q, want term=%q all=%v genero=%q registry=%q",
					term, all, genero, registry, c.wantTerm, c.wantAll, c.wantGenero, c.wantRegistry)
			}
		})
	}
}

func TestSearchDeprecatedStatus(t *testing.T) {
	cases := []struct {
		name       string
		deprecated bool
		movedTo    string
		want       string
	}{
		{"live", false, "", ""},
		{"live ignores stray movedTo", false, "chart-3d-ng", ""},
		{"deprecated no successor", true, "", "deprecated"},
		{"deprecated with successor", true, "chart-3d-ng", "deprecated -> chart-3d-ng"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := searchDeprecatedStatus(c.deprecated, c.movedTo); got != c.want {
				t.Errorf("searchDeprecatedStatus(%v, %q) = %q, want %q",
					c.deprecated, c.movedTo, got, c.want)
			}
		})
	}
}

func TestGradeCompat(t *testing.T) {
	v4 := genero.MustParse("4.01.12")
	cases := []struct {
		name       string
		target     *genero.Version
		constraint string
		variants   []string
		want       string
	}{
		{"no target version", nil, "^4.0.0", []string{"genero4"}, "?"},
		{"no signal at all", &v4, "", nil, "?"},
		{"no target and no signal", nil, "", nil, "?"},
		{"compatible", &v4, "^4.0.0", nil, "✓"},
		{"incompatible", &v4, "^3.0.0", nil, "✗"},
		{"star constraint is compatible", &v4, "*", nil, "✓"},
		{"unparseable constraint with nothing to fall back on", &v4, "not-a-constraint", nil, "?"},

		// The GIS-575 cases: no publisher declared a constraint, so the
		// published builds are the only evidence — and they are conclusive.
		{"builds for this major", &v4, "", []string{"genero4", "genero5", "genero6"}, "✓"},
		{"builds, but not for this major", &v4, "", []string{"genero5", "genero6"}, "✗"},
		{"web components run anywhere", &v4, "", []string{"webcomponent"}, "✓"},
		{"legacy single build runs anywhere", &v4, "", []string{"default"}, "✓"},
		// Reported, and empty: the version published nothing. Distinct from nil,
		// which is a registry that cannot answer.
		{"no builds published", &v4, "", []string{}, "✗"},

		// Both signals present: either one may veto.
		{"both agree", &v4, "^4.0.0", []string{"genero4"}, "✓"},
		{"constraint allows it, no build exists", &v4, "^4.0.0", []string{"genero6"}, "✗"},
		{"build exists, constraint forbids it", &v4, "^3.0.0", []string{"genero4"}, "✗"},

		// A constraint we cannot read drops that signal only. The builds are an
		// independent fact and still answer.
		{"unparseable constraint, builds say yes", &v4, "nonsense", []string{"genero4"}, "✓"},
		{"unparseable constraint, builds say no", &v4, "nonsense", []string{"genero6"}, "✗"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := gradeCompat(c.target, c.constraint, c.variants); got != c.want {
				t.Errorf("gradeCompat(%v, %q, %v) = %q, want %q",
					c.target, c.constraint, c.variants, got, c.want)
			}
		})
	}
}

// TestGradeCompatMatchesInstall pins the grade to what install actually does.
// A search that says "✓" over a version registry.Resolve would refuse — or "✗"
// over one it would happily install — is worse than no column at all, so both
// read the same registry.Runnable rather than each implementing the rule.
func TestGradeCompatMatchesInstall(t *testing.T) {
	v4 := genero.MustParse("4.01.12")
	cases := []struct {
		constraint string
		variants   []string
	}{
		{"", []string{"genero4"}},
		{"", []string{"genero5", "genero6"}},
		{"", []string{"webcomponent"}},
		{"", []string{}},
		{"^4.0.0", []string{"genero6"}},
		{"^3.0.0", []string{"genero4"}},
		{"^4.0.0", nil},
	}
	for _, c := range cases {
		runnable, _ := registry.Runnable(&v4, c.constraint, c.variants)
		want := "✗"
		if runnable {
			want = "✓"
		}
		if got := gradeCompat(&v4, c.constraint, c.variants); got != want {
			t.Errorf("gradeCompat(%q, %v) = %q but registry.Runnable says runnable=%v",
				c.constraint, c.variants, got, runnable)
		}
	}
}

// TestDisplayGenero pins the column to one rule: show the signal that DECIDED
// the row. A constraint is preferred while both agree, because it is the more
// precise of the two; when they disagree it would contradict the verdict
// printed beside it, so the builds are shown instead.
func TestDisplayGenero(t *testing.T) {
	v4 := genero.MustParse("4.01.12")
	cases := []struct {
		name       string
		target     *genero.Version
		constraint string
		variants   []string
		want       string
	}{
		// Both agree. The constraint carries a minimum patch level a list of
		// majors cannot express, so it wins.
		{"declared constraint wins when both agree", &v4, ">=4.1.3 <7.0.0", []string{"genero4", "genero5", "genero6"}, ">=4.1.3 <7.0.0"},
		// The constraint itself rules the row out and explains it.
		{"a constraint that excludes you is shown", &v4, "^6.0.0", []string{"genero6"}, "^6.0.0"},
		// The constraint admits Genero 4 but no genero4 build exists, so the
		// builds are what produced the ✗. Showing "^4.0.0 ✗" would read as a bug.
		{"the builds are shown when they are what said no", &v4, "^4.0.0", []string{"genero5", "genero6"}, "5, 6"},
		{"no builds outranks a constraint that admits you", &v4, "^4.0.0", []string{}, "none"},
		{"neither signal", &v4, "", nil, "-"},
		{"reported and empty", &v4, "", []string{}, "none"},
		{"build majors", &v4, "", []string{"genero4", "genero5", "genero6"}, "4, 5, 6"},
		{"single major", &v4, "", []string{"genero6"}, "6"},
		{"web component serves every major", &v4, "", []string{"webcomponent"}, "any"},
		{"legacy default serves every major", &v4, "", []string{"default"}, "any"},
		// Naming "6" here would understate it: VariantsSupport would install
		// this on Genero 4 too, and the column must not contradict the verdict.
		{"a universal build among majors", &v4, "", []string{"genero6", "webcomponent"}, "any"},
		// VariantsSupport has already read these as "no build for this major",
		// so the verdict beside them is ✗. "-" would claim nothing is known;
		// showing them verbatim explains the ✗ without vouching for them.
		{"unrecognised tags are shown verbatim", &v4, "", []string{"mystery"}, "mystery"},
		{"several unrecognised tags", &v4, "", []string{"foo", "bar"}, "foo, bar"},
		// With no runtime resolved nothing has been decided, so the more
		// precise signal is shown and the row grades "?".
		{"no target keeps the constraint", nil, "^4.0.0", []string{"genero6"}, "^4.0.0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := displayGenero(c.target, c.constraint, c.variants); got != c.want {
				t.Errorf("displayGenero(%v, %q, %v) = %q, want %q",
					c.target, c.constraint, c.variants, got, c.want)
			}
		})
	}
}

// TestDisplayGeneroNeverContradictsTheVerdict is the rule behind the cases
// above, asserted directly: a row whose verdict is ✗ must not show a GENERO
// cell that reads as "unknown", and a ✓ row must not show one that reads as
// "nothing to install". The column and the marker are the two halves of one
// sentence, and a reader takes them together.
func TestDisplayGeneroNeverContradictsTheVerdict(t *testing.T) {
	v4 := genero.MustParse("4.01.12")
	variantSets := [][]string{
		nil, {}, {"genero4"}, {"genero5", "genero6"}, {"webcomponent"},
		{"default"}, {"genero6", "webcomponent"}, {"mystery"}, {"foo", "bar"},
	}
	constraints := []string{"", "^4.0.0", "^6.0.0", ">=4.1.3 <7.0.0", "*"}
	for _, variants := range variantSets {
		for _, constraint := range constraints {
			cell := displayGenero(&v4, constraint, variants)
			switch gradeCompat(&v4, constraint, variants) {
			case gradeIncompatible:
				if cell == "-" {
					t.Errorf("constraint=%q variants=%v graded ✗ but the column says %q (unknown)",
						constraint, variants, cell)
				}
			case gradeCompatible:
				if cell == "none" || cell == "-" {
					t.Errorf("constraint=%q variants=%v graded ✓ but the column says %q",
						constraint, variants, cell)
				}
			}
		}
	}
}

// TestCmdSearchStatusColumnConditional confirms the STATUS column is omitted
// entirely when no match is deprecated (byte-for-byte the original layout) and
// appears only once at least one match carries a deprecation.
func TestCmdSearchStatusColumnConditional(t *testing.T) {
	cases := []struct {
		name       string
		packages   []map[string]any
		wantStatus bool   // STATUS header expected in output
		wantSubstr string // value that must appear when deprecated
	}{
		{
			name: "all live omits STATUS column",
			packages: []map[string]any{
				{"slug": "chart-lite", "latest_version": "0.9.0", "description": "lightweight charts"},
				{"slug": "chart-pro", "latest_version": "2.0.0", "description": "pro charts"},
			},
			wantStatus: false,
		},
		{
			name: "deprecated match shows STATUS column",
			packages: []map[string]any{
				{"slug": "chart-3d", "latest_version": "1.2.3", "description": "3D charts",
					"deprecated": true, "moved_to": "chart-3d-ng"},
				{"slug": "chart-lite", "latest_version": "0.9.0", "description": "lightweight charts"},
			},
			wantStatus: true,
			wantSubstr: "deprecated -> chart-3d-ng",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"packages": c.packages})
			}))
			defer ts.Close()
			t.Setenv("FGLPKG_REGISTRY", ts.URL)
			// Isolate the fglpkg home so a developer's real config doesn't add a
			// second provider and divert us onto the multi-provider path.
			t.Setenv("FGLPKG_HOME", t.TempDir())

			out, err := captureStdout(t, func() error { return cmdSearch([]string{"--all"}) })
			if err != nil {
				t.Fatalf("cmdSearch: %v", err)
			}
			if got := strings.Contains(out, "STATUS"); got != c.wantStatus {
				t.Errorf("STATUS column present = %v, want %v\noutput:\n%s", got, c.wantStatus, out)
			}
			if c.wantStatus {
				if !strings.Contains(out, c.wantSubstr) {
					t.Errorf("output missing %q\noutput:\n%s", c.wantSubstr, out)
				}
			} else if strings.Contains(out, "deprecated") {
				t.Errorf("live-only output unexpectedly mentions deprecation\noutput:\n%s", out)
			}
		})
	}
}

// browsePackagesServer returns an httptest server standing in for a registry:
// the browse endpoint (`GET /registry/packages?q=…`), wrapped in the
// {"packages":[…],"total":N} envelope registry.Search expects, and the
// per-package detail endpoint (`GET /registry/packages/<slug>`) search now
// consults to confirm a ✗ against the package's other versions.
//
// A package may carry a "versions" key — a list of {"version", "genero",
// "variants"} — to describe more than the one the listing names. Without it the
// detail view reports exactly the listed version, so a fixture that says
// nothing about older versions gets a registry where none exist, and the
// step-down check has nothing to find.
func browsePackagesServer(t *testing.T, packages []map[string]any) *httptest.Server {
	t.Helper()
	detail := func(p map[string]any) map[string]any {
		raw, ok := p["versions"].([]map[string]any)
		if !ok {
			raw = []map[string]any{{
				"version":  p["latest_version"],
				"genero":   p["genero"],
				"variants": p["variants"],
			}}
		}
		versions := make([]map[string]any, 0, len(raw))
		for _, v := range raw {
			variants, _ := v["variants"].([]string)
			artifacts := make([]map[string]any, 0, len(variants))
			for _, variant := range variants {
				artifacts = append(artifacts, map[string]any{"variant": variant})
			}
			versions = append(versions, map[string]any{
				"version":   v["version"],
				"genero":    v["genero"],
				"status":    "published",
				"artifacts": artifacts,
			})
		}
		return map[string]any{
			"slug":           p["slug"],
			"name":           p["name"],
			"latest_version": p["latest_version"],
			"versions":       versions,
		}
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if slug := strings.TrimPrefix(r.URL.Path, "/registry/packages/"); slug != r.URL.Path && slug != "" {
			for _, p := range packages {
				if p["slug"] == slug {
					_ = json.NewEncoder(w).Encode(detail(p))
					return
				}
			}
			http.Error(w, `{"error":"Package not found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"packages": packages,
			"total":    len(packages),
		})
	}))
	t.Cleanup(ts.Close)
	return ts
}

// lineContaining returns the first line of out that contains sub, or "".
func lineContaining(out, sub string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, sub) {
			return line
		}
	}
	return ""
}

// TestCmdSearchRendersAnnotatedTable is the golden rendering check: a mixed
// result set (compatible / incompatible / no-constraint) graded against an
// explicit --genero version must show the version in the header, a GENERO
// constraint column, and the ✓/✗/? verdict column.
func TestCmdSearchRendersAnnotatedTable(t *testing.T) {
	ts := browsePackagesServer(t, []map[string]any{
		{"slug": "jsonutils", "name": "jsonutils", "description": "JSON helpers",
			"latest_version": "2.1.0", "owner": map[string]any{"name": "ACME"}, "genero": "^4.0.0"},
		{"slug": "legacyjson", "name": "legacyjson", "description": "JSON for Genero 3",
			"latest_version": "1.4.0", "owner": map[string]any{"name": "ACME"}, "genero": "^3.0.0"},
		{"slug": "mystery", "name": "mystery", "description": "no constraint reported",
			"latest_version": "0.9.0", "owner": map[string]any{"name": "ACME"}},
	})
	t.Setenv("FGLPKG_REGISTRY", ts.URL)
	// Isolate fglpkg home so a real config's Artifactory repo doesn't divert
	// the search to the multi-provider (ungraded) path.
	t.Setenv("FGLPKG_HOME", t.TempDir())

	// A bare major.minor override (no patch) must be accepted and shown
	// verbatim in the header — grading does not require a patch level.
	out, err := captureStdout(t, func() error {
		return cmdSearch([]string{"--genero", "4.01", "json"})
	})
	if err != nil {
		t.Fatalf("cmdSearch: %v", err)
	}

	if want := `Results for "json" (Genero 4.01):`; !strings.Contains(out, want) {
		t.Errorf("header missing %q\n--- output ---\n%s", want, out)
	}
	header := lineContaining(out, "NAME")
	for _, col := range []string{"VERSION", "GENERO", "?", "DESCRIPTION"} {
		if !strings.Contains(header, col) {
			t.Errorf("column header %q missing from %q", col, header)
		}
	}

	cases := []struct {
		pkg, constraint, marker string
	}{
		{"jsonutils", "^4.0.0", "✓"},
		{"legacyjson", "^3.0.0", "✗"},
		{"mystery", "-", "?"},
	}
	for _, c := range cases {
		line := lineContaining(out, c.pkg)
		if line == "" {
			t.Errorf("no row for %q\n--- output ---\n%s", c.pkg, out)
			continue
		}
		if !strings.Contains(line, c.constraint) {
			t.Errorf("%s row = %q, want GENERO %q", c.pkg, line, c.constraint)
		}
		if !strings.Contains(line, c.marker) {
			t.Errorf("%s row = %q, want marker %q", c.pkg, line, c.marker)
		}
	}
}

// TestCmdSearchGradesFromPublishedBuilds is the GIS-575 reproduction, end to
// end over the browse endpoint: the registry's real package set, where only
// `qrcode` declares a `genero` constraint. Before this, every other row showed
// "-" / "?" — including `odatalib`, which publishes genero5 and genero6 only
// and provably cannot run here. The published builds now answer for the
// packages whose publisher declared nothing.
func TestCmdSearchGradesFromPublishedBuilds(t *testing.T) {
	ts := browsePackagesServer(t, []map[string]any{
		{"slug": "poiapi", "name": "poiapi", "description": "spreadsheets",
			"latest_version": "1.9.1", "owner": map[string]any{"name": "4Js"},
			"variants": []string{"genero4", "genero5", "genero6"}},
		{"slug": "odatalib", "name": "odatalib", "description": "OData client",
			"latest_version": "1.2.0", "owner": map[string]any{"name": "4Js"},
			"variants": []string{"genero5", "genero6"}},
		{"slug": "fglunit", "name": "fglunit", "description": "unit testing",
			"latest_version": "1.0.1", "owner": map[string]any{"name": "4Js"},
			"variants": []string{"genero6"}},
		{"slug": "qrcode", "name": "qrcode", "description": "QR codes",
			"latest_version": "1.2.0", "owner": map[string]any{"name": "4Js"},
			"genero": ">=4.1.3 <7.0.0", "variants": []string{"genero4", "genero5", "genero6"}},
		{"slug": "megamenu", "name": "megamenu", "description": "a menu widget",
			"latest_version": "2.1.1", "owner": map[string]any{"name": "4Js"},
			"variants": []string{"webcomponent"}},
		{"slug": "halfpublished", "name": "halfpublished", "description": "upload never finished",
			"latest_version": "0.1.0", "owner": map[string]any{"name": "4Js"},
			"variants": []string{}},
		// No `variants` key at all — a registry predating the field. Must stay
		// "unknown": reading its silence as "publishes nothing" would mark every
		// package on an older registry incompatible.
		{"slug": "oldreg", "name": "oldreg", "description": "served by an older registry",
			"latest_version": "0.9.0", "owner": map[string]any{"name": "4Js"}},
	})
	t.Setenv("FGLPKG_REGISTRY", ts.URL)
	t.Setenv("FGLPKG_HOME", t.TempDir())

	out, err := captureStdout(t, func() error {
		return cmdSearch([]string{"--all", "--genero", "4.01.12"})
	})
	if err != nil {
		t.Fatalf("cmdSearch: %v", err)
	}

	cases := []struct {
		pkg, genero, marker string
	}{
		{"poiapi", "4, 5, 6", "✓"},
		{"odatalib", "5, 6", "✗"},
		{"fglunit", "6", "✗"},
		// A declared constraint is more precise than a build list, so it is what
		// the column shows even though both signals are present.
		{"qrcode", ">=4.1.3 <7.0.0", "✓"},
		{"megamenu", "any", "✓"},
		{"halfpublished", "none", "✗"},
		{"oldreg", "-", "?"},
	}
	for _, c := range cases {
		line := lineContaining(out, c.pkg)
		if line == "" {
			t.Errorf("no row for %q\n--- output ---\n%s", c.pkg, out)
			continue
		}
		if !strings.Contains(line, c.genero) {
			t.Errorf("%s row = %q, want GENERO %q", c.pkg, line, c.genero)
		}
		if !strings.Contains(line, c.marker) {
			t.Errorf("%s row = %q, want marker %q", c.pkg, line, c.marker)
		}
	}
	// The whole point of the ticket: "?" is now the exception, not the rule.
	if n := strings.Count(out, "?"); n > 2 {
		t.Errorf("expected at most the header's and oldreg's %q, got %d\n--- output ---\n%s", "?", n, out)
	}
}

// TestCmdSearchStepsDownToTheVersionInstallWouldGive is the end-to-end form of
// the step-down check, with fglunit's real published shape: 1.0.1 ships a
// genero6 build only, 1.0.0 ships all three. Before this, search answered ✗ on
// Genero 4 for a package `fglpkg install fglunit` installs without complaint.
func TestCmdSearchStepsDownToTheVersionInstallWouldGive(t *testing.T) {
	ts := browsePackagesServer(t, []map[string]any{
		{"slug": "fglunit", "name": "fglunit", "description": "unit testing",
			"latest_version": "1.0.1", "owner": map[string]any{"name": "4Js"},
			"variants": []string{"genero6"},
			"versions": []map[string]any{
				{"version": "1.0.1", "genero": "", "variants": []string{"genero6"}},
				{"version": "1.0.0", "genero": "", "variants": []string{"genero4", "genero5", "genero6"}},
			}},
		// No version of this one runs on Genero 4, so its ✗ is confirmed rather
		// than overturned — and it must not pick up a * marker.
		{"slug": "odatalib", "name": "odatalib", "description": "OData client",
			"latest_version": "1.2.0", "owner": map[string]any{"name": "4Js"},
			"variants": []string{"genero5", "genero6"},
			"versions": []map[string]any{
				{"version": "1.2.0", "genero": "", "variants": []string{"genero5", "genero6"}},
				{"version": "1.1.0", "genero": "", "variants": []string{"genero5"}},
			}},
	})
	t.Setenv("FGLPKG_REGISTRY", ts.URL)
	t.Setenv("FGLPKG_HOME", t.TempDir())

	out, err := captureStdout(t, func() error {
		return cmdSearch([]string{"--all", "--genero", "4.01.12"})
	})
	if err != nil {
		t.Fatalf("cmdSearch: %v", err)
	}

	line := lineContaining(out, "fglunit")
	for _, want := range []string{"1.0.0*", "4, 5, 6", "✓"} {
		if !strings.Contains(line, want) {
			t.Errorf("fglunit row = %q, want %q", line, want)
		}
	}
	if strings.Contains(line, "1.0.1") {
		t.Errorf("fglunit row = %q, should describe 1.0.0 throughout", line)
	}
	// The latest version is not lost — it moves to the note under the table.
	for _, want := range []string{"* shows the newest version that runs on Genero 4.01.12", "latest is 1.0.1"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, out)
		}
	}

	odata := lineContaining(out, "odatalib")
	if !strings.Contains(odata, "✗") || strings.Contains(odata, "*") {
		t.Errorf("odatalib row = %q, want a confirmed ✗ with no * marker", odata)
	}
}

// TestCmdSearchKeepsColumnsAlignedForALongVersion guards the VERSION column's
// width: a long version plus the step-down marker overflowed the old fixed
// %-12s, shifting that one row's GENERO and verdict cells right relative to
// every other row. Both rows grade ✓, so with a correctly sized column their
// descriptions start at the same offset.
//
// The stepped-down version is stable rather than a prerelease: "latest" never
// resolves to a prerelease, so one can no longer carry the marker — a long
// VERSION cell now comes either from a long release or from a listing whose
// latest_version is itself a prerelease.
func TestCmdSearchKeepsColumnsAlignedForALongVersion(t *testing.T) {
	ts := browsePackagesServer(t, []map[string]any{
		{"slug": "longver", "name": "longver", "description": "LONGDESC",
			"latest_version": "100.200.3000", "owner": map[string]any{"name": "4Js"},
			"variants": []string{"genero6"},
			"versions": []map[string]any{
				{"version": "100.200.3000", "genero": "", "variants": []string{"genero6"}},
				{"version": "100.200.2999", "genero": "", "variants": []string{"genero4"}},
			}},
		{"slug": "shortver", "name": "shortver", "description": "SHORTDESC",
			"latest_version": "1.0.0", "owner": map[string]any{"name": "4Js"},
			"variants": []string{"genero4"}},
	})
	t.Setenv("FGLPKG_REGISTRY", ts.URL)
	t.Setenv("FGLPKG_HOME", t.TempDir())

	out, err := captureStdout(t, func() error {
		return cmdSearch([]string{"--all", "--genero", "4.01.12"})
	})
	if err != nil {
		t.Fatalf("cmdSearch: %v", err)
	}

	long := lineContaining(out, "LONGDESC")
	short := lineContaining(out, "SHORTDESC")
	if long == "" || short == "" {
		t.Fatalf("missing rows\n--- output ---\n%s", out)
	}
	// The marked version must render in full, not truncated.
	if !strings.Contains(long, "100.200.2999*") {
		t.Errorf("long row = %q, want the full marked version", long)
	}
	if a, b := strings.Index(long, "LONGDESC"), strings.Index(short, "SHORTDESC"); a != b {
		t.Errorf("descriptions start at %d and %d — columns are misaligned\n%s\n%s\n%s",
			a, b, out, long, short)
	}
}

// TestCmdSearchNoNoteWhenNothingSteppedDown keeps the common listing clean: the
// note is a cost paid only by the rows that needed it.
func TestCmdSearchNoNoteWhenNothingSteppedDown(t *testing.T) {
	ts := browsePackagesServer(t, []map[string]any{
		{"slug": "poiapi", "name": "poiapi", "description": "spreadsheets",
			"latest_version": "1.9.1", "owner": map[string]any{"name": "4Js"},
			"variants": []string{"genero4", "genero5", "genero6"}},
	})
	t.Setenv("FGLPKG_REGISTRY", ts.URL)
	t.Setenv("FGLPKG_HOME", t.TempDir())

	out, err := captureStdout(t, func() error {
		return cmdSearch([]string{"--all", "--genero", "4.01.12"})
	})
	if err != nil {
		t.Fatalf("cmdSearch: %v", err)
	}
	if strings.Contains(out, "*") {
		t.Errorf("unexpected step-down marker or note:\n%s", out)
	}
}

// TestCmdSearchUnknownVersionFallback covers the no-target path: when no Genero
// version can be resolved, search still runs, the header explains how to set
// one, and every result is graded "?" even when the registry reports a real
// constraint.
func TestCmdSearchUnknownVersionFallback(t *testing.T) {
	ts := browsePackagesServer(t, []map[string]any{
		{"slug": "jsonutils", "name": "jsonutils", "description": "JSON helpers",
			"latest_version": "2.1.0", "owner": map[string]any{"name": "ACME"}, "genero": "^4.0.0"},
	})
	t.Setenv("FGLPKG_REGISTRY", ts.URL)
	t.Setenv("FGLPKG_HOME", t.TempDir())
	// Force genero.Detect() to fail deterministically: no override, no $FGLDIR,
	// and an empty PATH so fglcomp cannot be discovered on a Genero dev machine.
	t.Setenv("FGLPKG_GENERO_VERSION", "")
	t.Setenv("FGLDIR", "")
	t.Setenv("PATH", t.TempDir())

	out, err := captureStdout(t, func() error {
		return cmdSearch([]string{"json"})
	})
	if err != nil {
		t.Fatalf("cmdSearch: %v", err)
	}

	if want := "Genero version unknown"; !strings.Contains(out, want) {
		t.Errorf("fallback header missing %q\n--- output ---\n%s", want, out)
	}
	// A real constraint is reported, but with no target it must still grade "?".
	line := lineContaining(out, "jsonutils")
	if !strings.Contains(line, "?") {
		t.Errorf("jsonutils row = %q, want marker %q", line, "?")
	}
	if strings.Contains(line, "✓") || strings.Contains(line, "✗") {
		t.Errorf("jsonutils row = %q, want no ✓/✗ verdict with unknown version", line)
	}
}

// TestCmdSearchAllSurfacesCleanErrorOn400 simulates an old registry that
// still rejects empty `q` and confirms the client surfaces the upgrade
// hint rather than leaking the raw HTTP 400.
func TestCmdSearchAllSurfacesCleanErrorOn400(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") == "" {
			http.Error(w, "missing query parameter q", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	}))
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)
	// Isolate the fglpkg home so a developer's real ~/.fglpkg/config.json
	// (e.g. a globally-configured Artifactory repo) doesn't add a second
	// provider that satisfies the search and masks the GI 400 under test.
	t.Setenv("FGLPKG_HOME", t.TempDir())

	err := cmdSearch([]string{"--all"})
	if err == nil {
		t.Fatal("expected error from old server's 400, got nil")
	}
	if !strings.Contains(err.Error(), "doesn't support --all") {
		t.Errorf("err = %v, want one explaining --all unsupported", err)
	}
}

// TestCmdSearchRegistrySingleRegistry covers --registry on the single-registry
// (only built-in GI configured) path: `--registry gi` is a harmless no-op that
// still searches, while any other name errors like install/update do, since no
// such repository is configured.
func TestCmdSearchRegistrySingleRegistry(t *testing.T) {
	ts := browsePackagesServer(t, []map[string]any{
		{"slug": "jsonutils", "name": "jsonutils", "description": "JSON helpers",
			"latest_version": "2.1.0", "owner": map[string]any{"name": "ACME"}},
	})
	t.Setenv("FGLPKG_REGISTRY", ts.URL)
	// Isolate the fglpkg home so a developer's real config doesn't add a second
	// provider and divert onto the multi-provider path.
	t.Setenv("FGLPKG_HOME", t.TempDir())

	// --registry gi: no-op, search still runs and returns the package.
	out, err := captureStdout(t, func() error {
		return cmdSearch([]string{"json", "--registry", "gi"})
	})
	if err != nil {
		t.Fatalf("--registry gi: unexpected error: %v", err)
	}
	if !strings.Contains(out, "jsonutils") {
		t.Errorf("--registry gi should still search; output missing result:\n%s", out)
	}

	// --registry <unknown>: hard error, matching install/update wording.
	err = cmdSearch([]string{"json", "--registry", "acme"})
	if err == nil {
		t.Fatal("--registry acme: expected error for unconfigured registry, got nil")
	}
	if !strings.Contains(err.Error(), "no repository named") {
		t.Errorf("err = %v, want one explaining the registry isn't configured", err)
	}
}

// TestSearchAcrossProvidersRestrict verifies the --registry filter on the
// multi-provider search path: with no restriction both repos are queried; with
// a restriction only the named repo's results appear; and an unknown name is a
// hard error listing the configured registries. (searchStub is defined in
// search_dedup_test.go.)
func TestSearchAcrossProvidersRestrict(t *testing.T) {
	gi := &searchStub{name: "gi", results: []registry.SearchResult{
		{Name: "gipkg", LatestVersion: "1.0.0", Description: "from gi"},
	}}
	acme := &searchStub{name: "acme", results: []registry.SearchResult{
		{Name: "acmepkg", LatestVersion: "2.0.0", Description: "from acme"},
	}}
	descs := []config.Registry{
		{Name: "gi", Type: config.TypeGenero, URL: "https://gi", Priority: 1},
		{Name: "acme", Type: config.TypeArtifactory, URL: "https://a", RepoKey: "k", Priority: 2},
	}
	rs := provider.NewRepositorySet([]provider.Provider{gi, acme}, descs, nil)

	// No restriction: both repos queried.
	out, err := captureStdout(t, func() error {
		return searchAcrossProviders(rs, "pkg", false, nil, "")
	})
	if err != nil {
		t.Fatalf("unrestricted: %v", err)
	}
	if !strings.Contains(out, "gipkg") || !strings.Contains(out, "acmepkg") {
		t.Errorf("unrestricted search should show both repos' results:\n%s", out)
	}

	// Restrict to acme: only acme's result appears.
	out, err = captureStdout(t, func() error {
		return searchAcrossProviders(rs, "pkg", false, nil, "acme")
	})
	if err != nil {
		t.Fatalf("restricted: %v", err)
	}
	if strings.Contains(out, "gipkg") {
		t.Errorf("--registry acme should exclude gi results:\n%s", out)
	}
	if !strings.Contains(out, "acmepkg") {
		t.Errorf("--registry acme should include acme results:\n%s", out)
	}

	// Unknown registry: hard error listing configured names.
	err = searchAcrossProviders(rs, "pkg", false, nil, "ghost")
	if err == nil {
		t.Fatal("--registry ghost: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no repository named") ||
		!strings.Contains(err.Error(), "gi, acme") {
		t.Errorf("err = %v, want one naming the unknown registry and listing gi, acme", err)
	}
}
