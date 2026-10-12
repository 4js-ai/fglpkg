package cli

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

// candidates builds a version list newest-LAST, to prove the step-down picks the
// newest runnable rather than the first it happens to see.
func candidates(specs ...struct {
	version    string
	constraint string
	variants   []string
}) func(string) ([]resolver.CandidateVersion, error) {
	out := make([]resolver.CandidateVersion, 0, len(specs))
	for _, sp := range specs {
		out = append(out, resolver.CandidateVersion{
			Version:          semver.MustParse(sp.version),
			GeneroConstraint: sp.constraint,
			Variants:         sp.variants,
		})
	}
	return func(string) ([]resolver.CandidateVersion, error) { return out, nil }
}

type versionSpec = struct {
	version    string
	constraint string
	variants   []string
}

// fglunitShape is the package that motivated this check, as published today:
// 1.0.1 dropped every build but genero6, while 1.0.0 still carries all three.
func fglunitShape() func(string) ([]resolver.CandidateVersion, error) {
	return candidates(
		versionSpec{"1.0.0", "", []string{"genero4", "genero5", "genero6"}},
		versionSpec{"1.0.1", "", []string{"genero6"}},
	)
}

// TestStepDownMatchesWhatInstallWouldGive is the finding this check exists for:
// the listing grades the latest version, `install` installs the newest version
// that runs. On Genero 4 fglunit's latest is incompatible and `install fglunit`
// still succeeds, with 1.0.0.
func TestStepDownMatchesWhatInstallWouldGive(t *testing.T) {
	v4 := genero.MustParse("4.01.12")
	rows := []searchRow{{
		name:     "fglunit",
		version:  "1.0.1",
		variants: []string{"genero6"},
		versions: fglunitShape(),
	}}
	gradeSearchRows(rows, &v4)

	if rows[0].grade != gradeCompatible {
		t.Errorf("grade = %q, want %q — 1.0.0 runs here and is what install gives",
			rows[0].grade, gradeCompatible)
	}
	// Every cell describes one version, so the version moved too.
	if rows[0].version != "1.0.0" {
		t.Errorf("version = %q, want 1.0.0", rows[0].version)
	}
	if rows[0].genero != "4, 5, 6" {
		t.Errorf("GENERO = %q, want the stepped-down version's builds", rows[0].genero)
	}
	// ...and what the listing said is kept, for the note under the table.
	if rows[0].latest != "1.0.1" {
		t.Errorf("latest = %q, want 1.0.1", rows[0].latest)
	}
}

// TestStepDownCoversTheConstraintToo: the listing pairs the LATEST version's
// constraint with the latest version's builds, so a package that tightened its
// declared range has the same bug through the other signal.
// TestStepDownTakesTheNewestRunnableNotTheFirst: `install` resolves "latest"
// to the HIGHEST runnable version, so a search that stopped at the first one it
// happened to see would name a version install does not give. The list here is
// ordered so the two answers differ: 1.0.0 is runnable and comes first, 1.5.0
// is runnable and is what install picks.
func TestStepDownTakesTheNewestRunnableNotTheFirst(t *testing.T) {
	v4 := genero.MustParse("4.01.12")
	rows := []searchRow{{
		name:     "manyversions",
		version:  "2.0.0",
		variants: []string{"genero6"},
		versions: candidates(
			versionSpec{"1.0.0", "", []string{"genero4"}},
			versionSpec{"1.5.0", "", []string{"genero4"}},
			versionSpec{"2.0.0", "", []string{"genero6"}},
		),
	}}
	gradeSearchRows(rows, &v4)

	if rows[0].version != "1.5.0" {
		t.Errorf("version = %q, want 1.5.0 — the newest that runs, not the first found",
			rows[0].version)
	}
}

func TestStepDownCoversTheConstraintToo(t *testing.T) {
	v4 := genero.MustParse("4.01.12")
	rows := []searchRow{{
		name:       "tightened",
		version:    "2.0.0",
		constraint: "^6.0.0",
		variants:   []string{"genero4", "genero6"},
		versions: candidates(
			versionSpec{"1.0.0", "^4.0.0", []string{"genero4", "genero6"}},
			versionSpec{"2.0.0", "^6.0.0", []string{"genero4", "genero6"}},
		),
	}}
	gradeSearchRows(rows, &v4)

	if rows[0].grade != gradeCompatible || rows[0].version != "1.0.0" {
		t.Errorf("grade=%q version=%q, want ✓ on 1.0.0", rows[0].grade, rows[0].version)
	}
	// The constraint moved with the version, so the column cannot show 2.0.0's.
	if rows[0].genero != "^4.0.0" {
		t.Errorf("GENERO = %q, want ^4.0.0 (1.0.0's constraint)", rows[0].genero)
	}
}

