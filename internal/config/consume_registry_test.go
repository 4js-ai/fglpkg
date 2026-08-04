package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGlobalConsumeRegistry_RoundTrip verifies the global config.json accepts a
// defaultConsumeRegistry key (it must, or DisallowUnknownFields would reject the
// whole file) and that GlobalConsumeRegistry reads it back (GIS-364). The
// registries and the publish default in the same file must still load — the new
// field is additive.
func TestGlobalConsumeRegistry_RoundTrip(t *testing.T) {
	home := t.TempDir()
	body := `{
	  "registries": [
	    {"name": "acme", "type": "artifactory", "url": "https://art.acme.example/artifactory", "repoKey": "fgl", "priority": 2}
	  ],
	  "defaultRegistry": "acme",
	  "defaultConsumeRegistry": "acme"
	}`
	if err := os.WriteFile(filepath.Join(home, GlobalFilename), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := GlobalConsumeRegistry(home)
	if err != nil {
		t.Fatalf("GlobalConsumeRegistry: %v", err)
	}
	if got != "acme" {
		t.Errorf("defaultConsumeRegistry = %q, want acme", got)
	}
	if v, err := GlobalDefaultRegistry(home); err != nil || v != "acme" {
		t.Errorf("publish default alongside consume default = (%q, %v), want (acme, nil)", v, err)
	}
	if regs, err := LoadGlobal(home); err != nil || len(regs) != 1 || regs[0].Name != "acme" {
		t.Errorf("registries alongside the defaults = %+v (err %v), want [acme]", regs, err)
	}
}

// TestGlobalConsumeRegistry_Absent confirms a config without the key (or no file
// at all) yields "" and no error — the default is opt-in, and its absence means
// "consult every configured repository".
func TestGlobalConsumeRegistry_Absent(t *testing.T) {
	home := t.TempDir()
	if v, err := GlobalConsumeRegistry(home); err != nil || v != "" {
		t.Fatalf("missing file: got (%q, %v), want (empty, nil)", v, err)
	}
	if err := os.WriteFile(filepath.Join(home, GlobalFilename), []byte(`{"registries": []}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if v, err := GlobalConsumeRegistry(home); err != nil || v != "" {
		t.Fatalf("no key: got (%q, %v), want (empty, nil)", v, err)
	}
}

// The publish and consume defaults are independent fields: setting one must not
// imply the other. This is the property that keeps existing publish-only setups
// from silently gaining scoped installs.
func TestGlobalDefaults_AreIndependent(t *testing.T) {
	home := t.TempDir()
	body := `{"defaultRegistry": "publishrepo"}`
	if err := os.WriteFile(filepath.Join(home, GlobalFilename), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if v, _ := GlobalDefaultRegistry(home); v != "publishrepo" {
		t.Errorf("publish default = %q, want publishrepo", v)
	}
	if v, _ := GlobalConsumeRegistry(home); v != "" {
		t.Errorf("consume default = %q, want empty (publish-only config)", v)
	}
}
