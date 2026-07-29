package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/config"
	"github.com/4js-mikefolcher/fglpkg/internal/credentials"
)

func TestRegistryLoginStatus(t *testing.T) {
	gi := config.Registry{Name: "gi", Type: config.TypeGenero, URL: "https://gi", Auth: config.AuthBearer}
	acme := config.Registry{Name: "acme", Type: config.TypeArtifactory, URL: "https://a", RepoKey: "k", Auth: config.AuthBearer}
	anon := config.Registry{Name: "pub", Type: config.TypeArtifactory, URL: "https://p", RepoKey: "k", Auth: config.AuthAnonymous}

	empty := &credentials.File{Registries: map[string]credentials.Entry{}}

	withAcme := &credentials.File{Registries: map[string]credentials.Entry{}}
	withAcme.Set("https://a", "tok", "")

	t.Run("anonymous repo", func(t *testing.T) {
		t.Setenv("FGLPKG_TOKEN", "")
		if got := registryLoginStatus(empty, anon); got != "anon" {
			t.Fatalf("got %q, want anon", got)
		}
	})

	t.Run("gi with no env and no stored creds", func(t *testing.T) {
		t.Setenv("FGLPKG_TOKEN", "")
		if got := registryLoginStatus(empty, gi); got != "no" {
			t.Fatalf("got %q, want no", got)
		}
	})

	t.Run("gi authenticated by FGLPKG_TOKEN", func(t *testing.T) {
		t.Setenv("FGLPKG_TOKEN", "env-tok")
		if got := registryLoginStatus(empty, gi); got != "env" {
			t.Fatalf("got %q, want env", got)
		}
	})

	t.Run("FGLPKG_TOKEN does not authenticate Artifactory", func(t *testing.T) {
		t.Setenv("FGLPKG_TOKEN", "env-tok")
		if got := registryLoginStatus(empty, acme); got != "no" {
			t.Fatalf("got %q, want no (env var is GI-only)", got)
		}
	})

	t.Run("Artifactory with stored credentials", func(t *testing.T) {
		t.Setenv("FGLPKG_TOKEN", "")
		if got := registryLoginStatus(withAcme, acme); got != "yes" {
			t.Fatalf("got %q, want yes", got)
		}
	})
}

// TestRegistrySources checks that `registry list`'s SOURCE column attributes
// each registry to the layer that defined it (GIS-366): the built-in GI entry is
// "builtin", an entry from the machine-wide config.json is "global", and one from
// the project fglpkg.json is "project" — with project winning per the cascade.
func TestRegistrySources(t *testing.T) {
	home := t.TempDir()
	global := `{"registries":[{"name":"corp","type":"artifactory","url":"https://c","repoKey":"k","priority":3}]}`
	if err := os.WriteFile(filepath.Join(home, config.GlobalFilename), []byte(global), 0o644); err != nil {
		t.Fatal(err)
	}
	proj := []config.Registry{{
		Name: "acme", Type: config.TypeArtifactory, URL: "https://a", RepoKey: "k", Priority: 2,
	}}

	src := registrySources(home, "", proj)
	for name, want := range map[string]string{"gi": "builtin", "corp": "global", "acme": "project"} {
		if src[name] != want {
			t.Fatalf("source[%q] = %q, want %q", name, src[name], want)
		}
	}
}
