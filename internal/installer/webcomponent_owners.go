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
type wcOwners struct {
	Packages map[string][]string `json:"packages"`
}

func wcOwnersPath(webcomponentsDir string) string {
	return filepath.Join(filepath.Dir(webcomponentsDir), wcOwnersFilename)
}

// loadWCOwners reads the ownership sidecar for a scope. A missing or empty file
// yields an empty (non-nil) record and no error.
func loadWCOwners(webcomponentsDir string) (*wcOwners, error) {
	o := &wcOwners{Packages: map[string][]string{}}
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
	return o, nil
}

// saveWCOwners writes the sidecar, or removes it when no package owns anything.
func saveWCOwners(webcomponentsDir string, o *wcOwners) error {
	path := wcOwnersPath(webcomponentsDir)
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

// recordWCOwnership records the file list owned by pkg, replacing any prior
// record for it (a reinstall re-states ownership). An empty list drops the
// package's entry. A no-op when files is empty and the package was unknown, so
// pure-BDL installs never create the sidecar.
func recordWCOwnership(webcomponentsDir, pkg string, files []string) error {
	o, err := loadWCOwners(webcomponentsDir)
	if err != nil {
		return err
	}
	_, known := o.Packages[pkg]
	if len(files) == 0 {
		if !known {
			return nil
		}
		delete(o.Packages, pkg)
		return saveWCOwners(webcomponentsDir, o)
	}
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	o.Packages[pkg] = sorted
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
