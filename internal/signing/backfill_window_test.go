package signing

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"testing"
)

// GIS-576. The registry signs backfilled historical artifacts with the current
// working key but keeps uploaded_at at the artifact's original created_at, and
// VerifyArtifact checks that timestamp against the key's window. prod-key-1 was
// minted with validFrom = 2026-09-28T21:42:18.238Z, after most of the corpus had
// been uploaded, so 11 of 20 production artifacts failed with ErrKeyExpired —
// warning noise under the default "warn", and nothing installable under
// "require".
//
// The agreed fix is server-side and needs no client change: re-issue the keys
// manifest with validFrom widened to cover the corpus the key actually attests
// to, keeping the same public key so existing signatures still verify
// (scripts/gen-signing-key.mjs reissue, in the genero-intelligence repo).
//
// These tests pin the two client properties that fix depends on, neither of
// which is obvious from the code:
//
//  1. A key window is honoured as written. Nothing requires validFrom to relate
//     to the manifest's issuedAt, so a window that opens before the key was
//     minted is accepted. Someone "hardening" the client by rejecting
//     validFrom < issuedAt would silently undo the fix and put the registry
//     back where it started.
//
//  2. Widening the window changes nothing about the signature itself. The
//     window is not part of the signed payload, so the same signature verifies
//     under both the old and new manifest. That is what makes re-issuing safe
//     without touching a single artifact.

// backfillUpload is poiapi 1.8.0 genero6's real upload time on the production
// registry — the oldest of the 11 artifacts GIS-576 affects.
const backfillUpload = "2026-09-04T19:59:47.781Z"

// backfillFixture returns an artifact uploaded at uploadedAt, signed over that
// exact value, plus the key that signed it — poiapi 1.8.0 genero6, the oldest of
// the 11 affected production artifacts.
//
// uploadedAt is a parameter rather than a constant the caller edits afterwards:
// mutating the field post-signing silently invalidates the signature, so such a
// test passes only while the window happens to be checked before the bytes, and
// would break the moment that order changed for unrelated reasons.
func backfillFixture(t *testing.T, uploadedAt string) (ArtifactFields, ArtifactSignature, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	f := ArtifactFields{
		Name:       "poiapi",
		Version:    "1.8.0",
		Variant:    "genero6",
		SHA256:     "b6e1",
		Size:       87477,
		UploadedAt: uploadedAt,
		Uploader:   "partner:fourjs",
	}
	payload, err := CanonicalArtifactPayload(f)
	if err != nil {
		t.Fatal(err)
	}
	sig := ArtifactSignature{
		KeyID: "prod-key-1",
		Alg:   "ed25519",
		Sig:   base64.StdEncoding.EncodeToString(ed25519.Sign(priv, payload)),
	}
	return f, sig, base64.StdEncoding.EncodeToString(pub)
}

func manifestWithWindow(pub, validFrom, validTo string) *Manifest {
	return &Manifest{
		IssuedAt: "2026-09-28T21:42:18.239Z",
		Keys: []Key{{
			KeyID: "prod-key-1", Alg: "ed25519", Pub: pub,
			ValidFrom: validFrom, ValidTo: validTo,
		}},
	}
}

// The bug as it stands in production: a validly-signed artifact is refused
// purely because it was uploaded before the key was minted.
func TestBackfilledArtifactFailsTheMintedWindow(t *testing.T) {
	f, sig, pub := backfillFixture(t, backfillUpload)
	m := manifestWithWindow(pub, "2026-09-28T21:42:18.238Z", "2027-09-28T21:42:18.238Z")

	err := m.VerifyArtifact(f, sig)
	if err == nil {
		t.Fatal("expected the window check to refuse an artifact uploaded before validFrom")
	}
	if !errors.Is(err, ErrKeyExpired) {
		t.Errorf("want ErrKeyExpired, got: %v", err)
	}
}

// The fix: the same signature, the same key, a widened window — and it verifies.
// Nothing about the artifact changed, which is the point: re-issuing the
// manifest repairs the whole backfilled corpus without rewriting any of it.
func TestWideningTheWindowAcceptsTheSameSignature(t *testing.T) {
	f, sig, pub := backfillFixture(t, backfillUpload)
	m := manifestWithWindow(pub, "2026-09-01T00:00:00.000Z", "2027-09-28T21:42:18.238Z")

	if err := m.VerifyArtifact(f, sig); err != nil {
		t.Fatalf("a widened window must accept the artifact it now covers: %v", err)
	}
}

// A window that opens before the manifest was issued is legitimate and must stay
// so: it is how the registry states that a key attests to artifacts older than
// itself.
//
// Scope, so this is not read as more than it is: it builds a Manifest directly
// and calls SelectKey, so it only guards against such a check being added
// THERE. A check added in ParseManifest or Verify — arguably the more natural
// place — would be caught instead by the existing manifest fixtures, which
// already carry a validFrom months before their issuedAt (the pinned keys.json,
// and the cache_test / signing_test fixtures).
func TestValidFromMayPrecedeIssuedAt(t *testing.T) {
	_, _, pub := backfillFixture(t, backfillUpload)
	m := manifestWithWindow(pub, "2026-09-01T00:00:00.000Z", "2027-09-28T21:42:18.238Z")
	if m.IssuedAt <= m.Keys[0].ValidFrom {
		t.Fatalf("fixture is wrong: issuedAt %q should be after validFrom %q", m.IssuedAt, m.Keys[0].ValidFrom)
	}

	at, err := parseTimestamp("2026-09-04T19:59:47.781Z")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.SelectKey("prod-key-1", at); err != nil {
		t.Errorf("a validFrom earlier than issuedAt must be honoured as written: %v", err)
	}
}

// Widening is not a licence to accept anything: an artifact older than the
// widened window is still refused. Without this, the two tests above would also
// pass for a client that had stopped checking the lower bound altogether.
func TestWideningStillRefusesWhatItDoesNotCover(t *testing.T) {
	// Signed over this upload time, so the signature is valid and the window is
	// the only thing that can refuse it. Mutating UploadedAt after signing would
	// invalidate the signature, and the test would then pass only for as long as
	// the window happens to be checked before the bytes.
	f, sig, pub := backfillFixture(t, "2026-08-01T00:00:00.000Z")
	m := manifestWithWindow(pub, "2026-09-01T00:00:00.000Z", "2027-09-28T21:42:18.238Z")

	err := m.VerifyArtifact(f, sig)
	if err == nil {
		t.Fatal("an artifact outside even the widened window must still be refused")
	}
	if !errors.Is(err, ErrKeyExpired) {
		t.Errorf("want ErrKeyExpired, got: %v", err)
	}
	// Specifically the lower bound, not just "some window error".
	if !errors.Is(err, ErrKeyNotYetValid) {
		t.Errorf("want ErrKeyNotYetValid for an upload before validFrom, got: %v", err)
	}
}
