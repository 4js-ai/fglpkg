package slug

import (
	"strings"
	"testing"
)

func TestCanonical(t *testing.T) {
	cases := []struct{ in, want string }{
		{"fgl_ai_sdk", "fgl-ai-sdk"},   // underscores → hyphens
		{"Fgl.AI.SDK", "fgl-ai-sdk"},   // dots → hyphens, lowercased
		{"fgl__ai--sdk", "fgl-ai-sdk"}, // runs collapse to one '-'
		{"fgl.ai_sdk-x", "fgl-ai-sdk-x"},
		{"fgl-ai-sdk", "fgl-ai-sdk"}, // already canonical
		{"POIAPI", "poiapi"},
		{"a", "a"}, // canonicalizes fine even though IsValid rejects (too short)
	}
	for _, c := range cases {
		if got := Canonical(c.in); got != c.want {
			t.Errorf("Canonical(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCanonicalIdempotent(t *testing.T) {
	for _, in := range []string{"fgl_ai_sdk", "Fgl.AI.SDK", "fgl__ai--sdk", "already-canonical"} {
		once := Canonical(in)
		if twice := Canonical(once); twice != once {
			t.Errorf("Canonical not idempotent for %q: %q -> %q", in, once, twice)
		}
	}
}

func TestIsValid(t *testing.T) {
	valid := []string{"ab", "fgl-ai-sdk", "a1", "x0y", "poiapi"}
	for _, s := range valid {
		if !IsValid(s) {
			t.Errorf("IsValid(%q) = false, want true", s)
		}
	}
	invalid := []string{
		"",       // empty
		"a",      // too short (< 2)
		"-ab",    // leading hyphen
		"ab-",    // trailing hyphen
		"fgl_ai", // underscore (not canonical)
		"Fgl",    // uppercase
		"a.b",    // dot
	}
	// ("fgl--ai" is intentionally NOT here: a double hyphen mid-slug is valid per
	// the shape — only the start/end are constrained — it just never arises from
	// Canonical, which collapses runs.)
	for _, s := range invalid {
		if IsValid(s) {
			t.Errorf("IsValid(%q) = true, want false", s)
		}
	}
}

// TestCanonicalOutputIsValidForRealNames guards the property that a normal
// name (start/end alphanumeric) always canonicalizes to a valid slug.
func TestCanonicalOutputIsValidForRealNames(t *testing.T) {
	for _, in := range []string{"fgl_ai_sdk", "Fgl.AI.SDK", "My_Cool.Pkg", "poiapi"} {
		if got := Canonical(in); !IsValid(got) {
			t.Errorf("Canonical(%q) = %q, which is not a valid slug", in, got)
		}
	}
}

// TestSanitize covers deriving a package name from an arbitrary directory name
// (GIS-568): everything outside the slug alphabet folds to a hyphen, and a name
// with nothing usable left returns "" so the caller supplies the fallback.
func TestSanitize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"newproj", "newproj"},
		{"NewProj", "newproj"},
		{"My Project", "my-project"},      // spaces fold to a hyphen, not dropped
		{"fgl_ai.sdk", "fgl-ai-sdk"},      // PEP 503 separators (Canonical)
		{"my  cool   pkg", "my-cool-pkg"}, // runs collapse to one hyphen
		{"--weird--", "weird"},            // leading/trailing hyphens trimmed
		{"...", ""},                       // all separators
		{"!!!", ""},                       // all punctuation
		{"", ""},                          // empty
		{".", ""},                         // the GIS-568 value itself
		{"x", ""},                         // one character is below the 2-char floor
		{"café", "caf"},                   // non-ASCII folds then trims
		{strings.Repeat("a", 80), strings.Repeat("a", 64)},         // truncated
		{strings.Repeat("a", 64) + "-bb", strings.Repeat("a", 64)}, // truncation never leaves a trailing hyphen
	}
	for _, tc := range cases {
		got := Sanitize(tc.in)
		if got != tc.want {
			t.Errorf("Sanitize(%q) = %q, want %q", tc.in, got, tc.want)
		}
		// Whatever comes back must be usable as a package name.
		if got != "" && !IsValid(got) {
			t.Errorf("Sanitize(%q) = %q, which is not a valid slug", tc.in, got)
		}
	}
}
