package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/charmap"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// These tests run on REAL compiled artifacts (internal/cli/testdata/encoding),
// not on source text written into files named .42m. That distinction is the
// whole point: fake fixtures never contain the structure bytes >= 0x80 that a
// real .42m carries, so a suite built on them agreed with a byte scan that
// disagrees with 78 of 99 real modules.

// stageFixtures copies named fixtures into a temp dir and returns the staged
// map pack would have built (archive path -> path on disk). Each artifact is
// copied with its own basename, and with the SOURCE it was built from when one
// exists, because the source beside an artifact is what proves its text ASCII.
func stageFixtures(t *testing.T, names ...string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	staged := map[string]string{}
	for _, name := range names {
		for _, f := range fixtureAndSource(name) {
			data, err := os.ReadFile(filepath.Join("testdata", "encoding", f))
			if err != nil {
				t.Fatalf("read fixture %s: %v", f, err)
			}
			dst := filepath.Join(dir, f)
			if err := os.WriteFile(dst, data, 0o644); err != nil {
				t.Fatalf("write %s: %v", dst, err)
			}
			if f == name {
				staged[f] = dst
			}
		}
	}
	return staged
}

// stageArtifactOnly copies an artifact WITHOUT the source beside it — a pack
// run against a dist directory rather than a build tree.
func stageArtifactOnly(t *testing.T, name string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	data, err := os.ReadFile(filepath.Join("testdata", "encoding", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	dst := filepath.Join(dir, name)
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
	return map[string]string{name: dst}
}

func fixtureAndSource(name string) []string {
	switch filepath.Ext(name) {
	case ".42m":
		return []string{name, strings.TrimSuffix(name, ".42m") + ".4gl"}
	case ".42f":
		return []string{name, strings.TrimSuffix(name, ".42f") + ".per"}
	}
	return []string{name}
}

func recordOrFail(t *testing.T, m *manifest.Manifest, staged map[string]string) {
	t.Helper()
	if err := recordEncoding(m, staged); err != nil {
		t.Fatalf("recordEncoding: %v", err)
	}
}

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

// ...but a form that really carries non-ASCII text IS described by its prolog,
// which is the one positive proof of a non-ASCII code encoding available.
func TestRecordEncoding_FormPrologNamesRealNonASCIIText(t *testing.T) {
	staged := stageFixtures(t, "Utf8Form.42f")
	m := &manifest.Manifest{}
	recordOrFail(t, m, staged)
	if m.CharacterEncoding != charmap.UTF8 {
		t.Errorf("CharacterEncoding = %q, want %q", m.CharacterEncoding, charmap.UTF8)
	}
}

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
	staged := stageFixtures(t, "Utf8Msg.42s", "AsciiMsg.42s")
	m := &manifest.Manifest{
		StringResourceEncodings: map[string]string{"Latin9Msg.42s": "ISO-8859-15"},
	}
	staged["Latin9Msg.42s"] = stageArtifactOnly(t, "Latin9Msg.42s")["Latin9Msg.42s"]
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

// The cross-check that stayed: a form carrying non-ASCII text names its
// encoding, so a declaration that disagrees is provably wrong.
func TestRecordEncoding_DeclarationContradictingARealPrologFails(t *testing.T) {
	staged := stageFixtures(t, "Utf8Form.42f")
	m := &manifest.Manifest{CharacterEncoding: "ISO-8859-15"}
	err := recordEncoding(m, staged)
	if err == nil {
		t.Fatal("a declaration contradicting a non-ASCII form's prolog must fail")
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

// Two forms that each carry non-ASCII text in different charsets cannot both
// be right, so the pack fails rather than picking one.
func TestRecordEncoding_DisagreeingFormsFail(t *testing.T) {
	staged := stageFixtures(t, "Utf8Form.42f")
	dir := t.TempDir()
	// A second form carrying a non-UTF-8 high byte and an ISO-8859-15 prolog.
	latin := []byte("<?xml version='1.0' encoding='ISO-8859-15'?><Form><t>caf\xe9</t></Form>")
	p := filepath.Join(dir, "Latin9Form.42f")
	if err := os.WriteFile(p, latin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	staged["Latin9Form.42f"] = p
	if err := recordEncoding(&manifest.Manifest{}, staged); err == nil {
		t.Fatal("two forms naming different charsets for real non-ASCII text must fail the pack")
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
