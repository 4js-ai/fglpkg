package manifest

import (
	"strings"
	"testing"
)

// TestProfileRejectsUnsafePaths: `profile` entries are joined onto a package's
// store directory at env time, so an absolute or escaping path must be refused
// at the manifest layer rather than reaching a consumer's FGLPROFILE.
func TestProfileRejectsUnsafePaths(t *testing.T) {
	cases := []struct {
		name  string
		entry string
		want  string
	}{
		{"escaping", "../../etc/passwd", "must not escape the package root"},
		{"empty", "", "must not be empty"},
		{"rooted", "/etc/passwd", "must be relative"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manifest{Name: "pkg", Version: "1.0.0", Profile: []string{tc.entry}}
			err := m.Validate()
			if err == nil {
				t.Fatalf("expected %q to be rejected", tc.entry)
			}
			if !strings.Contains(err.Error(), "profile[0]") {
				t.Errorf("error should name the offending entry as profile[0]; got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error should explain %q; got: %v", tc.want, err)
			}
		})
	}
}

// TestProfileAcceptsRelativePath: the ordinary case still validates.
func TestProfileAcceptsRelativePath(t *testing.T) {
	m := &Manifest{Name: "pkg", Version: "1.0.0", Profile: []string{"profiles/app.4gp"}}
	if err := m.Validate(); err != nil {
		t.Fatalf("a relative profile path should validate: %v", err)
	}
}

// TestLintWarnsDuplicateProfile: a repeated entry stages the same file twice
// and lists it twice on FGLPROFILE — harmless but always a mistake.
func TestLintWarnsDuplicateProfile(t *testing.T) {
	m := &Manifest{
		Name:    "pkg",
		Version: "1.0.0",
		Profile: []string{"profiles/app.4gp", "profiles/app.4gp"},
	}
	var r Report
	m.LintInto(&r)

	if !hasProfileWarning(&r, "profiles/app.4gp") {
		t.Errorf("expected a duplicate-profile warning; got %+v", r.Warnings())
	}
}

// TestLintWarnsDuplicateProfileAcrossSpellings: two spellings of one path are a
// duplicate to pack (both stage to the same archive entry and land on
// FGLPROFILE twice), so exact-string comparison would miss a real mistake. The
// warning reports the canonical spelling.
func TestLintWarnsDuplicateProfileAcrossSpellings(t *testing.T) {
	m := &Manifest{
		Name:    "pkg",
		Version: "1.0.0",
		Profile: []string{"profiles/app.4gp", "./profiles/app.4gp"},
	}
	var r Report
	m.LintInto(&r)

	if !hasProfileWarning(&r, "profiles/app.4gp") {
		t.Errorf("expected a duplicate-profile warning for differing spellings; got %+v", r.Warnings())
	}
}

// hasProfileWarning reports whether the lint report carries a `profile` warning
// mentioning want.
func hasProfileWarning(r *Report, want string) bool {
	for _, d := range r.Warnings() {
		if d.Field == "profile" && strings.Contains(d.Message, want) {
			return true
		}
	}
	return false
}
