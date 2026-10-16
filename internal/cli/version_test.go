package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"time"

	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
	"github.com/4js-mikefolcher/fglpkg/internal/updatecheck"
)

func TestBumpVersion(t *testing.T) {
	cases := []struct {
		name    string
		from    string
		kind    string
		want    string
		wantErr bool
	}{
		{"patch", "1.2.3", "patch", "1.2.4", false},
		{"patch_from_zero", "0.0.0", "patch", "0.0.1", false},
		{"minor", "1.2.3", "minor", "1.3.0", false},
		{"minor_resets_patch", "1.2.9", "minor", "1.3.0", false},
		{"major", "1.2.3", "major", "2.0.0", false},
		{"major_resets_minor_and_patch", "1.9.9", "major", "2.0.0", false},

		// Prerelease semantics (npm-compatible)
		{"prerelease_from_stable", "1.2.3", "prerelease", "1.2.4-0", false},
		{"prerelease_bump_numeric", "1.2.4-0", "prerelease", "1.2.4-1", false},
		{"prerelease_bump_dotted_numeric", "1.2.4-alpha.0", "prerelease", "1.2.4-alpha.1", false},
		{"prerelease_appends_to_non_numeric", "1.2.4-alpha", "prerelease", "1.2.4-alpha.0", false},

		// Explicit versions
		{"explicit_set", "1.2.3", "2.0.0", "2.0.0", false},
		{"explicit_with_prerelease", "1.2.3", "5.0.0-rc.1", "5.0.0-rc.1", false},

		// Errors
		{"unknown_kind", "1.2.3", "bogus", "", true},
		{"invalid_explicit", "1.2.3", "not-a-version", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cur := semver.MustParse(tc.from)
			got, err := bumpVersion(cur, tc.kind)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %s", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.String() != tc.want {
				t.Errorf("bump(%s, %s) = %s, want %s", tc.from, tc.kind, got, tc.want)
			}
		})
	}
}

