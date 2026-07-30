package credentials_test

import (
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/credentials"
)

// TestAuthHeadersForURL covers the prefix-aware mirror credential resolution
// (GIS-365): a login to an Artifactory base (".../artifactory") must
// authenticate a Maven mirror nested under it (".../artifactory/<repo>"),
// while boundary matching prevents a key from covering a sibling repo or a
// look-alike host.
func TestAuthHeadersForURL(t *testing.T) {
	const base = "https://artifactorytests.jfrog.io/artifactory"
	const mirror = "https://artifactorytests.jfrog.io/artifactory/maven-tests-libs-release"
	const jarURL = mirror + "/com/jfrog/my-jfrog-app/1.0.0/my-jfrog-app-1.0.0.jar"

	t.Run("enclosing registry base covers a nested mirror URL", func(t *testing.T) {
		f, _ := credentials.Load(t.TempDir())
		f.Set(base, "PAT-BASE", "")
		got := f.AuthHeadersForURL(jarURL, credentials.SchemeBearer)
		if got["Authorization"] != "Bearer PAT-BASE" {
			t.Errorf("Authorization = %q, want %q", got["Authorization"], "Bearer PAT-BASE")
		}
	})

	t.Run("exact mirror key wins over a shorter enclosing key", func(t *testing.T) {
		f, _ := credentials.Load(t.TempDir())
		f.Set(base, "PAT-BASE", "")
		f.Set(mirror, "PAT-MIRROR", "")
		got := f.AuthHeadersForURL(jarURL, credentials.SchemeBearer)
		if got["Authorization"] != "Bearer PAT-MIRROR" {
			t.Errorf("Authorization = %q, want the longer (mirror) key's PAT", got["Authorization"])
		}
	})

	t.Run("trailing slash on the mirror base still resolves", func(t *testing.T) {
		f, _ := credentials.Load(t.TempDir())
		f.Set(base, "PAT-BASE", "")
		got := f.AuthHeadersForURL(mirror+"/", credentials.SchemeBearer)
		if got["Authorization"] != "Bearer PAT-BASE" {
			t.Errorf("Authorization = %q, want %q", got["Authorization"], "Bearer PAT-BASE")
		}
	})

	t.Run("a sibling repo under the same base is not covered", func(t *testing.T) {
		f, _ := credentials.Load(t.TempDir())
		// Key is a specific repo; a different repo under the same base must miss.
		f.Set(base+"/maven-other", "PAT-OTHER", "")
		if got := f.AuthHeadersForURL(jarURL, credentials.SchemeBearer); got != nil {
			t.Errorf("sibling repo key matched: got %v, want nil", got)
		}
	})

	t.Run("a look-alike host is not covered", func(t *testing.T) {
		f, _ := credentials.Load(t.TempDir())
		f.Set("https://artifactorytests.jfrog.io", "PAT-REAL", "")
		spoof := "https://artifactorytests.jfrog.io.attacker.com/artifactory/x/a.jar"
		if got := f.AuthHeadersForURL(spoof, credentials.SchemeBearer); got != nil {
			t.Errorf("look-alike host matched: got %v, want nil", got)
		}
	})

	t.Run("no stored credential yields nil", func(t *testing.T) {
		f, _ := credentials.Load(t.TempDir())
		if got := f.AuthHeadersForURL(jarURL, credentials.SchemeBearer); got != nil {
			t.Errorf("empty store matched: got %v, want nil", got)
		}
	})
}
