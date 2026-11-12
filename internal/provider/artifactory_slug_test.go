package provider

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/config"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
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

// Search is the one read whose argument is NOT a name someone typed: it lists
// the repository's folders and then describes each one. Canonicalizing there
// makes a row lie about its own folder — and a legacy mixed-case folder is
// exactly what the repository will hold, because that is what fglpkg <= 4.2.9
// deployed and what docs/artifactory.md sends people to `search --all` to find.
//
// The repository below holds both: a legacy DateTime/ at 0.1.0 and the
// republished datetime/ at 0.2.0, which is the upgrade path the docs prescribe.
// With FetchVersions' canonicalizing in this path, the DateTime row reported
// 0.2.0 (read out of the OTHER folder) and a blank description (its sidecar
// lookup, which does not canonicalize, then 404'd).
func TestArtifactory_SearchDescribesEachFolderItself(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/storage/generic-local", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"children":[{"uri":"/DateTime","folder":true},{"uri":"/datetime","folder":true}]}`)
	})
	mux.HandleFunc("/api/storage/generic-local/DateTime", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"children":[{"uri":"/0.1.0","folder":true}]}`)
	})
	mux.HandleFunc("/api/storage/generic-local/datetime", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"children":[{"uri":"/0.2.0","folder":true}]}`)
	})
	mux.HandleFunc("/generic-local/DateTime/0.1.0/fglpkg.json", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"name":"DateTime","version":"0.1.0","description":"legacy","genero":"^6.0.0"}`)
	})
	mux.HandleFunc("/generic-local/datetime/0.2.0/fglpkg.json", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"name":"DateTime","version":"0.2.0","description":"current","genero":"^7.0.0"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	reg := config.Registry{Name: "acme", Type: config.TypeArtifactory, URL: srv.URL, RepoKey: "generic-local", Auth: config.AuthAnonymous}
	results, err := NewArtifactoryProvider(reg, nil, nil).Search("")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	// Every field of a row has to come from the folder the row names: version
	// from its own version list, description and constraint from its own
	// sidecar. The two folders disagree on all three, so a row filled from the
	// wrong one cannot look right by accident.
	want := map[string]registry.SearchResult{
		"DateTime": {LatestVersion: "0.1.0", Description: "legacy", GeneroConstraint: "^6.0.0"},
		"datetime": {LatestVersion: "0.2.0", Description: "current", GeneroConstraint: "^7.0.0"},
	}
	if len(results) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(results), len(want), results)
	}
	for _, got := range results {
		w, ok := want[got.Name]
		if !ok {
			t.Errorf("unexpected row %q", got.Name)
			continue
		}
		delete(want, got.Name)
		if got.LatestVersion != w.LatestVersion {
			t.Errorf("row %q: version = %q, want %q", got.Name, got.LatestVersion, w.LatestVersion)
		}
		if got.Description != w.Description {
			t.Errorf("row %q: description = %q, want %q", got.Name, got.Description, w.Description)
		}
		if got.GeneroConstraint != w.GeneroConstraint {
			t.Errorf("row %q: genero = %q, want %q", got.Name, got.GeneroConstraint, w.GeneroConstraint)
		}
	}
	for name := range want {
		t.Errorf("row %q missing from the results", name)
	}
}
