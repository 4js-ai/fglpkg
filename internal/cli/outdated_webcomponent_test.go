package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// outdatedDeprecatedStub serves "demo" with 1.0.0 marked deprecated.
func outdatedDeprecatedStub(t *testing.T) *httptest.Server {
	t.Helper()
	detail := map[string]any{
		"slug": "demo",
		"name": "demo",
		"versions": []map[string]any{
			{"version": "1.0.0", "deprecated": true, "moved_to": "demo-ng",
				"artifacts": []map[string]any{{"variant": "webcomponent", "sha256": "a", "download_url": "https://example.com/x.zip"}}},
			{"version": "2.0.0",
				"artifacts": []map[string]any{{"variant": "webcomponent", "sha256": "b", "download_url": "https://example.com/y.zip"}}},
		},
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(detail)
	}))
}

// GIS-582. `outdated` built its "what is installed" map from the lock file's
// `packages` array alone. Web components are locked in a separate
// `webcomponents` array, so every installed widget was reported as
// missing / not installed, and the command exited 1 — in a project where
// `install` had just succeeded. The help documents `outdated` as a CI gate, so
// in any project depending on a widget the gate always failed, with a wrong
// reason.

// writeProject writes a manifest and a lock file into a fresh dir and chdirs
// into it for the duration of the test.
func writeProject(t *testing.T, manifestJSON, lockJSON string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range map[string]string{
		"fglpkg.json":      manifestJSON,
		"fglpkg-lock.json": lockJSON,
	} {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	orig, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	return dir
}

// lockWithWebcomponent locks "demo" at version v in the webcomponents array,
// the shape `install <widget>` writes.
func lockWithWebcomponent(v string) string {
	return `{
  "lockfileVersion": 1,
  "generatedAt": "2026-10-07T00:00:00Z",
  "generoVersion": "6.00.01",
  "root": { "name": "proj", "version": "0.1.0" },
  "packages": [],
  "webcomponents": [
    {
      "name": "demo",
      "version": "` + v + `",
      "downloadUrl": "https://example.com/demo.zip",
      "requiredBy": ["<root>"]
    }
  ],
  "jars": []
}`
}

// The headline: a widget at the newest runnable version is "ok", so the gate
// passes and the command exits 0. Before the fix this reported "missing /
// not installed" and exited 1 no matter what was installed.
func TestOutdatedSeesAWebcomponentAsInstalled(t *testing.T) {
	ts := outdatedStub(t)
	t.Cleanup(ts.Close)
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	writeProject(t,
		`{"name":"proj","version":"0.1.0","dependencies":{"fgl":{"demo":"^2.0.0"}}}`,
		lockWithWebcomponent("2.0.0"))

	stdout, cmdErr := captureStdout(t, func() error {
		return cmdOutdated([]string{})
	})
	if cmdErr != nil {
		t.Fatalf("an up-to-date widget must pass the gate, got: %v\n%s", cmdErr, stdout)
	}
	for _, bad := range []string{"missing", "not installed"} {
		if strings.Contains(stdout, bad) {
			t.Errorf("table reports an installed widget as %q:\n%s", bad, stdout)
		}
	}
	if !strings.Contains(stdout, "2.0.0") {
		t.Errorf("table should show the installed version:\n%s", stdout)
	}
}

// A widget that genuinely is behind reports its real current version and the
// upgrade, rather than "missing". The exit is non-zero here because there is a
// real upgrade — not because the lock was misread.
func TestOutdatedReportsAWebcomponentUpgradeFromItsRealVersion(t *testing.T) {
	ts := outdatedStub(t)
	t.Cleanup(ts.Close)
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	writeProject(t,
		`{"name":"proj","version":"0.1.0","dependencies":{"fgl":{"demo":"^1.0.0"}}}`,
		lockWithWebcomponent("1.0.0"))

	stdout, cmdErr := captureStdout(t, func() error {
		return cmdOutdated([]string{})
	})
	if cmdErr == nil {
		t.Fatal("expected a non-zero exit: demo 1.0.0 has an upgrade to 1.2.0")
	}
	if strings.Contains(stdout, "missing") || strings.Contains(stdout, "not installed") {
		t.Errorf("an installed widget must not be reported as missing:\n%s", stdout)
	}
	for _, want := range []string{"demo", "1.0.0", "1.2.0", "update available"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("table missing %q:\n%s", want, stdout)
		}
	}
}

// Both lock arrays feed the same three maps, and each carries a Registry, so a
// widget resolved from a secondary repository is re-checked against that
// repository rather than being re-routed to the consume default or GI. The
// ticket assumed LockedWebcomponent had no registry field; it does, so this
// needed no lock-format change — only that the array be read.
func TestLockedStateReadsBothArraysWithTheirSources(t *testing.T) {
	dir := t.TempDir()
	lock := `{
  "lockfileVersion": 1,
  "generatedAt": "2026-10-07T00:00:00Z",
  "generoVersion": "6.00.01",
  "root": { "name": "proj", "version": "0.1.0" },
  "packages": [
    { "name": "lib", "version": "1.0.0", "downloadUrl": "https://example.com/lib.zip",
      "registry": "myrepo", "requiredBy": ["<root>"] }
  ],
  "webcomponents": [
    { "name": "widget", "version": "2.0.0", "downloadUrl": "https://example.com/w.zip",
      "registry": "myrepo", "requiredBy": ["<root>"] }
  ],
  "jars": []
}`
	if err := os.WriteFile(filepath.Join(dir, "fglpkg-lock.json"), []byte(lock), 0644); err != nil {
		t.Fatal(err)
	}

	current, sources, locked := lockedState(dir)
	for _, name := range []string{"lib", "widget"} {
		if !locked[name] {
			t.Errorf("%s should be known to the lock", name)
		}
		if sources[name] != "myrepo" {
			t.Errorf("%s source = %q, want myrepo — the locked source must win for widgets too", name, sources[name])
		}
	}
	if current["lib"] != "1.0.0" {
		t.Errorf("lib current = %q, want 1.0.0", current["lib"])
	}
	if current["widget"] != "2.0.0" {
		t.Errorf("widget current = %q, want 2.0.0", current["widget"])
	}
}

// No lock file at all is not an error: everything is simply not installed.
func TestLockedStateWithoutALockFile(t *testing.T) {
	current, sources, locked := lockedState(t.TempDir())
	if len(current) != 0 || len(sources) != 0 || len(locked) != 0 {
		t.Errorf("no lock file should yield empty maps, got %v %v %v", current, sources, locked)
	}
}

// A deprecated widget version is flagged. Deprecation needs an installed
// current version to compare against, so while the webcomponents array was
// unread no widget could ever be flagged — the advisory was silently dead for
// exactly the packages it was added for.
func TestOutdatedFlagsADeprecatedWebcomponentVersion(t *testing.T) {
	ts := outdatedDeprecatedStub(t)
	t.Cleanup(ts.Close)
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	writeProject(t,
		`{"name":"proj","version":"0.1.0","dependencies":{"fgl":{"demo":"1.0.0"}}}`,
		lockWithWebcomponent("1.0.0"))

	stdout, _ := captureStdout(t, func() error {
		return cmdOutdated([]string{})
	})
	if !strings.Contains(strings.ToLower(stdout), "deprecated") {
		t.Errorf("an installed deprecated widget version should be flagged:\n%s", stdout)
	}
}
