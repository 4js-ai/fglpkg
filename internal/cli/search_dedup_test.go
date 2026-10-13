package cli

import (
	"strings"
	"sync"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/config"
	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	"github.com/4js-mikefolcher/fglpkg/internal/provider"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

// searchStub is a minimal provider.Provider returning canned search results.
// versions, when set, answers the step-down check's version lookups; fetched
// records which names this provider was asked about, so a collision can be
// checked to consult the repository install would route to.
type searchStub struct {
	name     string
	results  []registry.SearchResult
	versions map[string][]resolver.CandidateVersion
	fetched  []string
	mu       sync.Mutex
}

func (s *searchStub) Name() string { return s.name }
func (s *searchStub) FetchVersions(name string) ([]resolver.CandidateVersion, error) {
	s.mu.Lock()
	s.fetched = append(s.fetched, name)
	s.mu.Unlock()
	if v, ok := s.versions[name]; ok {
		return v, nil
	}
	return nil, provider.ErrNotFound
}

// FetchInfo answers for a name this stub lists versions for, so a
// RepositorySet.Resolve over the stub can run to completion; everything else is
// absent, as the routing tests expect.
func (s *searchStub) FetchInfo(name, version, _ string) (*registry.PackageInfo, error) {
	if _, ok := s.versions[name]; !ok {
		return nil, provider.ErrNotFound
	}
	return &registry.PackageInfo{Name: name, Version: version, Source: s.name}, nil
}
func (s *searchStub) Search(string) ([]registry.SearchResult, error) { return s.results, nil }

func TestSearchAcrossProviders_DedupAndCollision(t *testing.T) {
	gi := &searchStub{name: "gi", results: []registry.SearchResult{
		{Name: "logft", LatestVersion: "2.0.0", Description: "logging"},
		{Name: "utils", LatestVersion: "1.3.0", Description: "gi utils"},
	}}
	acme := &searchStub{name: "acme", results: []registry.SearchResult{
		{Name: "utils", LatestVersion: "0.9.0", Description: "acme utils"},
		{Name: "acme-only", LatestVersion: "3.0.0", Description: "internal"},
	}}
	descs := []config.Registry{
		{Name: "gi", Type: config.TypeGenero, URL: "https://gi", Priority: 1},
		{Name: "acme", Type: config.TypeArtifactory, URL: "https://a", RepoKey: "k", Priority: 2},
	}
	rs := provider.NewRepositorySet([]provider.Provider{gi, acme}, descs, nil)

	out, err := captureStdout(t, func() error { return searchAcrossProviders(rs, "u", false, nil, "") })
	if err != nil {
		t.Fatalf("searchAcrossProviders: %v", err)
	}

	// "utils" appears once (deduped) with both sources shown, higher-priority
	// (gi) version/description kept.
	utilsRows := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "utils ") {
			utilsRows++
			if !strings.Contains(line, "gi, acme") {
				t.Errorf("utils row should list both sources: %q", line)
			}
			if !strings.Contains(line, "1.3.0") || !strings.Contains(line, "gi utils") {
				t.Errorf("utils row should keep gi's version/description: %q", line)
			}
		}
	}
	if utilsRows != 1 {
		t.Errorf("want exactly one deduped utils row, got %d\n%s", utilsRows, out)
	}
	if !strings.Contains(out, "more than one repository") {
		t.Errorf("expected a collision note:\n%s", out)
	}
}

