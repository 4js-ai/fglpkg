package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/registry"
)

func TestLatestVersionSemverOrdering(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"single", []string{"1.0.0"}, "1.0.0"},
		{"ordered", []string{"1.0.0", "1.1.0", "1.2.0"}, "1.2.0"},
		{"unordered", []string{"2.0.0", "1.5.0", "1.10.0"}, "2.0.0"},
		{"prerelease_of_next_patch_beats_current", []string{"1.0.0", "1.0.1-alpha"}, "1.0.1-alpha"},
		{"release_beats_its_own_prerelease", []string{"2.0.0-rc.1", "2.0.0"}, "2.0.0"},
		{"empty", []string{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := latestVersion(tc.in)
			if got != tc.want {
				t.Errorf("latestVersion(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// stubRegistry serves the consumer registry's /registry/packages/<slug>
// endpoint with the shape cmdInfo needs.
//
// Note: the new protocol returns one document with all versions + their
// artifacts; per-version description/license/etc. live on the version
// summary in the real backend, but our PackageInfo synthesis pulls the
// description/author from the package-level fields, so we put the
// "current" version's description there for the latest-version tests.
func stubRegistry(t *testing.T) *httptest.Server {
	t.Helper()
	detail := map[string]any{
		"slug":        "demo",
		"name":        "demo",
		"description": "demo package",
		"owner":       map[string]any{"name": "alice"},
		"versions": []map[string]any{
			{
				"version":      "1.0.0",
				"published_at": "2025-12-01T10:00:00Z",
				"artifacts": []map[string]any{
					{"variant": "default", "sha256": "oldsum", "download_url": "https://example.com/demo-1.0.0.zip"},
				},
			},
			{"version": "1.1.0", "artifacts": []map[string]any{
				{"variant": "default", "sha256": "midsum", "download_url": "https://example.com/demo-1.1.0.zip"},
			}},
			{
				"version":      "1.2.0",
				"published_at": "2026-04-23T10:00:00Z",
				"artifacts": []map[string]any{
					{"variant": "default", "sha256": "abc123", "download_url": "https://example.com/demo-1.2.0.zip"},
				},
			},
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/registry/packages/", func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimPrefix(r.URL.Path, "/registry/packages/")
		if slug != "demo" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(detail)
	})
	return httptest.NewServer(mux)
}

// TestCmdInfoLatest verifies `fglpkg info demo` resolves to the newest
// version and fetches its details.
func TestCmdInfoLatest(t *testing.T) {
	ts := stubRegistry(t)
	t.Cleanup(ts.Close)
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	stdout, err := captureStdout(t, func() error {
		return cmdInfo([]string{"demo"})
	})
	if err != nil {
		t.Fatalf("cmdInfo: %v", err)
	}

	// Under the new consumer protocol, License / FGLDeps / JavaDeps are
	// not returned by /registry/packages/<slug>, so they're absent here.
	// Matches the v1 limitation flagged in the spec.
	wantSubstrings := []string{
		"demo@1.2.0 (latest)",
		"Description: demo package",
		"Author:      alice",
		"sha256:abc123",
		"https://example.com/demo-1.2.0.zip",
		"Versions (3): 1.0.0, 1.1.0, 1.2.0",
		"fglpkg install demo@1.2.0",
	}
	for _, sub := range wantSubstrings {
		if !strings.Contains(stdout, sub) {
			t.Errorf("output missing %q\n---\n%s", sub, stdout)
		}
	}
}

// TestCmdInfoSpecificVersion verifies `fglpkg info demo@1.0.0` does NOT
// label the result as (latest).
func TestCmdInfoSpecificVersion(t *testing.T) {
	ts := stubRegistry(t)
	t.Cleanup(ts.Close)
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	stdout, err := captureStdout(t, func() error {
		return cmdInfo([]string{"demo@1.0.0"})
	})
	if err != nil {
		t.Fatalf("cmdInfo: %v", err)
	}
	if !strings.Contains(stdout, "demo@1.0.0") {
		t.Errorf("expected header demo@1.0.0, got:\n%s", stdout)
	}
	if strings.Contains(stdout, "(latest)") {
		t.Errorf("explicit version should not be labelled (latest), got:\n%s", stdout)
	}
}

// TestCmdInfoJSON exercises --json output (must be valid JSON matching
// the PackageInfo shape).
func TestCmdInfoJSON(t *testing.T) {
	ts := stubRegistry(t)
	t.Cleanup(ts.Close)
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	stdout, err := captureStdout(t, func() error {
		return cmdInfo([]string{"demo", "--json"})
	})
	if err != nil {
		t.Fatalf("cmdInfo: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("--json output is not valid JSON: %v\n---\n%s", err, stdout)
	}
	if payload["name"] != "demo" || payload["version"] != "1.2.0" {
		t.Errorf("unexpected JSON payload: %+v", payload)
	}
}

func TestCmdInfoUsageErrors(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantMsg string
	}{
		{"no args", []string{}, "usage:"},
		{"unknown flag", []string{"--bogus"}, "unknown flag"},
		{"too many args", []string{"a", "b"}, "too many arguments"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := cmdInfo(tc.args)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantMsg)
			}
		})
	}
}

// captureStdout redirects os.Stdout for the duration of fn and returns
// what was written. Simple enough for single-goroutine command tests.
func TestPrivatePackageAccessHint(t *testing.T) {
	const tenantAToken = "tenant-a-secret"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+tenantAToken {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"slug":  "secret-pkg",
			"owner": map[string]any{"name": "tenant-a"},
			"versions": []map[string]any{
				{"version": "1.0.0", "artifacts": []map[string]any{
					{"variant": "default", "sha256": "abc123", "download_url": "http://example.com/pkg.zip"},
				}},
			},
		})
	}))
	t.Cleanup(ts.Close)
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	t.Run("tenant_a_succeeds", func(t *testing.T) {
		t.Setenv("FGLPKG_TOKEN", tenantAToken)
		if err := cmdInfo([]string{"secret-pkg"}); err != nil {
			t.Errorf("tenant A should have access, got: %v", err)
		}
	})

	t.Run("anonymous_gets_login_hint", func(t *testing.T) {
		t.Setenv("FGLPKG_TOKEN", "")
		err := cmdInfo([]string{"secret-pkg"})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "fglpkg login") {
			t.Errorf("expected login hint in error, got: %v", err)
		}
	})

	t.Run("wrong_tenant_no_hint", func(t *testing.T) {
		t.Setenv("FGLPKG_TOKEN", "tenant-b-token")
		err := cmdInfo([]string{"secret-pkg"})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if strings.Contains(err.Error(), "fglpkg login") {
			t.Errorf("logged-in user should not get login hint, got: %v", err)
		}
	})
}

