package installer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// wcOwnersFilename is the sidecar that records, per scope, which files each
// webcomponent-bearing package installed under the webcomponents directory.
// It lives NEXT TO webcomponents/ (not inside it) so it is never mistaken for
// a COMPONENTTYPE dir by Genero's FGLIMAGEPATH / GWA discovery, which scans the
// webcomponents dir itself.
//
// The record is what lets `fglpkg remove` prune a package's webcomponent
// artifacts (GIS-372): the on-disk layout is keyed by COMPONENTTYPE, not by
// package name, and the resolver plan does not carry the COMPONENTTYPE list
// (it lives in the package manifest, read only at install time), so ownership
// must be persisted when the files are written.
const wcOwnersFilename = "webcomponent-owners.json"

// wcOwners maps a package name to the slash-relative paths (under the
// webcomponents dir) it installed. A file may be listed under more than one
// package when identical copies were deduplicated at install (see GIS-298);
// such a file is only deleted once its last owner is removed.
//
// Versions records the version each package was installed at. A web component
// leaves no manifest on disk — the publisher's fglpkg.json is deliberately not
// extracted, since several widgets would collide on it — so without this a lock
// replay had nothing to compare against and checked only that the webcomponents
// directory was non-empty. A pulled commit that bumped a widget and nothing
// else therefore reported "Nothing to install" and left the old bundle in place
// (GIS-579). It is the web-component counterpart of the BDL install stamp
// (lockfile.InstalledStampFilename).
//
// Versions is omitempty and may be absent: a sidecar written before this field
// existed has none, and every package it names then reads as installed at an
// unknown version, which counts as NOT the locked version. Such a store
// re-extracts its web components once and records them, so it converges by
// being used. Re-extraction is what a non-trivial install does to web
// components anyway, so that costs one run, and the alternative — treating
// "unknown" as "fine" — would keep the drift the field exists to end.
type wcOwners struct {
	Packages map[string][]string `json:"packages"`
	Versions map[string]string   `json:"versions,omitempty"`
}

func wcOwnersPath(webcomponentsDir string) string {
	return filepath.Join(filepath.Dir(webcomponentsDir), wcOwnersFilename)
}

// loadWCOwners reads the ownership sidecar for a scope. A missing or empty file
// yields an empty (non-nil) record and no error.
func loadWCOwners(webcomponentsDir string) (*wcOwners, error) {
	o := &wcOwners{Packages: map[string][]string{}, Versions: map[string]string{}}
	data, err := os.ReadFile(wcOwnersPath(webcomponentsDir))
	if err != nil {
		if os.IsNotExist(err) {
			return o, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return o, nil
	}
	if err := json.Unmarshal(data, o); err != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", wcOwnersFilename, err)
	}
	if o.Packages == nil {
		o.Packages = map[string][]string{}
	}
	if o.Versions == nil {
		o.Versions = map[string]string{}
	}
	return o, nil
}

// saveWCOwners writes the sidecar, or removes it when no package owns anything.
func saveWCOwners(webcomponentsDir string, o *wcOwners) error {
	path := wcOwnersPath(webcomponentsDir)
	// A version for a package that owns no files is a dangling claim that a
	// replay would believe, so the two maps are kept in step here as well as at
	// every call site.
	for pkg := range o.Versions {
		if _, owns := o.Packages[pkg]; !owns {
			delete(o.Versions, pkg)
		}
	}
	if len(o.Packages) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	data, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// wcOwnedBy returns the slash-relative paths the sidecar attributes to pkg.
//
// It is what lets an install tell the package's OWN previous files from another
// package's. extractWebcomponentZip's conflict check could not make that
// distinction and refused every differing file outside the package's declared
// COMPONENTTYPE dirs — including the files the package itself installed last
// time — so any widget shipping a BDL wrapper, docs or examples could not be
// moved to another version at all (GIS-579).
//
// A missing or unreadable sidecar yields an empty set rather than an error: the
// caller is deciding whether a file is safe to overwrite, and "no record" must
// mean "assume it is not ours", which is the cautious answer.
func wcOwnedBy(webcomponentsDir, pkg string) map[string]bool {
	o, err := loadWCOwners(webcomponentsDir)
	if err != nil {
		return nil
	}
	owned := make(map[string]bool, len(o.Packages[pkg]))
	for _, f := range o.Packages[pkg] {
		owned[f] = true
	}
	return owned
}

// recordWCOwnership records the file list owned by pkg, replacing any prior
// record for it (a reinstall re-states ownership). An empty list drops the
// package's entry. A no-op when files is empty and the package was unknown, so
// pure-BDL installs never create the sidecar.
//
// Files pkg owned before and does not ship now are DELETED, unless another
// package still owns them. Without that, a version that drops a file leaves it
// behind forever: nothing else ever looks at it again, it stays on
// FGLIMAGEPATH, and `remove` cannot prune it because the ownership record no
// longer mentions it. The deletion happens after extraction, so `files` is what
// is actually on disk and the difference is exactly what the new version
// dropped.
func recordWCOwnership(webcomponentsDir, pkg, version string, files []string) error {
	o, err := loadWCOwners(webcomponentsDir)
	if err != nil {
		return err
	}
	_, known := o.Packages[pkg]
	if len(files) == 0 && !known {
		return nil
	}

	keep := make(map[string]bool, len(files))
	for _, f := range files {
		keep[f] = true
	}
	// Co-ownership from install-time dedup (GIS-298): a file listed under
	// another package is that package's too, and must survive.
	stillOwned := map[string]bool{}
	for other, owned := range o.Packages {
		if other == pkg {
			continue
		}
		for _, f := range owned {
			stillOwned[f] = true
		}
	}
	for _, f := range o.Packages[pkg] {
		if keep[f] || stillOwned[f] {
			continue
		}
		target := filepath.Join(webcomponentsDir, filepath.FromSlash(f))
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("cannot remove superseded webcomponent file %s: %w", f, err)
		}
	}
	if err := removeEmptyDirs(webcomponentsDir); err != nil {
		return err
	}

	if len(files) == 0 {
		delete(o.Packages, pkg)
		delete(o.Versions, pkg)
		return saveWCOwners(webcomponentsDir, o)
	}
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	o.Packages[pkg] = sorted
	o.Versions[pkg] = version
	return saveWCOwners(webcomponentsDir, o)
}

