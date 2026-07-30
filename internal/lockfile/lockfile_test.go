package lockfile_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

// ─── Helpers ─────────────────────────────────────────────────────────────────

func makePlan() *resolver.Plan {
	return &resolver.Plan{
		GeneroVersion: genero.MustParse("4.01.12"),
		Packages: []resolver.ResolvedPackage{
			{
				Name:        "utils",
				Version:     semver.MustParse("1.2.3"),
				DownloadURL: "https://registry.fglpkg.dev/utils-1.2.3.zip",
				Checksum:    "aaaa1111",
				RequiredBy:  []string{"<root>"},
			},
			{
				Name:        "dbtools",
				Version:     semver.MustParse("2.1.0"),
				DownloadURL: "https://registry.fglpkg.dev/dbtools-2.1.0.zip",
				Checksum:    "bbbb2222",
				RequiredBy:  []string{"<root>", "utils"},
			},
		},
		JARs: []manifest.JavaDependency{
			{GroupID: "com.google.code.gson", ArtifactID: "gson", Version: "2.10.1"},
			{GroupID: "org.slf4j", ArtifactID: "slf4j-api", Version: "2.0.0"},
		},
	}
}

func makeRoot() *manifest.Manifest {
	return manifest.New("myapp", "1.0.0", "Test application", "Alice")
}

// ─── FromPlan ────────────────────────────────────────────────────────────────

func TestFromPlanPackageCount(t *testing.T) {
	lf := lockfile.FromPlan(makePlan(), makeRoot(), "")
	if len(lf.Packages) != 2 {
		t.Errorf("expected 2 packages, got %d", len(lf.Packages))
	}
	if len(lf.JARs) != 2 {
		t.Errorf("expected 2 JARs, got %d", len(lf.JARs))
	}
}

func TestFromPlanPackagesSortedByName(t *testing.T) {
	lf := lockfile.FromPlan(makePlan(), makeRoot(), "")
	for i := 1; i < len(lf.Packages); i++ {
		if lf.Packages[i].Name < lf.Packages[i-1].Name {
			t.Errorf("packages not sorted: %s before %s",
				lf.Packages[i-1].Name, lf.Packages[i].Name)
		}
	}
}

func TestFromPlanJARsSortedByKey(t *testing.T) {
	lf := lockfile.FromPlan(makePlan(), makeRoot(), "")
	for i := 1; i < len(lf.JARs); i++ {
		if lf.JARs[i].Key < lf.JARs[i-1].Key {
			t.Errorf("JARs not sorted: %s before %s",
				lf.JARs[i-1].Key, lf.JARs[i].Key)
		}
	}
}

func TestFromPlanPreservesChecksums(t *testing.T) {
	lf := lockfile.FromPlan(makePlan(), makeRoot(), "")
	byName := make(map[string]lockfile.LockedPackage)
	for _, p := range lf.Packages {
		byName[p.Name] = p
	}
	if byName["utils"].Checksum != "aaaa1111" {
		t.Errorf("utils checksum = %q, want %q", byName["utils"].Checksum, "aaaa1111")
	}
}

func TestFromPlanGeneroVersion(t *testing.T) {
	lf := lockfile.FromPlan(makePlan(), makeRoot(), "")
	if lf.GeneroVersion != "4.01.12" {
		t.Errorf("GeneroVersion = %q, want %q", lf.GeneroVersion, "4.01.12")
	}
}

func TestFromPlanRootManifest(t *testing.T) {
	root := makeRoot()
	lf := lockfile.FromPlan(makePlan(), root, "")
	if lf.RootManifest.Name != root.Name {
		t.Errorf("RootManifest.Name = %q, want %q", lf.RootManifest.Name, root.Name)
	}
	if lf.RootManifest.Version != root.Version {
		t.Errorf("RootManifest.Version = %q, want %q", lf.RootManifest.Version, root.Version)
	}
}

