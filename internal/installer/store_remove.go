package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/4js-mikefolcher/fglpkg/internal/classpath"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	slugutil "github.com/4js-mikefolcher/fglpkg/internal/slug"
)

// StoreRemoval reports what RemoveFromStore did, so the caller can render an
// honest summary: what actually left, what was never there, and what the
// removal implies for the rest of the store.
type StoreRemoval struct {
	// Removed holds the display names of the packages that were deleted.
	Removed []string
	// NotFound holds requested names that were not installed in this store —
	// reported rather than silently treated as success (the GIS-564 rule).
	NotFound []string
	// Pruned describes artifacts swept alongside the packages: the removed
	// packages' own JARs that nothing else declares, and their webcomponent
	// bundles.
	Pruned []string
	// Orphaned names packages that a removed package pulled in and that nothing
	// left in the store references. They are deliberately NOT deleted — see
	// RemoveFromStore — only reported, so the user can decide.
	Orphaned []string
	// KeptJars names JARs the removed packages declared that were left in place
	// because the store could not be read completely enough to prove they are
	// unused. Reported so the user knows why the sweep held back.
	KeptJars []string
	// StillRequiredBy maps a removed package to the remaining packages that
	// still declare a dependency on it. Removing one of these is the user's
	// call, but it leaves those dependents unsatisfied, so it is surfaced.
	StillRequiredBy map[string][]string
}

