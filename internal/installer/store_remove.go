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
	// Removed holds the store directory names that were deleted.
	Removed []string
	// NotFound holds requested names that were not installed in this store —
	// reported rather than silently treated as success (the GIS-564 rule).
	NotFound []string
	// Pruned describes artifacts swept alongside the packages (JARs and
	// webcomponent bundles no remaining package declares), in pruneTo's
	// human-readable form.
	Pruned []string
	// Orphaned names packages that a removed package pulled in and that nothing
	// left in the store references. They are deliberately NOT deleted — see
	// RemoveFromStore — only reported, so the user can decide.
	Orphaned []string
	// StillRequiredBy maps a removed package to the remaining packages that
	// still declare a dependency on it. Removing one of these is the user's
	// call, but it leaves those dependents unsatisfied, so it is surfaced.
	StillRequiredBy map[string][]string
}

// RemoveFromStore uninstalls the named packages from THIS installer's store and
// sweeps the artifacts no remaining package still declares. It is the
// counterpart of a global tool install (GIS-565): the store has no manifest and
// no lock of its own, so both what is installed and what depends on what are
// recovered by scanning the bundled fglpkg.json of every installed package
// (GIS-567).
//
// This is safe for a global (shared) store precisely because it prunes against
// the store's OWN reconstructed graph. ReconcileAfterRemove must not prune a
// global home for the opposite reason: it prunes against a single project's
// graph, which knows nothing about the other projects sharing the store.
//
// Scope, in order of decreasing certainty about what the user wants:
//   - the named packages are deleted;
//   - JARs and webcomponent bundles that nothing remaining declares are pruned,
//     since an artifact no installed package references can never be reached;
//   - packages the removed ones pulled in are only REPORTED (Orphaned), never
//     deleted. The store cannot distinguish a package installed as a dependency
//     from one the user installed in its own right, so deleting these would
//     throw away an explicit install on a guess.
//
// Names are matched canonically (GIS-271), so `remove demo.pkg` finds the store
// directory `demo-pkg`.
func (i *Installer) RemoveFromStore(names []string) (*StoreRemoval, error) {
	res := &StoreRemoval{StillRequiredBy: map[string][]string{}}

	installed, err := i.scanStore()
	if err != nil {
		return nil, err
	}

	// Resolve each requested name to a store directory, canonically.
	byCanonical := make(map[string]string, len(installed))
	for dir := range installed {
		byCanonical[slugutil.Canonical(dir)] = dir
	}
	removing := map[string]bool{}
	for _, name := range names {
		if dir, ok := byCanonical[slugutil.Canonical(name)]; ok {
			removing[dir] = true
		} else {
			res.NotFound = append(res.NotFound, name)
		}
	}
	if len(removing) == 0 {
		return res, nil
	}

	// Everything that survives, and what it still declares.
	keepPkg := make(map[string]bool, len(installed))
	for dir := range installed {
		if !removing[dir] {
			keepPkg[dir] = true
		}
	}

	// A dependent left behind by this removal is worth saying out loud.
	for dir := range keepPkg {
		for _, dep := range installed[dir].fglDeps {
			if removing[dep] {
				res.StillRequiredBy[dep] = append(res.StillRequiredBy[dep], dir)
			}
		}
	}
	for dep := range res.StillRequiredBy {
		sort.Strings(res.StillRequiredBy[dep])
	}

	// Orphans: pulled in by something being removed, referenced by nothing that
	// remains. Reported only — see the doc comment.
	referencedByKept := map[string]bool{}
	for dir := range keepPkg {
		for _, dep := range installed[dir].fglDeps {
			referencedByKept[dep] = true
		}
	}
	for dir := range removing {
		for _, dep := range installed[dir].fglDeps {
			if keepPkg[dep] && !referencedByKept[dep] {
				res.Orphaned = append(res.Orphaned, dep)
			}
		}
	}
	sort.Strings(res.Orphaned)
	res.Orphaned = dedupeSorted(res.Orphaned)

	// JARs worth keeping: every file name a surviving package declares. The
	// global store installs each package's own declared JARs rather than
	// resolving one shared version, so two versions of the same coordinate can
	// legitimately both be wanted.
	keepJar := map[string]bool{}
	for dir := range keepPkg {
		for _, jarFile := range installed[dir].jarFiles {
			keepJar[jarFile] = true
		}
	}

	for dir := range removing {
		if err := os.RemoveAll(filepath.Join(i.packagesDir, dir)); err != nil {
			return res, fmt.Errorf("cannot remove package %s: %w", dir, err)
		}
		res.Removed = append(res.Removed, dir)
	}
	sort.Strings(res.Removed)

	// Sweep what the removed packages uniquely owned. The package pass is a
	// no-op (they are already gone and everything else is in keepPkg); this is
	// here for the JAR and webcomponent passes, which need the keep-sets.
	pruned, err := i.pruneTo(keepPkg, keepPkg, keepJar)
	res.Pruned = pruned
	if err != nil {
		return res, err
	}

	// Best-effort, exactly as after a project remove: a stale anchor only lists
	// files that no longer exist, and a merged-root problem must never leave the
	// store half-removed.
	_ = classpath.Sync(i.jarsDir)
	i.RebuildMergedRoot()

	return res, nil
}

// storePkg is one installed package as recovered from its bundled manifest.
type storePkg struct {
	fglDeps  []string // direct FGL deps (prod + optional), by canonical store name
	jarFiles []string // on-disk file names of the JARs it declares
}

// scanStore reads every installed package's bundled manifest. A package whose
// manifest is missing or unreadable still counts as installed — it occupies a
// directory and can be removed by name — it simply contributes no edges, so it
// is never mistaken for something that depends on nothing being kept.
func (i *Installer) scanStore() (map[string]storePkg, error) {
	entries, err := os.ReadDir(i.packagesDir)
	if os.IsNotExist(err) {
		return map[string]storePkg{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make(map[string]storePkg, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sp := storePkg{}
		if m, err := manifest.Load(filepath.Join(i.packagesDir, e.Name())); err == nil {
			// Dev deps are stripped at publish and never installed, so prod and
			// optional are the scopes that put things in the store.
			for _, deps := range []map[string]string{m.Dependencies.FGL, m.OptionalDependencies.FGL} {
				for dep := range deps {
					sp.fglDeps = append(sp.fglDeps, slugutil.Canonical(dep))
				}
			}
			for _, jars := range [][]manifest.JavaDependency{m.Dependencies.Java, m.OptionalDependencies.Java} {
				for _, j := range jars {
					sp.jarFiles = append(sp.jarFiles, j.JarFileName())
				}
			}
		}
		out[e.Name()] = sp
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