func TestFromPlanJARDownloadURL(t *testing.T) {
	lf := lockfile.FromPlan(makePlan(), makeRoot(), "")
	for _, jar := range lf.JARs {
		if jar.DownloadURL == "" {
			t.Errorf("JAR %s has empty DownloadURL", jar.Key)
		}
	}
	// gson URL should follow Maven Central pattern
	for _, jar := range lf.JARs {
		if jar.ArtifactID == "gson" {
			want := "https://repo1.maven.org/maven2/com/google/code/gson/gson/2.10.1/gson-2.10.1.jar"
			if jar.DownloadURL != want {
				t.Errorf("gson DownloadURL = %q, want %q", jar.DownloadURL, want)
			}
		}
	}
}

// TestFromPlanJARMirrorURL verifies that a non-empty mirror base is baked into
// each locked JAR's DownloadURL (GIS-365), so the pinned URL replays through the
// same Maven mirror rather than public Maven Central.
func TestFromPlanJARMirrorURL(t *testing.T) {
	const base = "https://artifactory.acme.example/artifactory/libs-release"
	lf := lockfile.FromPlan(makePlan(), makeRoot(), base)
	for _, jar := range lf.JARs {
		if jar.ArtifactID == "gson" {
			want := base + "/com/google/code/gson/gson/2.10.1/gson-2.10.1.jar"
			if jar.DownloadURL != want {
				t.Errorf("gson DownloadURL = %q, want mirror URL %q", jar.DownloadURL, want)
			}
		}
		if !strings.HasPrefix(jar.DownloadURL, base+"/") {
			t.Errorf("JAR %s DownloadURL = %q, want prefix %q", jar.Key, jar.DownloadURL, base)
		}
	}
}

// ─── Save / Load round-trip ──────────────────────────────────────────────────

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	original := lockfile.FromPlan(makePlan(), makeRoot(), "")

	if err := original.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// File must exist.
	if _, err := os.Stat(filepath.Join(dir, lockfile.Filename)); err != nil {
		t.Fatalf("lock file not written: %v", err)
	}

	loaded, err := lockfile.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.GeneroVersion != original.GeneroVersion {
		t.Errorf("GeneroVersion: got %q, want %q", loaded.GeneroVersion, original.GeneroVersion)
	}
	if len(loaded.Packages) != len(original.Packages) {
		t.Errorf("Packages len: got %d, want %d", len(loaded.Packages), len(original.Packages))
	}
	if len(loaded.JARs) != len(original.JARs) {
		t.Errorf("JARs len: got %d, want %d", len(loaded.JARs), len(original.JARs))
	}
}

func TestSaveProducesValidJSON(t *testing.T) {
	dir := t.TempDir()
	lf := lockfile.FromPlan(makePlan(), makeRoot(), "")
	if err := lf.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, lockfile.Filename))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Errorf("saved file is not valid JSON: %v", err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := lockfile.Load(t.TempDir())
	if err == nil {
		t.Error("expected error loading missing lock file, got nil")
	}
}

func TestExists(t *testing.T) {
	dir := t.TempDir()
	if lockfile.Exists(dir) {
		t.Error("Exists() should be false before Save()")
	}
	lf := lockfile.FromPlan(makePlan(), makeRoot(), "")
	lf.Save(dir) //nolint:errcheck
	if !lockfile.Exists(dir) {
		t.Error("Exists() should be true after Save()")
	}
}

// ─── Validate ────────────────────────────────────────────────────────────────

func TestValidateClean(t *testing.T) {
	dir := t.TempDir()
	root := makeRoot()
	lf := lockfile.FromPlan(makePlan(), root, "")
	lf.Save(dir) //nolint:errcheck

	result := lf.Validate(root, "4.01.12", "", "", "")
	if !result.IsClean() {
		t.Errorf("expected clean result, got: schema=%v genero=%v manifest=%v missing=%v",
			result.SchemaError, result.GeneroMismatch,
			result.ManifestMismatch, result.MissingPackages)
	}
	if result.NeedsResolve() {
		t.Error("clean lock should not need re-resolve")
	}
}

