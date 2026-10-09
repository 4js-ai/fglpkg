package installer

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
)

// GIS-586, review round 1. PackageIsInstalled originally read the PUBLISHER's
// fglpkg.json as the record of what was installed. The installer accepts two
// kinds of artifact that never satisfy such a check — a zip with no root
// manifest, and one whose manifest names a version other than the one the
// registry published it as (Artifactory takes the version from the folder path,
// not the zip) — so each would have read as perpetually stale and re-downloaded
// on every single install, and a warm store holding one could no longer be
// replayed offline.
//
// The installer now writes its own stamp, and these tests drive the real
// install path end to end — a served zip, a real checksum, a real extraction —
// because the whole point is what the INSTALLER records, not what a fixture
// says it records.

// zipBytes builds a package zip from name -> contents, in the given order.
// Order matters: `fglpkg pack` emits entries alphabetically, which puts
// fglpkg.json in the middle, and that is what makes a half-extracted directory
// able to present a plausible manifest.
func zipBytes(t *testing.T, entries [][2]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// servePackage serves one zip and returns an installer plus the PackageInfo
// naming it at publishedVersion — which is the registry's version, and need not
// agree with anything inside the zip.
func servePackage(t *testing.T, name, publishedVersion string, entries [][2]string) (*Installer, *registry.PackageInfo) {
	t.Helper()
	body := zipBytes(t, entries)
	sum := sha256.Sum256(body)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(ts.Close)
	return New(t.TempDir(), "", "", ""), &registry.PackageInfo{
		Name:        name,
		Version:     publishedVersion,
		DownloadURL: ts.URL + "/pkg.zip",
		Checksum:    hex.EncodeToString(sum[:]),
	}
}

func manifestEntry(name, version string) [2]string {
	return [2]string{"fglpkg.json",
		`{"name":"` + name + `","version":"` + version + `","genero":">=3.20","license":"MIT"}`}
}

// readStamp returns the raw stamp file, failing the test if it is absent.
func readStamp(t *testing.T, inst *Installer, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(inst.packagesDir, name, lockfile.InstalledStampFilename))
	if err != nil {
		t.Fatalf("no install stamp for %s: %v", name, err)
	}
	return string(data)
}

func TestInstallWritesACompleteStamp(t *testing.T) {
	inst, info := servePackage(t, "demo-pkg", "1.0.0", [][2]string{
		{"a.42m", "stub"},
		manifestEntry("demo.pkg", "1.0.0"),
		{"src/com/acme/z.42m", "stub"},
	})
	if err := inst.Install(info); err != nil {
		t.Fatal(err)
	}

	stamp := readStamp(t, inst, "demo-pkg")
	for _, want := range []string{`"version": "1.0.0"`, `"complete": true`} {
		if !strings.Contains(stamp, want) {
			t.Errorf("stamp missing %s:\n%s", want, stamp)
		}
	}
	if !lockfile.PackageIsInstalled(inst.packagesDir, lockfile.LockedPackage{Name: "demo-pkg", Version: "1.0.0"}) {
		t.Error("a freshly installed package must read as installed")
	}
}

// The first artifact the publisher's manifest cannot describe: no manifest at
// all. The installer accepts it (readWebcomponentsFromZip returns no error for
// a zip without one), so a replay must too.
func TestInstallStampCoversAZipWithNoManifest(t *testing.T) {
	inst, info := servePackage(t, "nomani", "1.0.0", [][2]string{{"mod.42m", "stub"}})
	if err := inst.Install(info); err != nil {
		t.Fatalf("a zip with no root manifest must still install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inst.packagesDir, "nomani", "fglpkg.json")); !os.IsNotExist(err) {
		t.Fatal("precondition: this package must have no manifest on disk")
	}
	if !lockfile.PackageIsInstalled(inst.packagesDir, lockfile.LockedPackage{Name: "nomani", Version: "1.0.0"}) {
		t.Error("a manifest-less package would be re-downloaded on every install")
	}
}

// The second: an Artifactory re-upload under a new version folder, carrying a
// zip whose manifest still names the version it was built as. The registry's
// version is the one the lock records, so the registry's version is what the
// stamp must record.
func TestInstallStampBeatsAManifestNamingAnotherVersion(t *testing.T) {
	inst, info := servePackage(t, "skew", "1.0.1", [][2]string{manifestEntry("skew", "1.0.0")})
	if err := inst.Install(info); err != nil {
		t.Fatal(err)
	}
	if !lockfile.PackageIsInstalled(inst.packagesDir, lockfile.LockedPackage{Name: "skew", Version: "1.0.1"}) {
		t.Error("the published version is what was installed, so the replay must accept it")
	}
	if lockfile.PackageIsInstalled(inst.packagesDir, lockfile.LockedPackage{Name: "skew", Version: "1.0.0"}) {
		t.Error("the version inside the zip is not what the registry published")
	}
}

// A re-install over an existing version must move the stamp with it, or the
// store would keep claiming the version it used to hold.
func TestReinstallingMovesTheStamp(t *testing.T) {
	inst, info := servePackage(t, "demo-pkg", "1.0.0", [][2]string{manifestEntry("demo.pkg", "1.0.0")})
	if err := inst.Install(info); err != nil {
		t.Fatal(err)
	}
	_, newer := servePackage(t, "demo-pkg", "1.1.0", [][2]string{manifestEntry("demo.pkg", "1.1.0")})
	if err := inst.Install(newer); err != nil {
		t.Fatal(err)
	}
	if !lockfile.PackageIsInstalled(inst.packagesDir, lockfile.LockedPackage{Name: "demo-pkg", Version: "1.1.0"}) {
		t.Error("the new version must read as installed")
	}
	if lockfile.PackageIsInstalled(inst.packagesDir, lockfile.LockedPackage{Name: "demo-pkg", Version: "1.0.0"}) {
		t.Error("the replaced version must not still read as installed")
	}
}

// The reason the stamp is written twice. An extraction that dies part-way can
// leave a directory holding a manifest that names exactly the version the lock
// wants — fglpkg.json sits mid-zip — so absence of a completion marker is the
// only thing that distinguishes it from a finished install.
//
// The decompression cap is the vehicle: it fails the extraction on the entry
// AFTER the manifest, which is precisely the dangerous shape.
func TestAnInterruptedExtractionDoesNotReadAsInstalled(t *testing.T) {
	defer withCaps(t, 100, 1<<30)()

	inst, info := servePackage(t, "halfpkg", "2.0.0", [][2]string{
		manifestEntry("halfpkg", "2.0.0"),
		{"src/big.42m", strings.Repeat("a", 500)},
	})
	if err := inst.Install(info); err == nil {
		t.Fatal("precondition: the extraction was supposed to fail")
	}

	// The trap: the manifest landed, and it names the locked version.
	data, err := os.ReadFile(filepath.Join(inst.packagesDir, "halfpkg", "fglpkg.json"))
	if err != nil {
		t.Fatalf("precondition: the manifest should have been extracted first: %v", err)
	}
	if !strings.Contains(string(data), `"version":"2.0.0"`) {
		t.Fatalf("precondition: the extracted manifest should name the locked version:\n%s", data)
	}

	if lockfile.PackageIsInstalled(inst.packagesDir, lockfile.LockedPackage{Name: "halfpkg", Version: "2.0.0"}) {
		t.Error("a half-extracted package must not read as installed")
	}
	if stamp := readStamp(t, inst, "halfpkg"); !strings.Contains(stamp, `"complete": false`) {
		t.Errorf("the stamp should still be marked incomplete:\n%s", stamp)
	}
}
