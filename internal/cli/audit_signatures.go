package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
	"github.com/4js-mikefolcher/fglpkg/internal/signing"
)

// auditResult is one locked entry's outcome.
//
// All four non-OK results are failures and all count toward the non-zero exit:
// `install` under FGLPKG_SIGNING=require refuses every one of them, and an audit
// that passed what install refuses would be worse than one that did not
// distinguish them at all.
//
// They are reported separately because they demand different responses, and
// the two sides of the key's validity window demand opposite ones. Below
// validFrom the signature verifies and the registry simply minted the key after
// the artifact was published — a key-rollout matter for the registry operator
// (GIS-576). Above validTo the signature also verifies, but something signed
// with a RETIRED key, which is the condition validTo exists to catch and must
// never collect the benign explanation. A mismatch means the bytes are not what
// was signed, which is a tampering question. Rendering all of these as a bare
// "✗ ERROR" left the operator to tell them apart by reading each message.
type auditResult int

const (
	auditOK auditResult = iota
	auditMissing       // no signature recorded at all
	auditOutsideWindow // uploaded BEFORE the key's validFrom, signature otherwise valid
	auditKeyRetired    // uploaded AFTER the key's validTo — signed with a retired key
	auditMismatch      // the signature does not verify, its key is unknown, or the time is unreadable
)

// cmdAuditSignatures implements `fglpkg audit signatures`: it walks the lock
// file and re-verifies the Layer 1 registry signature of every entry against
// the current keys manifest, printing one line per package and exiting
// non-zero if anything is missing or fails to verify (for CI use).
//
// Exit codes mirror `fglpkg audit`:
//
//	0  every locked package has a valid signature
//	1  at least one package is unsigned or fails verification
//	2  the audit itself failed (missing lockfile, unverifiable manifest, …)
func cmdAuditSignatures(args []string) error {
	if len(args) > 0 {
		return &ExitError{Code: 2, Err: fmt.Errorf("unknown argument %q", args[0])}
	}

	projectDir, err := os.Getwd()
	if err != nil {
		return &ExitError{Code: 2, Err: fmt.Errorf("cannot determine working directory: %w", err)}
	}
	if !lockfile.Exists(projectDir) {
		return &ExitError{Code: 2, Err: fmt.Errorf("no %s in current directory; run `fglpkg install` first", lockfile.Filename)}
	}
	lf, err := lockfile.Load(projectDir)
	if err != nil {
		return &ExitError{Code: 2, Err: fmt.Errorf("failed to load %s: %w", lockfile.Filename, err)}
	}

	globalHome, err := fglpkgHome()
	if err != nil {
		return &ExitError{Code: 2, Err: err}
	}
	m, err := signing.LoadManifest(globalHome, defaultRegistry())
	if err != nil {
		return &ExitError{Code: 2, Err: fmt.Errorf("cannot load keys manifest: %w", err)}
	}

	counts := map[auditResult]int{}
	total := 0

	for _, p := range lf.Packages {
		total++
		variant := p.GeneroMajor
		if variant != "" {
			variant = "genero" + variant
		}
		counts[auditOne(m, p.Name, p.Version, variant, p.Checksum, p.Size,
			p.UploadedAt, p.Uploader, p.SignatureKeyID, p.Signature)]++
	}
	for _, w := range lf.Webcomponents {
		total++
		counts[auditOne(m, w.Name, w.Version, "webcomponent", w.Checksum, w.Size,
			w.UploadedAt, w.Uploader, w.SignatureKeyID, w.Signature)]++
	}

	return auditVerdict(os.Stdout, counts, total)
}

