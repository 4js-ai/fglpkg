package installer

import (
	"errors"
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

// TestDownloadErrorClassification pins the sentinels the message dispatch keys
// on: 404/410 -> ErrArtifactGone (permanent), 5xx and transport failures ->
// ErrDownloadTransient (retryable).
func TestDownloadErrorClassification(t *testing.T) {
	call := func(url string) error {
		return downloadAndVerify(url, "", "x", io.Discard, "", "", "", nil, false)
	}

	gone := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	defer gone.Close()
	if err := call(gone.URL); !errors.Is(err, ErrArtifactGone) {
		t.Errorf("404 should classify as ErrArtifactGone, got %v", err)
	}

	srvErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer srvErr.Close()
	if err := call(srvErr.URL); !errors.Is(err, ErrDownloadTransient) {
		t.Errorf("502 should classify as ErrDownloadTransient, got %v", err)
	}

	// A closed server yields a connection-refused transport error.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	if err := call(deadURL + "/x"); !errors.Is(err, ErrDownloadTransient) {
		t.Errorf("a transport failure should classify as ErrDownloadTransient, got %v", err)
	}
}
