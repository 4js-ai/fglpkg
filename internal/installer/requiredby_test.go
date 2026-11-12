package installer

import (
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
)

// TestRequiredByPhrase covers the line the customer actually read:
//
//	✓ dt@0.2.1  (required by: <root>)
//
// They asked support what "<root>" was. It is a storage sentinel that had no
// business being on screen (GIS-611).
func TestRequiredByPhrase(t *testing.T) {
	cases := []struct {
		name       string
		requiredBy []string
		want       string
		why        string
	}{
		{
			"the reported line",
			[]string{lockfile.RootRequirer},
			"direct dependency",
			"a package the project declares IS simply a direct dependency",
		},
		{
			"a transitive package is unchanged",
			[]string{"logft"},
			"required by: logft",
			"naming the requirer was never the problem",
		},
		{
			"two requirers",
			[]string{"logft", "qrcode"},
			"required by: logft, qrcode",
			"",
		},
		{
			"the project alongside a package",
			[]string{lockfile.RootRequirer, "logft"},
			"required by: this project, logft",
			"a mixed list keeps the list shape; only the sentinel is spelled out",
		},
		{
			"the project is not first",
			[]string{"logft", lockfile.RootRequirer},
			"required by: logft, this project",
			"order is the lock file's, not ours to sort",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := requiredByPhrase(tc.requiredBy)
			if got != tc.want {
				t.Errorf("requiredByPhrase(%v) = %q, want %q (%s)", tc.requiredBy, got, tc.want, tc.why)
			}
			// The acceptance criterion, asserted on every case rather than
			// only the obvious one: no user-facing rendering leaks the sentinel.
			if strings.Contains(got, lockfile.RootRequirer) {
				t.Errorf("rendering %v leaked the storage sentinel: %q", tc.requiredBy, got)
			}
		})
	}
}

// pkgLabel names the declaring side of a cross-check warning, and the root
// project can be that side. "<root>@1.0.0" reads as a package coordinate for a
// package that does not exist.
func TestPkgLabelDoesNotCoordinateTheRootProject(t *testing.T) {
	cases := []struct {
		pkg, version, want string
	}{
		{rootPkgLabel, "1.0.0", "this project"},
		{rootPkgLabel, "", "this project"},
		{"logft", "2.0.0", "logft@2.0.0"},
		{"logft", "", "logft"},
	}
	for _, c := range cases {
		got := pkgLabel(c.pkg, c.version)
		if got != c.want {
			t.Errorf("pkgLabel(%q, %q) = %q, want %q", c.pkg, c.version, got, c.want)
		}
		if strings.Contains(got, lockfile.RootRequirer) {
			t.Errorf("pkgLabel(%q, %q) leaked the storage sentinel: %q", c.pkg, c.version, got)
		}
	}
}

// (A test asserting rootPkgLabel == lockfile.RootRequirer used to sit here. It
// could not fail: crosscheck.go DEFINES rootPkgLabel as that constant, so the
// compiler folds the comparison away. What it was named for — that the value
// the resolver writes is the value every consumer matches on — is pinned where
// it can actually fail, in lockfile's TestResolverWritesTheRootSentinel.)
