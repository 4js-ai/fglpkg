package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/charmap"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// The invariant this file holds to: `pack` never writes a value the artifacts
// do not prove. Everything it cannot establish is left absent, which means no
// consumer-side check — the behaviour every package published before GIS-285
// already has.
//
// An earlier design derived the value from a byte scan of every artifact and,
// where that was undecidable, fell back to the packer's `fglrun -i`. Measuring
// it against real compiled output showed the premise was wrong and the
// fallback was the common path, not the rare one:
//
//   - Of 99 real modules compiled from pure-ASCII sources, only 21 had no byte
//     >= 0x80. A .42m's own structure carries high bytes (opcode operands, the
//     embedded source-path length), so a high byte is not evidence of
//     non-ASCII text.
//   - A .42m is byte-identical whatever locale it was built under, so it
//     records no charset at all.
//   - A .42f prolog names the BUILD LOCALE: the same pure-ASCII .per compiles
//     to a prolog of ASCII, UTF-8 or ISO-8859-15 depending on the shell.
//
// So most pure-ASCII packages became "undecidable" and were stamped with the
// packer's shell charset, which `install` then enforced: the same bytes packed
// under LANG=C installed everywhere, and packed under en_US.UTF-8 were refused
// on an ISO-8859-15 consumer. That is the environment dependence the feature
// existed to remove, moved to the consumer side.
//
// What each artifact can prove is now respected separately:
//
//	.42m  no high byte          -> its text is ASCII
//	      a high byte           -> nothing; ask the SOURCE beside it instead
//	.42f  no high byte          -> its text is ASCII, whatever the prolog says
//	      a high byte           -> the prolog names that text's encoding
//	.42s  byte scan             -> reliable, and per file (see below)

// sourceExtensions maps a compiled artifact to the source that produced it.
// When `pack` runs in the build tree the source sits beside the artifact, and
// a pure-ASCII source proves the compiled file carries no non-ASCII text,
// because fglcomp copies literal bytes straight through. This is what makes
// the documented practice — keep .4gl pure ASCII — mechanically provable; the
// artifact's own bytes cannot do it (21 of 99 real modules).
//
// The converse does not hold: a source with high bytes may carry them only in
// a comment, which never reaches the p-code. So a non-ASCII source proves
// nothing and yields no value rather than an error.
var sourceExtensions = map[string]string{
	".42m": ".4gl",
	".42f": ".per",
}

// encodingEvidence is what a scan of the staged artifacts established. It
// separates the CODE (.42m/.42f — what the program runs) from the STRING
// RESOURCES (.42s), because they behave differently and the recommended
// practice depends on telling them apart: keep the code ASCII and put
// localized text in .str files. Merging them meant a package that followed
// that advice was recorded as non-portable and refused on exactly the
// single-byte consumers the advice exists to serve.
type encodingEvidence struct {
	// sawCode records whether any code artifact was examined. A package of
	// resources or webcomponents alone has none, and must not be labelled
	// US-ASCII on a vacuous "every code artifact was ASCII".
	sawCode bool

	// unprovenFile is the first code artifact whose text could be established
	// neither as ASCII nor as any named encoding. Its presence means no value
	// can be recorded.
	unprovenFile string

	// formEncoding is the encoding named by the prolog of a form that ACTUALLY
	// CARRIES non-ASCII text, and formFile the form that named it. This is the
	// only positive proof of a non-ASCII code encoding fglpkg can obtain.
	formEncoding string
	formFile     string

	// resources maps a staged archive path to the encoding its bytes prove,
	// for resource files that carry non-ASCII text. ASCII resources are absent
	// because they constrain nothing.
	resources map[string]string

	// undecidableResources lists resource files whose high bytes are not
	// UTF-8, so bytes alone cannot name their 8-bit charset. Only a
	// declaration resolves those.
	undecidableResources []string
}

// codeEncoding returns the encoding the code artifacts prove, or "" when they
// prove nothing.
func (e encodingEvidence) codeEncoding() string {
	switch {
	case !e.sawCode:
		return ""
	case e.formEncoding != "":
		return e.formEncoding
	case e.unprovenFile != "":
		return ""
	default:
		return charmap.ASCII
	}
}

