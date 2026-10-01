package provider

import (
	"errors"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/config"
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

// ── RepositorySet.Resolve steps down when a provider refuses a version ───────

// `install <pkg>` resolves "latest" through RepositorySet.Resolve, not the
// dependency resolver. Artifactory reports no variants when it lists versions,
// so nothing can be skipped before the per-version fetch — and when that fetch
// first started refusing a wrong-major build, `install <pkg>` began failing
// outright for a package whose newest release is built only for another Genero,
// even though an older release has a build for this one (GIS-574).

// stepDownProvider lists versions without variants and refuses the ones named
// in noBuild, recording which versions had their info fetched.
type stepDownProvider struct {
	versionList []string
	noBuild     map[string][]string // version → the variants it does publish
	fetched     []string
}

func (p *stepDownProvider) Name() string { return "artifactory-fake" }

func (p *stepDownProvider) FetchVersions(string) ([]resolver.CandidateVersion, error) {
	out := make([]resolver.CandidateVersion, 0, len(p.versionList))
	for _, v := range p.versionList {
		out = append(out, resolver.CandidateVersion{Version: semver.MustParse(v)})
	}
	return out, nil
}

func (p *stepDownProvider) FetchInfo(name, version, major string) (*registry.PackageInfo, error) {
	p.fetched = append(p.fetched, version)
	if published, refused := p.noBuild[version]; refused {
		return nil, &registry.NoBuildError{
			Name: name, Version: version, GeneroMajor: major, Published: published,
		}
	}
	return &registry.PackageInfo{Name: name, Version: version, Checksum: "deadbeef"}, nil
}

func (p *stepDownProvider) Search(string) ([]registry.SearchResult, error) { return nil, nil }

func newStepDownSet(p Provider) *RepositorySet {
	return NewRepositorySet([]Provider{p}, []config.Registry{{Name: p.Name(), Priority: 1}}, nil)
}

func TestRepositorySetResolveStepsDownToTheNextBest(t *testing.T) {
	p := &stepDownProvider{
		versionList: []string{"1.0.0", "1.0.1"},
		noBuild:     map[string][]string{"1.0.1": {"genero6"}},
	}

	info, err := newStepDownSet(p).Resolve("probe", "latest", "4")
	if err != nil {
		t.Fatalf("Resolve must step down to 1.0.0, not fail: %v", err)
	}
	if info.Version != "1.0.0" {
		t.Errorf("resolved probe@%s, want 1.0.0", info.Version)
	}
	if strings.Join(p.fetched, ",") != "1.0.1,1.0.0" {
		t.Errorf("fetched %v, want [1.0.1 1.0.0]", p.fetched)
	}
}

func TestRepositorySetResolveReportsWhenEveryVersionIsRefused(t *testing.T) {
	p := &stepDownProvider{
		versionList: []string{"1.0.0", "1.0.1"},
		noBuild: map[string][]string{
			"1.0.0": {"genero5", "genero6"},
			"1.0.1": {"genero6"},
		},
	}

	_, err := newStepDownSet(p).Resolve("probe", "latest", "4")
	if err == nil {
		t.Fatal("expected an error when every version is refused, got nil")
	}
	for _, want := range []string{
		"has a build for Genero 4",
		"1.0.1 has builds for Genero 6",
		"1.0.0 has builds for Genero 5, 6",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// A transport failure is not evidence about variants, so it must abort rather
// than quietly resolving to an older version.
func TestRepositorySetResolveDoesNotStepDownOnOtherErrors(t *testing.T) {
	p := &failingProvider{}

	if _, err := newStepDownSet(p).Resolve("probe", "latest", "4"); err == nil {
		t.Fatal("a transport failure must abort Resolve, got nil")
	} else if !strings.Contains(err.Error(), "registry unreachable") {
		t.Errorf("error %q does not carry the underlying failure", err)
	}
	if p.calls != 1 {
		t.Errorf("fetched %d times, want 1 — a transport error must not step down", p.calls)
	}
}

type failingProvider struct{ calls int }

func (p *failingProvider) Name() string { return "artifactory-fake" }
func (p *failingProvider) FetchVersions(string) ([]resolver.CandidateVersion, error) {
	return []resolver.CandidateVersion{
		{Version: semver.MustParse("1.0.0")},
		{Version: semver.MustParse("1.0.1")},
	}, nil
}
func (p *failingProvider) FetchInfo(string, string, string) (*registry.PackageInfo, error) {
	p.calls++
	return nil, errors.New("registry unreachable")
}
func (p *failingProvider) Search(string) ([]registry.SearchResult, error) { return nil, nil }
