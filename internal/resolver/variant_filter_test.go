package resolver_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

// GIS-574. A package version is only installable on a Genero major it actually
// publishes a build for, and that is independent of the "genero" constraint it
// declares. Most publishers declare no constraint at all — and
// Version.Satisfies("") is unconditionally true — so before this the resolver
// saw every version as compatible with every runtime, picked the newest, and
// the registry client returned whatever build it had.
//
// The shape here is fglunit's: 1.0.1 ships only a genero6 build, 1.0.0 ships
// all three, and neither declares a constraint.

// variantDB is a minimal fetcher pair that reports per-version variants, which
// the shared packageDB helper deliberately does not model.
type variantDB map[string][]resolver.CandidateVersion

func (db variantDB) versions(name string) ([]resolver.CandidateVersion, error) {
	vs, ok := db[name]
	if !ok {
		return nil, errNotFound{name}
	}
	return vs, nil
}

func (db variantDB) info(name, version, _ string) (*registry.PackageInfo, error) {
	return &registry.PackageInfo{
		Name:        name,
		Version:     version,
		DownloadURL: "https://example.com/" + name + "-" + version + ".zip",
		Checksum:    "deadbeef",
	}, nil
}

func (db variantDB) resolve(t *testing.T, gv genero.Version, dep string) (*resolver.Plan, error) {
	t.Helper()
	root := manifest.New("myapp", "1.0.0", "", "")
	root.AddFGLDependency(dep, "^1.0.0")
	return resolver.NewWithFetchers(gv, db.versions, db.info).Resolve(root)
}

type errNotFound struct{ name string }

func (e errNotFound) Error() string { return "package not found: " + e.name }

// candidate builds a CandidateVersion with no declared Genero constraint, so
// the variant list is the only compatibility signal.
func candidate(version string, variants ...string) resolver.CandidateVersion {
	return resolver.CandidateVersion{Version: semver.MustParse(version), Variants: variants}
}

func resolvedVersion(t *testing.T, plan *resolver.Plan, name string) string {
	t.Helper()
	for _, p := range plan.Packages {
		if p.Name == name {
			return p.Version.String()
		}
	}
	t.Fatalf("%s not present in plan", name)
	return ""
}

// fglunitDB is the reported shape: newest version is Genero 6 only.
var fglunitDB = variantDB{
	"fglunit": {
		candidate("1.0.0", "genero4", "genero5", "genero6"),
		candidate("1.0.1", "genero6"),
	},
}

func TestNewerVersionWithoutABuildForThisMajorIsSkipped(t *testing.T) {
	for _, tc := range []struct {
		gv   string
		want string
	}{
		{"4.01.12", "1.0.0"}, // 1.0.1 has no genero4 build
		{"5.00.01", "1.0.0"}, // nor a genero5 one
		{"6.00.01", "1.0.1"}, // newest wins where a build exists
	} {
		t.Run(tc.gv, func(t *testing.T) {
			plan, err := fglunitDB.resolve(t, genero.MustParse(tc.gv), "fglunit")
			if err != nil {
				t.Fatalf("Resolve on Genero %s: %v", tc.gv, err)
			}
			if got := resolvedVersion(t, plan, "fglunit"); got != tc.want {
				t.Errorf("Genero %s resolved fglunit@%s, want %s", tc.gv, got, tc.want)
			}
		})
	}
}