// TestBumpCommandRoundTrip exercises the `bump` command end-to-end against a
// real on-disk manifest (no --git): Load → mutate → Save, confirming the new
// version survives a write/read cycle through the strict parser (GIS-288).
func TestBumpCommandRoundTrip(t *testing.T) {
	dir := t.TempDir()
	raw := `{
  "name": "rt-test",
  "version": "1.0.0",
  "dependencies": { "fgl": {} }
}`
	if err := os.WriteFile(filepath.Join(dir, manifest.Filename), []byte(raw), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	if _, err := captureStdout(t, func() error { return cmdBump([]string{"minor"}) }); err != nil {
		t.Fatalf("cmdBump: %v", err)
	}

	reloaded, err := manifest.Load(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Version != "1.1.0" {
		t.Errorf("reloaded version = %q, want %q", reloaded.Version, "1.1.0")
	}
}

// TestBumpUpdatesLockfileRootVersion: when a lockfile sits beside the manifest,
// `fglpkg bump` updates its root.version to match — the way npm's `npm version`
// also rewrites package-lock.json (GIS-492). Only root.version changes; the
// root.declared snapshot (and the resolved entries) must survive, because a
// version bump is not a re-resolution.
func TestBumpUpdatesLockfileRootVersion(t *testing.T) {
	dir := t.TempDir()
	raw := `{
  "name": "lock-sync",
  "version": "1.2.3",
  "dependencies": { "fgl": {} }
}`
	if err := os.WriteFile(filepath.Join(dir, manifest.Filename), []byte(raw), 0644); err != nil {
		t.Fatalf("setup manifest: %v", err)
	}
	lock := `{
  "lockfileVersion": 1,
  "generatedAt": "2026-01-01T00:00:00Z",
  "generoVersion": "6.00.01",
  "root": {
    "name": "lock-sync",
    "version": "1.2.3",
    "declared": { "dependencies": {}, "devDependencies": {}, "optionalDependencies": {} }
  },
  "packages": [],
  "jars": []
}`
	if err := os.WriteFile(filepath.Join(dir, lockfile.Filename), []byte(lock), 0644); err != nil {
		t.Fatalf("setup lock: %v", err)
	}

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	if _, err := captureStdout(t, func() error { return cmdBump([]string{"patch"}) }); err != nil {
		t.Fatalf("cmdBump: %v", err)
	}

	lf, err := lockfile.Load(dir)
	if err != nil {
		t.Fatalf("reload lock: %v", err)
	}
	if lf.RootManifest.Version != "1.2.4" {
		t.Errorf("lock root.version = %q, want %q", lf.RootManifest.Version, "1.2.4")
	}
	if lf.RootManifest.Declared == nil {
		t.Error("lock root.declared was dropped; bump must not re-resolve")
	}
	m, err := manifest.Load(dir)
	if err != nil {
		t.Fatalf("reload manifest: %v", err)
	}
	if m.Version != "1.2.4" {
		t.Errorf("manifest version = %q, want %q", m.Version, "1.2.4")
	}
}

// TestBumpWithoutLockfileCreatesNone: `fglpkg bump` in a project that has no
// lockfile must not fabricate one — bump never resolves (GIS-492).
func TestBumpWithoutLockfileCreatesNone(t *testing.T) {
	dir := t.TempDir()
	raw := `{
  "name": "no-lock",
  "version": "1.0.0",
  "dependencies": { "fgl": {} }
}`
	if err := os.WriteFile(filepath.Join(dir, manifest.Filename), []byte(raw), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	if _, err := captureStdout(t, func() error { return cmdBump([]string{"minor"}) }); err != nil {
		t.Fatalf("cmdBump: %v", err)
	}
	if lockfile.Exists(dir) {
		t.Error("bump created a lockfile where none existed; it must not resolve")
	}
}

// gitRun runs git in the current working directory, failing the test on error.
func gitRun(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// gitInitCommitted makes the current directory a repo with paths committed.
// Signing is disabled locally so a developer's global commit.gpgsign cannot
// make these tests prompt (or fail) on an unrelated machine.
func gitInitCommitted(t *testing.T, paths ...string) {
	t.Helper()
	gitRun(t, "init")
	gitRun(t, "config", "user.email", "test@example.com")
	gitRun(t, "config", "user.name", "fglpkg tests")
	gitRun(t, "config", "commit.gpgsign", "false")
	gitRun(t, append([]string{"add", "--"}, paths...)...)
	gitRun(t, "commit", "-m", "init")
}

// lockFixture is a lockfile with one resolved package and one JAR, so a test can
// tell a field-only edit from a re-resolution. Written in the canonical form
// Save emits, which makes a byte comparison meaningful.
const lockFixture = `{
  "lockfileVersion": 1,
  "generatedAt": "2026-01-01T00:00:00Z",
  "generoVersion": "6.00.01",
  "root": {
    "name": "lock-sync",
    "version": "1.2.3",
    "declared": {
      "dependencies": {
        "fgl": {
          "logger": "^1.0.0"
        }
      },
      "devDependencies": {},
      "optionalDependencies": {}
    }
  },
  "packages": [
    {
      "name": "logger",
      "version": "1.2.0",
      "downloadUrl": "https://example.invalid/logger-1.2.0.zip",
      "checksum": "sha256:deadbeef",
      "requiredBy": [
        "<root>"
      ]
    }
  ],
  "jars": []
}
`

// writeBumpFixture lays down a manifest at version 1.2.3 plus, when lock is
// non-empty, a lockfile, and chdirs into the result.
func writeBumpFixture(t *testing.T, lock string) string {
	t.Helper()
	dir := t.TempDir()
	raw := `{
  "name": "lock-sync",
  "version": "1.2.3",
  "dependencies": { "fgl": { "logger": "^1.0.0" } }
}`
	if err := os.WriteFile(filepath.Join(dir, manifest.Filename), []byte(raw), 0644); err != nil {
		t.Fatalf("setup manifest: %v", err)
	}
	if lock != "" {
		if err := os.WriteFile(filepath.Join(dir, lockfile.Filename), []byte(lock), 0644); err != nil {
			t.Fatalf("setup lock: %v", err)
		}
	}
	chdirTest(t, dir)
	return dir
}

// TestBumpPreservesResolvedLockEntries: the lock edit is a field-only one. Every
// byte except root.version — generatedAt, the resolved package, the declared
// snapshot — must come back unchanged, because a version bump moves no
// constraint and so must not look like a re-resolution (GIS-492).
func TestBumpPreservesResolvedLockEntries(t *testing.T) {
	dir := writeBumpFixture(t, lockFixture)
	if _, err := captureStdout(t, func() error { return cmdBump([]string{"patch"}) }); err != nil {
		t.Fatalf("cmdBump: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, lockfile.Filename))
	if err != nil {
		t.Fatalf("reread lock: %v", err)
	}
	want := strings.Replace(lockFixture, `"version": "1.2.3"`, `"version": "1.2.4"`, 1)
	if string(got) != want {
		t.Errorf("lock changed beyond root.version:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestBumpGitCommitsManifestAndLock: under --git both files land in the one
// commit the tag points at, so the tagged tree is self-consistent and no
// modification is left behind in the working tree.
func TestBumpGitCommitsManifestAndLock(t *testing.T) {
	writeBumpFixture(t, lockFixture)
	gitInitCommitted(t, manifest.Filename, lockfile.Filename)

	if _, err := captureStdout(t, func() error { return cmdBump([]string{"patch", "--git"}) }); err != nil {
		t.Fatalf("cmdBump --git: %v", err)
	}
	if out := gitRun(t, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Errorf("working tree not clean after bump --git:\n%s", out)
	}
	if out := gitRun(t, "tag"); !strings.Contains(out, "v1.2.4") {
		t.Errorf("tag v1.2.4 not created; tags = %q", out)
	}
	committed := gitRun(t, "show", "--name-only", "--format=", "HEAD")
	for _, want := range []string{manifest.Filename, lockfile.Filename} {
		if !strings.Contains(committed, want) {
			t.Errorf("%s missing from the bump commit; it touched:\n%s", want, committed)
		}
	}
}

// TestBumpGitignoredLockDoesNotBlockCommit: a project that deliberately does not
// commit its lock still gets a working `bump --git`. `git add` on an ignored path
// is a hard error, so staging it unconditionally aborted the bump *after* the
// manifest had been rewritten — and the resulting dirty tree then blocked the
// retry on requireCleanGitTree.
func TestBumpGitignoredLockDoesNotBlockCommit(t *testing.T) {
	dir := writeBumpFixture(t, lockFixture)
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(lockfile.Filename+"\n"), 0644); err != nil {
		t.Fatalf("setup .gitignore: %v", err)
	}
	gitInitCommitted(t, manifest.Filename, ".gitignore")

	if _, err := captureStdout(t, func() error { return cmdBump([]string{"patch", "--git"}) }); err != nil {
		t.Fatalf("cmdBump --git with an ignored lock: %v", err)
	}
	if out := gitRun(t, "tag"); !strings.Contains(out, "v1.2.4") {
		t.Errorf("tag v1.2.4 not created; tags = %q", out)
	}
	if out := gitRun(t, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Errorf("working tree not clean after bump --git:\n%s", out)
	}
	// The lock is still updated on disk — it is just not in the commit.
	lf, err := lockfile.Load(dir)
	if err != nil {
		t.Fatalf("reload lock: %v", err)
	}
	if lf.RootManifest.Version != "1.2.4" {
		t.Errorf("lock root.version = %q, want %q", lf.RootManifest.Version, "1.2.4")
	}
	if committed := gitRun(t, "show", "--name-only", "--format=", "HEAD"); strings.Contains(committed, lockfile.Filename) {
		t.Errorf("an ignored lock was forced into the commit:\n%s", committed)
	}
}

// TestBumpUnreadableLockChangesNothing: a lock that cannot be parsed fails the
// bump as a pre-flight, before the manifest is touched. Rewriting the manifest
// first and only then discovering the broken lock left the command half-done —
// version bumped, error returned, and under --git no commit or tag.
func TestBumpUnreadableLockChangesNothing(t *testing.T) {
	dir := writeBumpFixture(t, "{ not json at all")

	out, err := captureStdout(t, func() error { return cmdBump([]string{"patch"}) })
	if err == nil {
		t.Fatal("cmdBump succeeded with an unparseable lockfile; want an error")
	}
	if strings.Contains(out, "→") {
		t.Errorf("bump reported a version change before failing: %q", out)
	}
	m, mErr := manifest.Load(dir)
	if mErr != nil {
		t.Fatalf("reload manifest: %v", mErr)
	}
	if m.Version != "1.2.3" {
		t.Errorf("manifest was rewritten despite the error: version = %q, want %q", m.Version, "1.2.3")
	}
}

// TestBumpLeavesUnknownSchemaLockUntouched: a lock written by a newer fglpkg is
// left exactly as found. Load keeps only the fields this build has tags for, so
// rewriting one would silently strip whatever that version added — worse than a
// stale root version, which the next install re-resolves anyway.
func TestBumpLeavesUnknownSchemaLockUntouched(t *testing.T) {
	future := `{
  "lockfileVersion": 99,
  "generatedAt": "2026-01-01T00:00:00Z",
  "generoVersion": "6.00.01",
  "root": {
    "name": "lock-sync",
    "version": "1.2.3",
    "declared": { "dependencies": {}, "devDependencies": {}, "optionalDependencies": {} }
  },
  "packages": [],
  "jars": [],
  "fieldFromTheFuture": { "keep": "me" }
}
`
	dir := writeBumpFixture(t, future)
	if _, err := captureStdout(t, func() error { return cmdBump([]string{"patch"}) }); err != nil {
		t.Fatalf("cmdBump: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, lockfile.Filename))
	if err != nil {
		t.Fatalf("reread lock: %v", err)
	}
	if string(got) != future {
		t.Errorf("an unrecognised-schema lock was rewritten:\n--- got ---\n%s", got)
	}
	// The manifest bump itself still goes through.
	m, err := manifest.Load(dir)
	if err != nil {
		t.Fatalf("reload manifest: %v", err)
	}
	if m.Version != "1.2.4" {
		t.Errorf("manifest version = %q, want %q", m.Version, "1.2.4")
	}
}

// TestBumpRequiresKind: `fglpkg bump` with no bump kind is a usage error.
func TestBumpRequiresKind(t *testing.T) {
	err := cmdBump(nil)
	if err == nil {
		t.Fatal("expected usage error for bare `bump`, got nil")
	}
	if !strings.Contains(err.Error(), "usage: fglpkg bump") {
		t.Errorf("error = %q, want a bump usage message", err.Error())
	}
}

// TestVersionRejectsBumpKind: `fglpkg version patch` (and any argument) is now
// rejected and points the user at `fglpkg bump` (GIS-288). The manifest must
// not be touched.
func TestVersionRejectsBumpKind(t *testing.T) {
	dir := t.TempDir()
	raw := `{
  "name": "rt-test",
  "version": "1.0.0",
  "dependencies": { "fgl": {} }
}`
	if err := os.WriteFile(filepath.Join(dir, manifest.Filename), []byte(raw), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	err := cmdVersion([]string{"patch"})
	if err == nil {
		t.Fatal("expected `version patch` to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "fglpkg bump") {
		t.Errorf("error = %q, want it to redirect to `fglpkg bump`", err.Error())
	}
	// The manifest must be untouched.
	reloaded, err := manifest.Load(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Version != "1.0.0" {
		t.Errorf("version changed to %q; `version <kind>` must not mutate the manifest", reloaded.Version)
	}
}

// TestVersionPrintsToolVersion: bare `fglpkg version` prints the tool banner.
func TestVersionPrintsToolVersion(t *testing.T) {
	out, err := captureStdout(t, func() error { return cmdVersion(nil) })
	if err != nil {
		t.Fatalf("cmdVersion: %v", err)
	}
	if !strings.Contains(out, "fglpkg version") {
		t.Errorf("output %q missing tool-version banner", out)
	}
}

// TestVersionFlags: every spelling isVersionFlag accepts prints the tool
// version via the top-level flag path in Execute (GIS-288; `-V` SUPASIA-3231).
// Driven off the predicate rather than a copied list, so a spelling added there
// cannot reach users untested.
func TestVersionFlags(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })
	for _, flag := range versionFlagSpellings {
		t.Run(flag, func(t *testing.T) {
			out, err := captureStdout(t, func() error {
				os.Args = []string{"fglpkg", flag}
				return Execute()
			})
			if err != nil {
				t.Fatalf("Execute(%s): %v", flag, err)
			}
			if !strings.Contains(out, "fglpkg version") {
				t.Errorf("%s output %q missing tool-version banner", flag, out)
			}
		})
	}
}

// versionFlagSpellings is every argument isVersionFlag accepts, asserted
// exhaustive by TestIsVersionFlagSpellings so the tables below stay honest.
var versionFlagSpellings = []string{"--version", "-v", "-V"}

// TestIsVersionFlagSpellings pins the accepted set — both that each spelling is
// accepted and that nothing adjacent is. `-version` and `--v` are the near
// misses a loosened predicate would start swallowing, and `version` is the
// subcommand, which must stay a subcommand so `fglpkg version --help` works.
func TestIsVersionFlagSpellings(t *testing.T) {
	for _, flag := range versionFlagSpellings {
		if !isVersionFlag(flag) {
			t.Errorf("isVersionFlag(%q) = false, want true", flag)
		}
	}
	for _, flag := range []string{"version", "-version", "--v", "--Version", "-VV", "", "-h"} {
		if isVersionFlag(flag) {
			t.Errorf("isVersionFlag(%q) = true, want false", flag)
		}
	}
}

// TestVersionFlagsSkipTheUpdateCheck guards the drift that produced
// SUPASIA-3231: updatecheck.Allowed already listed "-V" while Execute did not
// accept it, so the two halves disagreed about what a version request looks
// like. A `fglpkg -V` must not emit an "update available" notice — it is the
// one command a user runs to read a version, and advisory chatter on stderr is
// exactly what a script scraping it does not want.
func TestVersionFlagsSkipTheUpdateCheck(t *testing.T) {
	base := updatecheck.Env{
		Version:     "4.2.8",
		Enabled:     true,
		StdoutIsTTY: true,
		StderrIsTTY: true,
		Interval:    24 * time.Hour,
		Now:         time.Now(),
	}
	for _, flag := range append([]string{"version"}, versionFlagSpellings...) {
		e := base
		e.Command = flag
		if updatecheck.Allowed(e) {
			t.Errorf("updatecheck.Allowed(Command=%q) = true, want false", flag)
		}
	}
	// A control: an ordinary command on the same Env is still allowed, so the
	// assertions above are about the command and not a mis-built Env.
	e := base
	e.Command = "install"
	if !updatecheck.Allowed(e) {
		t.Fatal("updatecheck.Allowed(Command=\"install\") = false; the Env, not the command, is disabling the check")
	}
}
