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

// fakeVerbatim is a provider that can address a folder by its exact name, as
// the Artifactory one does. Its folders map is CASE-SENSITIVE on purpose: the
// whole point of the verbatim path is that "DateTime" and "datetime" are two
// different folders, which is also the one thing a fixture directory on macOS
// cannot express.
type fakeVerbatim struct {
	fakeProvider
	folders map[string][]string // exact folder name → versions
}

func (f *fakeVerbatim) FetchVersionsVerbatim(folder string) ([]resolver.CandidateVersion, error) {
	if f.authErr {
		return nil, errors.New("authentication failed (401)")
	}
	vs, ok := f.folders[folder]
	if !ok {
		return nil, ErrNotFound
	}
	return versionCandidates(vs), nil
}

func (f *fakeVerbatim) FetchInfoVerbatim(folder, version, generoMajor string) (*registry.PackageInfo, error) {
	if _, ok := f.folders[folder]; !ok {
		return nil, ErrNotFound
	}
	return &registry.PackageInfo{Name: folder, Version: version, Source: f.name}, nil
}

func versionCandidates(vs []string) []resolver.CandidateVersion {
	out := make([]resolver.CandidateVersion, 0, len(vs))
	for _, s := range vs {
		v, _ := semver.Parse(s)
		out = append(out, resolver.CandidateVersion{Version: v})
	}
	return out
}

func legacySet(t *testing.T, scope []string, extra ...Provider) *RepositorySet {
	t.Helper()
	gi := &fakeProvider{name: "gi", versions: map[string][]string{}}
	acme := &fakeVerbatim{
		fakeProvider: fakeProvider{name: "acme", versions: map[string][]string{}},
		folders:      map[string][]string{"DateTime": {"0.1.0"}},
	}
	d := descriptors()
	d[1].Packages = scope
	return NewRepositorySet(append([]Provider{gi, acme}, extra...), d, nil)
}

// TestVerbatim_FindsTheFolderAsWritten is the read half of GIS-610: a folder
// left by fglpkg <= 4.2.9 is addressable by the name its publisher typed, and
// by nothing else.
func TestVerbatim_FindsTheFolderAsWritten(t *testing.T) {
	rs := legacySet(t, nil)

	vs, err := rs.VersionsVerbatim("DateTime")
	if err != nil || len(vs) != 1 || vs[0].Version.String() != "0.1.0" {
		t.Fatalf("VersionsVerbatim(DateTime) = %v, %v; want one version 0.1.0", vs, err)
	}
	info, err := rs.InfoVerbatim("DateTime", "0.1.0", "")
	if err != nil || info.Source != "acme" {
		t.Fatalf("InfoVerbatim(DateTime) = %+v, %v; want it from acme", info, err)
	}
	// The slug is NOT a second spelling of the folder. Nothing here guesses.
	if _, err := rs.VersionsVerbatim("datetime"); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("VersionsVerbatim(datetime) = %v, want not-found — the folder is DateTime", err)
	}
}

// TestVerbatim_ObeysTheRepositoryScope: the scope decides which repositories
// are consulted, here as everywhere else. A scope that does not admit the name
// must not be reachable through the legacy path either — that would be a way
// around the dependency-confusion guard the scope exists to be.
func TestVerbatim_ObeysTheRepositoryScope(t *testing.T) {
	for _, tc := range []struct {
		scope []string
		want  bool
		why   string
	}{
		{nil, true, "no scope admits everything"},
		{[]string{"date*"}, true, "the scope matches on the slug, from both sides (GIS-610)"},
		{[]string{"DateTime"}, true, "the name as written is a scope like any other"},
		{[]string{"other-*"}, false, "a scope that excludes the name excludes it here too"},
	} {
		_, err := legacySet(t, tc.scope).VersionsVerbatim("DateTime")
		if got := err == nil; got != tc.want {
			t.Errorf("scope %v: found = %v, want %v (%s; err = %v)", tc.scope, got, tc.want, tc.why, err)
		}
	}
}

// TestVerbatim_RefusesToGuessBetweenRepositories: two repositories holding the
// same folder is the same ambiguity as two holding the same package, and gets
// the same refusal rather than the first hit.
func TestVerbatim_RefusesToGuessBetweenRepositories(t *testing.T) {
	second := &fakeVerbatim{
		fakeProvider: fakeProvider{name: "other", versions: map[string][]string{}},
		folders:      map[string][]string{"DateTime": {"9.9.9"}},
	}
	rs := legacySet(t, nil, second)
	rs.descriptors["other"] = config.Registry{Name: "other", Type: config.TypeArtifactory, Priority: 3}

	_, err := rs.VersionsVerbatim("DateTime")
	if err == nil || !strings.Contains(err.Error(), "more than one repository") {
		t.Fatalf("VersionsVerbatim with two holders = %v, want the collision refusal", err)
	}
}

// TestVerbatim_AnAuthFailureIsNotAnAbsence: the rule route lives by (spec
// §7.2). A repository that could not answer must stop the lookup, not drop out
// of the count — dropping out is how a name that IS in two repositories comes
// back as a single clean hit.
func TestVerbatim_AnAuthFailureIsNotAnAbsence(t *testing.T) {
	broken := &fakeVerbatim{
		fakeProvider: fakeProvider{name: "other", versions: map[string][]string{}, authErr: true},
		folders:      map[string][]string{},
	}
	rs := legacySet(t, nil, broken)
	rs.descriptors["other"] = config.Registry{Name: "other", Type: config.TypeArtifactory, Priority: 3}

	_, err := rs.VersionsVerbatim("DateTime")
	if err == nil || !strings.Contains(err.Error(), "authentication") {
		t.Fatalf("VersionsVerbatim with a failing repository = %v, want the auth error", err)
	}
}

// TestVerbatim_SkipsAProviderThatCannotAddressAFolder: GI slugs the name
// server-side, so there is only ever one folder and nothing to look up
// verbatim. Asking it anyway would be a second lookup with the same answer.
func TestVerbatim_SkipsAProviderThatCannotAddressAFolder(t *testing.T) {
	gi := &fakeProvider{name: "gi", versions: map[string][]string{"DateTime": {"5.0.0"}}}
	rs := NewRepositorySet([]Provider{gi}, descriptors()[:1], nil)

	if _, err := rs.VersionsVerbatim("DateTime"); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("VersionsVerbatim against gi alone = %v, want not-found", err)
	}
}
