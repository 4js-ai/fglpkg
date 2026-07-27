package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGlobalMavenMirror_RoundTrip verifies the global config.json accepts a
// mavenMirror block (it must, or DisallowUnknownFields would reject the whole
// file) and that GlobalMavenMirror reads it back (GIS-365). It also confirms
// registries in the same file still load — the new field is additive.
func TestGlobalMavenMirror_RoundTrip(t *testing.T) {
	home := t.TempDir()
	body := `{
	  "registries": [
	    {"name": "acme", "type": "artifactory", "url": "https://art.acme.example/artifactory", "repoKey": "fgl", "priority": 2}
	  ],
	  "mavenMirror": {"url": "https://art.acme.example/artifactory/libs-release", "auth": "basic"}
	}`
	if err := os.WriteFile(filepath.Join(home, GlobalFilename), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	mm, err := GlobalMavenMirror(home)
	if err != nil {
		t.Fatalf("GlobalMavenMirror: %v", err)
	}
	if mm == nil {
		t.Fatal("expected a mavenMirror, got nil")
	}
	if mm.URL != "https://art.acme.example/artifactory/libs-release" || mm.Auth != AuthBasic {
		t.Errorf("mavenMirror = %+v, want url libs-release + auth basic", mm)
	}

	regs, err := LoadGlobal(home)
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if len(regs) != 1 || regs[0].Name != "acme" {
		t.Errorf("registries alongside mavenMirror = %+v, want [acme]", regs)
	}
}

// TestGlobalMavenMirror_Absent confirms a config without mavenMirror (or no
// file at all) yields nil, not an error — the mirror is opt-in.
func TestGlobalMavenMirror_Absent(t *testing.T) {
	home := t.TempDir()
	// No file at all.
	if mm, err := GlobalMavenMirror(home); err != nil || mm != nil {
		t.Fatalf("missing file: got (%+v, %v), want (nil, nil)", mm, err)
	}
	// File present but no mavenMirror key.
	if err := os.WriteFile(filepath.Join(home, GlobalFilename), []byte(`{"registries": []}`), 0644); err != nil {
		t.Fatal(err)
	}
	if mm, err := GlobalMavenMirror(home); err != nil || mm != nil {
		t.Fatalf("no mavenMirror key: got (%+v, %v), want (nil, nil)", mm, err)
	}
}
