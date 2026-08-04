package manifest

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestManifest_DefaultConsumeRegistryRoundTrip confirms fglpkg.json accepts the
// consume default (GIS-364) alongside the publish default, keeps them distinct,
// and omits the key when unset so an untouched manifest is never reformatted.
func TestManifest_DefaultConsumeRegistryRoundTrip(t *testing.T) {
	body := `{
	  "name": "demo", "version": "1.0.0",
	  "dependencies": {},
	  "registries": [
	    {"name":"acme","type":"artifactory","url":"https://a/artifactory","repoKey":"k","priority":2}
	  ],
	  "defaultRegistry": "acme",
	  "defaultConsumeRegistry": "acme"
	}`
	var m Manifest
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.DefaultConsumeRegistry != "acme" {
		t.Fatalf("defaultConsumeRegistry = %q, want acme", m.DefaultConsumeRegistry)
	}
	if m.DefaultRegistry != "acme" {
		t.Fatalf("defaultRegistry = %q, want acme", m.DefaultRegistry)
	}
	out, err := json.Marshal(&m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"defaultConsumeRegistry"`) {
		t.Fatalf("defaultConsumeRegistry dropped on marshal: %s", out)
	}

	// Unset → omitted entirely (omitempty), so `registry add`'s read-modify-write
	// cannot inject an empty setting into a hand-maintained manifest.
	var bare Manifest
	if err := json.Unmarshal([]byte(`{"name":"demo","version":"1.0.0"}`), &bare); err != nil {
		t.Fatalf("unmarshal bare: %v", err)
	}
	out, err = json.Marshal(&bare)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "defaultConsumeRegistry") {
		t.Fatalf("unset default should be omitted: %s", out)
	}
}

// TestPublishCopyDropsConsumeDefault: the consume default is the *consumer's*
// source policy. Shipping it in the published sidecar would let a dependency
// dictate where its consumer resolves packages from, so PublishCopy strips it —
// the same reasoning that strips the publish default.
func TestPublishCopyDropsConsumeDefault(t *testing.T) {
	m := New("demo", "1.0.0", "", "")
	m.DefaultConsumeRegistry = "acme"
	m.DefaultRegistry = "acme"

	clone := m.PublishCopy()
	if clone.DefaultConsumeRegistry != "" {
		t.Errorf("PublishCopy kept defaultConsumeRegistry = %q", clone.DefaultConsumeRegistry)
	}
	if clone.DefaultRegistry != "" {
		t.Errorf("PublishCopy kept defaultRegistry = %q", clone.DefaultRegistry)
	}
	// The receiver must be untouched — pack/publish reuse it afterwards.
	if m.DefaultConsumeRegistry != "acme" {
		t.Errorf("PublishCopy mutated the receiver: %q", m.DefaultConsumeRegistry)
	}
}
