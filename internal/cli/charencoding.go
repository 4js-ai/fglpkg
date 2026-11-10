package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/charmap"
	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// encodingEvidence is what a scan of the staged artifacts established.
//
// It separates what was PROVEN from what was merely inferred, because the
// cross-check against an author's declaration may only fail on a provable
// contradiction (GIS-285). Everything softer is a warning.
type encodingEvidence struct {
	// prolog is the encoding a compiled form declared in its XML prolog, and
	// prologFile the form that declared it. fglform writes the build encoding
	// there as an IANA name, so this is authoritative, not a heuristic.
	prolog     string
	prologFile string

	// highByteFile is the first artifact found to contain a byte >= 0x80. Its
	// presence disproves any claim that the package is pure ASCII.
	highByteFile string

	// undecidableFile is the first artifact whose high bytes do NOT decode as
	// UTF-8, so it uses some 8-bit charset that bytes alone cannot name.
	undecidableFile string

	// sawArtifact records whether any compiled BDL artifact was examined at
	// all. A pure-webcomponent package has none, and must not be labelled
	// US-ASCII on the strength of a vacuous "every artifact was ASCII".
	sawArtifact bool
}

// derived returns the encoding the evidence supports, or "" when the bytes
// are undecidable (high bytes that are not UTF-8, with no form prolog to
// name the charset).
func (e encodingEvidence) derived() string {
	switch {
	case !e.sawArtifact:
		return ""
	case e.prolog != "":
		return e.prolog // authoritative
	case e.highByteFile == "":
		return charmap.ASCII
	case e.undecidableFile == "":
		return charmap.UTF8
	default:
		return ""
	}
}

// scanEncoding examines the staged artifacts and reports what they prove.
//
// Only shipped compiled artifacts are examined — .42m and .42s classify by a
// byte scan, .42f by its XML prolog (falling back to a byte scan when a form
// carries none). Sources are deliberately not scanned: what a consumer runs
// is the compiled output, and a package may legitimately ship UTF-8 comments
// in a .4gl that compiles to pure ASCII p-code.
func scanEncoding(staged map[string]string) (encodingEvidence, error) {
	var ev encodingEvidence

	// Sort so the file named in a warning or error is stable across runs —
	// map iteration order would otherwise make the message vary between
	// identical packs and make a failure hard to reproduce.
	archivePaths := make([]string, 0, len(staged))
	for ap := range staged {
		archivePaths = append(archivePaths, ap)
	}
	sort.Strings(archivePaths)

	for _, archivePath := range archivePaths {
		ap := filepath.ToSlash(archivePath)
		ext := strings.ToLower(filepath.Ext(ap))
		switch ext {
		case ".42m", ".42s", ".42f":
		default:
			continue
		}
		srcDisk := staged[archivePath]
		data, err := os.ReadFile(srcDisk)
		if err != nil {
			return ev, fmt.Errorf("cannot read %s for encoding detection: %w", ap, err)
		}
		ev.sawArtifact = true

		if ext == ".42f" {
			if enc := charmap.PrologEncoding(data); enc != "" {
				if ev.prolog != "" && !charmap.Equal(ev.prolog, enc) {
					return ev, &charmap.ErrDisagree{
						FileA: ev.prologFile, EncA: ev.prolog,
						FileB: ap, EncB: enc,
					}
				}
				if ev.prolog == "" {
					ev.prolog, ev.prologFile = enc, ap
				}
				// The prolog names the encoding outright; its bytes add nothing.
				continue
			}
		}

		switch charmap.Classify(data) {
		case charmap.TierASCII:
			// No high bytes: contributes no constraint.
		case charmap.TierUTF8:
			if ev.highByteFile == "" {
				ev.highByteFile = ap
			}
		case charmap.TierUndecidable:
			if ev.highByteFile == "" {
				ev.highByteFile = ap
			}
			if ev.undecidableFile == "" {
				ev.undecidableFile = ap
			}
		}
	}
	return ev, nil
}

