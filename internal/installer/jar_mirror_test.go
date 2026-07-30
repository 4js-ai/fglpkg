package installer

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// TestInstallJar_MirrorWithAuth verifies GIS-365: with a Maven mirror base set
// (WithMavenBase) and the mirror registered for auth (WithRepoAuth), a JAR
// download is fetched from the mirror at the standard Maven2 path and carries
// the configured Authorization header — proving JARs no longer bypass the auth
// machinery.
func TestInstallJar_MirrorWithAuth(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("jar-bytes"))
	}))
	defer srv.Close()

	inst := New(t.TempDir(), "", "", "").
		WithMavenBase(srv.URL).
		WithRepoAuth([]RepoAuth{{
			URLPrefix: srv.URL,
			Headers:   map[string]string{"Authorization": "Basic dXNlcjpwYXNz"},
		}})

	dep := manifest.JavaDependency{GroupID: "com.example", ArtifactID: "lib", Version: "1.0.0"}
	if err := inst.InstallJar(dep); err != nil {
		t.Fatalf("InstallJar: %v", err)
	}

	if want := "/com/example/lib/1.0.0/lib-1.0.0.jar"; gotPath != want {
		t.Errorf("mirror request path = %q, want %q", gotPath, want)
	}
	if want := "Basic dXNlcjpwYXNz"; gotAuth != want {
		t.Errorf("mirror request Authorization = %q, want %q", gotAuth, want)
	}
	if _, err := os.Stat(filepath.Join(inst.jarsDir, "lib-1.0.0.jar")); err != nil {
		t.Errorf("expected JAR written to jars dir: %v", err)
	}
}

// TestInstallJar_MirrorAnonymous verifies that a mirror base with no matching
// repoAuth entry still reroutes the download to the mirror but sends no
// Authorization header — the anonymous mirror case stays byte-compatible with
// the old Maven-Central-anonymous behavior, just pointed at a different host.
func TestInstallJar_MirrorAnonymous(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("jar-bytes"))
	}))
	defer srv.Close()

	inst := New(t.TempDir(), "", "", "").WithMavenBase(srv.URL)

	dep := manifest.JavaDependency{GroupID: "org.acme", ArtifactID: "tool", Version: "2.1.0"}
	if err := inst.InstallJar(dep); err != nil {
		t.Fatalf("InstallJar: %v", err)
	}

	if want := "/org/acme/tool/2.1.0/tool-2.1.0.jar"; gotPath != want {
		t.Errorf("mirror request path = %q, want %q", gotPath, want)
	}
	if gotAuth != "" {
		t.Errorf("anonymous mirror received Authorization %q, want none", gotAuth)
	}
}

// TestInstallJar_ForbiddenSurfacesAuthError verifies the mirror auth-failure
// path: an Artifactory-style 403 is reported as a credentials error (a hard
// failure), not a silent not-found.
func TestInstallJar_ForbiddenSurfacesAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	inst := New(t.TempDir(), "", "", "").WithMavenBase(srv.URL)

	dep := manifest.JavaDependency{GroupID: "org.acme", ArtifactID: "tool", Version: "2.1.0"}
	err := inst.InstallJar(dep)
	if err == nil {
		t.Fatal("expected InstallJar to fail on HTTP 403")
	}
	if got := err.Error(); !contains(got, "403") || !contains(got, "credentials") {
		t.Errorf("error = %q, want a 403 credentials message", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
