package cli

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
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
	if !strings.Contains(out, "KEY NOT YET VALID") {
		t.Errorf("line should name the cause as the key not yet being valid:\n%s", out)
	}
	if strings.HasPrefix(strings.TrimSpace(out), "✗") {
		t.Errorf("a backfill window failure must not render as a plain verification failure:\n%s", out)
	}
}

// The other side of the window is the opposite finding. An artifact uploaded
// AFTER the key's validTo was signed with a retired key — the condition validTo
// exists to catch — so it must never collect the benign "the registry just
// minted the key late" diagnosis. Both halves share ErrKeyExpired, so without
// the split this landed in the backfill category and was explained away.
func TestAuditOneRetiredKeyIsNotTreatedAsBackfill(t *testing.T) {
	f, sig, m := signedFixture(t,
		"2028-01-15T10:00:00.000Z", // uploaded well after...
		"2026-09-28T21:42:18.238Z",
		"2027-09-28T21:42:18.238Z") // ...the key was retired

	got, out := runAuditOne(t, m, f, sig)
	if got == auditOutsideWindow {
		t.Fatal("a retired-key signature must not be filed as the backfill case")
	}
	if got != auditKeyRetired {
		t.Errorf("result = %v, want auditKeyRetired", got)
	}
	if !strings.Contains(out, "RETIRED KEY") {
		t.Errorf("line should name the retired key:\n%s", out)
	}
	if strings.Contains(out, "NOT YET VALID") {
		t.Errorf("a retired key is not a not-yet-valid key:\n%s", out)
	}
}

