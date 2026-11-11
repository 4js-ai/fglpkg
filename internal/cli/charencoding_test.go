package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/4js-mikefolcher/fglpkg/internal/charmap"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// These tests run on REAL compiled artifacts (internal/cli/testdata/encoding),
// not on source text written into files named .42m. That distinction is the
// whole point: fake fixtures never contain the structure bytes >= 0x80 that a
// real .42m carries, so a suite built on them agreed with a byte scan that
// disagrees with 78 of 99 real modules.

const fixtureDir = "testdata/encoding"

// stageFixtures copies the WHOLE fixture directory into a temp dir and returns
// the staged map pack would have built (archive path -> path on disk) for the
// named artifacts. Everything is copied so that each artifact keeps the source
// and the &include files it was built from beside it, which is what proves a
// .42m's text ASCII.
//
// Sources are backdated relative to the artifacts, because that is what a
// build tree looks like and because pack declines a source NEWER than the
// module it supposedly produced.
func stageFixtures(t *testing.T, names ...string) map[string]string {
	t.Helper()
	dir := copyFixtureTree(t)
	staged := map[string]string{}
	for _, name := range names {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("fixture %s: %v", name, err)
		}
		staged[name] = p
	}
	return staged
}

// copyFixtureTree materializes the fixtures with build-tree timestamps:
// sources and includes an hour older than the compiled artifacts.
func copyFixtureTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	entries, err := os.ReadDir(fixtureDir)
	if err != nil {
		t.Fatalf("read fixture dir: %v", err)
	}
	artifact := time.Now()
	source := artifact.Add(-time.Hour)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(fixtureDir, e.Name()))
		if err != nil {
			t.Fatalf("read fixture %s: %v", e.Name(), err)
		}
		dst := filepath.Join(dir, e.Name())
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			t.Fatalf("write %s: %v", dst, err)
		}
		when := source
		switch filepath.Ext(e.Name()) {
		case ".42m", ".42f", ".42s":
			when = artifact
		}
		if err := os.Chtimes(dst, when, when); err != nil {
			t.Fatalf("chtimes %s: %v", dst, err)
		}
	}
	return dir
}