func TestValidateGeneroMismatch(t *testing.T) {
	root := makeRoot()
	lf := lockfile.FromPlan(makePlan(), root, "") // locked at 4.01.12

	result := lf.Validate(root, "3.20.05", "", "", "") // now running 3.20
	if result.GeneroMismatch == nil {
		t.Fatal("expected GeneroMismatch, got nil")
	}
	if result.GeneroMismatch.Locked != "4.01.12" {
		t.Errorf("Locked = %q, want %q", result.GeneroMismatch.Locked, "4.01.12")
	}
	if result.GeneroMismatch.Current != "3.20.05" {
		t.Errorf("Current = %q, want %q", result.GeneroMismatch.Current, "3.20.05")
	}
	// Genero mismatch alone doesn't require re-resolution.
	if result.NeedsResolve() {
		t.Error("genero mismatch alone should not force re-resolve")
	}
}

func TestValidateManifestNameMismatch(t *testing.T) {
	root := makeRoot()
	lf := lockfile.FromPlan(makePlan(), root, "")

	changedRoot := makeRoot()
	changedRoot.Name = "otherapp"

	result := lf.Validate(changedRoot, "4.01.12", "", "", "")
	if result.ManifestMismatch == nil {
		t.Fatal("expected ManifestMismatch, got nil")
	}
	if !result.NeedsResolve() {
		t.Error("manifest mismatch should require re-resolve")
	}
}

func TestValidateManifestVersionMismatch(t *testing.T) {
	root := makeRoot()
	lf := lockfile.FromPlan(makePlan(), root, "")

	changedRoot := makeRoot()
	changedRoot.Version = "2.0.0"

	result := lf.Validate(changedRoot, "4.01.12", "", "", "")
	if result.ManifestMismatch == nil {
		t.Fatal("expected ManifestMismatch, got nil")
	}
	if !result.NeedsResolve() {
		t.Error("manifest version mismatch should require re-resolve")
	}
}

func TestValidateMissingPackages(t *testing.T) {
	dir := t.TempDir()
	root := makeRoot()
	lf := lockfile.FromPlan(makePlan(), root, "")

	// packagesDir exists but is empty — all packages are "missing"
	result := lf.Validate(root, "4.01.12", dir, "", "")
	if len(result.MissingPackages) != 2 {
		t.Errorf("expected 2 missing packages, got %d: %v",
			len(result.MissingPackages), result.MissingPackages)
	}
}

func TestValidatePresentPackages(t *testing.T) {
	dir := t.TempDir()
	root := makeRoot()
	lf := lockfile.FromPlan(makePlan(), root, "")

	// Create stub package directories to simulate a successful install.
	for _, pkg := range lf.Packages {
		os.MkdirAll(filepath.Join(dir, pkg.Name), 0755) //nolint:errcheck
	}

	result := lf.Validate(root, "4.01.12", dir, "", "")
	if len(result.MissingPackages) != 0 {
		t.Errorf("expected no missing packages, got: %v", result.MissingPackages)
	}
}

func TestValidateMissingJARs(t *testing.T) {
	dir := t.TempDir()
	root := makeRoot()
	lf := lockfile.FromPlan(makePlan(), root, "")

	// jarsDir exists but is empty — every locked JAR is "missing", so a
	// deleted JAR is re-fetched by plain `install`, not only `update`.
	result := lf.Validate(root, "4.01.12", "", "", dir)
	if len(result.MissingJARs) != 2 {
		t.Errorf("expected 2 missing JARs, got %d: %v",
			len(result.MissingJARs), result.MissingJARs)
	}
	if result.IsClean() {
		t.Error("lock with missing JARs should not be clean")
	}
	if result.NeedsResolve() {
		t.Error("missing JARs should be installable from the lock, not force re-resolve")
	}
}