// pruneWebcomponents deletes the webcomponent artifacts owned solely by
// packages that are no longer wanted, leaving any file still owned by a
// remaining package in place (co-ownership from dedup). wantWC is the set of
// package names whose webcomponent artifacts should remain. Empty directories
// left behind are removed. Returns "webcomponent <pkg>" notes for the packages
// whose artifacts were pruned.
func (i *Installer) pruneWebcomponents(wantWC map[string]bool) ([]string, error) {
	o, err := loadWCOwners(i.webcomponentsDir)
	if err != nil {
		return nil, err
	}
	if len(o.Packages) == 0 {
		return nil, nil
	}

	var removed []string
	for pkg := range o.Packages {
		if !wantWC[pkg] {
			removed = append(removed, pkg)
		}
	}
	if len(removed) == 0 {
		return nil, nil
	}
	sort.Strings(removed)

	// Files still owned by a package that remains must not be deleted.
	stillOwned := map[string]bool{}
	for pkg, files := range o.Packages {
		if wantWC[pkg] {
			for _, f := range files {
				stillOwned[f] = true
			}
		}
	}

	deleted := map[string]bool{}
	for _, pkg := range removed {
		for _, f := range o.Packages[pkg] {
			if stillOwned[f] || deleted[f] {
				continue
			}
			target := filepath.Join(i.webcomponentsDir, filepath.FromSlash(f))
			if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("cannot prune webcomponent file %s: %w", f, err)
			}
			deleted[f] = true
		}
		delete(o.Packages, pkg)
		delete(o.Versions, pkg)
	}

	if err := removeEmptyDirs(i.webcomponentsDir); err != nil {
		return nil, err
	}
	if err := saveWCOwners(i.webcomponentsDir, o); err != nil {
		return nil, err
	}

	pruned := make([]string, len(removed))
	for idx, pkg := range removed {
		pruned[idx] = "webcomponent " + pkg
	}
	return pruned, nil
}

// removeEmptyDirs removes empty directories beneath root (but never root
// itself), deepest first, in a single pass.
func removeEmptyDirs(root string) error {
	var dirs []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if d.IsDir() && path != root {
			dirs = append(dirs, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	// WalkDir visits parents before children; reverse so children are
	// considered (and removed) before their parents.
	for j := len(dirs) - 1; j >= 0; j-- {
		entries, err := os.ReadDir(dirs[j])
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if len(entries) == 0 {
			if err := os.Remove(dirs[j]); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

// installedWebcomponents returns the version each web-component package was
// installed at, for the lock replay's staleness check.
//
// It is passed to LockFile.Validate rather than read there, so the sidecar's
// format stays known to this package alone. Validate's other presence checks
// read the disk directly, but this one cannot: the sidecar is an installer
// artifact whose other half — the per-package file list — exists for `remove`
// and `prune`, and splitting its shape across two packages is how the two
// copies drift apart.
//
// An unreadable sidecar yields nil, which Validate reads as "nothing is
// installed" — the safe direction, and the same answer a missing one gives.
func (i *Installer) installedWebcomponents() map[string]string {
	o, err := loadWCOwners(i.webcomponentsDir)
	if err != nil {
		return nil
	}
	return o.Versions
}