// auditVerdict prints the per-category notes and returns the command's result:
// nil when every package verified, or an ExitError when any did not.
//
// It is separate from the walk, and takes its writer, so the command's whole
// contract — which categories fail, what each note says — is testable without a
// keys-manifest fixture. That absence is what left `audit signatures` with no
// test of its own: the one invariant that matters most, that EVERY non-OK
// category exits 1, could be removed without reddening anything.
//
// That invariant is deliberate and load-bearing. `install` under
// FGLPKG_SIGNING=require refuses every category here, so an audit that passed
// what install refuses would be the same drift as `outdated` offering a version
// `update` will not select. The tempting change — "a not-yet-valid key is
// benign, stop failing CI on it" — would make the gate disagree with install,
// and is pinned against below.
func auditVerdict(w io.Writer, counts map[auditResult]int, total int) error {
	if total == 0 {
		fmt.Fprintln(w, "No packages in the lock file to audit.")
		return nil
	}
	failures := total - counts[auditOK]
	if failures == 0 {
		fmt.Fprintf(w, "\nAll %d package signature%s verified.\n", total, pluralS(total))
		return nil
	}
	if n := counts[auditOutsideWindow]; n > 0 {
		fmt.Fprintf(w, "\nNote: %d package%s uploaded before the signing key became valid.\n", n, pluralS(n))
		fmt.Fprintln(w, "  The signature itself verifies — the key was minted after the artifact")
		fmt.Fprintln(w, "  was published, so the window does not reach back to cover it. That is a")
		fmt.Fprintln(w, "  registry key-rollout problem, not evidence the artifact was altered.")
		fmt.Fprintln(w, "  The registry operator fixes it by re-issuing the keys manifest.")
		fmt.Fprintln(w, "  FGLPKG_SIGNING=require refuses these packages until then.")
	}
	if n := counts[auditKeyRetired]; n > 0 {
		fmt.Fprintf(w, "\nWarning: %d package%s signed with a key that had already been retired.\n", n, pluralS(n))
		fmt.Fprintln(w, "  The signature verifies, but the artifact was uploaded after the key's")
		fmt.Fprintln(w, "  validTo — which is the condition that window exists to catch. Treat this")
		fmt.Fprintln(w, "  as a signing-pipeline fault, or as a key still in use past its rotation,")
		fmt.Fprintln(w, "  and establish why before trusting the artifact.")
	}
	return &ExitError{Code: 1, Err: fmt.Errorf(
		"%d of %d package%s failed signature verification (%s)",
		failures, total, pluralS(total), auditBreakdown(counts))}
}

// auditBreakdown renders the per-category counts for the summary line, so the
// exit message says which kind of failure occurred rather than only how many.
func auditBreakdown(counts map[auditResult]int) string {
	var parts []string
	for _, c := range []struct {
		r     auditResult
		label string
	}{
		// "failed verification" rather than "signature does not verify": this
		// category also counts an unknown key and an unreadable upload time,
		// neither of which is a statement about the signature bytes.
		{auditMismatch, "failed verification"},
		{auditKeyRetired, "signed with a retired key"},
		{auditMissing, "unsigned"},
		{auditOutsideWindow, "uploaded before the key was valid"},
	} {
		if n := counts[c.r]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, c.label))
		}
	}
	return strings.Join(parts, "; ")
}

// auditOne verifies a single locked entry and prints its result line.
func auditOne(m *signing.Manifest, name, version, variant, sha256 string, size int64,
	uploadedAt, uploader, keyid, sig string) auditResult {

	label := fmt.Sprintf("%s@%s (%s)", name, version, variant)
	if keyid == "" && sig == "" {
		fmt.Printf("✗ %-40s ERROR: signature missing\n", label)
		return auditMissing
	}
	p := signing.ArtifactFields{
		Name: name, Version: version, Variant: variant,
		SHA256: sha256, Size: size, UploadedAt: uploadedAt, Uploader: uploader,
	}
	err := m.VerifyArtifact(p, signing.ArtifactSignature{KeyID: keyid, Alg: "ed25519", Sig: sig})
	switch {
	case err == nil:
		fmt.Printf("✓ %-40s keyid=%s\n", label, keyid)
		return auditOK

	case errors.Is(err, signing.ErrKeyExpired):
		// The window is checked before the bytes are, so a window error on its
		// own says nothing about whether the artifact matches what was signed —
		// VerifyArtifact returned before it looked. Check the signature directly
		// against the named key (KeyByID deliberately ignores the window) before
		// calling this anything other than a mismatch.
		key, ok := m.KeyByID(keyid)
		if !ok || signing.VerifyArtifact(p, sig, key.Pub) != nil {
			fmt.Printf("✗ %-40s ERROR: signature does not verify, and %v\n", label, err)
			return auditMismatch
		}
		// Which side of the window matters. Below validFrom the registry
		// attested to an artifact older than the key — a rollout problem.
		// Above validTo it was signed with a RETIRED key, which is the
		// condition validTo exists to catch and must never be reported as
		// the benign one.
		if errors.Is(err, signing.ErrKeyNotYetValid) {
			fmt.Printf("! %-40s KEY NOT YET VALID: %v\n", label, err)
			return auditOutsideWindow
		}
		fmt.Printf("✗ %-40s RETIRED KEY: %v\n", label, err)
		return auditKeyRetired

	default:
		fmt.Printf("✗ %-40s ERROR: %v\n", label, err)
		return auditMismatch
	}
}
