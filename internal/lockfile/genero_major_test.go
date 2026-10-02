package lockfile_test

import (
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

// GIS-574. LockedPackage.GeneroMajor is not descriptive metadata: the
// installer, `audit signatures` and the SBOM writer all read it back and
// rebuild the signed artifact variant as "genero"+GeneroMajor. It used to be
// written from the *runtime's* major, so when resolution delivered a different
// build the reconstructed variant was wrong, the Ed25519 check failed, and
// `audit signatures` reported "signature does not match" — indistinguishable
// from a tampered artifact.
//
// It must therefore record the variant that was actually delivered.

func planWithVariant(runtime, variant string) *resolver.Plan {
	return &resolver.Plan{
		GeneroVersion: genero.MustParse(runtime),
		Packages: []resolver.ResolvedPackage{{
			Name:        "fglunit",
			Version:     semver.MustParse("1.0.1"),
			DownloadURL: "https://example.com/fglunit-1.0.1.zip",
			Checksum:    "deadbeef",
			Variant:     variant,
			RequiredBy:  []string{"<root>"},
		}},
	}
}

func lockedFglunit(t *testing.T, lf *lockfile.LockFile) lockfile.LockedPackage {
	t.Helper()
	for _, p := range lf.Packages {
		if p.Name == "fglunit" {
			return p
		}
	}
	t.Fatal("fglunit missing from lock file packages")
	return lockfile.LockedPackage{}
}

func TestGeneroMajorRecordsTheDeliveredVariant(t *testing.T) {
	root := manifest.New("myapp", "1.0.0", "", "")

	for _, tc := range []struct {
		name    string
		runtime string
		variant string
		want    string
	}{
		{
			// The reported case: Genero 4 runtime, genero6 build delivered.
			// Recording "4" here is what produced the false signature alarm.
			name: "variant differs from runtime", runtime: "4.01.12", variant: "genero6", want: "6",
		},
		{
			name: "variant matches runtime", runtime: "6.00.01", variant: "genero6", want: "6",
		},
		{
			// Artifactory reports "default"; no major can be derived, so the
			// previous behaviour (record the runtime's major) is kept.
			name: "default variant keeps the runtime major", runtime: "4.01.12", variant: "default", want: "4",
		},
		{
			// Providers that report no variant at all, likewise unchanged.
			name: "absent variant keeps the runtime major", runtime: "4.01.12", variant: "", want: "4",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lf := lockfile.FromPlan(planWithVariant(tc.runtime, tc.variant), root, "")
			if got := lockedFglunit(t, lf).GeneroMajor; got != tc.want {
				t.Errorf("GeneroMajor = %q, want %q (runtime %s, variant %q)",
					got, tc.want, tc.runtime, tc.variant)
			}
		})
	}
}

// The reconstruction the readers perform must round-trip to the variant that
// was actually downloaded, since that string goes into the signed payload.
func TestGeneroMajorRoundTripsToTheSignedVariant(t *testing.T) {
	root := manifest.New("myapp", "1.0.0", "", "")
	lf := lockfile.FromPlan(planWithVariant("4.01.12", "genero6"), root, "")

	// Mirrors installer.go and audit_signatures.go.
	major := lockedFglunit(t, lf).GeneroMajor
	reconstructed := ""
	if major != "" {
		reconstructed = "genero" + major
	}
	if reconstructed != "genero6" {
		t.Errorf("reconstructed variant = %q, want %q — signature verification "+
			"would fail against the wrong payload", reconstructed, "genero6")
	}
}

// A webcomponent package is routed to Webcomponents, which carries no
// GeneroMajor at all — its signed variant is the literal "webcomponent".
func TestWebcomponentIsNotGivenAGeneroMajor(t *testing.T) {
	root := manifest.New("myapp", "1.0.0", "", "")
	plan := planWithVariant("4.01.12", "webcomponent")
	plan.Packages[0].Name = "fjs-grid"

	lf := lockfile.FromPlan(plan, root, "")
	if len(lf.Packages) != 0 {
		t.Errorf("webcomponent leaked into Packages: %+v", lf.Packages)
	}
	if len(lf.Webcomponents) != 1 {
		t.Fatalf("Webcomponents = %d entries, want 1", len(lf.Webcomponents))
	}
}
