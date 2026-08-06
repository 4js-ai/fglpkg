package config

import (
	"strings"
	"testing"
)

// artiReg is a well-formed artifactory registry for the lint tests.
func artiReg(name string, priority int) Registry {
	return Registry{Name: name, Type: TypeArtifactory, URL: "https://" + name + ".example/artifactory", RepoKey: "K", Priority: priority}
}

// LintProjectRouting validates the checked-in project layer in isolation
// (GIS-368): malformed registries are errors; a default that names a registry the
// project does not declare is a warning (it may resolve only via the user's
// global config).
func TestLintProjectRouting(t *testing.T) {
	cases := []struct {
		name       string
		registries []Registry
		defReg     string
		defConsume string
		mirror     *MavenMirror
		wantErr    []string // substrings expected among error findings
		wantWarn   []string // substrings expected among warning findings
		wantClean  bool     // no findings at all
	}{
		{
			name:       "valid registry + matching consume default is clean",
			registries: []Registry{artiReg("acme", 2)},
			defConsume: "acme",
			wantClean:  true,
		},
		{
			name:      "default naming the built-in gi is clean",
			defReg:    "gi",
			wantClean: true,
		},
		{
			name:       "unknown registry type is an error",
			registries: []Registry{{Name: "acme", Type: "bogus", URL: "https://x", Priority: 2}},
			wantErr:    []string{"unknown type"},
		},
		{
			name:       "artifactory without repoKey is an error",
			registries: []Registry{{Name: "acme", Type: TypeArtifactory, URL: "https://x", Priority: 2}},
			wantErr:    []string{"repoKey"},
		},
		{
			name:       "duplicate priority within the project is an error",
			registries: []Registry{artiReg("a", 2), artiReg("b", 2)},
			wantErr:    []string{"share priority"},
		},
		{
			// The loader adds the built-in gi at priority 1, so a project registry
			// that also claims priority 1 hard-fails load — lint must catch it.
			name:       "project registry at priority 1 collides with the built-in gi",
			registries: []Registry{artiReg("acme", 1)},
			wantErr:    []string{"share priority", "gi"},
		},
		{
			name:       "redeclaring the built-in gi is an error",
			registries: []Registry{{Name: "gi", Type: TypeGenero, URL: "https://x", Priority: 3}},
			wantErr:    []string{"built-in registry"},
		},
		{
			name:       "dangling defaultRegistry is a warning",
			registries: []Registry{artiReg("acme", 2)},
			defReg:     "ghost",
			wantWarn:   []string{"defaultRegistry", "ghost", "not declared"},
		},
		{
			name:       "dangling defaultConsumeRegistry is a warning",
			registries: []Registry{artiReg("acme", 2)},
			defConsume: "ghost",
			wantWarn:   []string{"defaultConsumeRegistry", "ghost"},
		},
		{
			name:     "mavenMirror with no url warns it is ignored",
			mirror:   &MavenMirror{Auth: AuthBearer},
			wantWarn: []string{"mavenMirror", "no 'url'"},
		},
		{
			name:     "mavenMirror non-http url warns",
			mirror:   &MavenMirror{URL: "ftp://mirror.example/maven"},
			wantWarn: []string{"http(s)"},
		},
		{
			name:    "mavenMirror unknown auth is an error",
			mirror:  &MavenMirror{URL: "https://mirror.example/maven", Auth: "weird"},
			wantErr: []string{"unknown"},
		},
		{
			name:      "valid mavenMirror is clean",
			mirror:    &MavenMirror{URL: "https://mirror.example/maven", Auth: AuthBearer},
			wantClean: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := LintProjectRouting(tc.registries, tc.defReg, tc.defConsume, tc.mirror)
			var errs, warns []string
			for _, d := range got {
				if d.Warning {
					warns = append(warns, d.Message)
				} else {
					errs = append(errs, d.Message)
				}
			}
			if tc.wantClean && len(got) != 0 {
				t.Fatalf("expected no findings, got %+v", got)
			}
			for _, want := range tc.wantErr {
				if !containsAny(errs, want) {
					t.Errorf("missing error substring %q; errors = %v", want, errs)
				}
			}
			for _, want := range tc.wantWarn {
				if !containsAny(warns, want) {
					t.Errorf("missing warning substring %q; warnings = %v", want, warns)
				}
			}
			if len(tc.wantErr) > 0 && len(errs) == 0 {
				t.Errorf("expected an error finding, got none")
			}
		})
	}
}

func containsAny(msgs []string, sub string) bool {
	for _, m := range msgs {
		if strings.Contains(m, sub) {
			return true
		}
	}
	return false
}