func TestValidatePresentJARs(t *testing.T) {
	dir := t.TempDir()
	root := makeRoot()
	lf := lockfile.FromPlan(makePlan(), root, "")

	// Create stub JAR files under the names the installer writes.
	for _, jar := range lf.JARs {
		dep := manifest.JavaDependency{
			GroupID: jar.GroupID, ArtifactID: jar.ArtifactID, Version: jar.Version,
		}
		path := filepath.Join(dir, dep.JarFileName())
		if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	result := lf.Validate(root, "4.01.12", "", "", dir)
	if len(result.MissingJARs) != 0 {
		t.Errorf("expected no missing JARs, got: %v", result.MissingJARs)
	}
	if !result.IsClean() {
		t.Error("expected clean result when all JARs are present")
	}
}

func TestValidateSchemaVersionMismatch(t *testing.T) {
	root := makeRoot()
	lf := lockfile.FromPlan(makePlan(), root, "")
	lf.Version = 99 // future/unknown schema

	result := lf.Validate(root, "4.01.12", "", "", "")
	if result.SchemaError == nil {
		t.Fatal("expected SchemaError, got nil")
	}
	if !result.NeedsResolve() {
		t.Error("schema error should require re-resolve")
	}
}

// ─── ToInstallList ────────────────────────────────────────────────────────────

func TestToInstallList(t *testing.T) {
	lf := lockfile.FromPlan(makePlan(), makeRoot(), "")
	pkgs, jars, wcs := lf.ToInstallList()

	if len(pkgs) != 2 {
		t.Errorf("expected 2 packages, got %d", len(pkgs))
	}
	if len(jars) != 2 {
		t.Errorf("expected 2 JARs, got %d", len(jars))
	}
	if len(wcs) != 0 {
		t.Errorf("expected 0 webcomponents in BDL-only plan, got %d", len(wcs))
	}
}

// ─── Scopes ──────────────────────────────────────────────────────────────────

// Scope is written through from Plan to LockedPackage/LockedJAR, and prod
// entries omit the field so existing lock files remain backwards-compatible.
func TestFromPlanCarriesScope(t *testing.T) {
	plan := &resolver.Plan{
		GeneroVersion: genero.MustParse("4.01.12"),
		Packages: []resolver.ResolvedPackage{
			{Name: "a", Version: semver.MustParse("1.0.0"), Scope: manifest.ScopeProd},
			{Name: "b", Version: semver.MustParse("1.0.0"), Scope: manifest.ScopeDev},
			{Name: "c", Version: semver.MustParse("1.0.0"), Scope: manifest.ScopeOptional},
		},
		JARs: []manifest.JavaDependency{
			{GroupID: "g", ArtifactID: "prod-jar", Version: "1"},
			{GroupID: "g", ArtifactID: "dev-jar", Version: "1"},
		},
		JARScopes: map[string]manifest.Scope{
			"g:prod-jar": manifest.ScopeProd,
			"g:dev-jar":  manifest.ScopeDev,
		},
	}
	lf := lockfile.FromPlan(plan, makeRoot(), "")
	want := map[string]string{"a": "", "b": "dev", "c": "optional"}
	for _, p := range lf.Packages {
		if got := want[p.Name]; p.Scope != got {
			t.Errorf("package %s scope: got %q want %q", p.Name, p.Scope, got)
		}
	}
	for _, j := range lf.JARs {
		switch j.ArtifactID {
		case "prod-jar":
			if j.Scope != "" {
				t.Errorf("prod-jar: expected empty scope, got %q", j.Scope)
			}
		case "dev-jar":
			if j.Scope != "dev" {
				t.Errorf("dev-jar: expected dev, got %q", j.Scope)
			}
		}
	}
}

// FilterForProduction drops dev-scoped entries and keeps prod + optional.
func TestFilterForProduction(t *testing.T) {
	plan := &resolver.Plan{
		GeneroVersion: genero.MustParse("4.01.12"),
		Packages: []resolver.ResolvedPackage{
			{Name: "a", Version: semver.MustParse("1.0.0"), Scope: manifest.ScopeProd},
			{Name: "b", Version: semver.MustParse("1.0.0"), Scope: manifest.ScopeDev},
			{Name: "c", Version: semver.MustParse("1.0.0"), Scope: manifest.ScopeOptional},
		},
		JARs: []manifest.JavaDependency{
			{GroupID: "g", ArtifactID: "j1", Version: "1"},
			{GroupID: "g", ArtifactID: "j2", Version: "1"},
		},
		JARScopes: map[string]manifest.Scope{
			"g:j1": manifest.ScopeProd,
			"g:j2": manifest.ScopeDev,
		},
	}
	lf := lockfile.FromPlan(plan, makeRoot(), "")
	pkgs, jars, _ := lf.FilterForProduction()

	if len(pkgs) != 2 {
		t.Errorf("expected 2 packages (prod+optional), got %d", len(pkgs))
	}
	for _, p := range pkgs {
		if p.Scope == "dev" {
			t.Errorf("dev package %q leaked into production filter", p.Name)
		}
	}
	if len(jars) != 1 {
		t.Errorf("expected 1 JAR (prod only), got %d", len(jars))
	}
}

// ─── AddManifestJARs ───────────────────────────────────────────────────────────

func TestAddManifestJARs(t *testing.T) {
	lf := &lockfile.LockFile{
		JARs: []lockfile.LockedJAR{
			{Key: "g:existing", GroupID: "g", ArtifactID: "existing", Version: "1.0.0"},
		},
	}

	deps := []manifest.JavaDependency{
		// New coordinate — should be appended, marked "manifest".
		{GroupID: "org.apache.poi", ArtifactID: "poi", Version: "5.3.0"},
		// Already present by key — must NOT be added or downgraded to manifest.
		{GroupID: "g", ArtifactID: "existing", Version: "1.0.0"},
	}

	if !lf.AddManifestJARs(deps, "") {
		t.Fatal("AddManifestJARs should report an addition")
	}
	if len(lf.JARs) != 2 {
		t.Fatalf("expected 2 JARs after add, got %d", len(lf.JARs))
	}
	// Sorted by key: g:existing before org.apache.poi:poi.
	if lf.JARs[0].Key != "g:existing" || lf.JARs[0].Source != "" {
		t.Errorf("existing JAR must be untouched, got %+v", lf.JARs[0])
	}
	if lf.JARs[1].Key != "org.apache.poi:poi" || lf.JARs[1].Source != "manifest" {
		t.Errorf("new JAR must be manifest-sourced, got %+v", lf.JARs[1])
	}

	// Idempotent: re-adding the same coordinate is a no-op.
	if lf.AddManifestJARs(deps, "") {
		t.Error("second AddManifestJARs call should report no additions")
	}
}

// TestSaveDoesNotHTMLEscape: fglpkg.lock must keep the literal angle brackets
// in a requiredBy entry like "<root>". Under Go's default HTML escaping the
// brackets would be written as numeric Unicode escapes, so the literal
// "<root>" would NOT appear — the positive check alone distinguishes it (GIS-280).
func TestSaveDoesNotHTMLEscape(t *testing.T) {
	dir := t.TempDir()
	lf := &lockfile.LockFile{
		Version: 1,
		Packages: []lockfile.LockedPackage{{
			Name:        "dep",
			Version:     "1.0.0",
			DownloadURL: "https://example.test/dep-1.0.0.zip",
			RequiredBy:  []string{"<root>"},
		}},
	}
	if err := lf.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, lockfile.Filename))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := string(data); !strings.Contains(got, "<root>") {
		t.Errorf("fglpkg.lock is HTML-escaping requiredBy; want literal <root>:\n%s", got)
	}
}

