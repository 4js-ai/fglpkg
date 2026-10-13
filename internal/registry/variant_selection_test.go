package registry_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

// GIS-574. pickArtifact used to end in `return &arts[0]`, so a caller asking
// for a Genero major the version does not publish received an arbitrary build
// — the genero6 zip for a Genero 4 install, with no error and no warning. A
// named major that is not published must now fail.
//
// The fallback is kept for callers that name no major: `info` and `outdated`
// read metadata only and must keep working for a package that publishes
// nothing for the running runtime.

// genero6Only is fglunit 1.0.1's shape: one build, no "default", no constraint.
func genero6Only(t *testing.T) {
	t.Helper()
	ts := newPackagesServer(t, map[string]any{
		"slug": "fglunit",
		"versions": []map[string]any{
			{"version": "1.0.1", "artifacts": []map[string]any{
				{"variant": "genero6", "sha256": "bb", "download_url": "https://r2/g6.zip"},
			}},
		},
	}, nil)
	t.Cleanup(ts.Close)
	t.Setenv("FGLPKG_REGISTRY", ts.URL)
}

func TestFetchInfoForGeneroRejectsAMajorWithNoBuild(t *testing.T) {
	genero6Only(t)

	_, err := registry.FetchInfoForGenero("fglunit", "1.0.1", "4")
	if err == nil {
		t.Fatal("expected an error for a Genero major with no published build, got nil")
	}
	// Naming the variants that do exist is what lets the user act on this.
	for _, want := range []string{"fglunit", "1.0.1", "Genero 4", "genero6"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	// Typed, so a metadata-only caller (`info`) can tell it apart from a
	// transport failure and retry without a major.
	var nb *registry.NoBuildError
	if !errors.As(err, &nb) {
		t.Errorf("error %q is not a *registry.NoBuildError", err)
	}
}

func TestFetchInfoForGeneroServesAMajorThatExists(t *testing.T) {
	genero6Only(t)

	info, err := registry.FetchInfoForGenero("fglunit", "1.0.1", "6")
	if err != nil {
		t.Fatalf("FetchInfoForGenero: %v", err)
	}
	if info.Variant != "genero6" {
		t.Errorf("Variant = %q, want %q", info.Variant, "genero6")
	}
}

// `info` and `outdated` call FetchInfo, which passes an empty major. They read
// description and deprecation state, never install, so they must still resolve
// a package that has no build for the running runtime.
func TestFetchInfoWithoutAMajorStillFallsBack(t *testing.T) {
	genero6Only(t)

	info, err := registry.FetchInfo("fglunit", "1.0.1")
	if err != nil {
		t.Fatalf("FetchInfo with no Genero major must still return metadata: %v", err)
	}
	if info.Variant != "genero6" {
		t.Errorf("Variant = %q, want the only published artifact %q", info.Variant, "genero6")
	}
}

// The "default" fallback is unchanged and still covered by
// TestFetchInfoForGeneroFallsBackToDefault in registry_test.go — a default
// artifact is selected before the new nil return is reached.

// FetchVersionList must carry the variant tags, since they are the signal the
// resolver filters on.
func TestFetchVersionListProjectsVariants(t *testing.T) {
	ts := newPackagesServer(t, map[string]any{
		"slug": "fglunit",
		"versions": []map[string]any{
			{"version": "1.0.0", "artifacts": []map[string]any{
				{"variant": "genero4", "sha256": "a", "download_url": "https://r2/4.zip"},
				{"variant": "genero6", "sha256": "b", "download_url": "https://r2/6.zip"},
			}},
			{"version": "1.0.1", "artifacts": []map[string]any{
				{"variant": "genero6", "sha256": "c", "download_url": "https://r2/6b.zip"},
			}},
		},
	}, nil)
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	vl, err := registry.FetchVersionList("fglunit")
	if err != nil {
		t.Fatalf("FetchVersionList: %v", err)
	}
	for _, tc := range []struct {
		version string
		want    []string
	}{
		{"1.0.0", []string{"genero4", "genero6"}},
		{"1.0.1", []string{"genero6"}},
	} {
		e := vl.EntryFor(tc.version)
		if e == nil {
			t.Fatalf("no entry for %s", tc.version)
		}
		if strings.Join(e.Variants, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s Variants = %v, want %v", tc.version, e.Variants, tc.want)
		}
	}
}

// ── install <pkg>: the two compatibility signals, and whose fault a failure is ─

func gv(t *testing.T, s string) *genero.Version {
	t.Helper()
	v := genero.MustParse(s)
	return &v
}

// serve publishes one package whose versions are given as
// version → {declared genero constraint, variant tags}.
func serve(t *testing.T, slug string, versions [][3]any) {
	t.Helper()
	vs := make([]map[string]any, 0, len(versions))
	for _, row := range versions {
		arts := make([]map[string]any, 0)
		for _, variant := range row[2].([]string) {
			arts = append(arts, map[string]any{
				"variant": variant, "sha256": "aa", "download_url": "https://r2/" + variant + ".zip",
			})
		}
		vs = append(vs, map[string]any{
			"version": row[0].(string), "genero": row[1].(string), "artifacts": arts,
		})
	}
	ts := newPackagesServer(t, map[string]any{"slug": slug, "versions": vs}, nil)
	t.Cleanup(ts.Close)
	t.Setenv("FGLPKG_REGISTRY", ts.URL)
}

// GIS-574 #1. Resolve honoured the published variants but not the declared
// "genero" constraint, so `install fx` could select a version the dependency
// resolver then rejects — and the exact pin is written to fglpkg.json before
// the graph install, so the bad pin survived the failure.
func TestResolveHonoursTheDeclaredGeneroConstraint(t *testing.T) {
	serve(t, "fx", [][3]any{
		{"1.0.0", "", []string{"genero4"}},
		{"1.2.0", ">=6.00", []string{"genero4", "genero6"}},
	})

	info, err := registry.Resolve("fx", "^1.0.0", gv(t, "4.01.12"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// 1.2.0 publishes a genero4 build but declares it needs Genero 6.
	if info.Version != "1.0.0" {
		t.Errorf("resolved fx@%s, want 1.0.0 — 1.2.0 declares >=6.00", info.Version)
	}
}

// GIS-574 #2. Versions dropped for Genero were reported even when they never
// satisfied the constraint, so a plain "no such version" read as a Genero fault.
func TestResolveBlamesTheConstraintWhenNoVersionMatchesIt(t *testing.T) {
	serve(t, "fx", [][3]any{
		{"1.0.0", "", []string{"genero4"}},
		{"2.0.0", "", []string{"genero6"}},
	})

	_, err := registry.Resolve("fx", "^3.0.0", gv(t, "4.01.12"))
	if err == nil {
		t.Fatal("expected an error for ^3.0.0 with no 3.x published, got nil")
	}
	if !strings.Contains(err.Error(), `no version of "fx" satisfies constraint "^3.0.0"`) {
		t.Errorf("error %q should blame the constraint", err)
	}
	// 2.0.0 was dropped for Genero, but it never matched ^3.0.0 either, so
	// naming it here sends the user looking at Genero for a versioning problem.
	if strings.Contains(err.Error(), "can run on Genero") || strings.Contains(err.Error(), "2.0.0") {
		t.Errorf("error %q blames Genero for a constraint mismatch", err)
	}
}

// The Genero message is still used when a constraint-matching version exists
// but cannot run here.
func TestResolveBlamesGeneroWhenAMatchingVersionCannotRun(t *testing.T) {
	serve(t, "fx", [][3]any{{"1.0.0", "", []string{"genero6"}}})

	_, err := registry.Resolve("fx", "^1.0.0", gv(t, "4.01.12"))
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	for _, want := range []string{"can run on Genero 4", "1.0.0 has builds for Genero 6"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// GIS-574 #3. GI always builds the variants slice, so a version whose upload
// failed arrives empty-but-not-nil. That used to read as "provider reports no
// variants, allow", and the newest such version was chosen and then failed at
// fetch time with a bare "no artifact available", with no step-down.
func TestResolveSkipsAVersionWithNoArtifacts(t *testing.T) {
	serve(t, "fx", [][3]any{
		{"1.0.0", "", []string{"genero4"}},
		{"1.3.0", "", []string{}}, // upload failed: no artifacts at all
	})

	info, err := registry.Resolve("fx", "^1.0.0", gv(t, "4.01.12"))
	if err != nil {
		t.Fatalf("a version with no artifacts must be skipped, not chosen: %v", err)
	}
	if info.Version != "1.0.0" {
		t.Errorf("resolved fx@%s, want 1.0.0", info.Version)
	}
}

// A provider that reports no variants at all (nil, not empty) is still allowed:
// Artifactory and older registries cannot report them, and treating "unknown"
// as "incompatible" would make those packages unresolvable.
func TestVariantsSupportDistinguishesUnreportedFromNone(t *testing.T) {
	if !registry.VariantsSupport(nil, "4") {
		t.Error("nil variants mean the provider does not report them — must be allowed")
	}
	if registry.VariantsSupport([]string{}, "4") {
		t.Error("an empty-but-present variant list means no builds exist — must be refused")
	}
}

// cand is a VersionCandidate built from a version string, for selection tests.
func cand(version, constraint string, variants []string) registry.VersionCandidate {
	return registry.VersionCandidate{
		Version:          semver.MustParse(version),
		GeneroConstraint: constraint,
		Variants:         variants,
	}
}

// TestSelectInstallableSkipsPrereleases pins the rule that made search and
// install disagree: `install <pkg>` resolves "latest" through a constraint
// whose Matches rejects a prerelease unless the constraint names one. Any
// caller that selected by version comparison alone would pick the beta.
func TestSelectInstallableSkipsPrereleases(t *testing.T) {
	v4 := genero.MustParse("4.01.12")
	sel, err := registry.SelectInstallable([]registry.VersionCandidate{
		cand("1.0.0", "", []string{"genero4"}),
		cand("2.0.0-beta.1", "", []string{"genero4"}),
		cand("2.0.0", "", []string{"genero6"}),
	}, "latest", &v4)
	if err != nil {
		t.Fatalf("SelectInstallable: %v", err)
	}
	if !sel.Found || sel.Best.Version.String() != "1.0.0" {
		t.Errorf("found=%v best=%v, want 1.0.0 — the beta is newer but not selectable",
			sel.Found, sel.Best.Version)
	}
}

// A constraint that names a prerelease opts into them, and the selection must
// follow — otherwise `install pkg@2.0.0-beta.1` could not resolve at all.
func TestSelectInstallableHonoursAnExplicitPrereleaseConstraint(t *testing.T) {
	v4 := genero.MustParse("4.01.12")
	sel, err := registry.SelectInstallable([]registry.VersionCandidate{
		cand("1.0.0", "", []string{"genero4"}),
		cand("2.0.0-beta.1", "", []string{"genero4"}),
	}, "2.0.0-beta.1", &v4)
	if err != nil {
		t.Fatalf("SelectInstallable: %v", err)
	}
	if !sel.Found || sel.Best.Version.String() != "2.0.0-beta.1" {
		t.Errorf("found=%v best=%v, want the named prerelease", sel.Found, sel.Best.Version)
	}
}

// Both compatibility signals still gate the choice, and the version that wins
// carries its own constraint and builds with it.
func TestSelectInstallableStepsPastWhatCannotRun(t *testing.T) {
	v4 := genero.MustParse("4.01.12")
	sel, err := registry.SelectInstallable([]registry.VersionCandidate{
		cand("1.0.0", "", []string{"genero4", "genero5"}),
		cand("1.5.0", "^6.0.0", []string{"genero4"}), // constraint excludes Genero 4
		cand("2.0.0", "", []string{"genero6"}),       // no genero4 build
	}, "latest", &v4)
	if err != nil {
		t.Fatalf("SelectInstallable: %v", err)
	}
	if !sel.Found || sel.Best.Version.String() != "1.0.0" {
		t.Fatalf("found=%v best=%v, want 1.0.0", sel.Found, sel.Best.Version)
	}
	if got := sel.Best.Variants; len(got) != 2 || got[0] != "genero4" {
		t.Errorf("best.Variants = %v, want 1.0.0's own builds", got)
	}
	// Both were excluded by Genero, not by the version constraint, so both are
	// explainable — that is what separates "nothing matches" from "nothing runs".
	if len(sel.Skipped) != 2 {
		t.Errorf("Skipped = %v, want both rejected versions named", sel.Skipped)
	}
}

// A version the version-constraint never admitted is NOT reported as a Genero
// failure: `install fx@^3.0.0` must not be told that 4.x "has builds for
// Genero 5, 6" when no 3.x was ever published.
func TestSelectInstallableBlamesGeneroOnlyForVersionsTheConstraintAccepted(t *testing.T) {
	v4 := genero.MustParse("4.01.12")
	sel, err := registry.SelectInstallable([]registry.VersionCandidate{
		cand("4.0.0", "", []string{"genero6"}),
	}, "^3.0.0", &v4)
	if err != nil {
		t.Fatalf("SelectInstallable: %v", err)
	}
	if sel.Found || len(sel.Skipped) != 0 {
		t.Errorf("found=%v skipped=%v, want no match and nothing blamed on Genero",
			sel.Found, sel.Skipped)
	}
}

func TestSelectInstallableRejectsABadConstraint(t *testing.T) {
	if _, err := registry.SelectInstallable(nil, "not-a-constraint", nil); err == nil {
		t.Error("want an error for an unparseable constraint")
	}
}