// RemoveFromStore uninstalls the named packages from THIS installer's store and
// sweeps the artifacts those packages brought with them. It is the counterpart
// of a global tool install (GIS-565): the store has no manifest and no lock of
// its own, so what is installed is recovered by scanning — the bundled
// fglpkg.json under packages/ for BDL packages, and the webcomponent owners
// sidecar for webcomponent-bearing ones (GIS-567).
//
// Everything it deletes is something a package being removed owns. That
// restraint is the whole design: a GLOBAL store is shared, and the things
// sharing it are invisible from inside it.
//
//   - Projects reach the store's JARs directly through their classpath, and a
//     project's own `dependencies.java` land here when it runs
//     `install --global` from inside the project — recorded in the PROJECT's
//     manifest, which the store never sees. So "no installed package declares
//     this JAR" does NOT mean "nothing needs it", and a sweep of everything
//     unreferenced would break other projects. This is the same reason
//     ReconcileAfterRemove refuses to prune a global home at all; it differs
//     from this function only in that it prunes against one project's graph.
//   - Webcomponent bundles are keyed on disk by COMPONENTTYPE, and a pure
//     webcomponent package has no packages/<name> directory at all, so the
//     keep-set for them comes from the owners sidecar, never from packages/.
//
// Scope, in order of decreasing certainty about what the user wants:
//   - the named packages are deleted, along with their webcomponent bundles;
//   - JARs a removed package declared and no remaining package declares are
//     pruned — these are the artifacts it uniquely owned;
//   - packages the removed ones pulled in are only REPORTED (Orphaned), never
//     deleted. The store cannot distinguish a package installed as a dependency
//     from one the user installed in its own right, so deleting these would
//     throw away an explicit install on a guess.
//
// Names are matched canonically (GIS-271) on both sides, so `remove demo.pkg`
// finds the store entry `demo-pkg` and the dependency graph lines up even for a
// legacy directory that was not stored under its canonical slug.
func (i *Installer) RemoveFromStore(names []string) (*StoreRemoval, error) {
	res := &StoreRemoval{StillRequiredBy: map[string][]string{}}

	installed, err := i.scanStore()
	if err != nil {
		return nil, err
	}

	// Entries are keyed canonically, so a requested name resolves directly.
	removing := map[string]bool{}
	for _, name := range names {
		key := slugutil.Canonical(name)
		if _, ok := installed[key]; ok {
			removing[key] = true
		} else {
			res.NotFound = append(res.NotFound, name)
		}
	}
	if len(removing) == 0 {
		return res, nil
	}

	keep := make(map[string]bool, len(installed))
	for key := range installed {
		if !removing[key] {
			keep[key] = true
		}
	}

	// A dependent left behind by this removal is worth saying out loud.
	for key := range keep {
		for _, dep := range installed[key].fglDeps {
			if removing[dep] {
				res.StillRequiredBy[installed[dep].displayName()] = append(
					res.StillRequiredBy[installed[dep].displayName()], installed[key].displayName())
			}
		}
	}
	for dep := range res.StillRequiredBy {
		sort.Strings(res.StillRequiredBy[dep])
	}

	// Orphans: pulled in by something being removed, referenced by nothing that
	// remains. Reported only — see the doc comment.
	referencedByKept := map[string]bool{}
	for key := range keep {
		for _, dep := range installed[key].fglDeps {
			referencedByKept[dep] = true
		}
	}
	for key := range removing {
		for _, dep := range installed[key].fglDeps {
			if keep[dep] && !referencedByKept[dep] {
				res.Orphaned = append(res.Orphaned, installed[dep].displayName())
			}
		}
	}
	sort.Strings(res.Orphaned)
	res.Orphaned = dedupeSorted(res.Orphaned)

	// Delete the package directories. A pure webcomponent package has none —
	// its only footprint is under webcomponents/, swept below.
	for key := range removing {
		e := installed[key]
		if e.dir != "" {
			if err := os.RemoveAll(filepath.Join(i.packagesDir, e.dir)); err != nil {
				return res, fmt.Errorf("cannot remove package %s: %w", e.dir, err)
			}
		}
		res.Removed = append(res.Removed, e.displayName())
	}
	sort.Strings(res.Removed)

	jarPruned, keptJars, err := i.pruneOwnedJars(installed, removing, keep)
	if err != nil {
		return res, err
	}
	res.Pruned = append(res.Pruned, jarPruned...)
	res.KeptJars = keptJars

	// The webcomponent keep-set is keyed by the owners sidecar's own package
	// names — NOT by what is under packages/, which never contains a pure
	// webcomponent package and would therefore prune every one of them.
	keepWC := map[string]bool{}
	for key := range keep {
		if owner := installed[key].wcOwner; owner != "" {
			keepWC[owner] = true
		}
	}
	wcPruned, err := i.pruneWebcomponents(keepWC)
	if err != nil {
		return res, err
	}
	res.Pruned = append(res.Pruned, wcPruned...)

	// Best-effort, exactly as after a project remove: a stale anchor only lists
	// files that no longer exist, and a merged-root problem must never leave the
	// store half-removed.
	_ = classpath.Sync(i.jarsDir)
	i.RebuildMergedRoot()

	return res, nil
}

// pruneOwnedJars deletes the JARs the removed packages declared and no
// remaining package declares. It never sweeps jars/ wholesale: a global store
// also holds JARs installed for a PROJECT (`install --global` from inside one
// records them in the project's manifest, not in the store), and deleting those
// would break that project's classpath — the acceptance criterion GIS-567 words
// as "pruning stays scoped to the global store and does not touch any project".
//
// When a remaining package's manifest cannot be read, its JAR requirements are
// unknown, so a candidate that is only "unreferenced" because of that gap is
// KEPT and reported rather than deleted. A leftover JAR costs disk; a deleted
// one breaks a package that is still installed.
func (i *Installer) pruneOwnedJars(installed map[string]*storeEntry, removing, keep map[string]bool) (pruned, kept []string, err error) {
	declaredByKept := map[string]bool{}
	blind := false
	for key := range keep {
		if !installed[key].manifestRead {
			blind = true
		}
		for _, jarFile := range installed[key].jarFiles {
			declaredByKept[jarFile] = true
		}
	}

	candidates := map[string]bool{}
	for key := range removing {
		for _, jarFile := range installed[key].jarFiles {
			if !declaredByKept[jarFile] {
				candidates[jarFile] = true
			}
		}
	}
	names := make([]string, 0, len(candidates))
	for jarFile := range candidates {
		names = append(names, jarFile)
	}
	sort.Strings(names)

	if blind {
		return nil, names, nil
	}
	for _, jarFile := range names {
		path := filepath.Join(i.jarsDir, jarFile)
		if _, statErr := os.Stat(path); statErr != nil {
			continue // never installed, or already gone
		}
		if err := os.Remove(path); err != nil {
			return pruned, nil, fmt.Errorf("cannot prune jar %s: %w", jarFile, err)
		}
		pruned = append(pruned, "jar "+jarFile)
	}
	return pruned, nil, nil
}

