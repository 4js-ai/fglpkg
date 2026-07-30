package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/config"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// TestResolveMavenMirror covers the mirror resolution cascade for JAR downloads
// (GIS-365): FGLPKG_MAVEN_URL → project fglpkg.json → global config.json, with
// the env var overriding only the URL and the auth scheme defaulting to bearer.
func TestResolveMavenMirror(t *testing.T) {
	writeGlobal := func(t *testing.T, home, body string) {
		t.Helper()
		if body == "" {
			return
		}
		if err := os.WriteFile(filepath.Join(home, config.GlobalFilename), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	withManifest := func(url, auth string) *manifest.Manifest {
		m := manifest.New("app", "1.0.0", "", "")
		m.MavenMirror = &config.MavenMirror{URL: url, Auth: auth}
		return m
	}

	t.Run("none configured yields empty", func(t *testing.T) {
		t.Setenv("FGLPKG_MAVEN_URL", "")
		base, auth := resolveMavenMirror(t.TempDir(), manifest.New("app", "1.0.0", "", ""))
		if base != "" || auth != "" {
			t.Errorf("got (%q, %q), want empty", base, auth)
		}
	})

	t.Run("global only, auth default bearer", func(t *testing.T) {
		t.Setenv("FGLPKG_MAVEN_URL", "")
		home := t.TempDir()
		writeGlobal(t, home, `{"mavenMirror": {"url": "https://global.example/m2"}}`)
		base, auth := resolveMavenMirror(home, nil)
		if base != "https://global.example/m2" || auth != config.AuthBearer {
			t.Errorf("got (%q, %q), want global URL + bearer", base, auth)
		}
	})

	t.Run("manifest overrides global, keeps its auth", func(t *testing.T) {
		t.Setenv("FGLPKG_MAVEN_URL", "")
		home := t.TempDir()
		writeGlobal(t, home, `{"mavenMirror": {"url": "https://global.example/m2", "auth": "apikey"}}`)
		base, auth := resolveMavenMirror(home, withManifest("https://project.example/m2/", "basic"))
		if base != "https://project.example/m2" { // trailing slash trimmed
			t.Errorf("base = %q, want project URL (slash trimmed)", base)
		}
		if auth != config.AuthBasic {
			t.Errorf("auth = %q, want basic (manifest's scheme)", auth)
		}
	})

	t.Run("env overrides URL only, auth from config", func(t *testing.T) {
		t.Setenv("FGLPKG_MAVEN_URL", "https://env.example/m2/")
		home := t.TempDir()
		base, auth := resolveMavenMirror(home, withManifest("https://project.example/m2", "apikey"))
		if base != "https://env.example/m2" {
			t.Errorf("base = %q, want env URL (slash trimmed)", base)
		}
		if auth != config.AuthAPIKey {
			t.Errorf("auth = %q, want apikey (from manifest, since env carries no scheme)", auth)
		}
	})

	t.Run("env only, auth default bearer", func(t *testing.T) {
		t.Setenv("FGLPKG_MAVEN_URL", "https://env.example/m2")
		base, auth := resolveMavenMirror(t.TempDir(), nil)
		if base != "https://env.example/m2" || auth != config.AuthBearer {
			t.Errorf("got (%q, %q), want env URL + bearer", base, auth)
		}
	})
}
