package cli

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/signing"
)

// GIS-576. `audit signatures` rendered every failure as a bare "✗ ERROR", so an
// artifact whose signing key was minted after it was published looked exactly
// like one whose bytes do not match what was signed. The first is a registry
// key-rollout problem; the second is a tampering question. An operator auditing
// a store could only tell them apart by reading each message.
//
// The window is checked before the bytes are, so ErrKeyExpired on its own says
// nothing about whether the artifact is intact — VerifyArtifact returns before
// it looks. auditOne therefore re-checks the signature against the named key
// (KeyByID ignores the window) before calling it a window failure, so the two
// conditions are reported as what they actually are.

const auditKeyID = "prod-key-1"

// signedFixture returns an artifact, its signature, and a manifest naming the
// key that signed it with the given window.
func signedFixture(t *testing.T, uploadedAt, validFrom, validTo string) (signing.ArtifactFields, string, *signing.Manifest) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	f := signing.ArtifactFields{
		Name: "poiapi", Version: "1.8.0", Variant: "genero6",
		SHA256: "b6e1", Size: 87477, UploadedAt: uploadedAt, Uploader: "partner:fourjs",
	}
	payload, err := signing.CanonicalArtifactPayload(f)
	if err != nil {
		t.Fatal(err)
	}
	m := &signing.Manifest{
		IssuedAt: "2026-09-28T21:42:18.239Z",
		Keys: []signing.Key{{
			KeyID: auditKeyID, Alg: "ed25519",
			Pub:       base64.StdEncoding.EncodeToString(pub),
			ValidFrom: validFrom, ValidTo: validTo,
		}},
	}
	return f, base64.StdEncoding.EncodeToString(ed25519.Sign(priv, payload)), m
}

func runAuditOne(t *testing.T, m *signing.Manifest, f signing.ArtifactFields, sig string) (auditResult, string) {
	t.Helper()
	var got auditResult
	out, err := captureStdout(t, func() error {
		got = auditOne(m, f.Name, f.Version, f.Variant, f.SHA256, f.Size, f.UploadedAt, f.Uploader, auditKeyID, sig)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return got, out
}

// The GIS-576 case: a signature that verifies, on an artifact uploaded before
// the key's window opens. Its own category, and its own marker.
func TestAuditOneReportsAWindowFailureSeparately(t *testing.T) {
	f, sig, m := signedFixture(t,
		"2026-09-04T19:59:47.781Z", // uploaded before...
		"2026-09-28T21:42:18.238Z", // ...the key was minted
		"2027-09-28T21:42:18.238Z")

	got, out := runAuditOne(t, m, f, sig)
	if got != auditOutsideWindow {
		t.Errorf("result = %v, want auditOutsideWindow", got)
	}
	if !strings.Contains(out, "OUTSIDE KEY WINDOW") {
		t.Errorf("line should name the window as the cause:\n%s", out)
	}
	if strings.HasPrefix(strings.TrimSpace(out), "✗") {
		t.Errorf("a window failure must not render as a plain verification failure:\n%s", out)
	}
}

// The distinction has to be earned, not assumed. An artifact that is BOTH
// outside the window and corrupted is a mismatch, not a rollout problem —
// without the secondary byte check, tampering on an old artifact would be
// filed under "registry key-rollout, not evidence the artifact was altered".
func TestAuditOneOutsideWindowWithBadBytesIsAMismatch(t *testing.T) {
	f, sig, m := signedFixture(t,
		"2026-09-04T19:59:47.781Z",
		"2026-09-28T21:42:18.238Z",
		"2027-09-28T21:42:18.238Z")

	// Corrupt the artifact: same signature, different content.
	f.SHA256 = "deadbeef"

	got, out := runAuditOne(t, m, f, sig)
	if got != auditMismatch {
		t.Errorf("result = %v, want auditMismatch — the bytes do not match what was signed", got)
	}
	if strings.Contains(out, "OUTSIDE KEY WINDOW:") {
		t.Errorf("a corrupted artifact must not be excused as a window failure:\n%s", out)
	}
	if !strings.Contains(out, "does not verify") {
		t.Errorf("line should say the signature does not verify:\n%s", out)
	}
}

// A plain mismatch inside the window is unchanged.
func TestAuditOneMismatchInsideWindow(t *testing.T) {
	f, sig, m := signedFixture(t,
		"2026-10-01T00:00:00.000Z",
		"2026-09-28T21:42:18.238Z",
		"2027-09-28T21:42:18.238Z")
	f.SHA256 = "deadbeef"

	got, out := runAuditOne(t, m, f, sig)
	if got != auditMismatch {
		t.Errorf("result = %v, want auditMismatch", got)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "✗") {
		t.Errorf("a mismatch should render as a failure:\n%s", out)
	}
}

// A good artifact inside the window still passes.
func TestAuditOneVerifies(t *testing.T) {
	f, sig, m := signedFixture(t,
		"2026-10-01T00:00:00.000Z",
		"2026-09-28T21:42:18.238Z",
		"2027-09-28T21:42:18.238Z")

	got, out := runAuditOne(t, m, f, sig)
	if got != auditOK {
		t.Errorf("result = %v, want auditOK", got)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "✓") {
		t.Errorf("a verified artifact should render as a pass:\n%s", out)
	}
}

// An entry with no signature recorded is its own category too.
func TestAuditOneMissingSignature(t *testing.T) {
	f, _, m := signedFixture(t,
		"2026-10-01T00:00:00.000Z",
		"2026-09-28T21:42:18.238Z",
		"2027-09-28T21:42:18.238Z")

	var got auditResult
	out, err := captureStdout(t, func() error {
		got = auditOne(m, f.Name, f.Version, f.Variant, f.SHA256, f.Size, f.UploadedAt, f.Uploader, "", "")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != auditMissing {
		t.Errorf("result = %v, want auditMissing", got)
	}
	if !strings.Contains(out, "signature missing") {
		t.Errorf("line should say the signature is missing:\n%s", out)
	}
}

// The summary names which kinds of failure occurred, not just how many — the
// whole point is that "3 failed" hid the difference between a rollout problem
// and a tampered artifact.
func TestAuditBreakdownNamesEachCategory(t *testing.T) {
	got := auditBreakdown(map[auditResult]int{
		auditOK:            4,
		auditMismatch:      1,
		auditMissing:       2,
		auditOutsideWindow: 3,
	})
	for _, want := range []string{
		"1 signature does not verify",
		"2 unsigned",
		"3 outside the key's validity window",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("breakdown %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "4") {
		t.Errorf("breakdown should not mention passing packages: %q", got)
	}
}

// A category with no members is left out rather than printed as zero.
func TestAuditBreakdownOmitsEmptyCategories(t *testing.T) {
	got := auditBreakdown(map[auditResult]int{auditOutsideWindow: 2})
	if got != "2 outside the key's validity window" {
		t.Errorf("breakdown = %q, want only the non-empty category", got)
	}
}