// TestMaterializationFieldsRoundTrip verifies the GIS-346 ownership fields
// (generoPackages + materialized) survive Save/Load intact.
func TestMaterializationFieldsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	lf := &lockfile.LockFile{
		Version: 1,
		Packages: []lockfile.LockedPackage{{
			Name:           "dbconnection",
			Version:        "1.0.0",
			DownloadURL:    "https://example.test/dbconnection-1.0.0.zip",
			RequiredBy:     []string{"<root>"},
			GeneroPackages: []string{"com.fourjs.db"},
			Materialized:   []string{"com/fourjs/db/DbConnection.42m", "com/fourjs/db/Query.42m"},
		}},
	}
	if err := lf.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// The keys must appear in the serialized form.
	data, err := os.ReadFile(filepath.Join(dir, lockfile.Filename))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, key := range []string{"generoPackages", "materialized", "com.fourjs.db", "com/fourjs/db/DbConnection.42m"} {
		if !strings.Contains(string(data), key) {
			t.Errorf("saved lock missing %q:\n%s", key, data)
		}
	}

	loaded, err := lockfile.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p := loaded.Packages[0]
	if want := []string{"com.fourjs.db"}; !equalStrings(p.GeneroPackages, want) {
		t.Errorf("GeneroPackages = %v, want %v", p.GeneroPackages, want)
	}
	if want := []string{"com/fourjs/db/DbConnection.42m", "com/fourjs/db/Query.42m"}; !equalStrings(p.Materialized, want) {
		t.Errorf("Materialized = %v, want %v", p.Materialized, want)
	}
}

