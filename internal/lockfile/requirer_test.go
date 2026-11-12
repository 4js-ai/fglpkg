package lockfile

import "testing"

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
