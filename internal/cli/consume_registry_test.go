package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/config"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/provider"
)

// writeGlobalConfig drops a raw config.json into a fake fglpkg home.
func writeGlobalConfig(t *testing.T, home, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, config.GlobalFilename), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestResolveDefaultConsumeRegistry covers the consume-default cascade (GIS-364):
// FGLPKG_CONSUME_REGISTRY → project fglpkg.json → global config.json → "".
func TestResolveDefaultConsumeRegistry(t *testing.T) {
	withManifest := func(name string) *manifest.Manifest {
		m := manifest.New("app", "1.0.0", "", "")
		m.DefaultConsumeRegistry = name
		return m
	}

	t.Run("none configured yields empty", func(t *testing.T) {
		t.Setenv("FGLPKG_CONSUME_REGISTRY", "")
		if got := resolveDefaultConsumeRegistry(t.TempDir(), manifest.New("app", "1.0.0", "", "")); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("global only", func(t *testing.T) {
		t.Setenv("FGLPKG_CONSUME_REGISTRY", "")
		home := t.TempDir()
		writeGlobalConfig(t, home, `{"defaultConsumeRegistry": "corp"}`)
		if got := resolveDefaultConsumeRegistry(home, nil); got != "corp" {
			t.Errorf("got %q, want corp", got)
		}
	})

	t.Run("manifest overrides global", func(t *testing.T) {
		t.Setenv("FGLPKG_CONSUME_REGISTRY", "")
		home := t.TempDir()
		writeGlobalConfig(t, home, `{"defaultConsumeRegistry": "corp"}`)
		if got := resolveDefaultConsumeRegistry(home, withManifest("acme")); got != "acme" {
			t.Errorf("got %q, want acme (project wins)", got)
		}
	})

	t.Run("env overrides everything", func(t *testing.T) {
		t.Setenv("FGLPKG_CONSUME_REGISTRY", "  envrepo  ")
		home := t.TempDir()
		writeGlobalConfig(t, home, `{"defaultConsumeRegistry": "corp"}`)
		if got := resolveDefaultConsumeRegistry(home, withManifest("acme")); got != "envrepo" {
			t.Errorf("got %q, want envrepo (trimmed)", got)
		}
	})

	t.Run("publish default does not leak into consume", func(t *testing.T) {
		// The whole reason for a separate field: a team that set defaultRegistry
		// purely to publish to their Artifactory must not have installs silently
		// scoped to it.
		t.Setenv("FGLPKG_CONSUME_REGISTRY", "")
		t.Setenv("FGLPKG_PUBLISH_REGISTRY", "")
		home := t.TempDir()
		writeGlobalConfig(t, home, `{"defaultRegistry": "corp"}`)
		m := manifest.New("app", "1.0.0", "", "")
		m.DefaultRegistry = "acme"
		if got := resolveDefaultConsumeRegistry(home, m); got != "" {
			t.Errorf("got %q, want empty — defaultRegistry is publish-only", got)
		}
		// ...and the publish resolver still sees it.
		if got := resolveDefaultPublishRegistry(home, m); got != "acme" {
			t.Errorf("publish default = %q, want acme", got)
		}
	})
}

// twoRegistrySet builds a RepositorySet with gi + acme configured, matching what
// buildRepositorySet produces once a secondary repository exists.
func twoRegistrySet() *provider.RepositorySet {
	descs := []config.Registry{
		{Name: "gi", Type: config.TypeGenero, URL: "https://gi", Priority: 1},
		{Name: "acme", Type: config.TypeArtifactory, URL: "https://a", RepoKey: "k", Priority: 2},
	}
	provs := []provider.Provider{
		provider.NewGeneroProvider("gi"),
		provider.NewArtifactoryProvider(descs[1], nil, nil),
	}
	return provider.NewRepositorySet(provs, descs, nil)
}

// TestApplyConsumeRegistry covers the shared decision helper every consuming
// command routes through: explicit flag vs. sticky default, and the
// single-registry (rs == nil) special case.
func TestApplyConsumeRegistry(t *testing.T) {
	t.Run("nothing configured is a no-op", func(t *testing.T) {
		t.Setenv("FGLPKG_CONSUME_REGISTRY", "")
		name, fromDefault, err := applyConsumeRegistry(twoRegistrySet(), t.TempDir(), nil, "")
		if err != nil || name != "" || fromDefault {
			t.Fatalf("got (%q, %v, %v), want (empty, false, nil)", name, fromDefault, err)
		}
	})

	t.Run("explicit flag wins over a configured default", func(t *testing.T) {
		t.Setenv("FGLPKG_CONSUME_REGISTRY", "acme")
		name, fromDefault, err := applyConsumeRegistry(twoRegistrySet(), t.TempDir(), nil, "gi")
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if name != "gi" || fromDefault {
			t.Fatalf("got (%q, %v), want (gi, false)", name, fromDefault)
		}
	})

	t.Run("configured default is reported as such", func(t *testing.T) {
		t.Setenv("FGLPKG_CONSUME_REGISTRY", "acme")
		name, fromDefault, err := applyConsumeRegistry(twoRegistrySet(), t.TempDir(), nil, "")
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if name != "acme" || !fromDefault {
			t.Fatalf("got (%q, %v), want (acme, true)", name, fromDefault)
		}
	})

	t.Run("unknown default errors and names the field", func(t *testing.T) {
		t.Setenv("FGLPKG_CONSUME_REGISTRY", "bogus")
		_, _, err := applyConsumeRegistry(twoRegistrySet(), t.TempDir(), nil, "")
		if err == nil {
			t.Fatal("expected an error for an unconfigured default")
		}
		for _, want := range []string{`"bogus"`, "not configured", "gi, acme"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("message missing %q:\n%s", want, err.Error())
			}
		}
	})

	// An unknown *explicit* --registry keeps its pre-existing surface: the flag is
	// applied as a hard restriction and the routing layer reports the bad name at
	// resolve time. Only the sticky default is validated up front, because it can
	// come from a file the user is not looking at right now.
	t.Run("unknown explicit flag defers to the routing layer", func(t *testing.T) {
		t.Setenv("FGLPKG_CONSUME_REGISTRY", "")
		rs := twoRegistrySet()
		name, fromDefault, err := applyConsumeRegistry(rs, t.TempDir(), nil, "bogus")
		if err != nil {
			t.Fatalf("apply should not validate the explicit flag itself: %v", err)
		}
		if name != "bogus" || fromDefault {
			t.Fatalf("got (%q, %v), want (bogus, false)", name, fromDefault)
		}
		if _, err := rs.Versions("anything"); err == nil ||
			!strings.Contains(err.Error(), "not configured") {
			t.Fatalf("routing layer should reject the unknown restriction, got %v", err)
		}
	})

	// Single-registry case: buildRepositorySet returns nil when only the built-in
	// GI registry is configured. Naming gi is a no-op; any other name must error
	// rather than be silently ignored — the same rule --registry already follows.
	t.Run("single registry: gi default is a no-op", func(t *testing.T) {
		t.Setenv("FGLPKG_CONSUME_REGISTRY", config.GIName)
		name, fromDefault, err := applyConsumeRegistry(nil, t.TempDir(), nil, "")
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if name != config.GIName || !fromDefault {
			t.Fatalf("got (%q, %v), want (gi, true)", name, fromDefault)
		}
	})

	t.Run("single registry: other default errors", func(t *testing.T) {
		t.Setenv("FGLPKG_CONSUME_REGISTRY", "acme")
		_, _, err := applyConsumeRegistry(nil, t.TempDir(), nil, "")
		if err == nil {
			t.Fatal("expected an error: acme is not configured")
		}
		// The message must name the config field, not the flag — the user never
		// typed --registry here.
		if !strings.Contains(err.Error(), "default consume registry") {
			t.Errorf("message should name the default, got:\n%s", err.Error())
		}
	})

	t.Run("single registry: unknown explicit flag keeps the flag wording", func(t *testing.T) {
		t.Setenv("FGLPKG_CONSUME_REGISTRY", "")
		_, _, err := applyConsumeRegistry(nil, t.TempDir(), nil, "acme")
		if err == nil {
			t.Fatal("expected an error: acme is not configured")
		}
		if !strings.Contains(err.Error(), "--registry") {
			t.Errorf("message should name the flag, got:\n%s", err.Error())
		}
	})
}

// TestRegistryDefaultRole covers the DEFAULT column labels in `registry list`.
func TestRegistryDefaultRole(t *testing.T) {
	cases := []struct {
		name, consume, publish, want string
	}{
		{"acme", "acme", "", "consume"},
		{"acme", "", "acme", "publish"},
		{"acme", "acme", "acme", "both"},
		{"acme", "corp", "corp", "-"},
		// No default declared must not be implied on the gi row, even though an
		// unset publish default means publish goes to GI.
		{"gi", "", "", "-"},
	}
	for _, c := range cases {
		if got := registryDefaultRole(c.name, c.consume, c.publish); got != c.want {
			t.Errorf("registryDefaultRole(%q, %q, %q) = %q, want %q",
				c.name, c.consume, c.publish, got, c.want)
		}
	}
}

// TestCmdRegistryAdd_ConsumeDefaultProject verifies --consume-default writes the
// field into the same file the descriptor landed in (GIS-364/366: one committed
// file, so a clean clone consumes from the right repository).
func TestCmdRegistryAdd_ConsumeDefaultProject(t *testing.T) {
	chdirTemp(t)
	if err := manifest.New("app", "1.0.0", "", "").Save("."); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	if err := cmdRegistryAdd([]string{
		"acme", "https://a.example", "--repo-key", "K", "--project", "--consume-default",
	}); err != nil {
		t.Fatalf("registry add --project --consume-default: %v", err)
	}

	got, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("reload manifest: %v", err)
	}
	if got.DefaultConsumeRegistry != "acme" {
		t.Fatalf("defaultConsumeRegistry = %q, want acme", got.DefaultConsumeRegistry)
	}
	if _, ok := config.Find(got.Registries, "acme"); !ok {
		t.Fatalf("descriptor missing: %+v", got.Registries)
	}
	// The publish default is a separate knob and must be untouched.
	if got.DefaultRegistry != "" {
		t.Fatalf("defaultRegistry should stay empty, got %q", got.DefaultRegistry)
	}
}