// recordCharacterEncoding sets pub.CharacterEncoding from the staged
// artifacts, cross-checking any value the author declared.
//
// This derives rather than asking, which is deliberately the inverse of what
// GIS-285 originally proposed (deny publication when the declaration does not
// match the publisher's `fglrun -i`). Checking the environment couples
// correctness to the publisher's shell and fails both ways: a CI publish
// typically runs LANG=C, so a genuinely UTF-8 package would be rejected; and
// an all-ASCII package built on a UTF-8 workstation would be forced to declare
// UTF-8, needlessly locking out every ISO-8859 consumer for artifacts that are
// in fact portable. Deriving from bytes has neither failure mode, and makes
// the documented best practice — ship pure ASCII — mechanically provable
// rather than merely advised.
func recordCharacterEncoding(pub *manifest.Manifest, staged map[string]string) error {
	ev, err := scanEncoding(staged)
	if err != nil {
		return err
	}
	declared := strings.TrimSpace(pub.CharacterEncoding)

	if declared != "" {
		if err := crossCheckEncoding(declared, ev); err != nil {
			return err
		}
		// The declaration stands — it may legitimately NARROW an undecidable
		// result (naming ISO-8859-15 where bytes can only say "some 8-bit
		// charset"), which is the one thing an author can tell us that the
		// artifacts cannot.
		pub.CharacterEncoding = charmap.Canonical(declared)
		return nil
	}

	if d := ev.derived(); d != "" {
		pub.CharacterEncoding = d
		return nil
	}
	if !ev.sawArtifact {
		// No compiled BDL artifacts (a pure-webcomponent package). The field
		// describes compiled output, so leaving it absent is the honest
		// answer; absent means no consumer-side check, which is correct.
		return nil
	}

	// Undecidable: high bytes that are not UTF-8, and no form prolog to name
	// the charset. This is the ONLY place the build environment is consulted,
	// and it never overrides evidence — only fills a gap the bytes left.
	info, derr := genero.DetectRuntimeInfo()
	if derr != nil || strings.TrimSpace(info.Charmap) == "" {
		fmt.Fprintf(os.Stderr,
			"warning: %s uses a non-UTF-8 8-bit charset that cannot be identified from its bytes, "+
				"and the local Genero charmap could not be read.\n"+
				"  Declare it explicitly, e.g. \"characterEncoding\": \"ISO-8859-15\" in %s.\n",
			ev.undecidableFile, manifest.Filename)
		return nil
	}
	pub.CharacterEncoding = charmap.Canonical(info.Charmap)
	fmt.Fprintf(os.Stderr,
		"warning: %s uses an 8-bit charset that cannot be identified from its bytes "+
			"(ISO-8859-1 and ISO-8859-15 are indistinguishable).\n"+
			"  Assuming the build environment's charmap, %s. Declare it explicitly in %s to be sure.\n",
		ev.undecidableFile, pub.CharacterEncoding, manifest.Filename)
	return nil
}

// crossCheckEncoding fails only when the artifacts PROVE the declaration
// wrong. Two such proofs exist, and deliberately no more: a softer rule would
// reject packages that are in fact fine, and a wrong pack failure is worse
// than a missed catch here.
func crossCheckEncoding(declared string, ev encodingEvidence) error {
	// 1. A form's prolog names the encoding outright, so a declaration that
	//    disagrees with it is simply wrong.
	if ev.prolog != "" && !charmap.Equal(declared, ev.prolog) {
		return fmt.Errorf(
			"declared characterEncoding %q contradicts the package's artifacts: "+
				"%s declares %s in its XML prolog",
			declared, ev.prologFile, ev.prolog)
	}
	// 2. US-ASCII means "no byte >= 0x80". A single high byte disproves it.
	//    This matters more than it looks: US-ASCII is the tier that skips the
	//    consumer check entirely, so a wrong ASCII claim ships mojibake to
	//    every consumer silently.
	if charmap.Equal(declared, charmap.ASCII) && ev.highByteFile != "" {
		return fmt.Errorf(
			"declared characterEncoding %q contradicts the package's artifacts: "+
				"%s contains bytes >= 0x80, so the package is not pure ASCII",
			declared, ev.highByteFile)
	}
	return nil
}