// stageArtifactOnly copies an artifact WITHOUT the source beside it — a pack
// run against a dist directory rather than a build tree.
func stageArtifactOnly(t *testing.T, name string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	data, err := os.ReadFile(filepath.Join(fixtureDir, name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	dst := filepath.Join(dir, name)
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
	return map[string]string{name: dst}
}

func recordOrFail(t *testing.T, m *manifest.Manifest, staged map[string]string) {
	t.Helper()
	if err := recordEncoding(m, staged); err != nil {
		t.Fatalf("recordEncoding: %v", err)
	}
}

// --- .42m: the source beside it is the evidence ---------------------------

// The case the old byte scan got wrong. AsciiBig.42m is compiled from a
// pure-ASCII source and still contains a byte >= 0x80 (an opcode operand), so
// a scan of the artifact calls it "some 8-bit charset". The source beside it
// settles it.
func TestRecordEncoding_PureASCIISourceProvesASCIIDespiteHighBytesInPcode(t *testing.T) {
	staged := stageFixtures(t, "AsciiBig.42m")
	m := &manifest.Manifest{}
	recordOrFail(t, m, staged)
	if m.CharacterEncoding != charmap.ASCII {
		t.Errorf("CharacterEncoding = %q, want %q (the source is pure ASCII)", m.CharacterEncoding, charmap.ASCII)
	}
}

// The minority whose p-code happens to be clean: no source needed.
func TestRecordEncoding_CleanPcodeProvesASCIIWithoutASource(t *testing.T) {
	staged := stageArtifactOnly(t, "AsciiTiny.42m")
	m := &manifest.Manifest{}
	recordOrFail(t, m, staged)
	if m.CharacterEncoding != charmap.ASCII {
		t.Errorf("CharacterEncoding = %q, want %q", m.CharacterEncoding, charmap.ASCII)
	}
}

// Without a source, a .42m's high bytes prove nothing — they may be structure
// or a literal. Recording a value here is what stamped packages with the
// packer's shell charset, so the field stays absent and install does not check.
func TestRecordEncoding_HighBytesWithoutASourceProveNothing(t *testing.T) {
	for _, name := range []string{"AsciiBig.42m", "Utf8Lit.42m"} {
		t.Run(name, func(t *testing.T) {
			staged := stageArtifactOnly(t, name)
			m := &manifest.Manifest{}
			recordOrFail(t, m, staged)
			if m.CharacterEncoding != "" {
				t.Errorf("CharacterEncoding = %q, want it absent: a .42m's high bytes are not evidence",
					m.CharacterEncoding)
			}
		})
	}
}

// A non-ASCII source proves nothing either: the high bytes may be in a comment,
// which never reaches the p-code. So this is "no value", not UTF-8 and not an
// error — the author declares it if the package really carries non-ASCII text.
func TestRecordEncoding_NonASCIISourceIsNotProof(t *testing.T) {
	staged := stageFixtures(t, "Utf8Lit.42m")
	m := &manifest.Manifest{}
	recordOrFail(t, m, staged)
	if m.CharacterEncoding != "" {
		t.Errorf("CharacterEncoding = %q, want it absent", m.CharacterEncoding)
	}
}

// &include pulls another file's text into the module, so reading the .4gl
// alone is not enough. IncUtf8.4gl has no byte >= 0x80 and includes a UTF-8
// IncUtf8.inc; the compiled IncUtf8.42m really does carry the euro sign.
// Before the include walk this package shipped "US-ASCII" under every shell —
// the one tier install never checks — for text that is not portable at all.
func TestRecordEncoding_IncludeFileDefeatsAPureASCIISource(t *testing.T) {
	staged := stageFixtures(t, "IncUtf8.42m")
	m := &manifest.Manifest{}
	out, err := captureStderr(t, func() error { return recordEncoding(m, staged) })
	if err != nil {
		t.Fatalf("recordEncoding: %v", err)
	}
	if m.CharacterEncoding != "" {
		t.Errorf("CharacterEncoding = %q, want it absent: the include carries UTF-8 text",
			m.CharacterEncoding)
	}
	if !strings.Contains(out, "IncUtf8.inc") {
		t.Errorf("the note must name the include that defeated the proof, got: %s", out)
	}
}

// The same thing written without the space fglcomp does not require.
// IncTight.4gl is IncUtf8.4gl with `&include"IncTight.inc"`, compiled for real:
// no high byte in the source, `€` in the .42m. A matcher built around
// `&include ` + whitespace misses this and records US-ASCII, which install
// then never checks — the one failure mode of this scan that is silent.
func TestRecordEncoding_IncludeWithNoSpaceIsStillAnInclude(t *testing.T) {
	staged := stageFixtures(t, "IncTight.42m")
	m := &manifest.Manifest{}
	out, err := captureStderr(t, func() error { return recordEncoding(m, staged) })
	if err != nil {
		t.Fatalf("recordEncoding: %v", err)
	}
	if m.CharacterEncoding != "" {
		t.Errorf("CharacterEncoding = %q, want it absent: `&include\"f\"` is an include too",
			m.CharacterEncoding)
	}
	if !strings.Contains(out, "IncTight.inc") {
		t.Errorf("the note must name the include that defeated the proof, got: %s", out)
	}
}

// ...and an include that IS pure ASCII must not cost the package its value,
// or following includes would simply disable the rule for every module that
// uses one.
func TestRecordEncoding_PureASCIIIncludeStillProvesASCII(t *testing.T) {
	staged := stageFixtures(t, "IncAscii.42m")
	m := &manifest.Manifest{}
	recordOrFail(t, m, staged)
	if m.CharacterEncoding != charmap.ASCII {
		t.Errorf("CharacterEncoding = %q, want %q: the source and its include are both ASCII",
			m.CharacterEncoding, charmap.ASCII)
	}
}

// An include fglpkg cannot find is not evidence of anything. fglcomp also
// searches its -I paths, which pack cannot know, so an unresolved include
// means "no value" rather than a silent ASCII.
func TestRecordEncoding_UnresolvableIncludeProvesNothing(t *testing.T) {
	staged := stageFixtures(t, "IncAscii.42m")
	dir := filepath.Dir(staged["IncAscii.42m"])
	if err := os.Remove(filepath.Join(dir, "IncAscii.inc")); err != nil {
		t.Fatalf("remove include: %v", err)
	}
	m := &manifest.Manifest{}
	out, err := captureStderr(t, func() error { return recordEncoding(m, staged) })
	if err != nil {
		t.Fatalf("recordEncoding: %v", err)
	}
	if m.CharacterEncoding != "" {
		t.Errorf("CharacterEncoding = %q, want it absent: the include could not be read",
			m.CharacterEncoding)
	}
	if !strings.Contains(out, "IncAscii.inc") {
		t.Errorf("the note must name the missing include, got: %s", out)
	}
}

// A source edited after the build does not describe the artifact that ships.
// Compile a module with a literal, make the .4gl ASCII without recompiling,
// and the old rule read the new source as proof about the old p-code.
func TestRecordEncoding_SourceNewerThanTheArtifactProvesNothing(t *testing.T) {
	staged := stageFixtures(t, "AsciiBig.42m")
	src := filepath.Join(filepath.Dir(staged["AsciiBig.42m"]), "AsciiBig.4gl")
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(src, later, later); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	m := &manifest.Manifest{}
	out, err := captureStderr(t, func() error { return recordEncoding(m, staged) })
	if err != nil {
		t.Fatalf("recordEncoding: %v", err)
	}
	if m.CharacterEncoding != "" {
		t.Errorf("CharacterEncoding = %q, want it absent: the source is newer than the module",
			m.CharacterEncoding)
	}
	if !strings.Contains(out, "NEWER") {
		t.Errorf("the note must say the source is newer, got: %s", out)
	}
}

// ...but a source a few milliseconds newer is a COPY, not an edit. cp, a git
// checkout and an unpack all write a tree in arbitrary order within one
// moment, and rejecting those would cost the value to every package built from
// a copied tree — including the committed-artifact layout some shops use.
func TestRecordEncoding_SourceNewerByACopysMarginIsStillBelieved(t *testing.T) {
	staged := stageFixtures(t, "AsciiBig.42m")
	artifact := staged["AsciiBig.42m"]
	src := filepath.Join(filepath.Dir(artifact), "AsciiBig.4gl")
	st, err := os.Stat(artifact)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	justAfter := st.ModTime().Add(50 * time.Millisecond)
	if err := os.Chtimes(src, justAfter, justAfter); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	m := &manifest.Manifest{}
	recordOrFail(t, m, staged)
	if m.CharacterEncoding != charmap.ASCII {
		t.Errorf("CharacterEncoding = %q, want %q: a copy's ordering is not a stale source",
			m.CharacterEncoding, charmap.ASCII)
	}
}

// --- .42f: its own bytes are the evidence, never the prolog ---------------

// A pure-ASCII .per built under a UTF-8 shell writes a UTF-8 prolog. The
// prolog names the BUILD LOCALE, so reading it as the content's encoding
// labelled an ASCII-only package UTF-8 and refused it on ISO-8859-15
// consumers. The form carries no high byte, so it is ASCII.
func TestRecordEncoding_FormPrologIsIgnoredWhenTheFormIsPureASCII(t *testing.T) {
	staged := stageFixtures(t, "AsciiForm.42f")
	m := &manifest.Manifest{}
	recordOrFail(t, m, staged)
	if m.CharacterEncoding != charmap.ASCII {
		t.Errorf("CharacterEncoding = %q, want %q: the prolog names the build locale, not the content",
			m.CharacterEncoding, charmap.ASCII)
	}
}

// A form that carries real non-ASCII text is classified by its own BYTES, the
// same way a .42s is. Utf8Form.42f decodes as UTF-8, so it is UTF-8.
func TestRecordEncoding_FormCarryingUTF8TextIsProvenByItsBytes(t *testing.T) {
	staged := stageFixtures(t, "Utf8Form.42f")
	m := &manifest.Manifest{}
	recordOrFail(t, m, staged)
	if m.CharacterEncoding != charmap.UTF8 {
		t.Errorf("CharacterEncoding = %q, want %q", m.CharacterEncoding, charmap.UTF8)
	}
}

// The second half of that: the prolog must not override the bytes.
// Utf8Mislabelled.42f is the SAME .per as Utf8Form.per compiled under LANG=C,
// so its content bytes are identical UTF-8 while its prolog says ASCII. Under
// the old rule the same form packed from a C shell recorded US-ASCII — the
// tier install never checks — and from an ISO-8859-15 shell recorded
// ISO-8859-15, refusing the UTF-8 consumers it actually works on.
func TestRecordEncoding_FormPrologNeverOverridesTheBytes(t *testing.T) {
	staged := stageFixtures(t, "Utf8Mislabelled.42f")
	m := &manifest.Manifest{}
	out, err := captureStderr(t, func() error { return recordEncoding(m, staged) })
	if err != nil {
		t.Fatalf("recordEncoding: %v", err)
	}
	if m.CharacterEncoding != charmap.UTF8 {
		t.Fatalf("CharacterEncoding = %q, want %q: the bytes are UTF-8 whatever the prolog says",
			m.CharacterEncoding, charmap.UTF8)
	}
	// And it is not reported either. An earlier revision printed a note here,
	// on the theory that a front end honouring the prolog would mis-read the
	// form. Measured instead: this same .per built under three locales and run
	// in text mode under UTF-8 and ISO-8859-15 runtimes wrote its bytes as
	// stored in all six runs, and the front ends never read the file at all —
	// fglrun parses it and sends an AUI tree. A note with no reader behind it
	// fires on every pack of a UTF-8 form built from a non-UTF-8 shell and
	// asks for a rebuild that changes nothing.
	if strings.Contains(out, "prolog") {
		t.Errorf("the prolog has no Genero reader, so it must not be reported: %s", out)
	}
}

// A form whose high bytes are not UTF-8 is some 8-bit charset, and bytes
// cannot tell ISO-8859-1 from ISO-8859-15. Its prolog is not evidence either,
// so the package records nothing and the author declares it.
func TestRecordEncoding_EightBitFormProvesNothing(t *testing.T) {
	staged := stageFixtures(t, "Latin9Form.42f")
	m := &manifest.Manifest{}
	recordOrFail(t, m, staged)
	if m.CharacterEncoding != "" {
		t.Errorf("CharacterEncoding = %q, want it absent: an 8-bit charset cannot be named from bytes",
			m.CharacterEncoding)
	}
}

// An artifact that proves nothing outweighs one that proves something: a
// package is only as portable as its least portable module. Labelling this
// pair UTF-8 would claim an encoding for a form that may well be Latin-9.
func TestRecordEncoding_AnUnprovenArtifactOutweighsAProvenOne(t *testing.T) {
	staged := stageFixtures(t, "Utf8Form.42f", "Latin9Form.42f")
	m := &manifest.Manifest{}
	recordOrFail(t, m, staged)
	if m.CharacterEncoding != "" {
		t.Errorf("CharacterEncoding = %q, want it absent", m.CharacterEncoding)
	}
}

// --- resources ------------------------------------------------------------

// The practice the guide recommends: pure-ASCII code, localized text in .str.
// The package must come out portable (US-ASCII code) with the resource named
// separately — not stamped non-portable and refused on the single-byte
// consumers the practice exists to serve.
func TestRecordEncoding_ASCIICodePlusLocalizedResourcesStaysPortable(t *testing.T) {
	staged := stageFixtures(t, "AsciiBig.42m", "AsciiForm.42f", "Utf8Msg.42s")
	m := &manifest.Manifest{}
	recordOrFail(t, m, staged)
	if m.CharacterEncoding != charmap.ASCII {
		t.Fatalf("CharacterEncoding = %q, want %q: the CODE is pure ASCII", m.CharacterEncoding, charmap.ASCII)
	}
	if got := m.StringResourceEncodings["Utf8Msg.42s"]; got != charmap.UTF8 {
		t.Errorf("StringResourceEncodings[Utf8Msg.42s] = %q, want %q", got, charmap.UTF8)
	}
}

// A resource set in several charsets cannot be one package-wide value, so each
// file carries its own. An ASCII resource constrains nothing and is omitted.
func TestRecordEncoding_ResourcesAreRecordedPerFile(t *testing.T) {
	staged := stageFixtures(t, "Utf8Msg.42s", "AsciiMsg.42s", "Latin9Msg.42s")
	m := &manifest.Manifest{
		StringResourceEncodings: map[string]string{"Latin9Msg.42s": "ISO-8859-15"},
	}
	recordOrFail(t, m, staged)
	want := map[string]string{
		"Utf8Msg.42s":   charmap.UTF8,
		"Latin9Msg.42s": "ISO-8859-15", // only a declaration can name an 8-bit charset
	}
	for k, v := range want {
		if m.StringResourceEncodings[k] != v {
			t.Errorf("StringResourceEncodings[%s] = %q, want %q", k, m.StringResourceEncodings[k], v)
		}
	}
	if _, ok := m.StringResourceEncodings["AsciiMsg.42s"]; ok {
		t.Errorf("an ASCII resource constrains nothing and must not be recorded")
	}
}

// Bytes beat a declaration where they prove it wrong.
func TestRecordEncoding_ResourceDeclarationContradictingBytesFails(t *testing.T) {
	staged := stageFixtures(t, "Utf8Msg.42s")
	m := &manifest.Manifest{
		StringResourceEncodings: map[string]string{"Utf8Msg.42s": "ISO-8859-15"},
	}
	err := recordEncoding(m, staged)
	if err == nil {
		t.Fatal("a declaration contradicted by the file's bytes must fail the pack")
	}
	if !strings.Contains(err.Error(), "Utf8Msg.42s") {
		t.Errorf("the error must name the file, got: %v", err)
	}
}

// The other direction: a file whose high bytes are NOT valid UTF-8 cannot be
// UTF-8 or ASCII, so those two declarations are provably wrong. Any other
// name narrows an undecidable result and is accepted.
func TestRecordEncoding_ResourceDeclaredASCIIOrUTF8AgainstEightBitBytesFails(t *testing.T) {
	for _, declared := range []string{charmap.ASCII, "ascii", charmap.UTF8, "utf-8"} {
		t.Run(declared, func(t *testing.T) {
			staged := stageFixtures(t, "Latin9Msg.42s")
			m := &manifest.Manifest{
				StringResourceEncodings: map[string]string{"Latin9Msg.42s": declared},
			}
			err := recordEncoding(m, staged)
			if err == nil {
				t.Fatalf("declaring %q for bytes that are not valid UTF-8 must fail the pack", declared)
			}
			if !strings.Contains(err.Error(), "Latin9Msg.42s") {
				t.Errorf("the error must name the file, got: %v", err)
			}
		})
	}
}

// A declaration is stored in its canonical IANA spelling, so two packages
// declaring the same charset in different cases compare equal on the consumer
// side and read the same in the shipped manifest.
func TestRecordEncoding_DeclarationsAreStoredCanonically(t *testing.T) {
	staged := stageFixtures(t, "Latin9Msg.42s", "AsciiBig.42m")
	m := &manifest.Manifest{
		CharacterEncoding:       "ascii",
		StringResourceEncodings: map[string]string{"Latin9Msg.42s": "iso-8859-15"},
	}
	recordOrFail(t, m, staged)
	if m.CharacterEncoding != charmap.ASCII {
		t.Errorf("CharacterEncoding = %q, want the canonical %q", m.CharacterEncoding, charmap.ASCII)
	}
	if got := m.StringResourceEncodings["Latin9Msg.42s"]; got != "ISO-8859-15" {
		t.Errorf("StringResourceEncodings[Latin9Msg.42s] = %q, want the canonical %q", got, "ISO-8859-15")
	}
}

// --- declarations ---------------------------------------------------------

// The cross-check that stayed: a form whose own bytes decode as UTF-8 proves
// its text is UTF-8, so a declaration that disagrees is provably wrong.
func TestRecordEncoding_DeclarationContradictingTheBytesFails(t *testing.T) {
	staged := stageFixtures(t, "Utf8Form.42f")
	m := &manifest.Manifest{CharacterEncoding: "ISO-8859-15"}
	err := recordEncoding(m, staged)
	if err == nil {
		t.Fatal("a declaration contradicting a form's UTF-8 bytes must fail")
	}
	if !strings.Contains(err.Error(), "Utf8Form.42f") {
		t.Errorf("the error must name the form, got: %v", err)
	}
}

// The cross-checks that had to GO, because they rejected correct packages.
func TestRecordEncoding_DeclarationsThatUsedToFailWrongly(t *testing.T) {
	cases := []struct {
		name     string
		fixtures []string
		declared string
	}{
		// 78 of 99 real pure-ASCII modules contain a high byte, so "a high
		// byte disproves US-ASCII" rejected the correct declaration.
		{"US-ASCII against real p-code", []string{"AsciiBig.42m"}, charmap.ASCII},
		// A pure-ASCII form built under a UTF-8 shell has a UTF-8 prolog, so
		// "the prolog is authoritative" rejected the correct declaration.
		{"US-ASCII against a UTF-8 prolog on an ASCII form", []string{"AsciiForm.42f"}, charmap.ASCII},
		// And a form carrying real UTF-8 text built under LANG=C has an ASCII
		// prolog, so the same rule rejected a correct UTF-8 declaration.
		{"UTF-8 against an ASCII prolog on a UTF-8 form", []string{"Utf8Mislabelled.42f"}, charmap.UTF8},
		// The practice: ASCII code, localized resources. The resource must not
		// disprove a US-ASCII claim about the CODE.
		{"US-ASCII code beside a UTF-8 resource", []string{"AsciiBig.42m", "Utf8Msg.42s"}, charmap.ASCII},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			staged := stageFixtures(t, tc.fixtures...)
			m := &manifest.Manifest{CharacterEncoding: tc.declared}
			if err := recordEncoding(m, staged); err != nil {
				t.Fatalf("this declaration is correct and must be accepted, got: %v", err)
			}
			if m.CharacterEncoding != tc.declared {
				t.Errorf("CharacterEncoding = %q, want %q", m.CharacterEncoding, tc.declared)
			}
		})
	}
}