// An upload time that cannot be parsed tells us nothing about the window, so it
// is not a window finding. It used to be wrapped as ErrKeyExpired, which put an
// unreadable timestamp under the "key minted late, harmless" explanation.
func TestAuditOneUnparseableUploadTimeIsNotAWindowProblem(t *testing.T) {
	f, sig, m := signedFixture(t,
		"2026-10-01T00:00:00+0000", // inside the window, but not RFC 3339
		"2026-09-28T21:42:18.238Z",
		"2027-09-28T21:42:18.238Z")

	got, out := runAuditOne(t, m, f, sig)
	if got == auditOutsideWindow || got == auditKeyRetired {
		t.Fatalf("an unreadable upload time is not a window finding, got %v", got)
	}
	if got != auditMismatch {
		t.Errorf("result = %v, want auditMismatch", got)
	}
	if strings.Contains(out, "NOT YET VALID") || strings.Contains(out, "RETIRED KEY") {
		t.Errorf("must not diagnose a window problem it could not test for:\n%s", out)
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
	if strings.Contains(out, "KEY NOT YET VALID") {
		t.Errorf("a corrupted artifact must not be excused as a window failure:\n%s", out)
	}
	// Deliberately the marker, not the wording: which of the two failures is
	// named depends on whether the window or the bytes are checked first, and
	// the result assertion above already pins the classification.
	if !strings.HasPrefix(strings.TrimSpace(out), "✗") {
		t.Errorf("line should render as a failure:\n%s", out)
	}
}

// The retired side needs the same guard. Moving the retired branch above the
// byte re-check would render a tampered post-validTo artifact as
// "✗ RETIRED KEY", under a note that opens "The signature verifies" — which it
// does not. Every other test stays green under that mutation.
func TestAuditOneRetiredKeyWithBadBytesIsAMismatch(t *testing.T) {
	f, sig, m := signedFixture(t,
		"2028-01-15T10:00:00.000Z", // after validTo
		"2026-09-28T21:42:18.238Z",
		"2027-09-28T21:42:18.238Z")

	f.SHA256 = "deadbeef" // ...and the artifact is not what was signed

	got, out := runAuditOne(t, m, f, sig)
	if got == auditKeyRetired {
		t.Fatal("a tampered artifact must not be filed as a retired-key finding — that note claims the signature verifies")
	}
	if got != auditMismatch {
		t.Errorf("result = %v, want auditMismatch", got)
	}
	if strings.Contains(out, "RETIRED KEY") {
		t.Errorf("must not render as a retired-key finding:\n%s", out)
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
		auditKeyRetired:    5,
	})
	for _, want := range []string{
		"1 failed verification",
		"2 unsigned",
		"3 uploaded before the key was valid",
		"5 signed with a retired key",
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
	if got != "2 uploaded before the key was valid" {
		t.Errorf("breakdown = %q, want only the non-empty category", got)
	}
}

// ── the command's verdict ────────────────────────────────────────────────────
//
// `audit signatures` is documented as a CI gate, and the invariant that makes it
// one is that EVERY non-OK category exits 1. `install` under
// FGLPKG_SIGNING=require refuses all of them, so a gate that passed what install
// refuses would be the same drift as `outdated` offering a version `update` will
// not select.
//
// Until these tests the invariant was unpinned: cmdAuditSignatures had no test
// at all (functional case 150 skips it for want of signed fixtures), so dropping
// a category from the failure count reddened nothing. The tempting change — "a
// not-yet-valid key is benign, stop failing CI on it" — is exactly that.

func verdict(t *testing.T, counts map[auditResult]int, total int) (error, string) {
	t.Helper()
	var b strings.Builder
	return auditVerdict(&b, counts, total), b.String()
}

// Every failing category on its own must still exit 1.
func TestAuditVerdictFailsForEveryCategory(t *testing.T) {
	for name, r := range map[string]auditResult{
		"not yet valid": auditOutsideWindow,
		"retired key":   auditKeyRetired,
		"unsigned":      auditMissing,
		"mismatch":      auditMismatch,
	} {
		t.Run(name, func(t *testing.T) {
			err, _ := verdict(t, map[auditResult]int{auditOK: 2, r: 1}, 3)
			if err == nil {
				t.Fatalf("%s must fail the audit — install under require refuses it", name)
			}
			var ee *ExitError
			if !errors.As(err, &ee) || ee.Code != 1 {
				t.Errorf("want ExitError{Code:1}, got %#v", err)
			}
		})
	}
}

// The rollout note belongs only to the not-yet-valid category, and must not
// appear for a retired key — that was the misdiagnosis of the previous round.
func TestAuditVerdictNotesMatchTheCategory(t *testing.T) {
	_, out := verdict(t, map[auditResult]int{auditOutsideWindow: 1}, 1)
	if !strings.Contains(out, "re-issuing the keys manifest") {
		t.Errorf("not-yet-valid should carry the rollout note:\n%s", out)
	}
	if strings.Contains(out, "retired") {
		t.Errorf("not-yet-valid must not carry the retired-key warning:\n%s", out)
	}

	_, out = verdict(t, map[auditResult]int{auditKeyRetired: 1}, 1)
	if !strings.Contains(out, "retired") {
		t.Errorf("retired key should carry its own warning:\n%s", out)
	}
	if strings.Contains(out, "not evidence the artifact was altered") {
		t.Errorf("a retired key must never be explained away as benign:\n%s", out)
	}
	if strings.Contains(out, "re-issuing the keys manifest") {
		t.Errorf("re-issuing the manifest does not fix a retired-key signature:\n%s", out)
	}
}

// An all-clear reports success and exits 0.
func TestAuditVerdictPassesWhenEverythingVerifies(t *testing.T) {
	err, out := verdict(t, map[auditResult]int{auditOK: 3}, 3)
	if err != nil {
		t.Fatalf("all-verified must exit 0, got: %v", err)
	}
	if !strings.Contains(out, "All 3 package signatures verified.") {
		t.Errorf("expected the all-clear line:\n%s", out)
	}
}

// An empty lock file is not a failure — there is nothing to attest to.
func TestAuditVerdictEmptyLock(t *testing.T) {
	err, out := verdict(t, map[auditResult]int{}, 0)
	if err != nil {
		t.Fatalf("an empty lock must not fail the audit, got: %v", err)
	}
	if !strings.Contains(out, "No packages") {
		t.Errorf("expected the empty-lock line:\n%s", out)
	}
}

// The summary names the categories, so a CI log says which kind of failure
// occurred without the operator reading every line.
func TestAuditVerdictSummaryNamesTheCategories(t *testing.T) {
	err, _ := verdict(t, map[auditResult]int{auditOK: 1, auditOutsideWindow: 2, auditMismatch: 1}, 4)
	if err == nil {
		t.Fatal("expected a failure")
	}
	msg := err.Error()
	for _, want := range []string{"3 of 4", "2 uploaded before the key was valid", "1 failed verification"} {
		if !strings.Contains(msg, want) {
			t.Errorf("summary %q missing %q", msg, want)
		}
	}
}