// TestMaterializationFieldsOmittedWhenEmpty confirms the new fields are
// additive/omitempty: a package that owns no namespaces serializes without
// them, so existing locks and non-PACKAGE packages produce identical output.
func TestMaterializationFieldsOmittedWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	lf := &lockfile.LockFile{
		Version: 1,
		Packages: []lockfile.LockedPackage{{
			Name:        "flatpkg",
			Version:     "1.0.0",
			DownloadURL: "https://example.test/flatpkg-1.0.0.zip",
			RequiredBy:  []string{"<root>"},
		}},
	}
	if err := lf.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, lockfile.Filename))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, key := range []string{"generoPackages", "materialized"} {
		if strings.Contains(string(data), key) {
			t.Errorf("empty %q should be omitted from lock:\n%s", key, data)
		}
	}
}

// TestPreExistingLockParsesWithoutMaterializationFields confirms a lock
// written before GIS-346 (no generoPackages / materialized keys) loads
// cleanly with the fields left nil — no lockfileVersion bump required.
func TestPreExistingLockParsesWithoutMaterializationFields(t *testing.T) {
	dir := t.TempDir()
	legacy := `{
  "lockfileVersion": 1,
  "generatedAt": "2026-01-01T00:00:00Z",
  "generoVersion": "4.01.12",
  "root": { "name": "myapp", "version": "1.0.0" },
  "packages": [
    { "name": "dep", "version": "1.0.0", "downloadUrl": "https://example.test/dep.zip", "requiredBy": ["<root>"] }
  ],
  "jars": []
}`
	if err := os.WriteFile(filepath.Join(dir, lockfile.Filename), []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy lock: %v", err)
	}
	loaded, err := lockfile.Load(dir)
	if err != nil {
		t.Fatalf("Load legacy lock: %v", err)
	}
	p := loaded.Packages[0]
	if p.GeneroPackages != nil || p.Materialized != nil {
		t.Errorf("legacy lock should parse with nil materialization fields, got %v / %v",
			p.GeneroPackages, p.Materialized)
	}
}

// ─── Declared dependency-set staleness ───────────────────────────────────────
//
// Before the `root.declared` snapshot existed, Validate compared only the
// project's name and version, so hand-editing fglpkg.json left the lock looking
// up to date and `fglpkg install` reported "Nothing to install" — the manifest
// and the installed store silently diverged. These lock in the detection.

// rootWithDeps builds a root manifest declaring the given prod FGL deps.
func rootWithDeps(deps map[string]string) *manifest.Manifest {
	m := makeRoot()
	m.Dependencies.FGL = deps
	return m
}

