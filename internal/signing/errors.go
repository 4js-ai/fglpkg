// Package signing verifies Ed25519 signatures over fglpkg registry artifacts
// and release binaries, using the two-tier (offline root -> working key) model
// shared with the Genero Intelligence (GI) registry.
//
// The wire format is frozen by the live GI registry, so this package MUST match
// it byte for byte; the golden vector in signing_test.go is the conformance
// gate:
//
//   - Canonicalization: RFC 8785 (JCS). The signing input is the canonical
//     UTF-8 bytes directly — Ed25519 is not pre-hashed.
//   - Public keys: standard base64 (padded) of the raw 32-byte Ed25519 key.
//   - Signatures:  standard base64 (padded) of the raw 64-byte Ed25519 sig.
//   - Key manifest (keys.json): {issuedAt, keys, sig}; the offline root signs
//     the JCS of {issuedAt, keys} (the manifest minus its own sig block).
//
// Every signing input in fglpkg is, by design, a JSON object of strings plus at
// most one integer (an artifact's byte size) — never a float. That is why the
// small in-house JCS canonicalizer (jcs.go) is sufficient and the module stays
// dependency-free; a non-integer number is rejected (fail-closed) rather than
// risk diverging from the signer over ECMAScript number formatting.
//
// Two independent trust anchors are pinned (root.go): the fglpkg release-signing
// root (GIS-255 self-update) and the GI package-signing root (GIS-244
// verify-on-install). They are deliberately different keys so a compromise of
// one trust domain cannot forge the other.
package signing

import (
	"errors"
	"fmt"
)

var (
	// ErrManifestUnverified means keys.json did not verify against any pinned
	// root public key — the trust anchor. Keys from such a manifest are never
	// trusted.
	ErrManifestUnverified = errors.New("signing: keys manifest failed to verify against the pinned root")

	// ErrKeyUnknown means the signature's keyid is not present in the current
	// (verified) manifest.
	ErrKeyUnknown = errors.New("signing: signing key id not found in the current keys manifest")

	// ErrKeyExpired means the signing key exists but its validity window does
	// not cover the moment the artifact was signed.
	ErrKeyExpired = errors.New("signing: signing key is outside its validity window")

	// ErrKeyNotYetValid is the lower-bound half of ErrKeyExpired: the artifact
	// was uploaded BEFORE the key's validFrom. It wraps ErrKeyExpired, so
	// errors.Is(err, ErrKeyExpired) still matches and callers that do not care
	// about the distinction are unaffected.
	//
	// The two halves mean opposite things and the split exists so a caller can
	// say which it has. Below validFrom is the backfill case (GIS-576): the
	// registry attested to an artifact older than the key, which is a
	// key-rollout problem for the registry operator. Above validTo means
	// something signed with a RETIRED key — the condition validTo exists to
	// catch — and must never be reported as the benign one.
	ErrKeyNotYetValid = fmt.Errorf("%w (key not yet valid at that time)", ErrKeyExpired)

	// ErrBadUploadTime means the artifact's recorded upload time could not be
	// parsed, so no window comparison was possible. Deliberately NOT an
	// ErrKeyExpired: nothing is known about the window either way, and
	// reporting it as a window problem invites the wrong diagnosis.
	ErrBadUploadTime = errors.New("signing: cannot parse the artifact's upload time")

	// ErrSignatureMismatch means the Ed25519 signature did not verify against
	// the resolved public key over the canonical payload.
	ErrSignatureMismatch = errors.New("signing: signature does not match")

	// ErrUnsigned means an artifact record carries no signature at all. Whether
	// this is fatal is the caller's decision (the install enforce mode): under
	// "warn" it is logged and skipped, under "require" it aborts the install.
	ErrUnsigned = errors.New("signing: artifact is not signed")
)