func TestPublicPackageAccessibleToAnonymous(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"slug":  "public-pkg",
			"owner": map[string]any{"name": "someone"},
			"versions": []map[string]any{
				{"version": "1.0.0", "artifacts": []map[string]any{
					{"variant": "default", "sha256": "def456", "download_url": "http://example.com/pub.zip"},
				}},
			},
		})
	}))
	t.Cleanup(ts.Close)
	t.Setenv("FGLPKG_REGISTRY", ts.URL)
	t.Setenv("FGLPKG_TOKEN", "")

	if err := cmdInfo([]string{"public-pkg"}); err != nil {
		t.Errorf("public package should be accessible anonymously, got: %v", err)
	}
}

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	errCh := make(chan error, 1)
	go func() { errCh <- fn() }()

	// Close the writer once fn completes so the reader can finish.
	fnErr := <-errCh
	_ = w.Close()
	os.Stdout = orig

	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	for {
		n, readErr := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if readErr != nil {
			break
		}
	}
	_ = r.Close()
	return string(buf), fnErr
}

// TestPrintInfoLegacyFolderOffersNoInstallCommand pins the last line of a
// description read out of a folder named before fglpkg canonicalized names
// (GIS-610).
//
// `install` cannot address such a folder — that is the whole shape of the
// problem — and the warning that says so goes to stderr, so that --json stays a
// clean document. Printing the usual "Install: fglpkg install <name>@<version>"
// on stdout would contradict it there, and a reader who redirected stdout to a
// file would keep only the contradiction.
func TestPrintInfoLegacyFolderOffersNoInstallCommand(t *testing.T) {
	info := &registry.PackageInfo{Name: "DateTime", Version: "0.1.0"}
	versions := &registry.VersionList{Versions: []string{"0.1.0"}}

	legacy, err := captureStdout(t, func() error {
		printInfo(info, versions, true, true)
		return nil
	})
	if err != nil {
		t.Fatalf("printInfo: %v", err)
	}
	if strings.Contains(legacy, "fglpkg install") {
		t.Errorf("legacy folder output offers an install command that cannot work:\n%s", legacy)
	}
	for _, want := range []string{"Install: not possible", `"datetime"`, "4.2.10"} {
		if !strings.Contains(legacy, want) {
			t.Errorf("legacy folder output missing %q\n---\n%s", want, legacy)
		}
	}

	// The ordinary case is unchanged: same record, no legacy folder.
	normal, err := captureStdout(t, func() error {
		printInfo(info, versions, true, false)
		return nil
	})
	if err != nil {
		t.Fatalf("printInfo: %v", err)
	}
	if !strings.Contains(normal, "Install: fglpkg install DateTime@0.1.0") {
		t.Errorf("normal output lost its install hint:\n%s", normal)
	}
	if strings.Contains(normal, "not possible") {
		t.Errorf("normal output claims the package cannot be installed:\n%s", normal)
	}
}