func TestCmdRegistryAdd_ConsumeDefaultGlobal(t *testing.T) {
	home := chdirTemp(t)

	if err := cmdRegistryAdd([]string{
		"acme", "https://a.example", "--repo-key", "K", "--consume-default",
	}); err != nil {
		t.Fatalf("registry add --consume-default: %v", err)
	}

	g, err := config.LoadGlobalFile(home)
	if err != nil {
		t.Fatalf("load global: %v", err)
	}
	if g.DefaultConsumeRegistry != "acme" {
		t.Fatalf("defaultConsumeRegistry = %q, want acme", g.DefaultConsumeRegistry)
	}
	if g.DefaultRegistry != "" {
		t.Fatalf("defaultRegistry should stay empty, got %q", g.DefaultRegistry)
	}
}

// Without the flag, nothing is written — adding a repository must not silently
// rewire where packages come from.
func TestCmdRegistryAdd_WithoutConsumeDefaultWritesNothing(t *testing.T) {
	home := chdirTemp(t)

	if err := cmdRegistryAdd([]string{"acme", "https://a.example", "--repo-key", "K"}); err != nil {
		t.Fatalf("registry add: %v", err)
	}

	g, _ := config.LoadGlobalFile(home)
	if g.DefaultConsumeRegistry != "" {
		t.Fatalf("defaultConsumeRegistry = %q, want empty", g.DefaultConsumeRegistry)
	}
	// omitempty must keep the key out of the file entirely, so a hand-maintained
	// config is not reformatted with an empty setting.
	raw, err := os.ReadFile(filepath.Join(home, config.GlobalFilename))
	if err != nil {
		t.Fatalf("read config.json: %v", err)
	}
	if strings.Contains(string(raw), "defaultConsumeRegistry") {
		t.Fatalf("config.json gained an empty defaultConsumeRegistry:\n%s", raw)
	}
}

