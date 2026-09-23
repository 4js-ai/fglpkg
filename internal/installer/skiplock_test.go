package installer

import (
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
)

// TestSyncMergedRootSkipsLockWhenNotRecording backs the global-tool install
// (GIS-565): with recordLock=false — the value InstallAllWithOptions passes
// under Options.SkipLock — the merged FGLLDPATH root is still built (so a
// globally installed package resolves), but no lock file is written into
// projectDir. That is what keeps `install <pkg> --global` from leaving an
// fglpkg-lock.json in the user's current directory.
func TestSyncMergedRootSkipsLockWhenNotRecording(t *testing.T) {
	home := t.TempDir()
	projectDir := t.TempDir()
	i := New(home, "", "", "")

	writeStore(t, i, "dbconnection", `{
  "name": "dbconnection", "version": "1.0.0",
  "generoPackages": ["com.fourjs.db"],
  "dependencies": { "fgl": {} }
}`, map[string]string{
		"com/fourjs/db/DbConnection.42m": "DB",
	})

	if err := i.syncMergedRoot(projectDir, false); err != nil {
		t.Fatalf("syncMergedRoot: %v", err)
	}

	// The merged root must be built regardless — this is what makes the globally
	// installed package usable.
	if !mergedHas(i, "com/fourjs/db/DbConnection.42m") {
		t.Fatal("merged root should be built even when the lock is not recorded")
	}
	// ...but no lock may be written into the current directory.
	if lockfile.Exists(projectDir) {
		t.Fatalf("no lock file should be written when recordLock is false (projectDir=%s)", projectDir)
	}
}
