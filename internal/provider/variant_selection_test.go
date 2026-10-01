package provider

import (
	"errors"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

// GIS-574, Artifactory side. pickVariant used to end in `return tags[0]`, the
// twin of registry.pickArtifact's `&arts[0]`: a Genero 4 consumer of a version
// published only as genero6/genero7 silently received the genero6 build.
// Artifactory reports no variants when listing versions, so resolution cannot
// skip such a version; FetchInfo is where the wrong bytes must be refused.

func TestArtifactory_FetchInfo_RejectsAMajorWithNoBuild(t *testing.T) {
	srv := mockArtifactory(t)
	p := newTestProvider(srv.URL)

	_, err := p.FetchInfo("jfrog-test", "1.0.0", "4")
	if err == nil {
		t.Fatal("expected an error for a Genero major with no published build, got nil")
	}
	var nb *registry.NoBuildError
	if !errors.As(err, &nb) {
		t.Fatalf("error %q is not a *registry.NoBuildError", err)
	}
	for _, want := range []string{"acme", "jfrog-test@1.0.0", "Genero 4", "genero6, genero7"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// With no major named — `info`'s metadata-only fallback — the first build is
// still returned, as registry.FetchInfo does for the GI registry.
func TestArtifactory_FetchInfo_NoMajorStillFallsBack(t *testing.T) {
	srv := mockArtifactory(t)
	p := newTestProvider(srv.URL)

	info, err := p.FetchInfo("jfrog-test", "1.0.0", "")
	if err != nil {
		t.Fatalf("FetchInfo with no Genero major must still return metadata: %v", err)
	}
	if info.Variant != "genero6" {
		t.Errorf("Variant = %q, want the first published build %q", info.Variant, "genero6")
	}
}

func TestPickVariant(t *testing.T) {
	for _, tc := range []struct {
		tags  []string
		major string
		want  string
	}{
		{[]string{"genero6", "genero7"}, "7", "genero7"},
		{[]string{"genero6", "default"}, "4", "default"},
		{[]string{"webcomponent"}, "4", "webcomponent"},
		{[]string{"genero6"}, "4", ""},       // the GIS-574 case: no build, no fallback
		{[]string{"genero6"}, "", "genero6"}, // metadata-only caller
		{nil, "6", ""},
	} {
		if got := pickVariant(tc.tags, tc.major); got != tc.want {
			t.Errorf("pickVariant(%v, %q) = %q, want %q", tc.tags, tc.major, got, tc.want)
		}
	}
}

// variantProvider reports per-version variants, which fakeProvider does not.
type variantProvider struct {
	fakeProvider
	variants map[string][]string // version → variant tags
}

func (p *variantProvider) FetchVersions(name string) ([]resolver.CandidateVersion, error) {
	if _, ok := p.versions[name]; !ok {
		return nil, ErrNotFound
	}
	out := make([]resolver.CandidateVersion, 0, len(p.versions[name]))
	for _, s := range p.versions[name] {
		out = append(out, resolver.CandidateVersion{Version: semver.MustParse(s), Variants: p.variants[s]})
	}
	return out, nil
}

// The multi-provider resolve behind `install <pkg>` must explain a Genero
// exclusion as fully as registry.Resolve does — it is the path every install
// takes as soon as a second registry is configured.
func TestRepositorySetResolveNamesTheBuildsThatExist(t *testing.T) {
	gi := &variantProvider{
		fakeProvider: fakeProvider{name: "gi", versions: map[string][]string{"unitfx": {"1.0.0", "1.0.1"}}},
		variants:     map[string][]string{"1.0.0": {"genero4", "genero6"}, "1.0.1": {"genero6"}},
	}
	rs := NewRepositorySet([]Provider{gi}, descriptors(), nil)

	info, err := rs.Resolve("unitfx", "latest", "4")
	if err != nil {
		t.Fatalf("Resolve on Genero 4: %v", err)
	}
	if info.Version != "1.0.0" {
		t.Errorf("Genero 4 resolved unitfx@%s, want 1.0.0 (1.0.1 has no genero4 build)", info.Version)
	}

	_, err = rs.Resolve("unitfx", "latest", "3")
	if err == nil {
		t.Fatal("expected an error resolving on Genero 3, got nil")
	}
	for _, want := range []string{
		"has a build for Genero 3",
		"1.0.0 has builds for Genero 4, 6",
		"1.0.1 has builds for Genero 6",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
