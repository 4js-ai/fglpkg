package installer

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// lockOnePkg saves a single-package lock pointing at downloadURL and returns an
// installer (fresh home, so nothing is "already installed") plus the projectDir
// holding the lock, ready for installFromLock.
func lockOnePkg(t *testing.T, downloadURL string) (*Installer, string, *lockfile.LockFile) {
	t.Helper()
	inst := New(t.TempDir(), "", "", "")
	projectDir := t.TempDir()
	lf := &lockfile.LockFile{
		Version:       1,
		GeneroVersion: "6.00",
		RootManifest:  lockfile.RootEntry{Name: "app", Version: "1.0.0"},
		Packages: []lockfile.LockedPackage{
			{Name: "ghostpkg", Version: "2.1.0", DownloadURL: downloadURL, GeneroMajor: "6"},
		},
	}
	if err := lf.Save(projectDir); err != nil {
		t.Fatal(err)
	}
	return inst, projectDir, lf
}

// TestInstallFromLockGoneArtifact: a locked package the registry answers 404/410
// for fails with an actionable, remedy-bearing message — not a raw HTTP error
// (GIS-283, AC #1).
func TestInstallFromLockGoneArtifact(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusGone} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			}))
			t.Cleanup(ts.Close)

			inst, projectDir, lf := lockOnePkg(t, ts.URL+"/pkg.zip")
			err := inst.installFromLock(lf, manifest.New("app", "1.0.0", "", ""), Options{}, projectDir)
			if err == nil {
				t.Fatal("expected an error installing a gone package")
			}
			msg := err.Error()
			for _, want := range []string{"ghostpkg@2.1.0", "no longer available", "fglpkg update", "fglpkg remove ghostpkg"} {
				if !strings.Contains(msg, want) {
					t.Errorf("message should contain %q, got:\n%s", want, msg)
				}
			}
			if strings.Contains(msg, "downloading") { // the raw HTTP wording must not leak
				t.Errorf("gone message should be actionable, not the raw HTTP error:\n%s", msg)
			}
			// A 404 is not proof of deletion — a private registry answers it for
			// an artifact the caller may not see, so the message must offer the
			// access/login cause and not assert deletion as fact. Without this a
			// merely unauthenticated user is steered to `fglpkg remove`, dropping
			// a dependency they still need.
			for _, want := range []string{"fglpkg login", "do not have access"} {
				if !strings.Contains(msg, want) {
					t.Errorf("gone message should offer the access cause (%q), got:\n%s", want, msg)
				}
			}
			if strings.Contains(msg, "(deleted or withdrawn)") {
				t.Errorf("deletion must not be stated as fact, got:\n%s", msg)
			}
		})
	}
}

// TestInstallFromLockTransient: a 5xx is retryable, so the message must say so
// and must NOT claim the package is permanently gone (GIS-283, AC #2).
func TestInstallFromLockTransient(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(ts.Close)

	inst, projectDir, lf := lockOnePkg(t, ts.URL+"/pkg.zip")
	err := inst.installFromLock(lf, manifest.New("app", "1.0.0", "", ""), Options{}, projectDir)
	if err == nil {
		t.Fatal("expected an error on a 5xx download")
	}
	msg := err.Error()
	if !strings.Contains(msg, "ghostpkg@2.1.0") || !strings.Contains(msg, "temporary") || !strings.Contains(msg, "retry") {
		t.Errorf("transient message should name the package and suggest a retry, got:\n%s", msg)
	}
	if strings.Contains(msg, "no longer available") {
		t.Errorf("a 5xx must not be reported as permanently gone:\n%s", msg)
	}
}