func TestValidateDependencyRemovedIsStale(t *testing.T) {
	locked := rootWithDeps(map[string]string{"utils": "^1.0.0", "poiapi": "^2.0.0"})
	lf := lockfile.FromPlan(makePlan(), locked, "")

	// The user deletes poiapi from fglpkg.json by hand.
	edited := rootWithDeps(map[string]string{"utils": "^1.0.0"})

	result := lf.Validate(edited, "4.01.12", "", "")
	if result.ManifestMismatch == nil {
		t.Fatal("removing a dependency from the manifest should make the lock stale")
	}
	if !result.NeedsResolve() {
		t.Error("a removed dependency must force a re-resolve")
	}
	if got := result.ManifestMismatch.Summary(); !strings.Contains(got, "poiapi") ||
		!strings.Contains(got, "removed") {
		t.Errorf("Summary() = %q, want it to name poiapi as removed", got)
	}
}

func TestValidateDependencyAddedIsStale(t *testing.T) {
	lf := lockfile.FromPlan(makePlan(), rootWithDeps(map[string]string{"utils": "^1.0.0"}), "")
	edited := rootWithDeps(map[string]string{"utils": "^1.0.0", "newdep": "^3.0.0"})

	result := lf.Validate(edited, "4.01.12", "", "")
	if result.ManifestMismatch == nil {
		t.Fatal("adding a dependency by hand should make the lock stale")
	}
	if got := result.ManifestMismatch.Summary(); !strings.Contains(got, "newdep") ||
		!strings.Contains(got, "added") {
		t.Errorf("Summary() = %q, want it to name newdep as added", got)
	}
}

func TestValidateConstraintChangeIsStale(t *testing.T) {
	lf := lockfile.FromPlan(makePlan(), rootWithDeps(map[string]string{"utils": "^1.0.0"}), "")
	edited := rootWithDeps(map[string]string{"utils": "^2.0.0"})

	result := lf.Validate(edited, "4.01.12", "", "")
	if result.ManifestMismatch == nil {
		t.Fatal("widening a version constraint should make the lock stale")
	}
	if got := result.ManifestMismatch.Summary(); !strings.Contains(got, "^2.0.0") {
		t.Errorf("Summary() = %q, want it to mention the new constraint", got)
	}
}

func TestValidateUnchangedDependenciesStayClean(t *testing.T) {
	root := rootWithDeps(map[string]string{"utils": "^1.0.0", "poiapi": "^2.0.0"})
	lf := lockfile.FromPlan(makePlan(), root, "")

	// Same declarations, independently constructed — must not report staleness
	// just because the maps are different objects.
	result := lf.Validate(rootWithDeps(map[string]string{"poiapi": "^2.0.0", "utils": "^1.0.0"}),
		"4.01.12", "", "")
	if result.ManifestMismatch != nil {
		t.Errorf("unchanged declarations should stay clean, got: %v", result.ManifestMismatch)
	}
}

func TestValidateScopeMoveIsStale(t *testing.T) {
	root := makeRoot()
	root.Dependencies.FGL = map[string]string{"tester": "^1.0.0"}
	lf := lockfile.FromPlan(makePlan(), root, "")

	// Same package, same constraint — moved from prod to dev.
	moved := makeRoot()
	moved.DevDependencies.FGL = map[string]string{"tester": "^1.0.0"}

	if result := lf.Validate(moved, "4.01.12", "", ""); result.ManifestMismatch == nil {
		t.Error("moving a dependency between scopes should make the lock stale")
	}
}

func TestValidateJavaDependencyChangeIsStale(t *testing.T) {
	root := makeRoot()
	root.Dependencies.Java = []manifest.JavaDependency{
		{GroupID: "com.google.code.gson", ArtifactID: "gson", Version: "2.10.1"},
	}
	lf := lockfile.FromPlan(makePlan(), root, "")

	bumped := makeRoot()
	bumped.Dependencies.Java = []manifest.JavaDependency{
		{GroupID: "com.google.code.gson", ArtifactID: "gson", Version: "2.11.0"},
	}

	if result := lf.Validate(bumped, "4.01.12", "", ""); result.ManifestMismatch == nil {
		t.Error("a changed Java coordinate should make the lock stale")
	}
}

