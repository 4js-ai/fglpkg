package signing

import "testing"

// The GI package-signing roots are trust anchors: dropping or mistyping one
// silently breaks verify-on-install for every client built from that commit,
// and Manifest.Verify fails closed, so the symptom is "nothing verifies"
// rather than a compile error. Pin both explicitly.
func TestGeneroIntelligenceRootsArePinned(t *testing.T) {
	want := map[string]string{
		"root-test-1": "IT1y7PBb9/ZXkbIuWcAPRSANiez/A3yLe9z5ps+DoXk=",
		"root-prod-1": "IHTiMqz3Rx+FI9Y8wVOPO06YT7xmV+V/BqyqvG+/bpQ=",
	}
	got := map[string]string{}
	for _, r := range PinnedRoots() {
		got[r.KeyID] = r.PubB64
	}
	for keyid, pub := range want {
		if got[keyid] != pub {
			t.Errorf("pinned root %q = %q, want %q", keyid, got[keyid], pub)
		}
	}
}