// TestInstallFromLockRateLimited: 408/429 are retryable, so they must surface as
// transient (retry), not a dead-end (GIS-283 review #2).
func TestInstallFromLockRateLimited(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			}))
			t.Cleanup(ts.Close)
			inst, projectDir, lf := lockOnePkg(t, ts.URL+"/pkg.zip")
			err := inst.installFromLock(lf, manifest.New("app", "1.0.0", "", ""), Options{}, projectDir)
			if err == nil {
				t.Fatalf("expected an error on HTTP %d", status)
			}
			msg := err.Error()
			if !strings.Contains(msg, "temporary") || !strings.Contains(msg, "retry") {
				t.Errorf("HTTP %d should be reported as transient/retryable, got:\n%s", status, msg)
			}
			if strings.Contains(msg, "no longer available") {
				t.Errorf("HTTP %d must not be reported as permanently gone:\n%s", status, msg)
			}
		})
	}
}

// TestDownloadErrorClassification pins the sentinels the message dispatch keys
// on: 404/410 -> ErrArtifactGone (permanent); 5xx / 408 / 429 and transport
// failures -> ErrDownloadTransient (retryable).
func TestDownloadErrorClassification(t *testing.T) {
	call := func(url string) error {
		return downloadAndVerify(url, "", "x", io.Discard, "", "", "", nil, false)
	}

	gone := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	defer gone.Close()
	if err := call(gone.URL); !errors.Is(err, ErrArtifactGone) {
		t.Errorf("404 should classify as ErrArtifactGone, got %v", err)
	}

	for _, status := range []int{http.StatusBadGateway, http.StatusRequestTimeout, http.StatusTooManyRequests} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
		if err := call(s.URL); !errors.Is(err, ErrDownloadTransient) {
			t.Errorf("HTTP %d should classify as ErrDownloadTransient, got %v", status, err)
		}
		s.Close()
	}

	// A closed server yields a connection-refused transport error.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	if err := call(deadURL + "/x"); !errors.Is(err, ErrDownloadTransient) {
		t.Errorf("a transport failure should classify as ErrDownloadTransient, got %v", err)
	}
}

// TestLockInstallErrorPreservesChain: translating a download failure into an
// actionable message must not sever the error chain. ErrArtifactGone and
// ErrDownloadTransient are exported for classification, so a caller (a CI wrapper,
// a retry loop) has to be able to errors.Is the translated error — while the
// message stays free of the raw HTTP wording GIS-283 removed.
func TestLockInstallErrorPreservesChain(t *testing.T) {
	cases := []struct {
		name     string
		cause    error
		sentinel error
	}{
		{"gone", fmt.Errorf("HTTP 404 downloading x from http://r/x.zip: %w", ErrArtifactGone), ErrArtifactGone},
		{"transient", fmt.Errorf("HTTP 503 downloading x from http://r/x.zip: %w", ErrDownloadTransient), ErrDownloadTransient},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := lockInstallError("", "ghostpkg", "2.1.0", tc.cause)
			if !errors.Is(got, tc.sentinel) {
				t.Errorf("translated error should still unwrap to the sentinel, got: %v", got)
			}
		})
	}
	// The gone message must remain clean despite now wrapping the cause: %w would
	// have spliced "HTTP 404 downloading ..." back into the text.
	msg := lockInstallError("", "ghostpkg", "2.1.0", cases[0].cause).Error()
	if strings.Contains(msg, "downloading") {
		t.Errorf("wrapping must not reintroduce the raw HTTP wording:\n%s", msg)
	}
}

// TestLockInstallErrorKeepsWebcomponentQualifier: the fallback branch names the
// artifact kind, so an unclassified webcomponent failure still reads as one —
// matching the signature-verification error reported alongside it.
func TestLockInstallErrorKeepsWebcomponentQualifier(t *testing.T) {
	plain := errors.New("boom")
	if got := lockInstallError("webcomponent", "chart", "1.0.0", plain).Error(); !strings.Contains(got, "webcomponent chart@1.0.0") {
		t.Errorf("webcomponent failure should name the kind, got: %s", got)
	}
	if got := lockInstallError("", "mypkg", "1.0.0", plain).Error(); !strings.Contains(got, "install mypkg@1.0.0") {
		t.Errorf("a package needs no qualifier, got: %s", got)
	}
}