// TestSearchAcrossProviders_GradesAgainstTarget covers the multi-provider
// grading path: results carrying a per-package GeneroConstraint (as the Genero
// provider supplies) must be shown in the GENERO column and graded ✓/✗/? against
// a non-nil target, rather than blanked to "-"/"?" for every row.
func TestSearchAcrossProviders_GradesAgainstTarget(t *testing.T) {
	gi := &searchStub{name: "gi", results: []registry.SearchResult{
		{Name: "modern", LatestVersion: "2.0.0", Description: "for genero 4", GeneroConstraint: "^4.0.0"},
		{Name: "legacy", LatestVersion: "1.0.0", Description: "for genero 3", GeneroConstraint: "^3.0.0"},
		{Name: "anyver", LatestVersion: "1.0.0", Description: "no constraint"},
	}}
	descs := []config.Registry{
		{Name: "gi", Type: config.TypeGenero, URL: "https://gi", Priority: 1},
	}
	rs := provider.NewRepositorySet([]provider.Provider{gi}, descs, nil)
	target := genero.MustParse("4.01.12")

	out, err := captureStdout(t, func() error { return searchAcrossProviders(rs, "x", false, &target, "") })
	if err != nil {
		t.Fatalf("searchAcrossProviders: %v", err)
	}

	if want := "(Genero 4.01.12)"; !strings.Contains(out, want) {
		t.Errorf("header missing target %q\n%s", want, out)
	}
	cases := []struct{ pkg, constraint, marker string }{
		{"modern", "^4.0.0", "✓"},
		{"legacy", "^3.0.0", "✗"},
		{"anyver", "-", "?"},
	}
	for _, c := range cases {
		line := lineContaining(out, c.pkg)
		if line == "" {
			t.Errorf("no row for %q\n%s", c.pkg, out)
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

// TestSearchAcrossProviders_GradesFromPublishedBuilds covers the fan-out half of
// GIS-575: a result's published builds must survive the per-name merge, and on
// a collision both compatibility signals must come from the SAME repository as
// the version and description. Grading one repo's constraint against another's
// builds would invent a verdict for a package that exists in neither.
func TestSearchAcrossProviders_GradesFromPublishedBuilds(t *testing.T) {
	gi := &searchStub{name: "gi", results: []registry.SearchResult{
		{Name: "modern", LatestVersion: "2.0.0", Description: "builds for here",
			Variants: []string{"genero4", "genero6"}},
		{Name: "elsewhere", LatestVersion: "1.0.0", Description: "builds for genero 6 only",
			Variants: []string{"genero6"}},
		// Collides with acme's entry below. gi wins on priority, so the ✓ must
		// come from gi's genero4 build — not from acme's genero6-only one.
		{Name: "utils", LatestVersion: "1.3.0", Description: "gi utils",
			Variants: []string{"genero4"}},
	}}
	acme := &searchStub{name: "acme", results: []registry.SearchResult{
		{Name: "utils", LatestVersion: "0.9.0", Description: "acme utils",
			Variants: []string{"genero6"}},
		// Artifactory reports no variants at all: nil, meaning "cannot tell",
		// which must stay "?" rather than becoming "publishes nothing".
		{Name: "acmeonly", LatestVersion: "3.0.0", Description: "internal"},
	}}
	descs := []config.Registry{
		{Name: "gi", Type: config.TypeGenero, URL: "https://gi", Priority: 1},
		{Name: "acme", Type: config.TypeArtifactory, URL: "https://a", RepoKey: "k", Priority: 2},
	}
	rs := provider.NewRepositorySet([]provider.Provider{gi, acme}, descs, nil)
	target := genero.MustParse("4.01.12")

	out, err := captureStdout(t, func() error { return searchAcrossProviders(rs, "x", false, &target, "") })
	if err != nil {
		t.Fatalf("searchAcrossProviders: %v", err)
	}

	cases := []struct{ pkg, genero, marker string }{
		{"modern", "4, 6", "✓"},
		{"elsewhere", "6", "✗"},
		{"utils", "4", "✓"},
		{"acmeonly", "-", "?"},
	}
	for _, c := range cases {
		line := lineContaining(out, c.pkg)
		if line == "" {
			t.Errorf("no row for %q\n%s", c.pkg, out)
			continue
		}
		if !strings.Contains(line, c.genero) {
			t.Errorf("%s row = %q, want GENERO %q", c.pkg, line, c.genero)
		}
		if !strings.Contains(line, c.marker) {
			t.Errorf("%s row = %q, want marker %q", c.pkg, line, c.marker)
		}
	}
	// The collided row keeps gi's description too, so the signals and the text
	// are demonstrably from one source rather than merged field by field.
	if line := lineContaining(out, "utils"); !strings.Contains(line, "gi utils") {
		t.Errorf("utils row should keep gi's description: %q", line)
	}
}

// TestSearchAcrossProviders_StepsDownWithinTheOwningRepository covers the
// fan-out half of the step-down check. A ✗ is confirmed against the repository
// the row came from — the one install would route to — so a collided name is
// never re-graded from a different repository's version history.
func TestSearchAcrossProviders_StepsDownWithinTheOwningRepository(t *testing.T) {
	v4 := genero.MustParse("4.01.12")
	gi := &searchStub{
		name: "gi",
		results: []registry.SearchResult{
			{Name: "utils", LatestVersion: "2.0.0", Description: "gi utils", Variants: []string{"genero6"}},
		},
		versions: map[string][]resolver.CandidateVersion{
			"utils": {
				{Version: semver.MustParse("1.0.0"), Variants: []string{"genero4", "genero6"}},
				{Version: semver.MustParse("2.0.0"), Variants: []string{"genero6"}},
			},
		},
	}
	// acme also carries "utils", at a version that runs here. gi wins on
	// priority, so acme must never be asked — grading gi's row from acme's
	// history would describe a package the user is not going to get.
	acme := &searchStub{
		name: "acme",
		results: []registry.SearchResult{
			{Name: "utils", LatestVersion: "9.9.9", Description: "acme utils", Variants: []string{"genero4"}},
		},
		versions: map[string][]resolver.CandidateVersion{
			"utils": {{Version: semver.MustParse("9.9.9"), Variants: []string{"genero4"}}},
		},
	}
	descs := []config.Registry{
		{Name: "gi", Type: config.TypeGenero, URL: "https://gi", Priority: 1},
		{Name: "acme", Type: config.TypeArtifactory, URL: "https://a", RepoKey: "k", Priority: 2},
	}
	rs := provider.NewRepositorySet([]provider.Provider{gi, acme}, descs, nil)

	// The term is "x", not "utils": lineContaining would otherwise match the
	// `Results for "utils"` header before reaching the row.
	out, err := captureStdout(t, func() error { return searchAcrossProviders(rs, "x", false, &v4, "") })
	if err != nil {
		t.Fatalf("searchAcrossProviders: %v", err)
	}

	line := lineContaining(out, "utils")
	for _, want := range []string{"1.0.0*", "4, 6", "✓"} {
		if !strings.Contains(line, want) {
			t.Errorf("utils row = %q, want %q", line, want)
		}
	}
	if len(gi.fetched) != 1 || gi.fetched[0] != "utils" {
		t.Errorf("gi fetched %v, want exactly [utils]", gi.fetched)
	}
	if len(acme.fetched) != 0 {
		t.Errorf("acme fetched %v, want nothing — gi owns this row", acme.fetched)
	}
	if !strings.Contains(out, "latest is 2.0.0") {
		t.Errorf("note missing gi's latest version:\n%s", out)
	}
}

// TestSearchAcrossProviders_GeneroColumnWidensToFit guards the dynamic GENERO
// column width: a constraint longer than the 12-char floor must not push its
// row's later columns out of alignment with a short-constraint row (the "spill
// into the ? column" regression). Both rows grade ✓, so their verdict glyph is
// the same width; with a correctly widened GENERO column their descriptions
// start at the same byte offset. A fixed %-12s would misalign them.
func TestSearchAcrossProviders_GeneroColumnWidensToFit(t *testing.T) {
	gi := &searchStub{name: "gi", results: []registry.SearchResult{
		{Name: "shortc", LatestVersion: "1.0.0", Description: "SHORTDESC", GeneroConstraint: "^4.0.0"},
		{Name: "longc", LatestVersion: "1.0.0", Description: "LONGDESC", GeneroConstraint: ">=3.20.00 <5.00.00"},
	}}
	descs := []config.Registry{
		{Name: "gi", Type: config.TypeGenero, URL: "https://gi", Priority: 1},
	}
	rs := provider.NewRepositorySet([]provider.Provider{gi}, descs, nil)
	target := genero.MustParse("4.01.12")

	out, err := captureStdout(t, func() error { return searchAcrossProviders(rs, "x", false, &target, "") })
	if err != nil {
		t.Fatalf("searchAcrossProviders: %v", err)
	}

	// The long constraint must render in full (not truncated).
	if !strings.Contains(out, ">=3.20.00 <5.00.00") {
		t.Errorf("long constraint missing or truncated:\n%s", out)
	}
	shortLine := lineContaining(out, "SHORTDESC")
	longLine := lineContaining(out, "LONGDESC")
	if shortLine == "" || longLine == "" {
		t.Fatalf("expected both rows in output:\n%s", out)
	}
	// Both rows grade ✓; with the GENERO column widened to fit the long
	// constraint, the description starts at the same offset in both.
	if got, want := strings.Index(longLine, "LONGDESC"), strings.Index(shortLine, "SHORTDESC"); got != want {
		t.Errorf("columns misaligned: LONGDESC at %d, SHORTDESC at %d (GENERO column did not widen)\n%s",
			got, want, out)
	}
}

// TestSearchAcrossProviders_StatusColumn covers the STATUS column in the
// multi-provider path: it appears only when a result is deprecated, and renders
// the plain and relocation ("deprecated -> successor") forms.
func TestSearchAcrossProviders_StatusColumn(t *testing.T) {
	gi := &searchStub{name: "gi", results: []registry.SearchResult{
		{Name: "gone", LatestVersion: "1.0.0", Description: "retired", Deprecated: true},
		{Name: "moved", LatestVersion: "1.0.0", Description: "relocated", Deprecated: true, MovedTo: "newpkg"},
		{Name: "live", LatestVersion: "1.0.0", Description: "still here"},
	}}
	descs := []config.Registry{
		{Name: "gi", Type: config.TypeGenero, URL: "https://gi", Priority: 1},
	}
	rs := provider.NewRepositorySet([]provider.Provider{gi}, descs, nil)

	out, err := captureStdout(t, func() error { return searchAcrossProviders(rs, "x", false, nil, "") })
	if err != nil {
		t.Fatalf("searchAcrossProviders: %v", err)
	}

	if !strings.Contains(lineContaining(out, "NAME"), "STATUS") {
		t.Errorf("STATUS column header missing when a result is deprecated:\n%s", out)
	}
	if line := lineContaining(out, "gone"); !strings.Contains(line, "deprecated") {
		t.Errorf("gone row = %q, want STATUS deprecated", line)
	}
	if line := lineContaining(out, "moved"); !strings.Contains(line, "deprecated -> newpkg") {
		t.Errorf("moved row = %q, want STATUS relocation", line)
	}
}

// TestSearchAndMultiProviderInstallPickTheSameVersion is the cross-path pin.
//
// `fglpkg install <pkg>` has two resolve paths: RepositorySet.Resolve whenever
// a secondary repository is configured, registry.Resolve otherwise. The
// step-down only ever claimed to match the second. This drives BOTH halves of
// the multi-provider path off one fixture — the search table and the resolve
// install would actually perform — and asserts they name the same version.
//
// The fixture is a prerelease, because that is where they came closest to
// disagreeing: the beta runs on Genero 4 and outranks the stable release, but
// "latest" does not select it.
func TestSearchAndMultiProviderInstallPickTheSameVersion(t *testing.T) {
	v4 := genero.MustParse("4.01.12")
	newStub := func() *searchStub {
		return &searchStub{
			name: "gi",
			results: []registry.SearchResult{
				{Name: "pre", LatestVersion: "2.0.0", Description: "PREDESC", Variants: []string{"genero6"}},
			},
			versions: map[string][]resolver.CandidateVersion{
				"pre": {
					{Version: semver.MustParse("1.0.0"), Variants: []string{"genero4"}},
					{Version: semver.MustParse("2.0.0-beta.1"), Variants: []string{"genero4", "genero6"}},
					{Version: semver.MustParse("2.0.0"), Variants: []string{"genero6"}},
				},
			},
		}
	}
	descs := []config.Registry{
		{Name: "gi", Type: config.TypeGenero, URL: "https://gi", Priority: 1},
		{Name: "acme", Type: config.TypeArtifactory, URL: "https://a", RepoKey: "k", Priority: 2},
	}

	// What the search table says.
	searchSet := provider.NewRepositorySet([]provider.Provider{newStub()}, descs, nil)
	out, err := captureStdout(t, func() error { return searchAcrossProviders(searchSet, "x", false, &v4, "") })
	if err != nil {
		t.Fatalf("searchAcrossProviders: %v", err)
	}
	line := lineContaining(out, "PREDESC")
	if line == "" {
		t.Fatalf("no row for the fixture\n%s", out)
	}

	// What `install` would actually do on this configuration.
	installSet := provider.NewRepositorySet([]provider.Provider{newStub()}, descs, nil)
	info, err := installSet.Resolve("pre", "latest", &v4)
	if err != nil {
		t.Fatalf("rs.Resolve: %v", err)
	}
	if info.Version != "1.0.0" {
		t.Fatalf("rs.Resolve picked %s, want 1.0.0", info.Version)
	}
	if !strings.Contains(line, info.Version+"*") {
		t.Errorf("search row = %q, but install resolves %s — the two paths disagree",
			line, info.Version)
	}
	if strings.Contains(line, "2.0.0-beta.1") {
		t.Errorf("search row = %q names a prerelease install would not install", line)
	}
}
