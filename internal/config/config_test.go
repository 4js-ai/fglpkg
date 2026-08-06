package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolve_BuiltinOnly(t *testing.T) {
	regs, err := Resolve(BuiltinGI(""), nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(regs) != 1 {
		t.Fatalf("want 1 registry, got %d", len(regs))
	}
	gi := regs[0]
	if gi.Name != GIName || gi.Type != TypeGenero || gi.Priority != 1 || gi.Auth != AuthBearer {
		t.Fatalf("unexpected builtin gi: %+v", gi)
	}
	if gi.URL != defaultGIURL {
		t.Fatalf("gi URL = %q, want %q", gi.URL, defaultGIURL)
	}
}

func TestResolve_FGLPKGRegistryRetargetsGI(t *testing.T) {
	regs, err := Resolve(BuiltinGI("https://mirror.example/reg/"), nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if regs[0].URL != "https://mirror.example/reg" { // trailing slash trimmed
		t.Fatalf("gi URL = %q", regs[0].URL)
	}
}

func TestResolve_ProjectAddsArtifactoryAndSorts(t *testing.T) {
	project := []Registry{{
		Name: "acme", Type: TypeArtifactory, URL: "https://art.acme.example/artifactory",
		RepoKey: "fgl-generic", Priority: 2, Auth: AuthBearer, Packages: []string{"acme-*"},
	}}
	regs, err := Resolve(BuiltinGI(""), nil, project)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(regs) != 2 {
		t.Fatalf("want 2, got %d", len(regs))
	}
	// Sorted by priority: gi(1) then acme(2).
	if regs[0].Name != "gi" || regs[1].Name != "acme" {
		t.Fatalf("order = %q,%q", regs[0].Name, regs[1].Name)
	}
}

func TestResolve_ProjectWinsPerName(t *testing.T) {
	global := []Registry{{Name: "acme", Type: TypeArtifactory, URL: "https://old", RepoKey: "k", Priority: 2}}
	project := []Registry{{Name: "acme", Type: TypeArtifactory, URL: "https://new", RepoKey: "k", Priority: 2}}
	regs, err := Resolve(BuiltinGI(""), global, project)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	acme, ok := Find(regs, "acme")
	if !ok || acme.URL != "https://new" {
		t.Fatalf("project should win: %+v (ok=%v)", acme, ok)
	}
}

// A same-named project registry REPLACES the global one wholesale, it does not
// field-merge: fields the project omits (auth, packages) revert to their own
// defaults/empty rather than inheriting the global entry's values. Guards the
// documented "repeat url/auth/repoKey when you override" contract against a
// future accidental field-merge.
func TestResolve_ProjectReplacesSameNameWholesale(t *testing.T) {
	global := []Registry{{
		Name: "acme", Type: TypeArtifactory, URL: "https://global/artifactory", RepoKey: "G",
		Priority: 2, Auth: AuthBasic, Packages: []string{"acme-*"},
	}}
	// The project restates only url/repoKey/priority — no auth, no packages.
	project := []Registry{{Name: "acme", Type: TypeArtifactory, URL: "https://local/artifactory", RepoKey: "L", Priority: 2}}
	regs, err := Resolve(BuiltinGI(""), global, project)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	acme, ok := Find(regs, "acme")
	if !ok {
		t.Fatal("acme missing from merged set")
	}
	if acme.URL != "https://local/artifactory" || acme.RepoKey != "L" {
		t.Errorf("project url/repoKey should win: %+v", acme)
	}
	if acme.Auth == AuthBasic {
		t.Errorf("global auth leaked into the project entry (field-merge, not wholesale replace): %+v", acme)
	}
	if acme.Auth != AuthBearer { // omitted auth normalises to the bearer default
		t.Errorf("omitted project auth should default to bearer, got %q", acme.Auth)
	}
	if len(acme.Packages) != 0 {
		t.Errorf("global packages allow-list leaked into the project entry: %+v", acme.Packages)
	}
}

func TestResolve_RetargetGIByName(t *testing.T) {
	project := []Registry{{Name: "gi", Type: TypeGenero, URL: "https://internal-gi.example"}}
	regs, err := Resolve(BuiltinGI(""), nil, project)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	gi, _ := Find(regs, "gi")
	if gi.URL != "https://internal-gi.example" {
		t.Fatalf("gi URL = %q", gi.URL)
	}
	if gi.Priority != 1 { // inherited default even though project omitted it
		t.Fatalf("gi priority = %d, want 1", gi.Priority)
	}
}

func TestResolve_DuplicatePriorityError(t *testing.T) {
	project := []Registry{{Name: "acme", Type: TypeArtifactory, URL: "https://a", RepoKey: "k", Priority: 1}}
	_, err := Resolve(BuiltinGI(""), nil, project) // collides with gi's priority 1
	if err == nil {
		t.Fatal("expected duplicate-priority error")
	}
}

func TestResolve_MissingRepoKeyError(t *testing.T) {
	project := []Registry{{Name: "acme", Type: TypeArtifactory, URL: "https://a", Priority: 2}}
	_, err := Resolve(BuiltinGI(""), nil, project)
	if err == nil {
		t.Fatal("expected missing-repoKey error")
	}
}

func TestResolve_UnknownTypeError(t *testing.T) {
	project := []Registry{{Name: "acme", Type: "npm", URL: "https://a", Priority: 2}}
	_, err := Resolve(BuiltinGI(""), nil, project)
	if err == nil {
		t.Fatal("expected unknown-type error")
	}
}

func TestResolve_UnknownAuthError(t *testing.T) {
	project := []Registry{{Name: "acme", Type: TypeArtifactory, URL: "https://a", RepoKey: "k", Priority: 2, Auth: "kerberos"}}
	_, err := Resolve(BuiltinGI(""), nil, project)
	if err == nil {
		t.Fatal("expected unknown-auth error")
	}
}

// TestResolve_NonGIGeneroRejected is the regression test for GIS-249 C1: a
// type=genero registry with any name other than the built-in "gi" must be
// rejected, since its configured URL is dead config (the Genero client only
// honours the process-global registryBase()) and would mis-attribute results.
func TestResolve_NonGIGeneroRejected(t *testing.T) {
	project := []Registry{{Name: "mirror", Type: TypeGenero, URL: "https://other.example", Priority: 2}}
	_, err := Resolve(BuiltinGI(""), nil, project)
	if err == nil {
		t.Fatal("expected a non-gi genero registry to be rejected")
	}
}

// TestResolve_GIGeneroRetargetStillAllowed confirms the C1 rule does not block
// retargeting the built-in GI registry by name (a genero entry named "gi").
func TestResolve_GIGeneroRetargetStillAllowed(t *testing.T) {
	project := []Registry{{Name: "gi", Type: TypeGenero, URL: "https://internal-gi.example"}}
	if _, err := Resolve(BuiltinGI(""), nil, project); err != nil {
		t.Fatalf("gi genero retarget should be allowed: %v", err)
	}
}

func TestLoadGlobal_MissingIsEmpty(t *testing.T) {
	regs, err := LoadGlobal(t.TempDir())
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if regs != nil {
		t.Fatalf("want nil, got %+v", regs)
	}
}

func TestGlobalDefaultRegistry(t *testing.T) {
	// No file → empty, no error.
	if v, err := GlobalDefaultRegistry(t.TempDir()); err != nil || v != "" {
		t.Fatalf("missing file: got (%q, %v), want (\"\", nil)", v, err)
	}
	// File with defaultRegistry → returned.
	home := t.TempDir()
	body := `{"defaultRegistry":"acme","registries":[{"name":"acme","type":"artifactory","url":"https://a","repoKey":"k","priority":2}]}`
	if err := os.WriteFile(filepath.Join(home, GlobalFilename), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if v, err := GlobalDefaultRegistry(home); err != nil || v != "acme" {
		t.Fatalf("got (%q, %v), want (\"acme\", nil)", v, err)
	}
	// LoadGlobal still returns the registries from the same file.
	if regs, err := LoadGlobal(home); err != nil || len(regs) != 1 || regs[0].Name != "acme" {
		t.Fatalf("LoadGlobal: regs=%+v err=%v", regs, err)
	}
}

func TestLoadGlobal_BlankIsEmpty(t *testing.T) {
	for _, body := range []string{"", "   ", "\n\t \r\n"} {
		home := t.TempDir()
		if err := os.WriteFile(filepath.Join(home, GlobalFilename), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		regs, err := LoadGlobal(home)
		if err != nil {
			t.Fatalf("LoadGlobal(%q): %v", body, err)
		}
		if regs != nil {
			t.Fatalf("LoadGlobal(%q): want nil, got %+v", body, regs)
		}
	}
}

func TestLoadGlobal_ReadsFile(t *testing.T) {
	home := t.TempDir()
	body := `{"registries":[{"name":"acme","type":"artifactory","url":"https://a","repoKey":"k","priority":2}]}`
	if err := os.WriteFile(filepath.Join(home, GlobalFilename), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	regs, err := LoadGlobal(home)
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if len(regs) != 1 || regs[0].Name != "acme" || regs[0].RepoKey != "k" {
		t.Fatalf("unexpected: %+v", regs)
	}
}

// TestWriteGlobal_DoesNotInjectUpdateFields pins issue #21 item 2: `registry
// add`/`remove` do a read-modify-write of the whole file, so a user who never set
// the advisory update-check settings must not find them appearing in their
// hand-edited config.json.
func TestWriteGlobal_DoesNotInjectUpdateFields(t *testing.T) {
	home := t.TempDir()
	g := GlobalFile{Registries: []Registry{{Name: "acme", Type: TypeArtifactory, URL: "https://a", RepoKey: "k", Priority: 2}}}
	if err := WriteGlobalFile(home, g); err != nil {
		t.Fatalf("WriteGlobalFile: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, GlobalFilename))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"updateCheck", "updateCheckInterval"} {
		if strings.Contains(string(data), field) {
			t.Errorf("write injected %q into a config that never set it:\n%s", field, data)
		}
	}
}

// TestWriteGlobal_KeepsExplicitUpdateFields: omitempty must not swallow a real
// opt-out. A non-nil *bool is non-empty even when it points at false, so an
// explicit `"updateCheck": false` survives the read-modify-write cycle.
func TestWriteGlobal_KeepsExplicitUpdateFields(t *testing.T) {
	home := t.TempDir()
	off := false
	if err := WriteGlobalFile(home, GlobalFile{UpdateCheck: &off, UpdateCheckInterval: "12h"}); err != nil {
		t.Fatalf("WriteGlobalFile: %v", err)
	}
	g, err := LoadGlobalFile(home)
	if err != nil {
		t.Fatalf("LoadGlobalFile: %v", err)
	}
	if g.UpdateCheck == nil || *g.UpdateCheck {
		t.Errorf("updateCheck did not round-trip as false: %v", g.UpdateCheck)
	}
	if g.UpdateCheckInterval != "12h" {
		t.Errorf("updateCheckInterval = %q, want 12h", g.UpdateCheckInterval)
	}
	s, err := LoadUpdateSettings(home)
	if err != nil {
		t.Fatalf("LoadUpdateSettings: %v", err)
	}
	if s.Enabled || s.Interval != 12*time.Hour {
		t.Errorf("resolved settings = %+v, want disabled with a 12h interval", s)
	}
}

func TestLoadGlobal_RejectsUnknownField(t *testing.T) {
	home := t.TempDir()
	body := `{"registries":[{"name":"acme","type":"artifactory","url":"https://a","repoKey":"k","priority":2,"bogus":true}]}`
	if err := os.WriteFile(filepath.Join(home, GlobalFilename), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadGlobal(home); err == nil {
		t.Fatal("expected error on unknown field")
	}
}

// TestLoadGlobal_AcceptsSigningPolicy pins the fix for the PR #68 blocker:
// config.json may carry a signing.enforce policy — the README and the manifest
// policy-key hint both direct users to set it there. loadGlobalFile decodes with
// DisallowUnknownFields, so without GlobalFile.Signing this exact JSON is
// rejected, and the consuming path (LoadGlobal/Load) then silently drops EVERY
// configured registry. Assert both halves: the file loads, and the registry
// survives resolution.
func TestLoadGlobal_AcceptsSigningPolicy(t *testing.T) {
	home := t.TempDir()
	body := `{
  "signing": { "enforce": "require" },
  "registries": [{"name":"acme","type":"artifactory","url":"https://a","repoKey":"k","priority":2}]
}`
	if err := os.WriteFile(filepath.Join(home, GlobalFilename), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := LoadGlobalFile(home)
	if err != nil {
		t.Fatalf("LoadGlobalFile rejected a signing-bearing config: %v", err)
	}
	if g.Signing == nil || g.Signing.Enforce != "require" {
		t.Errorf("signing.enforce did not decode: %+v", g.Signing)
	}
	regs, err := LoadGlobal(home)
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if _, ok := Find(regs, "acme"); !ok {
		t.Errorf("acme registry was dropped despite a valid config; resolved set = %+v", regs)
	}
}

// TestWriteGlobal_DoesNotInjectSigning: the `registry add`/`remove`
// read-modify-write cycle must not inject an empty "signing" block into a config
// that never set one — the same omitempty guarantee as updateCheck / mavenMirror.
func TestWriteGlobal_DoesNotInjectSigning(t *testing.T) {
	home := t.TempDir()
	g := GlobalFile{Registries: []Registry{{Name: "acme", Type: TypeArtifactory, URL: "https://a", RepoKey: "k", Priority: 2}}}
	if err := WriteGlobalFile(home, g); err != nil {
		t.Fatalf("WriteGlobalFile: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, GlobalFilename))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "signing") {
		t.Errorf("write injected an empty signing block into a config that never set it:\n%s", data)
	}
}

func TestLoad_CascadeGlobalThenProject(t *testing.T) {
	home := t.TempDir()
	body := `{"registries":[{"name":"acme","type":"artifactory","url":"https://global","repoKey":"k","priority":2}]}`
	if err := os.WriteFile(filepath.Join(home, GlobalFilename), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	project := []Registry{{Name: "acme", Type: TypeArtifactory, URL: "https://project", RepoKey: "k", Priority: 2}}
	regs, err := Load(home, "", project)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	acme, _ := Find(regs, "acme")
	if acme.URL != "https://project" {
		t.Fatalf("project should override global: %q", acme.URL)
	}
}

func TestAdmits(t *testing.T) {
	r := Registry{Packages: []string{"acme-*", "internal-*"}}
	if !r.Admits("acme-utils") || !r.Admits("internal-x") {
		t.Fatal("should admit matching names")
	}
	if r.Admits("logft") {
		t.Fatal("should not admit non-matching name")
	}
	// No allow-list admits everything.
	if !(Registry{}).Admits("anything") {
		t.Fatal("empty allow-list should admit all")
	}
}