// --- invariants -----------------------------------------------------------

// pack must never consult the build environment. Whatever FGLPKG_CHARMAP says,
// an unprovable package records nothing — that fallback is what made the same
// bytes install everywhere when packed under LANG=C and be refused when packed
// under en_US.UTF-8.
func TestRecordEncoding_NeverFallsBackToTheBuildEnvironment(t *testing.T) {
	t.Setenv("FGLPKG_CHARMAP", "ISO-8859-15")
	staged := stageArtifactOnly(t, "AsciiBig.42m")
	m := &manifest.Manifest{}
	recordOrFail(t, m, staged)
	if m.CharacterEncoding != "" {
		t.Errorf("CharacterEncoding = %q, want it absent: the packer's shell is not evidence",
			m.CharacterEncoding)
	}
}

// A package with no compiled BDL artifact (a pure-webcomponent package) must
// not be labelled US-ASCII on a vacuous "every code artifact was ASCII".
func TestRecordEncoding_NoCodeArtifactsLeavesFieldAbsent(t *testing.T) {
	staged := stageFixtures(t, "AsciiMsg.42s")
	m := &manifest.Manifest{}
	recordOrFail(t, m, staged)
	if m.CharacterEncoding != "" {
		t.Errorf("CharacterEncoding = %q, want it absent", m.CharacterEncoding)
	}
}

