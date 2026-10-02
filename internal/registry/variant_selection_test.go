package registry_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
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