// Removing the registry a consume default points at must clear the default too,
// or every consuming command would fail against a repository that no longer
// exists. Mirrors the publish-default behaviour (GIS-249 C3).
func TestCmdRegistryRemove_ClearsDanglingConsumeDefault(t *testing.T) {
	t.Run("project", func(t *testing.T) {
		chdirTemp(t)
		m := manifest.New("app", "1.0.0", "", "")
		m.Registries = []config.Registry{{
			Name: "acme", Type: config.TypeArtifactory, URL: "https://a.example", RepoKey: "K", Priority: 2,
		}}
		m.DefaultConsumeRegistry = "acme"
		if err := m.Save("."); err != nil {
			t.Fatalf("save manifest: %v", err)
		}

		if err := cmdRegistryRemove([]string{"acme", "--project"}); err != nil {
			t.Fatalf("registry remove --project: %v", err)
		}
		got, err := manifest.Load(".")
		if err != nil {
			t.Fatalf("reload manifest: %v", err)
		}
		if got.DefaultConsumeRegistry != "" {
			t.Fatalf("defaultConsumeRegistry should be cleared, got %q", got.DefaultConsumeRegistry)
		}
	})

	t.Run("global", func(t *testing.T) {
		home := chdirTemp(t)
		writeGlobalConfig(t, home, `{"registries":[{"name":"acme","type":"artifactory",`+
			`"url":"https://a","repoKey":"k","priority":2}],"defaultConsumeRegistry":"acme"}`)

		if err := cmdRegistryRemove([]string{"acme"}); err != nil {
			t.Fatalf("registry remove: %v", err)
		}
		g, err := config.LoadGlobalFile(home)
		if err != nil {
			t.Fatalf("load global: %v", err)
		}
		if g.DefaultConsumeRegistry != "" {
			t.Fatalf("defaultConsumeRegistry should be cleared, got %q", g.DefaultConsumeRegistry)
		}
	})

	t.Run("unrelated default is kept", func(t *testing.T) {
		chdirTemp(t)
		m := manifest.New("app", "1.0.0", "", "")
		m.Registries = []config.Registry{
			{Name: "acme", Type: config.TypeArtifactory, URL: "https://a.example", RepoKey: "K", Priority: 2},
			{Name: "corp", Type: config.TypeArtifactory, URL: "https://c.example", RepoKey: "K", Priority: 3},
		}
		m.DefaultConsumeRegistry = "corp"
		if err := m.Save("."); err != nil {
			t.Fatalf("save manifest: %v", err)
		}

		if err := cmdRegistryRemove([]string{"acme", "--project"}); err != nil {
			t.Fatalf("registry remove --project: %v", err)
		}
		got, _ := manifest.Load(".")
		if got.DefaultConsumeRegistry != "corp" {
			t.Fatalf("unrelated default should be kept, got %q", got.DefaultConsumeRegistry)
		}
	})
}
