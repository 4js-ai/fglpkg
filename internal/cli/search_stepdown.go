package cli

import (
	"sync"

	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
)

// Compatibility markers for the verdict column.
const (
	gradeCompatible   = "✓"
	gradeIncompatible = "✗"
	gradeUnknown      = "?"
)

// maxStepDownFetches bounds how many version lists are fetched at once. Only
// rows already graded ✗ are fetched, so the work is proportional to the rows
// that would otherwise be wrong — but a user on an old Genero searching a
// registry that has moved on can condemn most of a page, and those must not go
// out one at a time.
const maxStepDownFetches = 8

// gradeSearchRows fills in each row's GENERO cell and verdict, then confirms
// every ✗ against the package's other versions.
//
// The browse listing describes one version — the latest — but `fglpkg install
// <pkg>` does not install the latest. It installs the newest version that RUNS,
// stepping down past the ones that cannot (GIS-574). A package whose latest
// version dropped a Genero major therefore grades ✗ here while install succeeds
// with an older version, and `fglunit` is exactly that shape in the registry
// today: 1.0.1 publishes genero6 only, 1.0.0 publishes genero4, genero5 and
// genero6. A user on Genero 4 would be told "no" about a package that installs
// fine. "Unknown" was at least honest; "no" would be confident and wrong.
//
// So a ✗ from the listing is provisional. ✓ needs no second look — if the
// version the listing names runs, it is the one install picks — and a row with
// no signal at all has nothing to confirm. Only the ✗ is a claim strong enough
// to be worth a request, and it is checked with registry.Runnable over the same
// candidate list install resolves from, so search's ✗ and install's refusal
// cannot disagree.
//
// The ✓ path therefore trusts the listing's latest_version, and that trust is
// the one thing here that a registry can break: GI computes "latest" as the
// most recently published version by wall clock, not the highest version
// number, so a batch approval run in descending order leaves the LOWEST version
// pointed at. Observed on the test instance, where fglwebrun's latest_version
// is 1.0.0 while 1.0.6 is approved and is what install resolves. The verdict
// stays correct — install does succeed — but the VERSION column shows the
// version the registry named rather than the one delivered.
//
// That is not worth fixing here. Confirming the ✓ rows too would mean a fetch
// for every row in the table, which is the N+1 this design exists to avoid, and
// it would paper over a registry bug that also misreports "latest" to the
// portal and to anything else that reads the listing. It belongs in the
// registry, and until it is fixed a stale pointer costs one stale version
// number, not a wrong answer.
func gradeSearchRows(rows []searchRow, target *genero.Version) {
	for i := range rows {
		rows[i].grade = gradeCompat(target, rows[i].constraint, rows[i].variants)
		rows[i].genero = displayGenero(target, rows[i].constraint, rows[i].variants)
	}
	confirmIncompatibleRows(rows, target)
}

// confirmIncompatibleRows re-grades the condemned rows, in parallel.
func confirmIncompatibleRows(rows []searchRow, target *genero.Version) {
	if target == nil {
		return
	}
	sem := make(chan struct{}, maxStepDownFetches)
	var wg sync.WaitGroup
	for i := range rows {
		if rows[i].grade != gradeIncompatible || rows[i].versions == nil {
			continue
		}
		wg.Add(1)
		go func(r *searchRow) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			stepDownRow(r, target)
		}(&rows[i])
	}
	wg.Wait()
}

// stepDownRow re-points one condemned row at the newest version that runs here,
// leaving it alone when there is none.
//
// On a fetch error the provisional grade stands: the listing's answer is what
// this branch would have shown anyway, so a registry that cannot be reached for
// the second opinion costs accuracy, never the search itself.
//
// Every cell on a row describes one version, so when the answer moves to an
// older one the version, its constraint and its builds move with it — a row
// reading "1.0.1 / 4, 5, 6 / ✓" would be three facts about two different
// versions. latest records what the listing said, for the note below the table.
func stepDownRow(r *searchRow, target *genero.Version) {
	candidates, err := r.versions(r.name)
	if err != nil {
		return
	}
	var best *resolver.CandidateVersion
	for i := range candidates {
		if ok, _ := registry.Runnable(target, candidates[i].GeneroConstraint, candidates[i].Variants); !ok {
			continue
		}
		if best == nil || candidates[i].Version.GreaterThan(best.Version) {
			best = &candidates[i]
		}
	}
	if best == nil {
		return // nothing runs here: the ✗ was right, and now it is confirmed
	}
	if picked := best.Version.String(); picked != r.version {
		r.latest = r.version
		r.version = picked
	}
	r.constraint = best.GeneroConstraint
	r.variants = best.Variants
	r.grade = gradeCompat(target, r.constraint, r.variants)
	r.genero = displayGenero(target, r.constraint, r.variants)
}
