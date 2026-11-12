package provider

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/config"
)

// A package is stored under its canonical slug, so a read must ask for the
// slug whatever the caller typed. `fglpkg install <name>` / `info <name>` hand
// the typed name straight to the provider (cli.go), and the customer's package
// was published as "DateTime": before this, install asked for .../DateTime and
// got a 404 from a repository that held the package under .../datetime
// (GIS-610). The mock serves ONLY the canonical path, so a provider that
// forwards the typed spelling fails every row below.
func TestArtifactory_ReadsUseTheCanonicalSlug(t *testing.T) {
	srv := mockArtifactory(t)
	p := newTestProvider(srv.URL)

	// Every spelling here canonicalizes to "jfrog-test", the only package the
	// mock serves: case, and each of the three separator characters.
	for _, typed := range []string{"jfrog-test", "JFrog-Test", "JFROG-TEST", "jfrog_test", "JFrog.Test", "jfrog--test"} {
		t.Run(typed, func(t *testing.T) {
			vs, err := p.FetchVersions(typed)
			if err != nil {
				t.Fatalf("FetchVersions(%q): %v", typed, err)
			}
			if len(vs) != 2 {
				t.Errorf("FetchVersions(%q) = %d versions, want 2", typed, len(vs))
			}

			info, err := p.FetchInfo(typed, "1.0.0", "6")
			if err != nil {
				t.Fatalf("FetchInfo(%q): %v", typed, err)
			}
			// The resolved name is the slug, not the typed spelling: it becomes
			// the lock file key and the install directory, which every other
			// registry also keys by slug.
			if info.Name != "jfrog-test" {
				t.Errorf("FetchInfo(%q).Name = %q, want %q", typed, info.Name, "jfrog-test")
			}
			// The variant is parsed back out of the zip file name using the
			// folder name as its prefix, so a half-canonicalized path would
			// resolve the folder and then find no installable variant.
			if info.Variant != "genero6" {
				t.Errorf("FetchInfo(%q).Variant = %q, want %q", typed, info.Variant, "genero6")
			}
			if info.Checksum != "aaa6" {
				t.Errorf("FetchInfo(%q).Checksum = %q, want %q", typed, info.Checksum, "aaa6")
			}
		})
	}
}

// Canonicalizing must not turn "absent" into "found": a name that is genuinely
// not in the repository still has to report ErrNotFound, which is what the
// cascade uses to try the next repository instead of failing the install.
func TestArtifactory_CanonicalizingDoesNotHideAMissingPackage(t *testing.T) {
	srv := mockArtifactory(t)
	p := newTestProvider(srv.URL)
	for _, typed := range []string{"NoSuchPkg", "no-such-pkg"} {
		if _, err := p.FetchVersions(typed); !errors.Is(err, ErrNotFound) {
			t.Errorf("FetchVersions(%q) error = %v, want ErrNotFound", typed, err)
		}
	}
}

// Publish canonicalizes at the same boundary as the reads. The CLI already
// normalizes the manifest name (and says so), but a provider that trusted its
// caller would store whatever the next publish path forgot to normalize — and
// a stored path that disagrees with the read path is the whole of GIS-610.
func TestArtifactory_PublishStoresTheCanonicalSlug(t *testing.T) {
	var mu sync.Mutex
	var puts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			mu.Lock()
			puts = append(puts, r.URL.Path)
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	reg := config.Registry{Name: "acme", Type: config.TypeArtifactory, URL: srv.URL, RepoKey: "generic-local", Auth: config.AuthAnonymous}
	p := NewArtifactoryProvider(reg, nil, nil)
	err := p.Publish(PublishRequest{
		Name: "My_Date.Lib", Version: "0.1.0", Variant: "genero6",
		Zip: []byte("zip"), Checksum: "abc", Manifest: []byte("{}"),
		Force: true, // the overwrite guard is not what this test is about
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	want := []string{
		"/generic-local/my-date-lib/0.1.0/my-date-lib-0.1.0-genero6.zip",
		"/generic-local/my-date-lib/0.1.0/fglpkg.json",
	}
	mu.Lock()
	got := append([]string(nil), puts...)
	mu.Unlock()
	if len(got) != len(want) {
		t.Fatalf("PUT paths = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("PUT[%d] = %q, want %q", i, got[i], want[i])
		}
		// The folder and the zip must carry the SAME identity: variantTag parses
		// the variant back out using the folder name as the prefix.
		if strings.Contains(got[i], "My_Date") || strings.Contains(got[i], "My_date") {
			t.Errorf("PUT[%d] stored the verbatim name: %q", i, got[i])
		}
	}
}
