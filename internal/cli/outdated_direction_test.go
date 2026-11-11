package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// versionsStub serves "demo" with exactly the versions given, so a test can
// put the registry BEHIND what is installed.
func versionsStub(t *testing.T, vers ...string) *httptest.Server {
	t.Helper()
	vs := make([]map[string]any, 0, len(vers))
	for _, v := range vers {
		vs = append(vs, map[string]any{"version": v, "artifacts": []map[string]any{
			{"variant": "default", "sha256": "a", "download_url": "https://example.com/demo-" + v + ".zip"},
		}})
	}
	detail := map[string]any{"slug": "demo", "name": "demo", "versions": vs}
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

// The newest version a registry lists can be OLDER than the installed one: a
// version stops being listed, the Genero filter excludes the newest builds, or
// a prerelease is installed ahead of the stable line. `outdated` then announced
// a DOWNGRADE as an upgrade, and --exit-code failed a build over a package with
// nothing newer to install.
//
// Each case is a different branch of buildOutdatedRow's switch, which is why a
// guard inside outsideConstraintStatus alone would not have covered them: the
// range case never reaches that function.
func TestBuildOutdatedRowNeverReportsADowngrade(t *testing.T) {
	cases := []struct {
		name       string
		constraint string
		installed  string
		published  []string
		branch     string
	}{
		{
			name: "exact pin, registry rolled back", constraint: "1.2.0", installed: "1.2.0",
			published: []string{"1.0.0", "1.1.0"},
			branch:    "the outside-the-constraint branch (said \"minor available\")",
		},
		{
			name: "range, registry rolled back", constraint: "^1.0.0", installed: "1.2.0",
			published: []string{"1.0.0", "1.1.0"},
			branch:    "the Wanted branch (said \"update available\")",
		},
		{
			name: "prerelease installed ahead of the stable line", constraint: "2.0.0-rc.1", installed: "2.0.0-rc.1",
			published: []string{"1.0.0"},
			branch:    "the outside-the-constraint branch (said \"major available\")",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := versionsStub(t, tc.published...)
			t.Cleanup(ts.Close)
			t.Setenv("FGLPKG_REGISTRY", ts.URL)

			row := buildOutdatedRow(nil, "demo", tc.constraint, tc.installed, "", nil)
			if row.Status != "ok" {
				t.Errorf("status = %q, want \"ok\": nothing newer than %s is published (%v) — %s",
					row.Status, tc.installed, tc.published, tc.branch)
			}
		})
	}
}

// The guard must not silence a real update: the same shapes with the registry
// genuinely ahead still report one.
func TestBuildOutdatedRowStillReportsRealUpdates(t *testing.T) {
	cases := []struct {
		name       string
		constraint string
		installed  string
		published  []string
		want       string
	}{
		{"exact pin, patch ahead", "1.0.0", "1.0.0", []string{"1.0.0", "1.0.1"}, "patch available"},
		{"exact pin, minor ahead", "1.0.0", "1.0.0", []string{"1.0.0", "1.1.0"}, "minor available"},
		{"exact pin, major ahead", "1.0.0", "1.0.0", []string{"1.0.0", "2.0.0"}, "major available"},
		{"range, newer in range", "^1.0.0", "1.0.0", []string{"1.0.0", "1.1.0"}, "update available"},
		{"prerelease stabilised", "1.0.0-rc.1", "1.0.0-rc.1", []string{"1.0.0-rc.1", "1.0.0"}, "newer available"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := versionsStub(t, tc.published...)
			t.Cleanup(ts.Close)
			t.Setenv("FGLPKG_REGISTRY", ts.URL)

			row := buildOutdatedRow(nil, "demo", tc.constraint, tc.installed, "", nil)
			if row.Status != tc.want {
				t.Errorf("status = %q, want %q — the direction guard swallowed a real update", row.Status, tc.want)
			}
		})
	}
}

// A version fglpkg cannot parse must not SILENCE an update. The guard exists to
// suppress a verdict, so an unreadable version has to fall on the noisy side:
// staying noisy is recoverable, going quiet hides the update GIS-594 is about.
func TestIsNewerThanIsLenientOnUnparseableVersions(t *testing.T) {
	cases := [][2]string{
		{"not-a-version", "1.0.0"},
		{"1.0.0", "not-a-version"},
		{"", "1.0.0"},
	}
	for _, c := range cases {
		if !isNewerThan(c[0], c[1]) {
			t.Errorf("isNewerThan(%q, %q) = false, want true: an unreadable version must not silence a verdict", c[0], c[1])
		}
	}
}
