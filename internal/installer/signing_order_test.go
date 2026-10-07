package installer

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
	"github.com/4js-mikefolcher/fglpkg/internal/signing"
)

// GIS-580. Verification used to run *after* the download and extraction, at
// every one of the three install sites. Under FGLPKG_SIGNING=require that
// failed the first run but left the rejected artifact in .fglpkg/ and its entry
// in the lock, so the next run took the already-installed fast path, printed
// "Nothing to install" and exited 0. A CI retry step defeated `require`
// outright.
//
// These tests use an *unsigned* package as the rejection vehicle:
// verifySignature refuses a nil signature before it even loads the keys
// manifest, so the ordering can be exercised without a key fixture. The reason
// for the refusal is immaterial here — what is under test is that the refusal
// happens before anything is fetched or extracted, and that it is not skipped
// for a package that is already on disk.

// captureStdout runs fn with os.Stdout redirected and returns what it wrote.
// printSync writes to stdout, so this is the only way to assert on what the
// install phase does and does not announce.
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

// Nothing may be fetched before the signature is accepted. The download server
// counts its hits: a refused package must leave it untouched, and leave no
// directory behind in the store.
func TestRequireRejectsBeforeFetchingTheArtifact(t *testing.T) {
	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)

	inst, projectDir, lf := lockOnePkg(t, ts.URL+"/pkg.zip")
	inst.WithSigning(signing.EnforceRequire, "", "")

	err := inst.installFromLock(lf, manifest.New("app", "1.0.0", "", ""), Options{}, projectDir)
	if err == nil {
		t.Fatal("an unsigned package must be refused under require")
	}
	if !errors.Is(err, signing.ErrUnsigned) {
		t.Errorf("want the signing refusal, got: %v", err)
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Errorf("the artifact was fetched %d time(s); verification must precede the download", n)
	}
	if _, err := os.Stat(filepath.Join(inst.packagesDir, "ghostpkg")); !os.IsNotExist(err) {
		t.Error("a refused package must leave nothing in the store")
	}
}

// The reported bug. An aborted require run leaves the package directory
// behind; the next run must not treat that as evidence the package is
// trustworthy. Before the fix this returned nil and printed "already
// installed", which is how a retry turned exit 1 into exit 0.
func TestRequireReVerifiesAPackageAlreadyOnDisk(t *testing.T) {
	inst, projectDir, lf := lockOnePkg(t, "http://127.0.0.1:1/never.zip")
	inst.WithSigning(signing.EnforceRequire, "", "")

	// What the aborted first run left behind.
	if err := os.MkdirAll(filepath.Join(inst.packagesDir, "ghostpkg"), 0o755); err != nil {
		t.Fatal(err)
	}

	var err error
	out := captureStdout(t, func() {
		err = inst.installFromLock(lf, manifest.New("app", "1.0.0", "", ""), Options{}, projectDir)
	})
	if err == nil {
		t.Fatal("a package on disk whose signature is refused must still fail under require")
	}
	if !errors.Is(err, signing.ErrUnsigned) {
		t.Errorf("want the signing refusal, got: %v", err)
	}
	if strings.Contains(out, "already installed") {
		t.Errorf("a refused package must not be announced as installed:\n%s", out)
	}
}

// Under "warn" a package already on disk is left alone: the warning was emitted
// when it was installed, and repeating it on every replay is noise that changes
// no outcome. `fglpkg audit signatures` is the command that audits the store.
// This matters because warn is the default mode and, while GIS-576 is open,
// most of the registry fails verification.
func TestWarnDoesNotReWarnForAPackageAlreadyOnDisk(t *testing.T) {
	inst, projectDir, lf := lockOnePkg(t, "http://127.0.0.1:1/never.zip")
	inst.WithSigning(signing.EnforceWarn, "", "")
	if err := os.MkdirAll(filepath.Join(inst.packagesDir, "ghostpkg"), 0o755); err != nil {
		t.Fatal(err)
	}

	var err error
	out := captureStdout(t, func() {
		err = inst.installFromLock(lf, manifest.New("app", "1.0.0", "", ""), Options{}, projectDir)
	})
	if err != nil {
		t.Fatalf("warn must not fail the install: %v", err)
	}
	if strings.Contains(out, "signature check failed") {
		t.Errorf("warn must not re-warn for a package already on disk:\n%s", out)
	}
	if !strings.Contains(out, "already installed") {
		t.Errorf("expected the already-installed prelude, got:\n%s", out)
	}
}

// The resolve path's gate. An optional-scoped package is skipped when it cannot
// be fetched, but a signature failure is a trust failure rather than an
// availability one, so it aborts the install like any other.
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

// "off" disables verification entirely, so the gate must pass an unsigned plan
// straight through — otherwise the fix would break every project that has not
// opted into signing.
func TestPlanSignaturesPassWhenSigningIsOff(t *testing.T) {
	inst := New(t.TempDir(), "", "", "")
	inst.WithSigning(signing.EnforceOff, "", "")

	plan := &resolver.Plan{
		Packages: []resolver.ResolvedPackage{
			{Name: "unsigned", Version: semver.MustParse("1.0.0")},
		},
	}
	if err := inst.verifyPlanSignatures(plan); err != nil {
		t.Fatalf("signing off must not reject an unsigned package: %v", err)
	}
}