// A single-version package with no build for this major has no older version to
// fall back to, so it must fail rather than resolve to something unusable —
// odatalib's shape (genero5/genero6 only, one version).
func TestNoVersionWithABuildForThisMajorIsAnError(t *testing.T) {
	db := variantDB{"odatalib": {candidate("1.2.0", "genero5", "genero6")}}

	_, err := db.resolve(t, genero.MustParse("4.01.12"), "odatalib")
	if err == nil {
		t.Fatal("expected an error resolving a package with no Genero 4 build, got nil")
	}
	// The message must say what *is* published, or the user cannot tell whether
	// to pin, upgrade Genero, or ask the publisher for a build.
	for _, want := range []string{"odatalib", "Genero 4", "1.2.0", "Genero 5, 6"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// Artifactory and older registries report no variants. "Unknown" must not
// become "incompatible", or every such package would stop resolving.
func TestVersionsReportingNoVariantsStillResolve(t *testing.T) {
	db := variantDB{"legacy": {candidate("1.0.0")}}

	plan, err := db.resolve(t, genero.MustParse("4.01.12"), "legacy")
	if err != nil {
		t.Fatalf("a package reporting no variants must still resolve: %v", err)
	}
	if got := resolvedVersion(t, plan, "legacy"); got != "1.0.0" {
		t.Errorf("resolved legacy@%s, want 1.0.0", got)
	}
}

// Webcomponents ship browser assets rather than p-code, so their single
// "webcomponent" artifact serves every Genero major.
func TestWebcomponentVariantServesEveryMajor(t *testing.T) {
	db := variantDB{"fjs-grid": {candidate("1.0.0", "webcomponent")}}

	for _, gv := range []string{"4.01.12", "6.00.01"} {
		if _, err := db.resolve(t, genero.MustParse(gv), "fjs-grid"); err != nil {
			t.Errorf("webcomponent package must resolve on Genero %s: %v", gv, err)
		}
	}
}

// The variant check is additional to the declared constraint, not a replacement
// for it: a version that publishes a matching build but excludes this runtime
// by constraint is still rejected (the GIS-373 behaviour).
func TestDeclaredConstraintStillRejectsAMatchingVariant(t *testing.T) {
	db := variantDB{"strict": {{
		Version:          semver.MustParse("1.0.0"),
		GeneroConstraint: ">=6.00",
		Variants:         []string{"genero4", "genero6"},
	}}}

	_, err := db.resolve(t, genero.MustParse("4.01.12"), "strict")
	if err == nil {
		t.Fatal("a declared constraint excluding Genero 4 must still reject the version")
	}
	// The constraint is the reason, so it is what the message must name.
	// Listing the builds instead — "1.0.0 has builds for Genero 4, 6" — would
	// contradict "not compatible with Genero 4" in the same sentence.
	if !strings.Contains(err.Error(), "1.0.0 requires Genero >=6.00") {
		t.Errorf("error %q does not name the excluding constraint", err)
	}
	if strings.Contains(err.Error(), "has builds for") {
		t.Errorf("error %q explains a constraint rejection by its builds", err)
	}
}

// Pinning a version that exists but has no build here is not a clash between
// constraints, so the conflict must say why that version cannot be used —
// otherwise `<root> requires "1.0.1"` reads as nonsense next to a registry that
// plainly lists 1.0.1.
func TestPinToAVersionWithNoBuildExplainsTheConflict(t *testing.T) {
	root := manifest.New("myapp", "1.0.0", "", "")
	root.AddFGLDependency("fglunit", "1.0.1")
	_, err := resolver.NewWithFetchers(genero.MustParse("4.01.12"), fglunitDB.versions, fglunitDB.info).Resolve(root)
	if err == nil {
		t.Fatal("expected a conflict pinning fglunit@1.0.1 on Genero 4, got nil")
	}
	for _, want := range []string{
		`version conflict for "fglunit"`,
		"no matching version can be used on Genero 4.01.12",
		"1.0.1 has builds for Genero 6",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// A genuine constraint clash — nothing satisfies the constraint on any Genero —
// gets no Genero note, which would send the user looking in the wrong place.
func TestConstraintClashCarriesNoGeneroNote(t *testing.T) {
	root := manifest.New("myapp", "1.0.0", "", "")
	root.AddFGLDependency("fglunit", "^2.0.0")
	_, err := resolver.NewWithFetchers(genero.MustParse("4.01.12"), fglunitDB.versions, fglunitDB.info).Resolve(root)
	if err == nil {
		t.Fatal("expected a conflict for fglunit ^2.0.0, got nil")
	}
	if strings.Contains(err.Error(), "Genero") {
		t.Errorf("error %q blames Genero for a plain constraint clash", err)
	}
}

// ── Stepping down when a provider refuses the chosen version ─────────────────

// A provider that cannot report variants when it lists versions — Artifactory —
// only reveals "no build for this Genero" when the version's details are
// fetched. Resolution must then step down to the next-best version rather than
// failing the whole install, which is what it did when the refusal first became
// an error: on Genero 4 a genero6-only 1.0.1 broke `install`, a `^1.0.0`
// dependency and `update`, even though 1.0.0 publishes a genero4 build.

// noVariantDB reports no variants at all (so nothing is filtered up front) and
// refuses the versions named in noBuild when info is fetched.
type noVariantDB struct {
	versionList []string
	noBuild     map[string][]string // version → the variants it does publish
	fetched     []string            // versions info was requested for, in order
}

func (db *noVariantDB) versions(string) ([]resolver.CandidateVersion, error) {
	out := make([]resolver.CandidateVersion, 0, len(db.versionList))
	for _, v := range db.versionList {
		out = append(out, resolver.CandidateVersion{Version: semver.MustParse(v)})
	}
	return out, nil
}

func (db *noVariantDB) info(name, version, major string) (*registry.PackageInfo, error) {
	db.fetched = append(db.fetched, version)
	if published, refused := db.noBuild[version]; refused {
		return nil, &registry.NoBuildError{
			Name: name, Version: version, GeneroMajor: major, Published: published,
		}
	}
	return &registry.PackageInfo{Name: name, Version: version, Checksum: "deadbeef"}, nil
}

func (db *noVariantDB) resolve(t *testing.T, gv, dep, constraint string) (*resolver.Plan, error) {
	t.Helper()
	root := manifest.New("myapp", "1.0.0", "", "")
	root.AddFGLDependency(dep, constraint)
	return resolver.NewWithFetchers(genero.MustParse(gv), db.versions, db.info).Resolve(root)
}

func TestRefusedVersionStepsDownToTheNextBest(t *testing.T) {
	db := &noVariantDB{
		versionList: []string{"1.0.0", "1.0.1"},
		noBuild:     map[string][]string{"1.0.1": {"genero6"}},
	}

	plan, err := db.resolve(t, "4.01.12", "probe", "^1.0.0")
	if err != nil {
		t.Fatalf("resolution must step down to 1.0.0, not fail: %v", err)
	}
	if got := resolvedVersion(t, plan, "probe"); got != "1.0.0" {
		t.Errorf("resolved probe@%s, want 1.0.0", got)
	}
	// Newest first, then the fallback — and no wasted lookups beyond that.
	if strings.Join(db.fetched, ",") != "1.0.1,1.0.0" {
		t.Errorf("fetched %v, want [1.0.1 1.0.0]", db.fetched)
	}
}

// Stepping down must not paper over a version that genuinely cannot be used:
// when every candidate is refused, the error names what each one publishes.
func TestAllVersionsRefusedReportsTheBuilds(t *testing.T) {
	db := &noVariantDB{
		versionList: []string{"1.0.0", "1.0.1"},
		noBuild: map[string][]string{
			"1.0.0": {"genero5", "genero6"},
			"1.0.1": {"genero6"},
		},
	}

	_, err := db.resolve(t, "4.01.12", "probe", "^1.0.0")
	if err == nil {
		t.Fatal("expected an error when every version is refused, got nil")
	}
	for _, want := range []string{
		"no version of \"probe\" is compatible with Genero 4.01.12",
		"1.0.1 has builds for Genero 6",
		"1.0.0 has builds for Genero 5, 6",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// A fetch failure that is not a no-build refusal must still abort rather than
// silently stepping down — a transport error is not evidence about variants.
func TestNonNoBuildFetchErrorStillFails(t *testing.T) {
	root := manifest.New("myapp", "1.0.0", "", "")
	root.AddFGLDependency("probe", "^1.0.0")

	versions := func(string) ([]resolver.CandidateVersion, error) {
		return []resolver.CandidateVersion{
			{Version: semver.MustParse("1.0.0")},
			{Version: semver.MustParse("1.0.1")},
		}, nil
	}
	calls := 0
	info := func(string, string, string) (*registry.PackageInfo, error) {
		calls++
		return nil, errors.New("registry unreachable")
	}

	_, err := resolver.NewWithFetchers(genero.MustParse("4.01.12"), versions, info).Resolve(root)
	if err == nil {
		t.Fatal("a transport failure must abort resolution, got nil")
	}
	if !strings.Contains(err.Error(), "registry unreachable") {
		t.Errorf("error %q does not carry the underlying failure", err)
	}
	if calls != 1 {
		t.Errorf("fetched %d times, want 1 — a transport error must not step down", calls)
	}
}

// ── Version-selection failures are not fetch failures ────────────────────────

// bestVersion has two failure modes: no candidate matches the constraints, and
// a constraint that will not parse. Only the first was a sentinel, so an
// unparseable constraint fell through to the metadata-fetch branch and was
// reported as `failed to fetch info for b@0.0.0` — naming a fetch that never
// happened, against a zero version. Worse, it aborted resolution on the spot
// instead of recording a conflict and resolving the rest of the graph.

// badConstraintDB gives "a" a dependency on "b" with an unparseable constraint.
type badConstraintDB struct{ scope string }

func (db badConstraintDB) versions(string) ([]resolver.CandidateVersion, error) {
	return []resolver.CandidateVersion{{Version: semver.MustParse("1.0.0")}}, nil
}

func (db badConstraintDB) info(name, version, _ string) (*registry.PackageInfo, error) {
	info := &registry.PackageInfo{Name: name, Version: version, Checksum: "deadbeef"}
	if name == "a" {
		info.FGLDeps = map[string]string{"b": ">>>not-a-constraint"}
	}
	return info, nil
}

func TestInvalidConstraintIsAConflictNotAFetchFailure(t *testing.T) {
	db := badConstraintDB{}
	root := manifest.New("myapp", "1.0.0", "", "")
	root.AddFGLDependency("a", "^1.0.0")

	_, err := resolver.NewWithFetchers(genero.MustParse("4.01.12"), db.versions, db.info).Resolve(root)
	if err == nil {
		t.Fatal("expected an error for an unparseable constraint, got nil")
	}
	// A fetch that never happened must not be blamed, least of all at @0.0.0.
	if strings.Contains(err.Error(), "failed to fetch info") || strings.Contains(err.Error(), "@0.0.0") {
		t.Errorf("error %q reports a version-selection failure as a fetch failure", err)
	}
	var cl *resolver.ConflictList
	if !errors.As(err, &cl) {
		t.Errorf("error %q is not a *resolver.ConflictList", err)
	}
	// The invalid constraint is still named — that is the actionable detail.
	if !strings.Contains(err.Error(), ">>>not-a-constraint") {
		t.Errorf("error %q does not name the invalid constraint", err)
	}
}

// The same, as an optional dependency: skipped rather than fatal, and not
// mislabelled as a fetch problem.
func TestInvalidConstraintOnAnOptionalDepIsNotLabelledAFetch(t *testing.T) {
	db := badConstraintDB{}
	root := manifest.New("myapp", "1.0.0", "", "")
	root.AddFGLDependencyScoped("opt", ">>>not-a-constraint", manifest.ScopeOptional)

	plan, err := resolver.NewWithFetchers(genero.MustParse("4.01.12"), db.versions, db.info).
		ResolveWithOptions(root, resolver.ResolveOptions{IncludeOptional: true})
	if err != nil {
		t.Fatalf("an optional dependency must be skipped, not fatal: %v", err)
	}
	if len(plan.OptionalSkipped) != 1 {
		t.Fatalf("OptionalSkipped = %v, want 1 entry", plan.OptionalSkipped)
	}
	if reason := plan.OptionalSkipped[0]; strings.Contains(reason, "fetch info") {
		t.Errorf("skip reason %q blames a fetch that never happened", reason)
	}
}

// An optional dependency whose every version is refused for this Genero must
// say so. The scope check used to run before the refused-builds check, so the
// reason collapsed to the generic "no version satisfies all constraints" and
// the one fact that explains the skip was dropped.
func TestOptionalDepKeepsTheGeneroReasonWhenAllVersionsAreRefused(t *testing.T) {
	db := &noVariantDB{
		versionList: []string{"1.0.0", "1.0.1"},
		noBuild: map[string][]string{
			"1.0.0": {"genero6"},
			"1.0.1": {"genero6"},
		},
	}
	root := manifest.New("myapp", "1.0.0", "", "")
	root.AddFGLDependencyScoped("opt", "^1.0.0", manifest.ScopeOptional)

	plan, err := resolver.NewWithFetchers(genero.MustParse("4.01.12"), db.versions, db.info).
		ResolveWithOptions(root, resolver.ResolveOptions{IncludeOptional: true})
	if err != nil {
		t.Fatalf("an optional dependency must be skipped, not fatal: %v", err)
	}
	if len(plan.OptionalSkipped) != 1 {
		t.Fatalf("OptionalSkipped = %v, want 1 entry", plan.OptionalSkipped)
	}
	reason := plan.OptionalSkipped[0]
	if !strings.Contains(reason, "has builds for Genero 6") {
		t.Errorf("skip reason %q does not say why — the refused builds are the explanation", reason)
	}
	if strings.Contains(reason, "no version satisfies all constraints") {
		t.Errorf("skip reason %q is the generic constraint message, not the Genero one", reason)
	}
}

// ── Every dropped version is explained, and explained by its actual cause ────

// filterByGenero and describeCandidates both go through registry.Runnable, so
// the message cannot describe a different rule from the one that did the
// dropping. Deriving the reason separately left two gaps.

// A version that reports an empty variant list publishes no builds at all, so
// it is dropped — but the old message only described versions that listed
// builds, leaving this one rejected with no reason given.
func TestVersionWithNoBuildsIsExplained(t *testing.T) {
	db := variantDB{"fx": {
		{Version: semver.MustParse("1.0.0"), Variants: []string{}},
	}}

	_, err := db.resolve(t, genero.MustParse("4.01.12"), "fx")
	if err == nil {
		t.Fatal("a version publishing no builds must not resolve, got nil")
	}
	if !strings.Contains(err.Error(), "1.0.0 publishes no builds") {
		t.Errorf("error %q does not explain why 1.0.0 was dropped", err)
	}
}

// A version dropped for an unparseable constraint used to be explained by its
// builds, which may include this very major — contradicting the error.
func TestInvalidDeclaredConstraintIsExplainedAsSuch(t *testing.T) {
	db := variantDB{"fx": {
		{Version: semver.MustParse("1.0.0"), GeneroConstraint: ">>bad", Variants: []string{"genero4"}},
	}}

	_, err := db.resolve(t, genero.MustParse("4.01.12"), "fx")
	if err == nil {
		t.Fatal("a version with an unparseable constraint must not resolve, got nil")
	}
	if !strings.Contains(err.Error(), `1.0.0 has an invalid genero constraint ">>bad"`) {
		t.Errorf("error %q does not name the unparseable constraint", err)
	}
	// Its genero4 build is real, so citing it would contradict "not compatible
	// with Genero 4.01.12" in the same sentence.
	if strings.Contains(err.Error(), "has builds for") {
		t.Errorf("error %q explains a constraint parse failure by the builds", err)
	}
}

// Every version dropped must appear in the message — none may be silently
// omitted, whatever the reason it was rejected for.
func TestEveryDroppedVersionGetsAReason(t *testing.T) {
	db := variantDB{"fx": {
		{Version: semver.MustParse("1.0.0"), Variants: []string{"genero6"}},
		{Version: semver.MustParse("1.1.0"), Variants: []string{}},
		{Version: semver.MustParse("1.2.0"), GeneroConstraint: ">=6.00", Variants: []string{"genero4"}},
	}}

	_, err := db.resolve(t, genero.MustParse("4.01.12"), "fx")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	for _, want := range []string{
		"1.0.0 has builds for Genero 6",
		"1.1.0 publishes no builds",
		"1.2.0 requires Genero >=6.00",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
