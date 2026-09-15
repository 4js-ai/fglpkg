package installer

import (
	"os"
	"path/filepath"
	"testing"
)

// mustReadWC reads a file under the webcomponents dir, failing the test if it
// is absent.
func mustReadWC(t *testing.T, wcDir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(wcDir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("expected %s under webcomponents/: %v", rel, err)
	}
	return string(b)
}

// mustAbsentWC asserts a path under the webcomponents dir does not exist.
func mustAbsentWC(t *testing.T, wcDir, rel string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(wcDir, filepath.FromSlash(rel))); err == nil {
		t.Errorf("expected %s NOT to exist under webcomponents/", rel)
	}
}

// writeWC materialises a file under the webcomponents dir, standing in for an
// extraction that already happened.
func writeWC(t *testing.T, wcDir, rel, content string) {
	t.Helper()
	p := filepath.Join(wcDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// TestSyncWCSettingsMirrorsDescriptorAndIcon covers the core of GIS-536: a
// component that ships <NAME>/<NAME>.wcsettings (the authoring layout, which
// `pack` already stages) has it mirrored into the flat wcsettings/ directory
// Genero Studio's GSTWCDIR points at — alongside its optional same-named icon.
func TestSyncWCSettingsMirrorsDescriptorAndIcon(t *testing.T) {
	wcDir := t.TempDir()
	installed := []string{
		"MyWidget/MyWidget.html",
		"MyWidget/MyWidget.js",
		"MyWidget/MyWidget.wcsettings",
		"MyWidget/MyWidget.png",
	}
	for _, rel := range installed {
		writeWC(t, wcDir, rel, "content-of-"+rel)
	}

	written, err := syncWCSettings(wcDir, installed)
	if err != nil {
		t.Fatalf("syncWCSettings: %v", err)
	}

	if got := mustReadWC(t, wcDir, "wcsettings/MyWidget.wcsettings"); got != "content-of-MyWidget/MyWidget.wcsettings" {
		t.Errorf("descriptor content = %q, want the authored copy", got)
	}
	mustReadWC(t, wcDir, "wcsettings/MyWidget.png")

	// The authored copies stay put — the component dir is what a reinstall
	// rewrites, and the two must be independently removable.
	mustReadWC(t, wcDir, "MyWidget/MyWidget.wcsettings")

	// The returned paths are what the caller appends to the ownership record,
	// so `remove` prunes the mirrored copies with the package.
	wantWritten := map[string]bool{
		"wcsettings/MyWidget.wcsettings": true,
		"wcsettings/MyWidget.png":        true,
	}
	if len(written) != len(wantWritten) {
		t.Fatalf("written = %v, want the descriptor and its icon", written)
	}
	for _, w := range written {
		if !wantWritten[w] {
			t.Errorf("unexpected recorded path %q", w)
		}
	}
}

// TestSyncWCSettingsIgnoresComponentsWithoutDescriptor verifies a component
// that ships no .wcsettings contributes nothing — no wcsettings/ dir is created,
// so `env --gst` emits no GSTWCDIR for it.
func TestSyncWCSettingsIgnoresComponentsWithoutDescriptor(t *testing.T) {
	wcDir := t.TempDir()
	installed := []string{"MyWidget/MyWidget.html", "MyWidget/MyWidget.js"}
	for _, rel := range installed {
		writeWC(t, wcDir, rel, "x")
	}

	written, err := syncWCSettings(wcDir, installed)
	if err != nil {
		t.Fatalf("syncWCSettings: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("written = %v, want none", written)
	}
	mustAbsentWC(t, wcDir, "wcsettings")
}

// TestSyncWCSettingsOnlyMatchesTheDirectorysOwnName pins the naming rule: the
// descriptor counts only at <NAME>/<NAME>.wcsettings, the same
// name-matches-directory contract Genero applies to the .html entry point. A
// differently-named descriptor would flatten to an ambiguous COMPONENTTYPE.
func TestSyncWCSettingsOnlyMatchesTheDirectorysOwnName(t *testing.T) {
	wcDir := t.TempDir()
	installed := []string{
		"MyWidget/MyWidget.html",
		"MyWidget/SomethingElse.wcsettings",
	}
	for _, rel := range installed {
		writeWC(t, wcDir, rel, "x")
	}

	written, err := syncWCSettings(wcDir, installed)
	if err != nil {
		t.Fatalf("syncWCSettings: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("written = %v, want none for a mis-named descriptor", written)
	}
	mustAbsentWC(t, wcDir, "wcsettings/SomethingElse.wcsettings")
}

// TestSyncWCSettingsNeedsARealComponent guards the rule that a descriptor is
// only mirrored from a directory that is itself a loadable component. The
// pure-WC installer extracts a package's docs globs into the shared namespace
// too (the GIS-248 pollution), so without the entry-point check a stray
// docs/docs.wcsettings would offer "docs" to Studio as a COMPONENTTYPE that
// cannot be loaded.
func TestSyncWCSettingsNeedsARealComponent(t *testing.T) {
	wcDir := t.TempDir()
	installed := []string{
		"MyWidget/MyWidget.html",
		"MyWidget/MyWidget.wcsettings",
		"docs/docs.wcsettings", // ancillary tree, no docs/docs.html
		"docs/guide.md",
	}
	for _, rel := range installed {
		writeWC(t, wcDir, rel, "x")
	}

	written, err := syncWCSettings(wcDir, installed)
	if err != nil {
		t.Fatalf("syncWCSettings: %v", err)
	}
	if len(written) != 1 || written[0] != "wcsettings/MyWidget.wcsettings" {
		t.Fatalf("written = %v, want only the real component's descriptor", written)
	}
	mustAbsentWC(t, wcDir, "wcsettings/docs.wcsettings")
}

// TestSyncWCSettingsIconNeedsItsDescriptor verifies an icon alone is never
// mirrored. Component bundles routinely ship a <NAME>.png as a RUNTIME asset;
// only a package that also ships <NAME>.wcsettings has opted into Form Designer
// metadata, so only then is the image treated as an icon.
func TestSyncWCSettingsIconNeedsItsDescriptor(t *testing.T) {
	wcDir := t.TempDir()
	installed := []string{"MyWidget/MyWidget.html", "MyWidget/MyWidget.png"}
	for _, rel := range installed {
		writeWC(t, wcDir, rel, "x")
	}

	written, err := syncWCSettings(wcDir, installed)
	if err != nil {
		t.Fatalf("syncWCSettings: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("written = %v, want none — a runtime asset is not an icon", written)
	}
	mustAbsentWC(t, wcDir, "wcsettings/MyWidget.png")
}

// TestSyncWCSettingsClearsStaleDescriptorOnReinstall covers the upgrade case: a
// new version that DROPS its descriptor must not leave the old one stranded.
// Reinstall replaces the package's ownership list, so a leftover would never be
// pruned and Studio would keep offering a component that no longer describes
// itself.
func TestSyncWCSettingsClearsStaleDescriptorOnReinstall(t *testing.T) {
	wcDir := t.TempDir()

	// v1 ships a descriptor + icon.
	v1 := []string{"MyWidget/MyWidget.html", "MyWidget/MyWidget.wcsettings", "MyWidget/MyWidget.png"}
	for _, rel := range v1 {
		writeWC(t, wcDir, rel, "v1")
	}
	if _, err := syncWCSettings(wcDir, v1); err != nil {
		t.Fatalf("syncWCSettings v1: %v", err)
	}
	mustReadWC(t, wcDir, "wcsettings/MyWidget.wcsettings")
	mustReadWC(t, wcDir, "wcsettings/MyWidget.png")

	// v2 drops both (extraction cleared the component dir first).
	if err := os.RemoveAll(filepath.Join(wcDir, "MyWidget")); err != nil {
		t.Fatalf("clean: %v", err)
	}
	v2 := []string{"MyWidget/MyWidget.html"}
	for _, rel := range v2 {
		writeWC(t, wcDir, rel, "v2")
	}
	written, err := syncWCSettings(wcDir, v2)
	if err != nil {
		t.Fatalf("syncWCSettings v2: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("written = %v, want none", written)
	}
	mustAbsentWC(t, wcDir, "wcsettings/MyWidget.wcsettings")
	mustAbsentWC(t, wcDir, "wcsettings/MyWidget.png")
}

// TestSyncWCSettingsUpdatesDescriptorOnReinstall verifies the mirrored copy
// tracks the authored one across versions rather than sticking at v1.
func TestSyncWCSettingsUpdatesDescriptorOnReinstall(t *testing.T) {
	wcDir := t.TempDir()
	rels := []string{"MyWidget/MyWidget.html", "MyWidget/MyWidget.wcsettings"}

	for _, rel := range rels {
		writeWC(t, wcDir, rel, "v1")
	}
	if _, err := syncWCSettings(wcDir, rels); err != nil {
		t.Fatalf("syncWCSettings v1: %v", err)
	}

	for _, rel := range rels {
		writeWC(t, wcDir, rel, "v2")
	}
	if _, err := syncWCSettings(wcDir, rels); err != nil {
		t.Fatalf("syncWCSettings v2: %v", err)
	}
	if got := mustReadWC(t, wcDir, "wcsettings/MyWidget.wcsettings"); got != "v2" {
		t.Errorf("mirrored descriptor = %q, want the v2 copy", got)
	}
}

// TestSyncWCSettingsLeavesOtherPackagesAlone verifies the stale-clearing pass is
// scoped to the components THIS install touched: a second package's descriptor
// survives an unrelated reinstall.
func TestSyncWCSettingsLeavesOtherPackagesAlone(t *testing.T) {
	wcDir := t.TempDir()

	alpha := []string{"Alpha/Alpha.html", "Alpha/Alpha.wcsettings"}
	beta := []string{"Beta/Beta.html", "Beta/Beta.wcsettings"}
	for _, rel := range append(append([]string{}, alpha...), beta...) {
		writeWC(t, wcDir, rel, "x")
	}
	if _, err := syncWCSettings(wcDir, alpha); err != nil {
		t.Fatalf("syncWCSettings alpha: %v", err)
	}
	if _, err := syncWCSettings(wcDir, beta); err != nil {
		t.Fatalf("syncWCSettings beta: %v", err)
	}

	// Reinstalling alpha must not disturb beta's descriptor.
	if _, err := syncWCSettings(wcDir, alpha); err != nil {
		t.Fatalf("syncWCSettings alpha (reinstall): %v", err)
	}
	mustReadWC(t, wcDir, "wcsettings/Alpha.wcsettings")
	mustReadWC(t, wcDir, "wcsettings/Beta.wcsettings")
}

// TestSyncWCSettingsSkipsTheSettingsDirItself guards the one self-referential
// case: a package whose zip already carries a wcsettings/ tree (only reachable
// through a docs glob) must not have that folded into its own destination.
func TestSyncWCSettingsSkipsTheSettingsDirItself(t *testing.T) {
	wcDir := t.TempDir()
	installed := []string{"wcsettings/wcsettings.wcsettings"}
	for _, rel := range installed {
		writeWC(t, wcDir, rel, "x")
	}

	written, err := syncWCSettings(wcDir, installed)
	if err != nil {
		t.Fatalf("syncWCSettings: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("written = %v, want none", written)
	}
	// The pre-existing file is untouched — not deleted by the clearing pass.
	mustReadWC(t, wcDir, "wcsettings/wcsettings.wcsettings")
}

// TestExtractZipRoutedThenSyncWCSettings ties the two halves together on the
// mixed-package path: extraction routes the component (descriptor included)
// into webcomponents/, and the sync then mirrors the descriptor into
// wcsettings/ — the exact sequence installBDL performs.
func TestExtractZipRoutedThenSyncWCSettings(t *testing.T) {
	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "pkg.zip")
	writeTestZip(t, zipPath, map[string]string{
		"fglpkg.json":                `{"name":"chart-3d","version":"1.0.0","webcomponents":["3DChart"]}`,
		"ChartDemo.42m":              "BDL\n",
		"3DChart/3DChart.html":       "<html/>",
		"3DChart/3DChart.wcsettings": "<WebComponent/>",
	})

	destDir := filepath.Join(tmp, "packages", "chart-3d")
	wcDir := filepath.Join(tmp, "webcomponents")

	wcInstalled, err := extractZipRouted(zipPath, destDir, wcDir, []string{"3DChart"})
	if err != nil {
		t.Fatalf("extractZipRouted: %v", err)
	}
	written, err := syncWCSettings(wcDir, wcInstalled)
	if err != nil {
		t.Fatalf("syncWCSettings: %v", err)
	}
	if len(written) != 1 || written[0] != "wcsettings/3DChart.wcsettings" {
		t.Fatalf("written = %v, want [wcsettings/3DChart.wcsettings]", written)
	}
	mustReadWC(t, wcDir, "wcsettings/3DChart.wcsettings")
	// The descriptor must not have leaked into the package dir.
	if _, err := os.Stat(filepath.Join(destDir, "3DChart", "3DChart.wcsettings")); err == nil {
		t.Error("descriptor leaked into packages/ — routing broken")
	}
}