// Everything pack writes must survive its own validator, or the shipped
// manifest cannot be read back. ASCII code plus a UTF-8 resource and a "char"
// declaration is the recommended practice, and it used to pack a manifest that
// Validate() then rejected.
func TestRecordEncoding_WhatPackWritesPassesValidate(t *testing.T) {
	staged := stageFixtures(t, "AsciiBig.42m", "Utf8Msg.42s")
	m := &manifest.Manifest{
		Name:            "demo-pkg",
		Version:         "1.0.0",
		LengthSemantics: manifest.LengthSemanticsChar,
	}
	recordOrFail(t, m, staged)
	if m.CharacterEncoding != charmap.ASCII {
		t.Fatalf("CharacterEncoding = %q, want %q", m.CharacterEncoding, charmap.ASCII)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("pack wrote a manifest its own Validate() rejects: %v", err)
	}
}

// The file named in a message must be stable across runs, or an identical pack
// reports a different file each time and a failure cannot be reproduced.
func TestScanEncoding_NamesAStableFile(t *testing.T) {
	staged := stageArtifactOnly(t, "AsciiBig.42m")
	second := stageArtifactOnly(t, "Utf8Lit.42m")
	staged["Utf8Lit.42m"] = second["Utf8Lit.42m"]
	var first string
	for i := 0; i < 20; i++ {
		ev, err := scanEncoding(staged)
		if err != nil {
			t.Fatalf("scanEncoding: %v", err)
		}
		if i == 0 {
			first = ev.unprovenFile
			continue
		}
		if ev.unprovenFile != first {
			t.Fatalf("unprovenFile varies between runs: %q then %q", first, ev.unprovenFile)
		}
	}
	if first == "" {
		t.Fatal("expected an unproven file to be recorded")
	}
}

