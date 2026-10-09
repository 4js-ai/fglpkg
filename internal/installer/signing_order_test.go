package installer

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
	"github.com/4js-mikefolcher/fglpkg/internal/signing"
)

// GIS-580. Verification used to run *after* the download and extraction, at
// every install site. Under FGLPKG_SIGNING=require that failed the first run
// but left the rejected artifact in .fglpkg/ and its entry in the lock, so the
// next run took the already-installed fast path, printed "Nothing to install"
// and exited 0. A CI retry step defeated `require` outright.
//
// Verification now happens in three gates, each of which runs before anything
// is pruned, fetched or extracted:
//
//   - verifyPlanSignatures     — the resolve path, before the lock is written
//   - verifyLockInstallSet     — the replay path, before the prune
//   - verifyLockIsStillTrusted — the no-op replay that installs nothing
//
// installFromLock and installFromPlan only fetch and extract; their callers are
// the gates. These tests exercise the gates directly. That the gates are called
// in the right place is pinned end to end by functional case 120.
//
// The rejection vehicle is an *unsigned* package: verifySignature refuses a nil
// signature before it even loads the keys manifest, so the ordering can be
// exercised without a key fixture. The reason for the refusal is immaterial —
// what is under test is that it happens at all, and early.

// captureStdout runs fn with os.Stdout redirected and returns what it wrote.
// printSync writes to stdout, so this is the only way to assert on what a mode
// does and does not announce.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	fn()
	w.Close()
	os.Stdout = old
	return <-done
}

// lockOneWC is lockOnePkg's web-component twin: a lock whose only entry is an
// unsigned web component.
func lockOneWC(t *testing.T, downloadURL string) (*Installer, *lockfile.LockFile) {
	t.Helper()
	inst := New(t.TempDir(), "", "", "")
	projectDir := t.TempDir()
	lf := &lockfile.LockFile{
		Version:       1,
		GeneroVersion: "6.00",
		RootManifest:  lockfile.RootEntry{Name: "app", Version: "1.0.0"},
		Webcomponents: []lockfile.LockedWebcomponent{
			{Name: "ghostwc", Version: "3.0.0", DownloadURL: downloadURL},
		},
	}
	if err := lf.Save(projectDir); err != nil {
		t.Fatal(err)
	}
	return inst, lf
}

// countingServer records every request it receives, so a test can assert that
// the gate reached no further than the metadata.
func countingServer(t *testing.T, hits *int32) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)
	return ts
}

// ── BDL packages ─────────────────────────────────────────────────────────────

// A locked package that is not on disk is about to be fetched, so it is checked
// under the configured mode — and the gate itself touches the network not at
// all.
func TestRequireRejectsAnUnsignedLockedPackage(t *testing.T) {
	var hits int32
	ts := countingServer(t, &hits)

	inst, _, lf := lockOnePkg(t, ts.URL+"/pkg.zip")
	inst.WithSigning(signing.EnforceRequire, "", "")

	err := inst.verifyLockInstallSet(lf, Options{})
	if err == nil {
		t.Fatal("an unsigned package must be refused under require")
	}
	if !errors.Is(err, signing.ErrUnsigned) {
		t.Errorf("want the signing refusal, got: %v", err)
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Errorf("the gate made %d request(s); it must decide from the lock record alone", n)
	}
}

// The reported bug. An aborted require run leaves the package directory behind;
// the next run must not treat that as evidence the record was ever accepted.
func TestRequireReVerifiesAPackageAlreadyOnDisk(t *testing.T) {
	inst, _, lf := lockOnePkg(t, "http://127.0.0.1:1/never.zip")
	inst.WithSigning(signing.EnforceRequire, "", "")

	// What the aborted first run left behind — the extracted package, at the
	// version the lock names, so this really does take the on-disk path and not
	// the about-to-be-fetched one (GIS-586).
	extractPackage(t, inst, "ghostpkg", "2.1.0")

	err := inst.verifyLockInstallSet(lf, Options{})
	if err == nil {
		t.Fatal("a package on disk whose signature is refused must still fail under require")
	}
	if !errors.Is(err, signing.ErrUnsigned) {
		t.Errorf("want the signing refusal, got: %v", err)
	}
}

