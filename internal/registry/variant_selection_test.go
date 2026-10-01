package registry_test

import (
	"errors"
	"strings"
	"testing"

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
