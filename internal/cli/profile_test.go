package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// TestBuildPackageZip_ShipsProfileNotMatchedByFilesGlobs: a declared profile is
// always packed. The default `files` globs (*.42m/*.42f/*.sch) can never match
// one, so without force-staging a declared profile could not ship at all — and
// the consumer's FGLPROFILE would silently have nothing to point at.
func TestBuildPackageZip_ShipsProfileNotMatchedByFilesGlobs(t *testing.T) {
	stagePackTestDir(t, map[string]string{
		"fglpkg.json": `{
  "name": "fglpkgtest",
  "version": "1.0.0",
  "files": ["*.42m"],
  "profile": ["profiles/app.4gp"],
  "dependencies": { "fgl": {} }
}`,
		"ModuleA.42m":      "MAIN END MAIN\n",
		"profiles/app.4gp": "gwc.server.name = \"demo\"\n",
	})

	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	data, _, err := buildPackageZip(m)
	if err != nil {
		t.Fatalf("buildPackageZip: %v", err)
	}
	got := zipEntries(t, data)
	if _, ok := got["profiles/app.4gp"]; !ok {
		t.Errorf("expected profiles/app.4gp in the archive; got %v", keys(boolKeys(got)))
	}
}

// TestBuildPackageZip_RewritesProfileToArchivePath: author-side `profile` is
// relative to `root`, but staging strips importRoot. The shipped manifest must
// describe the post-strip layout, or the installed copy's FGLPROFILE lookup
// (which joins the entry onto the store dir) would miss the file.
//
// The fixture deliberately leaves `root` at its default "." while importRoot is
// "lib", so the author-side entry ("lib/profiles/app.4gp") and the archive path
// ("profiles/app.4gp") are DIFFERENT strings. A fixture where root == importRoot
// makes the strip a no-op, and the rewrite could be deleted without failing.
func TestBuildPackageZip_RewritesProfileToArchivePath(t *testing.T) {
	stagePackTestDir(t, map[string]string{
		"fglpkg.json": `{
  "name": "fglpkgtest",
  "version": "1.0.0",
  "importRoot": "lib",
  "files": ["*.42m"],
  "profile": ["lib/profiles/app.4gp"],
  "dependencies": { "fgl": {} }
}`,
		"lib/ModuleA.42m":      "MAIN END MAIN\n",
		"lib/profiles/app.4gp": "gwc.server.name = \"demo\"\n",
	})

	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	data, _, err := buildPackageZip(m)
	if err != nil {
		t.Fatalf("buildPackageZip: %v", err)
	}
	got := zipEntries(t, data)

	// The file itself is rebased out of lib/.
	if _, ok := got["profiles/app.4gp"]; !ok {
		t.Errorf("expected the profile rebased to profiles/app.4gp; got %v", keys(boolKeys(got)))
	}
	if _, ok := got["lib/profiles/app.4gp"]; ok {
		t.Error("the profile must not keep its lib/ prefix in the archive")
	}
	// And the shipped manifest points at where it actually landed, not at the
	// author-side path — this is what env's os.Stat gate consumes.
	var shipped manifest.Manifest
	if err := json.Unmarshal([]byte(got[manifest.Filename]), &shipped); err != nil {
		t.Fatalf("unmarshal shipped manifest: %v", err)
	}
	if len(shipped.Profile) != 1 || shipped.Profile[0] != "profiles/app.4gp" {
		t.Errorf("shipped manifest profile should be the archive path; got %v", shipped.Profile)
	}
}

// TestBuildPackageZip_ProfileOutsideImportRootPointsAtRoot: a profile that
// cannot be rebased under importRoot is a hard pack error (never a silent
// mis-ship), and the remedy offered must be one that works. `include` is not:
// it folds by basename into the archive root and does not feed the shipped
// `profile` path, so the author has to fix root/importRoot instead.
func TestBuildPackageZip_ProfileOutsideImportRootPointsAtRoot(t *testing.T) {
	stagePackTestDir(t, map[string]string{
		"fglpkg.json": `{
  "name": "fglpkgtest",
  "version": "1.0.0",
  "importRoot": "lib",
  "files": ["*.42m"],
  "profile": ["cfg/app.4gp"],
  "dependencies": { "fgl": {} }
}`,
		"lib/ModuleA.42m": "MAIN END MAIN\n",
		"cfg/app.4gp":     "gwc.server.name = \"demo\"\n",
	})

	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	_, _, err = buildPackageZip(m)
	if err == nil {
		t.Fatal("expected an error for a profile outside importRoot")
	}
	msg := err.Error()
	if !strings.Contains(msg, "profile file") || !strings.Contains(msg, "importRoot") {
		t.Errorf("error should name the profile file and importRoot; got: %v", err)
	}
	if strings.Contains(msg, "add it to include") {
		t.Errorf("`include` cannot fix a profile — do not suggest it; got: %v", err)
	}
}

// TestBuildPackageZip_MissingProfileFails: a declared profile that does not
// exist is a packaging error, not a silent omission.
func TestBuildPackageZip_MissingProfileFails(t *testing.T) {
	stagePackTestDir(t, map[string]string{
		"fglpkg.json": `{
  "name": "fglpkgtest",
  "version": "1.0.0",
  "files": ["*.42m"],
  "profile": ["profiles/gone.4gp"],
  "dependencies": { "fgl": {} }
}`,
		"ModuleA.42m": "MAIN END MAIN\n",
	})

	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, _, err := buildPackageZip(m); err == nil {
		t.Fatal("expected an error for a missing profile file")
	} else if !strings.Contains(err.Error(), "profile file") {
		t.Errorf("error should name the profile file; got: %v", err)
	}
}

// TestBuildPackageZip_ShipsProfileDespiteIgnore: .fglpkgignore must not be able
// to drop a declared profile — same rule as `bin`.
func TestBuildPackageZip_ShipsProfileDespiteIgnore(t *testing.T) {
	stagePackTestDir(t, map[string]string{
		"fglpkg.json": `{
  "name": "fglpkgtest",
  "version": "1.0.0",
  "files": ["*.42m"],
  "profile": ["profiles/app.4gp"],
  "dependencies": { "fgl": {} }
}`,
		"ModuleA.42m":      "MAIN END MAIN\n",
		"profiles/app.4gp": "gwc.server.name = \"demo\"\n",
		".fglpkgignore":    "profiles/\n",
	})

	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	data, _, err := buildPackageZip(m)
	if err != nil {
		t.Fatalf("buildPackageZip: %v", err)
	}
	if got := zipEntries(t, data); got["profiles/app.4gp"] == "" {
		t.Errorf("a declared profile must ship even when .fglpkgignore covers it; got %v", keys(boolKeys(got)))
	}
}
