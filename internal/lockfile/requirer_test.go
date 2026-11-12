package lockfile

import (
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

// TestRootRequirerIsTheStoredValue guards the half of GIS-611 that must NOT
// change. "<root>" is written into every lock file's requiredBy list and is
// matched on by the SBOM generator, `fglpkg list` and the post-install
// cross-check, so changing it would silently reinterpret every lock already on
// disk — a package declared by the project would start reading as one declared
// by a package nobody can find.
//
// Only the rendering changed. If this test fails, the fix has gone too far.
func TestRootRequirerIsTheStoredValue(t *testing.T) {
	if RootRequirer != "<root>" {
		t.Fatalf("RootRequirer = %q, want %q — this value is PERSISTED in every lock file",
			RootRequirer, "<root>")
	}
}

func TestDisplayRequirer(t *testing.T) {
	cases := []struct {
		in, want, why string
	}{
		{RootRequirer, "this project", "the sentinel a new user had to ask support about"},
		{"logft", "logft", "a real package keeps its name"},
		{"", "", "nothing to render"},
		{"<ROOT>", "<ROOT>", "only the exact sentinel is translated; the match is not fuzzy"},
		{"root", "root", "a package legitimately named root is not the sentinel"},
	}
	for _, c := range cases {
		if got := DisplayRequirer(c.in); got != c.want {
			t.Errorf("DisplayRequirer(%q) = %q, want %q (%s)", c.in, got, c.want, c.why)
		}
	}
}

// The rendered form must never be mistakable for a package name, or a reader
// will go looking for it in the registry.
func TestDisplayRequirerIsNotASlug(t *testing.T) {
	const rendered = "this project"
	if DisplayRequirer(RootRequirer) != rendered {
		t.Fatalf("expected %q", rendered)
	}
	for _, c := range rendered {
		if c == ' ' {
			return // a space cannot appear in a slug: unmistakable, as intended
		}
	}
	t.Errorf("%q contains no space, so it reads like a package name", rendered)
}

// TestResolverWritesTheRootSentinel closes the loop on the WRITER. Everything
// else about RootRequirer is a reader: this package persists it, the SBOM, the
// tree and the cross-check match on it, and DisplayRequirer renders it. None of
// those would notice if the resolver stopped tagging root-declared
// dependencies with it, or started tagging them with something else — the lock
// file would simply be written with a different value and every consumer would
// agree it was not the root.
//
// The test lives here rather than in the resolver because lockfile imports
// resolver (not the reverse), so this is the lower of the two packages that can
// see both the constant's documented contract and the code that mints it.
func TestResolverWritesTheRootSentinel(t *testing.T) {
	root := manifest.New("app", "1.0.0", "", "")
	root.AddFGLDependency("dep", "*")

	versions := func(string) ([]resolver.CandidateVersion, error) {
		return []resolver.CandidateVersion{{Version: semver.MustParse("1.0.0")}}, nil
	}
	info := func(name, version, _ string) (*registry.PackageInfo, error) {
		return &registry.PackageInfo{
			Name:        name,
			Version:     version,
			DownloadURL: "https://example.test/" + name + ".zip",
			Checksum:    "deadbeef",
		}, nil
	}

	plan, err := resolver.NewWithFetchers(genero.MustParse("6.00.01"), versions, info).Resolve(root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(plan.Packages) != 1 {
		t.Fatalf("plan has %d packages, want 1: %+v", len(plan.Packages), plan.Packages)
	}
	got := plan.Packages[0].RequiredBy
	if len(got) != 1 || got[0] != RootRequirer {
		t.Fatalf("RequiredBy = %v, want [%q] — a dependency the project declares "+
			"itself must be tagged with the sentinel every reader matches on", got, RootRequirer)
	}
}
