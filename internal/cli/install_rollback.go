package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// projectSnapshot captures fglpkg.json and fglpkg-lock.json so a failed
// `fglpkg install <pkg>` can put the project back exactly as it found it.
//
// The add path declares the dependency and saves the manifest BEFORE anything
// is fetched or extracted, and the lock is written inside the install. When the
// install then fails, both files keep describing a version that was never
// installed. The loud failure is recoverable; what follows is not — the project
// now claims a version the disk does not have, and `install` reports success on
// every subsequent run (GIS-579).
//
// The contract this restores is the one people expect from a package manager:
// `fglpkg install <pkg>` either adds the dependency and installs it, or changes
// nothing. Note that it covers the whole install, not only extraction: a
// failure anywhere leaves the same false claim behind.
//
// A file that did not exist is restored by being removed again — `fglpkg
// install <pkg>` in an empty directory generates a manifest, and a failed
// install should not leave a project behind that nobody asked for.
type projectSnapshot struct {
	files []snapshotFile
}

type snapshotFile struct {
	path    string
	data    []byte
	existed bool
	// skip is set when the file existed but could not be read. Restoring it
	// would mean deleting content we never captured, so it is left alone.
	skip bool
}

// snapshotProject captures the project files the add path may rewrite.
// manifestDir is where the manifest is saved (the add path saves to "."), and
// projectDir is where the lock is written; under a workspace they differ, so
// both are passed rather than derived.
func snapshotProject(manifestDir, projectDir string) *projectSnapshot {
	s := &projectSnapshot{}
	for _, p := range []string{
		filepath.Join(manifestDir, manifest.Filename),
		filepath.Join(projectDir, lockfile.Filename),
	} {
		s.files = append(s.files, captureFile(p))
	}
	return s
}

func captureFile(path string) snapshotFile {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		return snapshotFile{path: path, data: data, existed: true}
	case os.IsNotExist(err):
		return snapshotFile{path: path, existed: false}
	default:
		return snapshotFile{path: path, existed: true, skip: true}
	}
}

// restore puts the captured files back. It reports what it could not undo
// rather than failing: the caller is already returning the install's error, and
// that error is the one the user needs to see.
func (s *projectSnapshot) restore() {
	if s == nil {
		return
	}
	for _, f := range s.files {
		if f.skip {
			continue
		}
		var err error
		if f.existed {
			err = os.WriteFile(f.path, f.data, 0o644)
		} else if err = os.Remove(f.path); os.IsNotExist(err) {
			err = nil
		}
		if err != nil {
			fmt.Fprintf(os.Stderr,
				"warning: could not restore %s after the failed install: %v\n"+
					"  It may still record a package that was not installed; check it before committing.\n",
				f.path, err)
		}
	}
}