// storeEntry is one installed package as recovered by scanning the store. It is
// keyed canonically; dir and wcOwner keep the names the filesystem and the
// owners sidecar actually use, since those are what gets deleted.
type storeEntry struct {
	dir          string   // directory under packages/; "" for a pure webcomponent package
	wcOwner      string   // key in the webcomponent owners sidecar; "" if it owns no bundles
	fglDeps      []string // direct FGL deps (prod + optional), canonical
	jarFiles     []string // on-disk file names of the JARs it declares
	manifestRead bool     // false when a packages/ entry's manifest could not be read
}

// displayName is the name to show a user: the store directory when there is one,
// otherwise the name the webcomponent owners sidecar records.
func (e *storeEntry) displayName() string {
	if e.dir != "" {
		return e.dir
	}
	return e.wcOwner
}

// scanStore recovers what is installed, from both places a global install can
// leave something: a directory with a bundled manifest under packages/, and an
// entry in the webcomponent owners sidecar. A pure webcomponent package appears
// only in the latter — its manifest is deliberately not extracted, since several
// of them would collide on it — so scanning packages/ alone makes it invisible
// and unremovable.
//
// A package whose manifest is missing or unreadable still counts as installed:
// it occupies a directory and must remain removable by name. It contributes no
// edges, and is flagged so the JAR sweep knows the graph is incomplete.
func (i *Installer) scanStore() (map[string]*storeEntry, error) {
	out := map[string]*storeEntry{}

	entries, err := os.ReadDir(i.packagesDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		se := &storeEntry{dir: e.Name()}
		if m, loadErr := manifest.Load(filepath.Join(i.packagesDir, e.Name())); loadErr == nil {
			se.manifestRead = true
			// Dev deps are stripped at publish and never installed, so prod and
			// optional are the scopes that put things in the store.
			for _, deps := range []map[string]string{m.Dependencies.FGL, m.OptionalDependencies.FGL} {
				for dep := range deps {
					se.fglDeps = append(se.fglDeps, slugutil.Canonical(dep))
				}
			}
			for _, jars := range [][]manifest.JavaDependency{m.Dependencies.Java, m.OptionalDependencies.Java} {
				for _, j := range jars {
					se.jarFiles = append(se.jarFiles, j.JarFileName())
				}
			}
		}
		out[slugutil.Canonical(e.Name())] = se
	}

	owners, err := loadWCOwners(i.webcomponentsDir)
	if err != nil {
		return nil, err
	}
	for pkg := range owners.Packages {
		key := slugutil.Canonical(pkg)
		if se, ok := out[key]; ok {
			se.wcOwner = pkg // a mixed package: BDL directory AND webcomponent bundles
			continue
		}
		// Pure webcomponent package: bundles only, no manifest on disk to read.
		// It declares nothing this scan can see, but it is not a "blind" entry —
		// there is no manifest here to be missing.
		out[key] = &storeEntry{wcOwner: pkg, manifestRead: true}
	}
	return out, nil
}

// dedupeSorted collapses runs of equal entries in an already-sorted slice.
func dedupeSorted(in []string) []string {
	if len(in) < 2 {
		return in
	}
	out := in[:1]
	for _, s := range in[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}