// Under "warn" a package already on disk is left alone: the warning was emitted
// when it was installed, and repeating it on every replay is noise that changes
// no outcome. This matters because warn is the default mode and, while GIS-576
// is open, most of the registry fails verification.
func TestWarnDoesNotReWarnForAPackageAlreadyOnDisk(t *testing.T) {
	inst, _, lf := lockOnePkg(t, "http://127.0.0.1:1/never.zip")
	inst.WithSigning(signing.EnforceWarn, "", "")
	extractPackage(t, inst, "ghostpkg", "2.1.0")

	var err error
	out := captureStdout(t, func() { err = inst.verifyLockInstallSet(lf, Options{}) })
	if err != nil {
		t.Fatalf("warn must not fail the install: %v", err)
	}
	if strings.Contains(out, "signature check failed") {
		t.Errorf("warn must not re-warn for a package already on disk:\n%s", out)
	}
}

// The gate splits the install set by what is on disk, and since GIS-586 "on
// disk" means the locked VERSION is extracted. A package the store holds at
// some other version is about to be re-fetched, so it must be checked under the
// configured mode like any other fetch — classifying it as already-installed is
// what let a pulled dependency bump sail past both the gate and the installer.
func TestGateTreatsAStaleVersionOnDiskAsAFetch(t *testing.T) {
	inst, _, lf := lockOnePkg(t, "http://127.0.0.1:1/never.zip")
	inst.WithSigning(signing.EnforceWarn, "", "")
	extractPackage(t, inst, "ghostpkg", "1.0.0") // the lock names 2.1.0

	var err error
	out := captureStdout(t, func() { err = inst.verifyLockInstallSet(lf, Options{}) })
	if err != nil {
		t.Fatalf("warn must not fail the install: %v", err)
	}
	if !strings.Contains(out, "signature check failed") {
		t.Errorf("a package on disk at another version is about to be fetched, so warn should warn:\n%s", out)
	}
}

// (There is deliberately no require-side twin of the test above. Under require
// both branches call verifySignature with the same arguments, so the two
// classifications are indistinguishable there and such a test could not fail.
// Warn is the only mode where the split is observable.)

// The fetch pass must agree with the gate: a stale version is work to do,
// not an "(already installed)" line. installFromLock cannot reach the network
// here (port 1), so the attempt is visible as the failure.
func TestInstallFromLockRefetchesAStaleVersion(t *testing.T) {
	inst, projectDir, lf := lockOnePkg(t, "http://127.0.0.1:1/never.zip")
	extractPackage(t, inst, "ghostpkg", "1.0.0")

	var err error
	out := captureStdout(t, func() {
		err = inst.installFromLock(lf, manifest.New("app", "1.0.0", "", ""), Options{}, projectDir)
	})
	if err == nil {
		t.Fatal("expected the re-fetch of the stale package to be attempted (and fail)")
	}
	if strings.Contains(out, "already installed") {
		t.Errorf("a package on disk at another version must not report as already installed:\n%s", out)
	}
}

// The complement, so the test above cannot pass merely because warn never warns:
// a package about to be FETCHED still warns under warn.
func TestWarnStillWarnsForAPackageAboutToBeFetched(t *testing.T) {
	inst, _, lf := lockOnePkg(t, "http://127.0.0.1:1/never.zip")
	inst.WithSigning(signing.EnforceWarn, "", "")

	var err error
	out := captureStdout(t, func() { err = inst.verifyLockInstallSet(lf, Options{}) })
	if err != nil {
		t.Fatalf("warn must not fail the install: %v", err)
	}
	if !strings.Contains(out, "signature check failed") {
		t.Errorf("warn should warn for a package it is about to fetch:\n%s", out)
	}
}

// A dev-scoped package is part of a normal replay but not a --production one,
// so it is exactly the entry that falls through a gate whose selection has
// drifted from the installer's. Both now read lockInstallSet, and this pins
// that: narrow the gate's set and the package installs unverified under
// require.
func TestRequireVerifiesADevScopedLockedPackage(t *testing.T) {
	inst := New(t.TempDir(), "", "", "")
	projectDir := t.TempDir()
	lf := &lockfile.LockFile{
		Version:       1,
		GeneroVersion: "6.00",
		RootManifest:  lockfile.RootEntry{Name: "app", Version: "1.0.0"},
		Packages: []lockfile.LockedPackage{
			{Name: "devpkg", Version: "1.0.0", DownloadURL: "http://127.0.0.1:1/x.zip",
				GeneroMajor: "6", Scope: "dev"},
		},
	}
	if err := lf.Save(projectDir); err != nil {
		t.Fatal(err)
	}
	inst.WithSigning(signing.EnforceRequire, "", "")

	err := inst.verifyLockInstallSet(lf, Options{})
	if err == nil {
		t.Fatal("a dev-scoped locked package must be verified on a normal replay")
	}
	if !errors.Is(err, signing.ErrUnsigned) {
		t.Errorf("want the signing refusal, got: %v", err)
	}

	// The complement: under --production it is not installed, so the gate has
	// nothing to refuse. Without this, the test above would also pass for a
	// gate that simply verified every entry in the file regardless of scope.
	if err := inst.verifyLockInstallSet(lf, Options{Production: true}); err != nil {
		t.Errorf("a dev package is not installed under --production, so it must not be gated: %v", err)
	}
}