func TestStepDownLeavesAGenuineIncompatibilityAlone(t *testing.T) {
	v4 := genero.MustParse("4.01.12")
	rows := []searchRow{{
		name:     "odatalib",
		version:  "1.2.0",
		variants: []string{"genero5", "genero6"},
		versions: candidates(
			versionSpec{"1.1.0", "", []string{"genero5"}},
			versionSpec{"1.2.0", "", []string{"genero5", "genero6"}},
		),
	}}
	gradeSearchRows(rows, &v4)

	if rows[0].grade != gradeIncompatible {
		t.Errorf("grade = %q, want ✗ — no version runs on Genero 4", rows[0].grade)
	}
	if rows[0].version != "1.2.0" || rows[0].latest != "" {
		t.Errorf("version=%q latest=%q, want the row left on 1.2.0 unmarked",
			rows[0].version, rows[0].latest)
	}
}

// TestStepDownOnlyFetchesForARefusal: ✓ needs no second look (if the latest
// runs, it is the one install picks) and a row with no signal has nothing to
// confirm. Fetching those would spend a request per row to learn nothing.
func TestStepDownOnlyFetchesForARefusal(t *testing.T) {
	v4 := genero.MustParse("4.01.12")
	var fetches int32
	counting := func(string) ([]resolver.CandidateVersion, error) {
		atomic.AddInt32(&fetches, 1)
		return nil, nil
	}
	rows := []searchRow{
		{name: "fine", version: "1.0.0", variants: []string{"genero4"}, versions: counting},
		{name: "unknown", version: "1.0.0", versions: counting},
		{name: "refused", version: "1.0.0", variants: []string{"genero6"}, versions: counting},
	}
	gradeSearchRows(rows, &v4)

	if got := atomic.LoadInt32(&fetches); got != 1 {
		t.Errorf("%d version lists fetched, want 1 (the ✗ row only)", got)
	}
	if rows[0].grade != gradeCompatible || rows[1].grade != gradeUnknown {
		t.Errorf("grades = %q/%q, want ✓/?", rows[0].grade, rows[1].grade)
	}
}

// TestStepDownKeepsTheListingsAnswerOnAFetchError: the provisional grade is
// what this branch would have shown without the check at all, so a registry
// that cannot be reached for the second opinion costs accuracy, never the
// search itself.
func TestStepDownKeepsTheListingsAnswerOnAFetchError(t *testing.T) {
	v4 := genero.MustParse("4.01.12")
	rows := []searchRow{{
		name:     "fglunit",
		version:  "1.0.1",
		variants: []string{"genero6"},
		// A partial list WITH an error, which is the case that distinguishes
		// "check the error" from "see what came back": a provider that paged
		// and then failed has some versions and no idea how many are missing.
		// Grading off that would answer from an arbitrary prefix of the truth.
		versions: func(string) ([]resolver.CandidateVersion, error) {
			return []resolver.CandidateVersion{
				{Version: semver.MustParse("1.0.0"), Variants: []string{"genero4", "genero5", "genero6"}},
			}, errors.New("registry unreachable")
		},
	}}
	gradeSearchRows(rows, &v4)

	if rows[0].grade != gradeIncompatible || rows[0].version != "1.0.1" || rows[0].latest != "" {
		t.Errorf("grade=%q version=%q latest=%q, want the listing's own answer, unmarked",
			rows[0].grade, rows[0].version, rows[0].latest)
	}
}

// TestStepDownSkippedWithoutATarget: with no Genero version resolved every row
// grades "?", and there is no question to ask the registry.
func TestStepDownSkippedWithoutATarget(t *testing.T) {
	var fetches int32
	rows := []searchRow{{
		name:     "fglunit",
		version:  "1.0.1",
		variants: []string{"genero6"},
		versions: func(string) ([]resolver.CandidateVersion, error) {
			atomic.AddInt32(&fetches, 1)
			return nil, nil
		},
	}}
	gradeSearchRows(rows, nil)

	if got := atomic.LoadInt32(&fetches); got != 0 {
		t.Errorf("%d version lists fetched with no target, want 0", got)
	}
	if rows[0].grade != gradeUnknown {
		t.Errorf("grade = %q, want ?", rows[0].grade)
	}
}

// TestStepDownWithoutAVersionLister covers a provider that cannot list versions
// (and the zero-value row): the check is skipped rather than panicking.
func TestStepDownWithoutAVersionLister(t *testing.T) {
	v4 := genero.MustParse("4.01.12")
	rows := []searchRow{{name: "fglunit", version: "1.0.1", variants: []string{"genero6"}}}
	gradeSearchRows(rows, &v4)
	if rows[0].grade != gradeIncompatible {
		t.Errorf("grade = %q, want the listing's ✗", rows[0].grade)
	}
}
