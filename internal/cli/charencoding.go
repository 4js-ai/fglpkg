package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/4js-mikefolcher/fglpkg/internal/charmap"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// The invariant this file holds to: `pack` never writes a value the artifacts
// do not prove. Everything it cannot establish is left absent, which means no
// consumer-side check — the behaviour every package published before GIS-285
// already has.
//
// Two earlier designs were measured against real compiled output and failed:
//
//  1. Derive from a byte scan of every artifact, falling back to the packer's
//     `fglrun -i` where that was undecidable. But of 99 real modules compiled
//     from pure-ASCII sources only 21 had no byte >= 0x80 — a .42m's own
//     structure carries high bytes (opcode operands, the embedded source-path
//     length) — so most pure-ASCII packages became "undecidable" and were
//     stamped with the packer's shell charset, which `install` then enforced.
//  2. Trust a compiled form's XML prolog once the form carried a high byte.
//     But the prolog records the SHELL fglform ran under, not the file. The
//     same UTF-8 .per compiled under en_US.UTF-8, LANG=C and en_US.ISO8859-15
//     produces byte-identical form content (`Caf\xc3\xa9` every time) and
//     three different prologs, so pack recorded UTF-8, US-ASCII and
//     ISO-8859-15 for one unchanged file.
//
// Both put the packer's environment back into a field whose whole purpose was
// to remove it. What each artifact can actually prove is respected instead:
//
//	.42m  no high byte       -> its text is ASCII
//	      a high byte        -> nothing; ask the .4gl SOURCE beside it
//	.42f  no high byte       -> its text is ASCII, whatever the prolog says
//	      high bytes, UTF-8  -> its text is UTF-8
//	      high bytes, 8-bit  -> nothing; the prolog is only a note
//	.42s  byte scan          -> reliable, and recorded per file (see below)
//
// A .42m is the one artifact whose bytes cannot classify it, so it is the one
// that needs the source. A .42f is decided by its own bytes, which is why no
// .per is consulted: a .per and its includes that are all ASCII cannot produce
// a form with a high byte, so that rule could never fire.

// includeRe matches a Genero preprocessor include. The directive must be the
// FIRST token on its line — verified against fglcomp 6.00.01: `DISPLAY "x"
// &include "f"` is error -6609, a commented `# &include "f"` is ignored, and so
// is one inside a string literal. Anchoring here keeps those three out without
// writing a BDL lexer. The spelling is lower-case (`&INCLUDE` is error -8053)
// and either quote is accepted.
var includeRe = regexp.MustCompile(`^[ \t]*&include[ \t]+["']([^"']+)["']`)

// maxIncludeDepth bounds the include walk. Genero rejects a cycle itself; this
// only stops a pathological tree from costing a pack more than it is worth.
const maxIncludeDepth = 16

// staleSourceGrace is how much newer than its artifact a source may be before
// pack stops believing it describes that artifact.
//
// A timestamp is a weak signal, so the threshold has to separate the two
// things it can mean. A copy, a checkout or an unpack writes a tree's files in
// arbitrary order within the same moment, and rejecting a source for being
// microseconds newer would silently cost the value to every package built that
// way — including the committed-artifact layout some shops use. A forgotten
// rebuild is the opposite: you edit, you run something, you pack, and the gap
// is human-scale. One minute sits between them.
const staleSourceGrace = time.Minute

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
	// can be recorded, even if another artifact proved one: the package would
	// otherwise be labelled with an encoding part of it may not use.
	// unprovenWhy says which check declined, so the note can tell an author
	// whose build tree is one `fglcomp` away from provable apart from one
	// whose package genuinely carries non-ASCII text.
	unprovenFile string
	unprovenWhy  string

	// codeEnc is the non-ASCII encoding a code artifact's own bytes prove, and
	// codeFile the artifact that proved it. In practice this is only ever
	// UTF-8, from a form carrying UTF-8 text: no other encoding is decidable
	// from bytes.
	codeEnc  string
	codeFile string

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
// prove nothing. An unproven artifact dominates a proven one: a package is
// only as portable as its least portable module.
func (e encodingEvidence) codeEncoding() string {
	switch {
	case !e.sawCode:
		return ""
	case e.unprovenFile != "":
		return ""
	case e.codeEnc != "":
		return e.codeEnc
	default:
		return charmap.ASCII
	}
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
			// A form's content is its source's bytes copied through, so the
			// form's OWN bytes classify it, exactly as a .42s is classified.
			// The prolog is reported but never believed.
			noteFormProlog(ap, data, tier)
			if tier == charmap.TierUTF8 {
				if ev.codeEnc == "" {
					ev.codeEnc, ev.codeFile = charmap.UTF8, ap
				}
				continue
			}
			// High bytes that are not UTF-8: some 8-bit charset, which bytes
			// cannot name (ISO-8859-1 and -15 are indistinguishable).
			ev.noteUnproven(ap, "it carries bytes >= 0x80 that are not valid UTF-8, "+
				"and bytes alone cannot tell ISO-8859-1 from ISO-8859-15")
			continue
		}
		// A .42m with a high byte proves nothing by itself. Ask the source.
		proven, why := sourceProvesASCII(srcDisk)
		if proven {
			continue
		}
		ev.noteUnproven(ap, why)
	}
	return ev, nil
}