// ── Web components ───────────────────────────────────────────────────────────
//
// Before this change web components were verified after Install; that call is
// gone, so the loops in verifyLockInstallSet and verifyOnDiskSignatures are now
// the only things verifying a web component installed from a lock. Deleting
// either used to leave the whole suite green.

// A locked web component is re-extracted on every replay, so it always counts
// as about to be fetched and is checked under the configured mode.
func TestRequireRejectsAnUnsignedWebcomponentOnTheReplayPath(t *testing.T) {
	var hits int32
	ts := countingServer(t, &hits)

	inst, lf := lockOneWC(t, ts.URL+"/wc.zip")
	inst.WithSigning(signing.EnforceRequire, "", "")

	err := inst.verifyLockInstallSet(lf, Options{})
	if err == nil {
		t.Fatal("an unsigned web component must be refused under require")
	}
	if !errors.Is(err, signing.ErrUnsigned) {
		t.Errorf("want the signing refusal, got: %v", err)
	}
	if !strings.Contains(err.Error(), "ghostwc") {
		t.Errorf("error should name the web component, got: %v", err)
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Errorf("the gate made %d request(s); it must decide from the lock record alone", n)
	}
}

// The no-op replay: a lock naming only a web component, everything present, so
// nothing is fetched. "Nothing to install" must not report success for a web
// component whose signature is refused.
func TestRequireReVerifiesAWebcomponentOnTheCleanPath(t *testing.T) {
	inst, lf := lockOneWC(t, "http://127.0.0.1:1/never.zip")
	inst.WithSigning(signing.EnforceRequire, "", "")

	err := inst.verifyLockIsStillTrusted(lf, Options{})
	if err == nil {
		t.Fatal("a clean lock naming an unsigned web component must be refused under require")
	}
	if !errors.Is(err, signing.ErrUnsigned) {
		t.Errorf("want the signing refusal, got: %v", err)
	}
	if !strings.Contains(err.Error(), "ghostwc") {
		t.Errorf("error should name the web component, got: %v", err)
	}
}

// ── The resolve path ─────────────────────────────────────────────────────────

// An optional-scoped package is skipped when it cannot be fetched, but a
// signature failure is a trust failure rather than an availability one, so it
// aborts the install like any other.
func TestPlanSignatureFailureAbortsEvenForAnOptionalPackage(t *testing.T) {
	inst := New(t.TempDir(), "", "", "")
	inst.WithSigning(signing.EnforceRequire, "", "")

	plan := &resolver.Plan{
		Packages: []resolver.ResolvedPackage{
			{Name: "opt", Version: semver.MustParse("1.0.0"), Scope: manifest.ScopeOptional},
		},
	}
	err := inst.verifyPlanSignatures(plan)
	if err == nil {
		t.Fatal("an unsigned optional package must be refused under require")
	}
	if !errors.Is(err, signing.ErrUnsigned) {
		t.Errorf("want the signing refusal, got: %v", err)
	}
}

// "off" disables verification entirely, so both gates must pass an unsigned set
// straight through — otherwise this would break every project that has not
// opted into signing.
func TestSigningOffPassesEveryGate(t *testing.T) {
	inst, _, lf := lockOnePkg(t, "http://127.0.0.1:1/never.zip")
	inst.WithSigning(signing.EnforceOff, "", "")

	plan := &resolver.Plan{
		Packages: []resolver.ResolvedPackage{
			{Name: "unsigned", Version: semver.MustParse("1.0.0")},
		},
	}
	if err := inst.verifyPlanSignatures(plan); err != nil {
		t.Errorf("signing off must not reject an unsigned plan: %v", err)
	}
	if err := inst.verifyLockInstallSet(lf, Options{}); err != nil {
		t.Errorf("signing off must not reject an unsigned lock: %v", err)
	}
	if err := inst.verifyLockIsStillTrusted(lf, Options{}); err != nil {
		t.Errorf("signing off must not reject a clean unsigned lock: %v", err)
	}
}
