package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOutsideConstraintStatus(t *testing.T) {
	cases := []struct {
		name        string
		current     string
		latest      string
		want        string
		description string
	}{
		// The GIS-594 report: an exact pin is satisfied only by itself, so a
		// patch release lands outside the constraint and used to be announced
		// as a major bump — warning of a breaking change that is a bug fix.
		{"patch", "1.0.0", "1.0.1", "patch available", "the reported bug"},
		{"minor", "1.0.0", "1.1.0", "minor available", ""},
		{"major", "1.0.0", "2.0.0", "major available", "still named correctly"},
		{"major across a minor", "1.2.0", "2.0.0", "major available", ""},
		{"minor across a patch", "1.0.3", "1.1.0", "minor available", ""},
		{"patch many steps", "1.0.0", "1.0.9", "patch available", ""},
		// Same release triple, differing only in the prerelease part: not a
		// major/minor/patch step, so it gets the neutral wording.
		{"prerelease only", "1.0.0", "1.0.0-rc.1", "newer available", ""},
		// Never guess from an unparseable version.
		{"unparseable current", "not-a-version", "1.0.1", "newer available", ""},
		{"unparseable latest", "1.0.0", "latest", "newer available", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := outsideConstraintStatus(tc.current, tc.latest); got != tc.want {
				t.Errorf("outsideConstraintStatus(%q, %q) = %q, want %q (%s)",
					tc.current, tc.latest, got, tc.want, tc.description)
			}
		})
	}
}

// patchOnlyStub serves "demo" with 1.0.0 and a single patch successor, so an
// exact pin has something newer outside it that is NOT a major bump.
func patchOnlyStub(t *testing.T) *httptest.Server {
	t.Helper()
	detail := map[string]any{
		"slug": "demo",
		"name": "demo",
		"versions": []map[string]any{
			{"version": "1.0.0", "artifacts": []map[string]any{
				{"variant": "default", "sha256": "a", "download_url": "https://example.com/demo-1.0.0.zip"},
			}},
			{"version": "1.0.1", "artifacts": []map[string]any{
				{"variant": "default", "sha256": "b", "download_url": "https://example.com/demo-1.0.1.zip"},
			}},
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/registry/packages/", func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimPrefix(r.URL.Path, "/registry/packages/") != "demo" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(detail)
	})
	return httptest.NewServer(mux)
}

// End to end through buildOutdatedRow: the exact pin is what triggers the bug,
// because it makes Wanted equal the installed version and pushes every newer
// release down the "outside the constraint" branch.
func TestBuildOutdatedRowExactPinPatchIsNotMajor(t *testing.T) {
	ts := patchOnlyStub(t)
	t.Cleanup(ts.Close)
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	row := buildOutdatedRow(nil, "demo", "1.0.0", "1.0.0", "", nil)
	if row.Status == "major available" {
		t.Fatalf("an exact pin with a patch successor reported %q — GIS-594 is not fixed", row.Status)
	}
	if row.Status != "patch available" {
		t.Errorf("status = %q, want \"patch available\"", row.Status)
	}
	if row.Latest != "1.0.1" {
		t.Errorf("latest = %q, want 1.0.1", row.Latest)
	}
}

// A caret range absorbs the patch, so there is nothing outside the constraint
// and the row stays "update available" — the in-range path is untouched.
func TestBuildOutdatedRowCaretRangeStillUpdateAvailable(t *testing.T) {
	ts := patchOnlyStub(t)
	t.Cleanup(ts.Close)
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	row := buildOutdatedRow(nil, "demo", "^1.0.0", "1.0.0", "", nil)
	if row.Status != "update available" {
		t.Errorf("status = %q, want \"update available\"", row.Status)
	}
}