// noteUnproven records the FIRST artifact that could not be established.
// Only the first is kept: the note exists to give the author somewhere to
// start, and listing every module of a large package would bury it.
func (e *encodingEvidence) noteUnproven(archivePath, why string) {
	if e.unprovenFile != "" {
		return
	}
	e.unprovenFile, e.unprovenWhy = archivePath, why
}

// noteFormProlog reports a compiled form whose XML prolog disagrees with its
// own content. The prolog names the shell fglform ran under, so a form
// carrying UTF-8 text built under LANG=C declares ASCII. fglpkg ignores it for
// derivation, but a front end that honours the prolog would mis-read such a
// form for EVERY consumer, which the author can only fix by rebuilding.
func noteFormProlog(archivePath string, data []byte, tier charmap.Tier) {
	prolog := charmap.PrologEncoding(data)
	if prolog == "" {
		return
	}
	switch {
	case tier == charmap.TierUTF8 && !charmap.Equal(prolog, charmap.UTF8):
		fmt.Fprintf(os.Stderr,
			"note: %s carries UTF-8 text but its XML prolog declares %s. The prolog records "+
				"the locale fglform ran under, not the file's content, so fglpkg ignores it "+
				"here.\n"+
				"  Rebuild the form under a UTF-8 locale if your front end honours the prolog.\n",
			archivePath, prolog)
	case tier != charmap.TierUTF8 && charmap.Equal(prolog, charmap.ASCII):
		fmt.Fprintf(os.Stderr,
			"note: %s contains bytes >= 0x80 but its XML prolog declares %s, so the prolog "+
				"cannot be describing it. It records the locale fglform ran under.\n"+
				"  Rebuild the form under the locale its text is written in if your front end "+
				"honours the prolog.\n",
			archivePath, prolog)
	}
}

// sourceProvesASCII reports whether the .4gl that produced a .42m establishes
// that the compiled module carries no non-ASCII text. fglcomp copies string
// literal bytes straight through, so a pure-ASCII source cannot have emitted
// any — and this is what makes the documented practice (keep .4gl pure ASCII,
// put localized text in .str) mechanically provable, where the .42m's own
// bytes cannot: 21 of 99 real modules, against 99 of 107 source pairs.
//
// Two ways a pure-ASCII .4gl still produces non-ASCII p-code, both reproduced
// with real fglcomp, so both are checked:
//
//   - `&include` pulls another file's text in. An ASCII Hi.4gl including a
//     UTF-8 greet.inc compiles to a .42m carrying `café crème`.
//   - the source was edited after the build. Compile a source with a literal,
//     then make the .4gl ASCII without recompiling.
//
// The converse never holds: a source with high bytes may carry them only in a
// comment, which never reaches the p-code. So a non-ASCII source proves
// nothing and yields no value rather than an error.
func sourceProvesASCII(artifactDisk string) (proven bool, why string) {
	srcPath := strings.TrimSuffix(artifactDisk, filepath.Ext(artifactDisk)) + ".4gl"
	artifactTime, err := modTime(artifactDisk)
	if err != nil {
		return false, "its own modification time could not be read"
	}
	return fileProvesASCII(srcPath, artifactTime, map[string]bool{}, 0)
}