// The include scanner must match everything fglcomp treats as a directive.
// The two errors are not symmetric: a spelling it misses records US-ASCII for
// a module that carries the include's text, which install then never checks,
// while a false match only costs the derived value. So it errs wide, and this
// table is the measurement that says how wide. Every row was run through
// fglcomp 6.00.01 with an ASCII source including a UTF-8 file, checking
// whether that text reached the .42m.
func TestIncludeRe_MatchesWhatFglcompTreatsAsADirective(t *testing.T) {
	const ff, vt = "\f", "\v"
	cases := []struct {
		line string
		want string
	}{
		{`&include "greet.inc"`, "greet.inc"},
		{`   &include "greet.inc"`, "greet.inc"},
		{"\t&include 'greet.inc'", "greet.inc"},
		{`&include  "inc/greet.inc"`, "inc/greet.inc"},
		// Separators fglcomp accepts that a `&include ` + space matcher missed.
		// Each of these compiled and put `café crème` into the .42m.
		{`&include"greet.inc"`, "greet.inc"},
		{`& include "greet.inc"`, "greet.inc"},
		{"&\tinclude \"greet.inc\"", "greet.inc"},
		{"&" + ff + `include "greet.inc"`, "greet.inc"},
		{`&   include   "greet.inc"`, "greet.inc"},
		{"&include\t\"greet.inc\"", "greet.inc"},
		{ff + `&include "greet.inc"`, "greet.inc"},
		{`&include "greet.inc"  # trailing comment`, "greet.inc"},
		// Not directives: fglcomp compiles these two without reading a file.
		{`# &include "greet.inc"`, ""},
		{`   DISPLAY "&include \"greet.inc\""`, ""},
		// Not directives: fglcomp refuses these outright, so matching them
		// would only decline to prove a package that cannot be built anyway.
		{`&INCLUDE "greet.inc"`, ""},             // -8053, unknown directive
		{`DISPLAY "x" &include "greet.inc"`, ""}, // -6609, not the first token
		{`&include greet.inc`, ""},               // -8102, quotes required
		{`&include <greet.inc>`, ""},             // -8102, quotes required
		{vt + `&include "greet.inc"`, ""},        // -6609, VT is not a separator
		{"&" + vt + `include "greet.inc"`, ""},   // -8102, VT is not a separator
		{`&define GREETING "hi"`, ""},
	}
	for _, tc := range cases {
		m := includeRe.FindStringSubmatch(tc.line)
		got := ""
		if m != nil {
			got = m[1]
		}
		if got != tc.want {
			t.Errorf("includeRe on %q = %q, want %q", tc.line, got, tc.want)
		}
	}
}
