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

// A blank charmap is the same situation as an error reading the environment,
// and must warn rather than refuse. DetectRuntimeInfo returns no error when
// fglrun is absent but FGLPKG_LENGTH_SEMANTICS is set, leaving Charmap empty;
// every declared charset then "mismatched" it, and the refusal read
// "...but this environment uses " with nothing after it.
func TestCheckEncodingEmptyCharmapWarnsNotFails(t *testing.T) {
	t.Setenv("FGLPKG_CHARMAP", "")
	t.Setenv("FGLPKG_LENGTH_SEMANTICS", "CHAR")
	t.Setenv("PATH", t.TempDir()) // no fglrun reachable
	t.Setenv("FGLDIR", "")

	i := New(t.TempDir(), "", "", "")
	zipPath := zipWithManifest(t, `{"name":"a","version":"1.0.0","characterEncoding":"UTF-8"}`)
	if err := i.checkEncoding(zipPath, "acme-widget"); err != nil {
		t.Fatalf("an empty charmap means the environment is unknown, so this must warn: %v", err)
	}
}

// The US-ASCII exemption inside the mismatch test, not the fast path above it.
// With lengthSemantics set, a US-ASCII package gets past the fast path and
// reaches the charset comparison, where it must still be exempt: ASCII-7 is a
// subset of every charset Genero supports.
func TestCheckEncodingASCIIExemptEvenWithLengthSemantics(t *testing.T) {
	t.Setenv("FGLPKG_CHARMAP", "UTF-8")
	t.Setenv("FGLPKG_LENGTH_SEMANTICS", "CHAR")

	i := New(t.TempDir(), "", "", "")
	zipPath := zipWithManifest(t,
		`{"name":"a","version":"1.0.0","characterEncoding":"US-ASCII","lengthSemantics":"char"}`)
	if err := i.checkEncoding(zipPath, "acme-widget"); err != nil {
		t.Fatalf("a US-ASCII package is portable to every charmap, so it must not be refused: %v", err)
	}
}

// The refusal also fires during `fglpkg update`, which REJECTS a bare
// --force-character-encoding. Naming the command makes the advice correct from
// either entry point.
func TestCheckEncodingRefusalNamesTheInstallCommand(t *testing.T) {
	t.Setenv("FGLPKG_CHARMAP", "UTF-8")
	t.Setenv("FGLPKG_LENGTH_SEMANTICS", "")

	i := New(t.TempDir(), "", "", "")
	zipPath := zipWithManifest(t, `{"name":"a","version":"1.0.0","characterEncoding":"ISO-8859-15"}`)
	err := i.checkEncoding(zipPath, "acme-widget")
	if err == nil {
		t.Fatal("a real mismatch must refuse")
	}
	if !strings.Contains(err.Error(), "fglpkg install --force-character-encoding") {
		t.Errorf("the refusal must name the command, not just the flag, got:\n%s", err.Error())
	}
}

// String resources WARN and never refuse: a package may ship one resource file
// per language and charset for the consumer to select at runtime, so a file
// that does not match this environment may simply be one it never loads.
func TestCheckEncodingResourceMismatchDoesNotRefuse(t *testing.T) {
	t.Setenv("FGLPKG_CHARMAP", "UTF-8")
	t.Setenv("FGLPKG_LENGTH_SEMANTICS", "")

	i := New(t.TempDir(), "", "", "")
	zipPath := zipWithManifest(t, `{"name":"a","version":"1.0.0","characterEncoding":"US-ASCII",`+
		`"stringResourceEncodings":{"Msg.latin9.42s":"ISO-8859-15","Msg.utf8.42s":"UTF-8"}}`)
	if err := i.checkEncoding(zipPath, "acme-widget"); err != nil {
		t.Fatalf("a resource charset mismatch must warn, never refuse: %v", err)
	}
}

// ...but the warning has to actually name the offending file, or it tells the
// consumer nothing they can act on.
func TestWarnResourceEncodingsNamesTheMismatchedFileOnly(t *testing.T) {
	out := captureStderr(t, func() {
		warnResourceEncodings("acme-widget", map[string]string{
			"Msg.latin9.42s": "ISO-8859-15",
			"Msg.utf8.42s":   "UTF-8",
			"Msg.plain.42s":  "US-ASCII",
		}, "UTF-8")
	})
	if !strings.Contains(out, "Msg.latin9.42s") {
		t.Errorf("the warning must name the mismatched resource, got:\n%s", out)
	}
	for _, fine := range []string{"Msg.utf8.42s", "Msg.plain.42s"} {
		if strings.Contains(out, fine) {
			t.Errorf("%s matches this environment and must not be named, got:\n%s", fine, out)
		}
	}
}

// Nothing to say means nothing printed, or every install of a localized
// package becomes noisy.
func TestWarnResourceEncodingsSilentWhenEverythingMatches(t *testing.T) {
	out := captureStderr(t, func() {
		warnResourceEncodings("acme-widget", map[string]string{
			"Msg.utf8.42s":  "UTF-8",
			"Msg.plain.42s": "US-ASCII",
		}, "UTF-8")
	})
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected no warning, got:\n%s", out)
	}
}

// A localization package — resources only, no characterEncoding and no
// lengthSemantics — must still reach warnResourceEncodings. Both early returns
// in checkEncoding have to test the resource map as well as the two scalar
// fields, or such a package falls out at the first one and its consumer is
// never warned about a single file.
func TestCheckEncodingResourceOnlyPackageIsStillChecked(t *testing.T) {
	t.Setenv("FGLPKG_CHARMAP", "UTF-8")
	t.Setenv("FGLPKG_LENGTH_SEMANTICS", "")

	i := New(t.TempDir(), "", "", "")
	zipPath := zipWithManifest(t,
		`{"name":"a","version":"1.0.0","stringResourceEncodings":{"Msg.latin9.42s":"ISO-8859-15"}}`)
	out := captureStderr(t, func() {
		if err := i.checkEncoding(zipPath, "acme-l10n"); err != nil {
			t.Fatalf("a resource charset mismatch must warn, never refuse: %v", err)
		}
	})
	if !strings.Contains(out, "Msg.latin9.42s") {
		t.Errorf("a package whose only encoding information is its resources must still be "+
			"checked, got:\n%s", out)
	}
}

// The same package under the US-ASCII early return: a pure-ASCII code
// encoding exempts the CODE from any check, but it says nothing about the
// resources, which are a separate constraint.
func TestCheckEncodingASCIICodeDoesNotExemptItsResources(t *testing.T) {
	t.Setenv("FGLPKG_CHARMAP", "ISO-8859-15")
	t.Setenv("FGLPKG_LENGTH_SEMANTICS", "")

	i := New(t.TempDir(), "", "", "")
	zipPath := zipWithManifest(t, `{"name":"a","version":"1.0.0","characterEncoding":"US-ASCII",`+
		`"stringResourceEncodings":{"Msg.utf8.42s":"UTF-8"}}`)
	out := captureStderr(t, func() {
		if err := i.checkEncoding(zipPath, "acme-widget"); err != nil {
			t.Fatalf("resources warn, never refuse: %v", err)
		}
	})
	if !strings.Contains(out, "Msg.utf8.42s") {
		t.Errorf("US-ASCII code must not exempt a non-ASCII resource from the warning, got:\n%s", out)
	}
}