func TestValidateRegistryPinChangeIsStale(t *testing.T) {
	root := rootWithDeps(map[string]string{"utils": "^1.0.0"})
	root.Dependencies.FGLPins = map[string]string{"utils": "gi"}
	lf := lockfile.FromPlan(makePlan(), root, "")

	repinned := rootWithDeps(map[string]string{"utils": "^1.0.0"})
	repinned.Dependencies.FGLPins = map[string]string{"utils": "acme"}

	if result := lf.Validate(repinned, "4.01.12", "", ""); result.ManifestMismatch == nil {
		t.Error("re-pinning a dependency to another repository should make the lock stale")
	}
}

// TestValidateNoDependenciesStaysClean guards the empty-vs-absent distinction:
// a project that genuinely declares nothing must not read as a legacy lock.
func TestValidateNoDependenciesStaysClean(t *testing.T) {
	root := makeRoot()
	lf := lockfile.FromPlan(makePlan(), root, "")

	if result := lf.Validate(makeRoot(), "4.01.12", "", ""); result.ManifestMismatch != nil {
		t.Errorf("a dependency-less project should be clean, got: %v", result.ManifestMismatch)
	}
}

// TestLegacyLockWithoutDeclaredIsStale covers locks written before the snapshot
// existed: they carry no record of what was declared, so no comparison is
// possible and the only safe answer is one re-resolve to record it. A round-trip
// through Save/Load must then be clean.
func TestLegacyLockWithoutDeclaredIsStale(t *testing.T) {
	dir := t.TempDir()
	legacy := `{
  "lockfileVersion": 1,
  "generatedAt": "2026-01-01T00:00:00Z",
  "generoVersion": "4.01.12",
  "root": { "name": "myapp", "version": "1.0.0" },
  "packages": [
    { "name": "dep", "version": "1.0.0", "downloadUrl": "https://example.test/dep.zip", "requiredBy": ["<root>"] }
  ],
  "jars": []
}`
	if err := os.WriteFile(filepath.Join(dir, lockfile.Filename), []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy lock: %v", err)
	}
	loaded, err := lockfile.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.RootManifest.Declared != nil {
		t.Fatal("legacy lock should parse with a nil Declared snapshot")
	}
	result := loaded.Validate(makeRoot(), "4.01.12", "", "")
	if result.ManifestMismatch == nil || !result.NeedsResolve() {
		t.Error("a lock with no declared snapshot must be treated as stale")
	}

	// After a re-resolve writes the snapshot, validation settles.
	rewritten := lockfile.FromPlan(makePlan(), makeRoot(), "")
	if rewritten.RootManifest.Declared == nil {
		t.Fatal("FromPlan must record the declared snapshot")
	}
	if rewritten.Validate(makeRoot(), "4.01.12", "", "").ManifestMismatch != nil {
		t.Error("a freshly written lock should validate clean against its own manifest")
	}
}

// TestDeclaredSurvivesSaveLoad guards the snapshot against a JSON round trip —
// if it did not persist, every install would see a "legacy" lock and re-resolve.
func TestDeclaredSurvivesSaveLoad(t *testing.T) {
	dir := t.TempDir()
	root := rootWithDeps(map[string]string{"utils": "^1.0.0"})
	if err := lockfile.FromPlan(makePlan(), root, "").Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := lockfile.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.RootManifest.Declared == nil {
		t.Fatal("declared snapshot did not survive Save/Load")
	}
	if got := loaded.RootManifest.Declared.Prod.FGL["utils"]; got != "^1.0.0" {
		t.Errorf("round-tripped constraint = %q, want %q", got, "^1.0.0")
	}
	if loaded.Validate(root, "4.01.12", "", "").ManifestMismatch != nil {
		t.Error("round-tripped lock should validate clean against the same manifest")
	}
}

// equalStrings reports whether two string slices are element-wise equal.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