// hasHighByte reports whether a file on disk contains a byte >= 0x80. A
// missing or unreadable file answers false with ok=false, so a caller can tell
// "proven ASCII" from "could not look".
func hasHighByte(path string) (high bool, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, false
	}
	return charmap.Classify(data) != charmap.TierASCII, true
}

// scanEncoding examines the staged artifacts and reports what they prove.
func scanEncoding(staged map[string]string) (encodingEvidence, error) {
	ev := encodingEvidence{resources: map[string]string{}}

	// Sort so the file named in a message is stable across runs; map iteration
	// order would otherwise make identical packs report different files.
	archivePaths := make([]string, 0, len(staged))
	for ap := range staged {
		archivePaths = append(archivePaths, ap)
	}
	sort.Strings(archivePaths)

	for _, archivePath := range archivePaths {
		ap := filepath.ToSlash(archivePath)
		ext := strings.ToLower(filepath.Ext(ap))
		if ext != ".42m" && ext != ".42f" && ext != ".42s" {
			continue
		}
		srcDisk := staged[archivePath]
		data, err := os.ReadFile(srcDisk)
		if err != nil {
			return ev, fmt.Errorf("cannot read %s for encoding detection: %w", ap, err)
		}
		tier := charmap.Classify(data)

		if ext == ".42s" {
			switch tier {
			case charmap.TierASCII:
				// Constrains nothing: ASCII-7 is a subset of every charset.
			case charmap.TierUTF8:
				ev.resources[ap] = charmap.UTF8
			default:
				ev.undecidableResources = append(ev.undecidableResources, ap)
			}
			continue
		}

		// Code artifact.
		ev.sawCode = true
		if tier == charmap.TierASCII {
			// No high byte at all: its text is ASCII. For a .42f this also
			// settles it regardless of the prolog, which names the build
			// locale rather than the content.
			continue
		}
		if ext == ".42f" {
			// The form really does carry non-ASCII text, so its prolog names
			// that text's encoding and IS authoritative here.
			if enc := charmap.PrologEncoding(data); enc != "" {
				if ev.formEncoding != "" && !charmap.Equal(ev.formEncoding, enc) {
					return ev, &charmap.ErrDisagree{
						FileA: ev.formFile, EncA: ev.formEncoding,
						FileB: ap, EncB: enc,
					}
				}
				if ev.formEncoding == "" {
					ev.formEncoding, ev.formFile = enc, ap
				}
				continue
			}
			if tier == charmap.TierUTF8 {
				if ev.formEncoding == "" {
					ev.formEncoding, ev.formFile = charmap.UTF8, ap
				}
				continue
			}
		}
		// A .42m with a high byte, or a form with neither prolog nor valid
		// UTF-8: the artifact proves nothing. Ask the source beside it.
		if srcExt, ok := sourceExtensions[ext]; ok {
			srcPath := strings.TrimSuffix(srcDisk, filepath.Ext(srcDisk)) + srcExt
			if high, read := hasHighByte(srcPath); read && !high {
				// A pure-ASCII source cannot have produced non-ASCII text.
				continue
			}
		}
		if ev.unprovenFile == "" {
			ev.unprovenFile = ap
		}
	}
	return ev, nil
}

// recordEncoding sets the shipped manifest's encoding fields from the staged
// artifacts, cross-checking anything the author declared.
func recordEncoding(pub *manifest.Manifest, staged map[string]string) error {
	ev, err := scanEncoding(staged)
	if err != nil {
		return err
	}
	if err := recordCodeEncoding(pub, ev); err != nil {
		return err
	}
	return recordResourceEncodings(pub, ev)
}

