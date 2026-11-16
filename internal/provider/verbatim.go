package provider

import (
	"errors"

	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
)

// This file holds the read path for a package that sits in a repository under
// the name its publisher typed, rather than under the canonical slug fglpkg
// addresses packages by (GIS-610).
//
// fglpkg <= 4.2.9 published to Artifactory under the manifest's name as
// written, so a repository can hold `DateTime/0.1.0/...`. From 4.2.10 every
// name is canonicalized at the provider boundary, which is what makes a package
// reachable however its name is typed — and which left those folders
// unaddressable. `install` could never reach them (it resolved the typed name
// against the server's own spelling and 404'd as often as not), so nothing is
// lost there; `info` could, and the regression is that it stopped.
//
// Only `info` uses this. The line between the two is not fussiness: `install`
// writes what it resolved into fglpkg.json and the lock, under the slug. A
// package installed out of `DateTime/` would be recorded as `datetime`, and the
// next install — or anyone else's, on another machine — would look for a folder
// that does not exist. `info` records nothing, so it can afford to look.

// VerbatimLister is implemented by a provider whose packages are addressed by a
// name in storage, so that a name as typed and its canonical slug can point at
// two different places. The Artifactory provider is one; GI is not, because the
// registry slugs the name server-side and there is only ever one folder.
type VerbatimLister interface {
	// FetchVersionsVerbatim lists the versions stored under EXACTLY this name.
	FetchVersionsVerbatim(folder string) ([]resolver.CandidateVersion, error)
	// FetchInfoVerbatim returns the metadata for a version of the package
	// stored under EXACTLY this name.
	FetchInfoVerbatim(folder, version, generoMajor string) (*registry.PackageInfo, error)
}

// VersionsVerbatim lists the versions of the package stored under exactly the
// name given, in the one repository that holds it.
//
// It applies the same scoping and the same collision guard as Versions: a
// --registry restriction, a pin or a consume default each narrow the set to one
// repository, and a name found in two repositories is refused rather than
// guessed. What it does not do is canonicalize, so a caller must have a reason
// to believe the name is a folder — in practice, that the canonical lookup
// already returned "not found".
//
// ErrNotFound means no admitting repository holds such a folder. Callers report
// the error from the canonical lookup instead, which names what was searched.
func (rs *RepositorySet) VersionsVerbatim(name string) ([]resolver.CandidateVersion, error) {
	d, err := rs.routeVerbatim(name)
	if err != nil {
		return nil, err
	}
	return d.versions, nil
}

// InfoVerbatim returns metadata for a version of the package stored under
// exactly the name given, from the repository VersionsVerbatim routed to.
func (rs *RepositorySet) InfoVerbatim(name, version, generoMajor string) (*registry.PackageInfo, error) {
	d, err := rs.routeVerbatim(name)
	if err != nil {
		return nil, err
	}
	v, ok := d.provider.(VerbatimLister)
	if !ok {
		// Unreachable: routeVerbatim only ever records a provider it asked
		// through this interface.
		return nil, registry.ErrNotFound
	}
	return v.FetchInfoVerbatim(name, version, generoMajor)
}

// routeVerbatim is route's counterpart for an exact folder name: same
// precedence, same collision guard, over the providers that can address a
// folder at all. The decision is memoized separately from route's, because the
// two answer different questions about the same name and a package can exist
// under both spellings.
func (rs *RepositorySet) routeVerbatim(name string) (routeDecision, error) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if d, ok := rs.verbatimRoutes[name]; ok {
		return d, nil
	}
	var hits []routeDecision
	for _, p := range rs.verbatimCandidates(name) {
		v, ok := p.(VerbatimLister)
		if !ok {
			continue
		}
		vs, err := v.FetchVersionsVerbatim(name)
		if err != nil {
			if errors.Is(err, registry.ErrNotFound) {
				continue
			}
			// Same rule as route: an auth or transport failure must never be
			// read as "absent", or a repository drops out of the count and the
			// collision guard stops guarding (spec §7.2).
			return routeDecision{}, err
		}
		hits = append(hits, routeDecision{provider: p, versions: vs})
	}
	switch len(hits) {
	case 0:
		return routeDecision{}, registry.ErrNotFound
	case 1:
		rs.verbatimRoutes[name] = hits[0]
		return hits[0], nil
	default:
		return routeDecision{}, collisionError(name, hits)
	}
}

// verbatimCandidates returns the providers a verbatim lookup may ask, in the
// same precedence route uses: a --registry restriction, then a pin, then a
// consume default, each of which narrows to exactly one repository; otherwise
// every repository whose scope admits the name.
//
// A restriction naming a repository that is not configured yields no candidates
// rather than falling through to the fan-out. Widening the consulted set when a
// user asked for one repository would undo the scoping, which is the mistake
// this returns nothing to avoid.
func (rs *RepositorySet) verbatimCandidates(name string) []Provider {
	switch {
	case rs.restrictTo != "":
		return onlyProvider(rs.byName(rs.restrictTo))
	case rs.pinFor(name) != "":
		return onlyProvider(rs.byName(rs.pinFor(name)))
	case rs.consumeDefault != "":
		return onlyProvider(rs.byName(rs.consumeDefault))
	}
	out := make([]Provider, 0, len(rs.providers))
	for _, p := range rs.providers {
		if rs.admits(p, name) {
			out = append(out, p)
		}
	}
	return out
}

func onlyProvider(p Provider) []Provider {
	if p == nil {
		return nil
	}
	return []Provider{p}
}
