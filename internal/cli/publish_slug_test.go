package cli

import (
	"strings"
	"testing"
)

// TestPublishSlug pins the rule BOTH publish paths now share.
//
// GIS-610 was not a wrong rule but a missing one: the GI path canonicalized the
// package name and the Artifactory path did not, so the same manifest was
// stored under two different identities and install — which always asks for the
// canonical slug — could not find the Artifactory one. The two paths call this
// function now, so a change here cannot reach one and miss the other.
func TestPublishSlug(t *testing.T) {
	cases := []struct {
		name      string
		want      string
		announced bool
		why       string
	}{
		{"DateTime", "datetime", true, "the reported bug: case alone made the package unreachable"},
		{"My_Date.Lib", "my-date-lib", true, "separator runs collapse too (PEP 503), not just case"},
		{"fgl..utils", "fgl-utils", true, "a run of separators collapses to one hyphen"},
		{"demo-pkg", "demo-pkg", false, "already canonical: unchanged, and nothing to announce"},
		{"a1", "a1", false, "the shortest valid slug is left alone"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			out, err := captureStdout(t, func() error {
				got = publishSlug(tc.name)
				return nil
			})
			if err != nil {
				t.Fatalf("captureStdout: %v", err)
			}
			if got != tc.want {
				t.Errorf("publishSlug(%q) = %q, want %q (%s)", tc.name, got, tc.want, tc.why)
			}
			// The author chose the name; the path they will see in the registry
			// is not the one they typed, so a silent rewrite is its own bug.
			said := strings.Contains(out, "Normalized name")
			if said != tc.announced {
				t.Errorf("announced = %v, want %v (%s)\noutput: %q", said, tc.announced, tc.why, out)
			}
			if tc.announced {
				if !strings.Contains(out, tc.name) || !strings.Contains(out, tc.want) {
					t.Errorf("the notice must name both forms, got %q", out)
				}
			}
		})
	}
}

// TestPublishSlugIsIdempotent: publishing a package twice must target the same
// path, and a slug fed back through must not drift or re-announce.
func TestPublishSlugIsIdempotent(t *testing.T) {
	for _, name := range []string{"DateTime", "My_Date.Lib", "demo-pkg"} {
		var once, twice string
		_, _ = captureStdout(t, func() error { once = publishSlug(name); return nil })
		out, _ := captureStdout(t, func() error { twice = publishSlug(once); return nil })
		if twice != once {
			t.Errorf("publishSlug(%q) = %q, then publishSlug(%q) = %q — not idempotent", name, once, once, twice)
		}
		if strings.Contains(out, "Normalized name") {
			t.Errorf("re-normalizing %q announced a change it did not make: %q", once, out)
		}
	}
}