// recordCodeEncoding fills characterEncoding, which describes the text the
// package's CODE carries.
func recordCodeEncoding(pub *manifest.Manifest, ev encodingEvidence) error {
	declared := strings.TrimSpace(pub.CharacterEncoding)
	if declared != "" {
		if err := crossCheckEncoding(declared, ev); err != nil {
			return err
		}
		// The declaration stands. It is the only way to state an encoding the
		// artifacts cannot prove, which after the measurements above is the
		// common case for a package that genuinely carries non-ASCII code.
		pub.CharacterEncoding = charmap.Canonical(declared)
		return nil
	}
	if enc := ev.codeEncoding(); enc != "" {
		pub.CharacterEncoding = enc
		return nil
	}
	if ev.unprovenFile != "" {
		// Absent means no consumer-side check, which is correct: a value
		// guessed from the packer's shell would be enforced against consumers
		// for a package that may well be pure ASCII.
		fmt.Fprintf(os.Stderr,
			"note: the character encoding of %s could not be established from the package "+
				"(a .42m's own structure contains bytes >= 0x80, and no pure-ASCII source was "+
				"staged beside it), so none is recorded and consumers are not checked.\n"+
				"  If this package carries non-ASCII text, declare it, e.g. "+
				"\"characterEncoding\": \"UTF-8\" in %s.\n",
			ev.unprovenFile, manifest.Filename)
	}
	return nil
}

// recordResourceEncodings fills stringResourceEncodings, which names the
// encoding of each .42s that carries non-ASCII text.
//
// These are recorded PER FILE and never block an install. A package may ship
// one resource file per language and charset and let the consumer select at
// runtime, so a single package-wide value cannot describe the set, and a
// mismatch on one file is not a reason to refuse the package: the consumer may
// be about to use another. Verified with the real runtime — a .42s is used as
// its bytes stand, with no transcoding, so the encoding of each file is a real
// constraint on the consumer that selects it, and nothing more.
func recordResourceEncodings(pub *manifest.Manifest, ev encodingEvidence) error {
	declared := pub.StringResourceEncodings
	out := map[string]string{}

	for path, derivedEnc := range ev.resources {
		if d, ok := declared[path]; ok && strings.TrimSpace(d) != "" {
			if !charmap.Equal(d, derivedEnc) {
				return fmt.Errorf(
					"declared stringResourceEncodings[%q] = %q contradicts the file's bytes, "+
						"which decode as %s", path, d, derivedEnc)
			}
			out[path] = charmap.Canonical(d)
			continue
		}
		out[path] = derivedEnc
	}

	for _, path := range ev.undecidableResources {
		if d, ok := declared[path]; ok && strings.TrimSpace(d) != "" {
			if charmap.Equal(d, charmap.ASCII) || charmap.Equal(d, charmap.UTF8) {
				return fmt.Errorf(
					"declared stringResourceEncodings[%q] = %q contradicts the file's bytes, "+
						"which contain high bytes that are not valid UTF-8", path, d)
			}
			// A declaration is the only thing that can tell ISO-8859-1 from
			// ISO-8859-15, so a narrowing one is exactly what is wanted.
			out[path] = charmap.Canonical(d)
			continue
		}
		fmt.Fprintf(os.Stderr,
			"note: %s uses an 8-bit charset that cannot be identified from its bytes "+
				"(ISO-8859-1 and ISO-8859-15 are indistinguishable), so consumers are not "+
				"warned about it.\n"+
				"  Declare it if you know it, e.g. \"stringResourceEncodings\": {%q: \"ISO-8859-15\"} in %s.\n",
			path, path, manifest.Filename)
	}

	// A declaration for a file that is not a non-ASCII resource constrains
	// nothing, so it is dropped rather than shipped as a claim about a file
	// whose bytes say otherwise.
	if len(out) == 0 {
		pub.StringResourceEncodings = nil
		return nil
	}
	pub.StringResourceEncodings = out
	return nil
}

// crossCheckEncoding fails only when the artifacts PROVE the declaration
// wrong. Exactly one such proof exists, and deliberately no more.
//
// An earlier version also failed when a .42m contained a byte >= 0x80 against
// a declared US-ASCII, and when any form's prolog differed from the
// declaration. Both rejected correct packages: 78 of 99 real pure-ASCII
// modules contain a high byte, and a pure-ASCII .per built under a UTF-8 shell
// writes a UTF-8 prolog. A wrong pack failure is worse than a missed catch,
// so neither survives.
func crossCheckEncoding(declared string, ev encodingEvidence) error {
	if ev.formEncoding != "" && !charmap.Equal(declared, ev.formEncoding) {
		return fmt.Errorf(
			"declared characterEncoding %q contradicts the package's artifacts: "+
				"%s carries non-ASCII text and declares %s in its XML prolog",
			declared, ev.formFile, ev.formEncoding)
	}
	return nil
}
