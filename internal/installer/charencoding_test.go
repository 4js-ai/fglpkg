package installer

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// zipWithManifest writes a one-entry zip whose fglpkg.json is the given JSON.
func zipWithManifest(t *testing.T, manifestJSON string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pkg.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	w, err := zw.Create(manifest.Filename)
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}
	if _, err := w.Write([]byte(manifestJSON)); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return path
}

func TestCheckEncoding(t *testing.T) {
	cases := []struct {
		name        string
		manifest    string
		envCharmap  string
		envLength   string
		force       bool
		wantErr     bool
		wantInErr   string
		description string
	}{
		{
			name:       "absent declaration proceeds silently",
			manifest:   `{"name":"acme-widget","version":"1.0.0"}`,
			envCharmap: "UTF-8", envLength: "CHAR",
			description: "every package published before GIS-285 declares nothing",
		},
		{
			name:       "US-ASCII proceeds against any charmap",
			manifest:   `{"name":"a","version":"1.0.0","characterEncoding":"US-ASCII"}`,
			envCharmap: "ISO-8859-15", envLength: "BYTE",
			description: "ASCII-7 is a subset of every charset Genero supports",
		},
		{
			name:       "Genero's ASCII alias also proceeds",
			manifest:   `{"name":"a","version":"1.0.0","characterEncoding":"ASCII"}`,
			envCharmap: "UTF-8", envLength: "CHAR",
		},
		{
			name:       "matching charset proceeds",
			manifest:   `{"name":"a","version":"1.0.0","characterEncoding":"UTF-8"}`,
			envCharmap: "UTF-8", envLength: "CHAR",
		},
		{
			name:       "case-insensitive match proceeds",
			manifest:   `{"name":"a","version":"1.0.0","characterEncoding":"utf-8"}`,
			envCharmap: "UTF-8", envLength: "CHAR",
		},
		{
			name:       "mismatched charset is refused",
			manifest:   `{"name":"acme-widget","version":"1.0.0","characterEncoding":"ISO-8859-15"}`,
			envCharmap: "UTF-8", envLength: "CHAR",
			wantErr: true, wantInErr: "ISO-8859-15",
		},
		{
			name:       "the refusal names the escape hatch",
			manifest:   `{"name":"a","version":"1.0.0","characterEncoding":"ISO-8859-15"}`,
			envCharmap: "UTF-8", envLength: "CHAR",
			wantErr: true, wantInErr: "--force-character-encoding",
		},
		{
			name:       "--force-character-encoding installs anyway",
			manifest:   `{"name":"a","version":"1.0.0","characterEncoding":"ISO-8859-15"}`,
			envCharmap: "UTF-8", envLength: "CHAR",
			force: true,
		},
		{
			name:       "mismatched length semantics is refused",
			manifest:   `{"name":"a","version":"1.0.0","characterEncoding":"UTF-8","lengthSemantics":"char"}`,
			envCharmap: "UTF-8", envLength: "BYTE",
			wantErr: true, wantInErr: "FGL_LENGTH_SEMANTICS",
		},
		{
			name:       "matching length semantics proceeds",
			manifest:   `{"name":"a","version":"1.0.0","characterEncoding":"UTF-8","lengthSemantics":"char"}`,
			envCharmap: "UTF-8", envLength: "CHAR",
		},
		{
			name:       "absent length semantics is no constraint",
			manifest:   `{"name":"a","version":"1.0.0","characterEncoding":"UTF-8"}`,
			envCharmap: "UTF-8", envLength: "BYTE",
		},
		{
			name:       "an unknown manifest field does not break the peek",
			manifest:   `{"name":"a","version":"1.0.0","characterEncoding":"UTF-8","futureField":{"x":1}}`,
			envCharmap: "UTF-8", envLength: "CHAR",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FGLPKG_CHARMAP", tc.envCharmap)
			t.Setenv("FGLPKG_LENGTH_SEMANTICS", tc.envLength)
			i := New(t.TempDir(), "", "", "").WithForceCharacterEncoding(tc.force)
			err := i.checkEncoding(zipWithManifest(t, tc.manifest), "acme-widget")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("checkEncoding() = nil, want an error (%s)", tc.description)
				}
				if tc.wantInErr != "" && !strings.Contains(err.Error(), tc.wantInErr) {
					t.Errorf("error %q does not mention %q", err, tc.wantInErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("checkEncoding() = %v, want nil (%s)", err, tc.description)
			}
		})
	}
}

// A package declaring a requirement we cannot check must still install. A
// build container that vendors packages has no fglrun, and refusing there
// would make fglpkg unusable for a declaration that is advisory until a
// program actually runs.
func TestCheckEncodingUnreadableEnvironmentWarnsNotFails(t *testing.T) {
	t.Setenv("FGLPKG_CHARMAP", "")
	t.Setenv("FGLPKG_LENGTH_SEMANTICS", "")
	t.Setenv("PATH", t.TempDir()) // no fglrun reachable
	t.Setenv("FGLDIR", "")

	i := New(t.TempDir(), "", "", "")
	zipPath := zipWithManifest(t, `{"name":"a","version":"1.0.0","characterEncoding":"ISO-8859-15"}`)
	if err := i.checkEncoding(zipPath, "acme-widget"); err != nil {
		t.Fatalf("an unreadable environment must warn, not fail: %v", err)
	}
}

// The ASCII fast path must not need the environment at all: a pure-ASCII
// package is portable by construction, so it installs even where fglrun
// cannot be found.
func TestCheckEncodingASCIINeedsNoEnvironment(t *testing.T) {
	t.Setenv("FGLPKG_CHARMAP", "")
	t.Setenv("FGLPKG_LENGTH_SEMANTICS", "")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("FGLDIR", "")

	i := New(t.TempDir(), "", "", "")
	zipPath := zipWithManifest(t, `{"name":"a","version":"1.0.0","characterEncoding":"US-ASCII"}`)
	if err := i.checkEncoding(zipPath, "acme-widget"); err != nil {
		t.Fatalf("a US-ASCII package must install without consulting fglrun: %v", err)
	}
}

func TestReadEncodingFromZipNoManifest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	zw := zip.NewWriter(f)
	if _, err := zw.Create("README.md"); err != nil {
		t.Fatalf("create entry: %v", err)
	}
	zw.Close()
	f.Close()

	enc, err := readEncodingFromZip(path)
	if err != nil {
		t.Fatalf("readEncodingFromZip: %v", err)
	}
	if enc.CharacterEncoding != "" || enc.LengthSemantics != "" {
		t.Errorf("a zip with no manifest must report nothing, got %+v", enc)
	}
}