// fileProvesASCII reports whether path, and everything it includes, is pure
// ASCII and no newer than the artifact. Anything it cannot read — a missing
// file, an include resolved through an -I path fglpkg does not know — answers
// false, because not being able to look is not evidence of ASCII.
func fileProvesASCII(path string, artifactTime time.Time, seen map[string]bool, depth int) (bool, string) {
	if depth > maxIncludeDepth {
		return false, fmt.Sprintf("its include tree is deeper than %d files", maxIncludeDepth)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, fmt.Sprintf("%s could not be resolved", filepath.Base(path))
	}
	if seen[abs] {
		// Already proven on this walk; a second visit adds nothing.
		return true, ""
	}
	seen[abs] = true

	// A source edited after the module was compiled does not describe it. The
	// artifact is what ships, so a source that cannot have produced it is no
	// better than no source at all.
	mt, err := modTime(path)
	if err != nil {
		return false, fmt.Sprintf("%s was not staged beside it", filepath.Base(path))
	}
	if mt.Sub(artifactTime) > staleSourceGrace {
		return false, fmt.Sprintf(
			"%s is NEWER than the compiled file, so it may not be the source it was built from "+
				"(recompile it, or declare the encoding)",
			filepath.Base(path))
	}

	f, err := os.Open(path)
	if err != nil {
		return false, fmt.Sprintf("%s could not be read", filepath.Base(path))
	}
	defer f.Close()

	var includes []string
	sc := bufio.NewScanner(f)
	// Genero accepts long lines; the default 64 KiB token limit would make a
	// minified or generated source read as an error and silently prove nothing.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		for _, b := range line {
			if b >= 0x80 {
				return false, fmt.Sprintf("%s contains bytes >= 0x80 itself", filepath.Base(path))
			}
		}
		if m := includeRe.FindSubmatch(line); m != nil {
			includes = append(includes, string(m[1]))
		}
	}
	if sc.Err() != nil {
		return false, fmt.Sprintf("%s could not be read to the end", filepath.Base(path))
	}

	for _, inc := range includes {
		resolved, ok := resolveInclude(inc, filepath.Dir(path))
		if !ok {
			return false, fmt.Sprintf(
				"%s includes %q, which is not beside it or in the working directory "+
					"(fglcomp also searches its -I paths, which fglpkg cannot know)",
				filepath.Base(path), inc)
		}
		if ok, why := fileProvesASCII(resolved, artifactTime, seen, depth+1); !ok {
			return false, why
		}
	}
	return true, ""
}

// resolveInclude finds an &include target the way fglcomp does, minus the -I
// paths fglpkg cannot know: beside the including file first, then the working
// directory. Verified against fglcomp 6.00.01, which searches the source's own
// directory, the working directory and each -I path.
func resolveInclude(name, sourceDir string) (string, bool) {
	if filepath.IsAbs(name) {
		if _, err := os.Stat(name); err == nil {
			return name, true
		}
		return "", false
	}
	for _, dir := range []string{sourceDir, "."} {
		cand := filepath.Join(dir, name)
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return cand, true
		}
	}
	return "", false
}

// modTime returns a file's modification time.
func modTime(path string) (time.Time, error) {
	st, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return st.ModTime(), nil
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
		// for a package that may well be pure ASCII. The reason is named so an
		// author can act on it — a stale or unstaged source is a build fix, a
		// genuinely non-ASCII one is a declaration.
		fmt.Fprintf(os.Stderr,
			"note: the character encoding of %s could not be established: %s.\n"+
				"  None is recorded, so consumers are not checked. If this package carries "+
				"non-ASCII text, declare it, e.g. \"characterEncoding\": \"UTF-8\" in %s.\n",
			ev.unprovenFile, ev.unprovenWhy, manifest.Filename)
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
// wrong, which in practice means a form whose own bytes are valid UTF-8 under
// a declaration that is not UTF-8.
//
// Two earlier rules were removed because they rejected correct packages: that
// a byte >= 0x80 in a .42m disproves a declared US-ASCII (78 of 99 real
// pure-ASCII modules contain one), and that a form's prolog disproves any
// other declaration (the prolog names the build shell). A wrong pack failure
// is worse than a missed catch, so only what the bytes themselves show
// survives.
func crossCheckEncoding(declared string, ev encodingEvidence) error {
	if ev.codeEnc != "" && !charmap.Equal(declared, ev.codeEnc) {
		return fmt.Errorf(
			"declared characterEncoding %q contradicts the package's artifacts: "+
				"the text in %s decodes as %s",
			declared, ev.codeFile, ev.codeEnc)
	}
	return nil
}
