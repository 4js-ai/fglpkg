package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/charmap"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// stageFiles writes each archivePath->content pair to a temp dir and returns
// the staged map pack would have built (archive path -> source path on disk).
func stageFiles(t *testing.T, files map[string]string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	staged := make(map[string]string, len(files))
	for archivePath, content := range files {
		src := filepath.Join(dir, strings.ReplaceAll(archivePath, "/", "_"))
		if err := os.WriteFile(src, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", archivePath, err)
		}
		staged[archivePath] = src
	}
	return staged
}

const (
	utf8Literal = "caf\xc3\xa9 na\xc3\xafve \xe2\x82\xac" // 7 high bytes, all decode
	latin9Lit   = "caf\xe9 na\xefve \xa4"                 // 3 high bytes, none decode
)

func TestRecordCharacterEncoding_DerivesASCII(t *testing.T) {
	staged := stageFiles(t, map[string]string{
		"lib/Util.42m": "DISPLAY \"hello\"",
		"forms/F.42f":  `<?xml version='1.0' encoding='ASCII'?><Form/>`,
	})
	m := &manifest.Manifest{}
	if err := recordCharacterEncoding(m, staged); err != nil {
		t.Fatalf("recordCharacterEncoding: %v", err)
	}
	if m.CharacterEncoding != charmap.ASCII {
		t.Errorf("CharacterEncoding = %q, want %q", m.CharacterEncoding, charmap.ASCII)
	}
}

func TestRecordCharacterEncoding_DerivesUTF8FromBytes(t *testing.T) {
	staged := stageFiles(t, map[string]string{
		"lib/Util.42m": "DISPLAY \"" + utf8Literal + "\"",
	})
	m := &manifest.Manifest{}
	if err := recordCharacterEncoding(m, staged); err != nil {
		t.Fatalf("recordCharacterEncoding: %v", err)
	}
	if m.CharacterEncoding != charmap.UTF8 {
		t.Errorf("CharacterEncoding = %q, want %q", m.CharacterEncoding, charmap.UTF8)
	}
}

// A form's prolog is authoritative: it names the encoding outright, so it
// decides even when every other artifact is pure ASCII and the byte scan alone
// would have concluded US-ASCII.
func TestRecordCharacterEncoding_PrologWinsOverASCIIBytes(t *testing.T) {
	staged := stageFiles(t, map[string]string{
		"lib/Util.42m": "DISPLAY \"plain\"",
		"forms/F.42f":  `<?xml version='1.0' encoding='ISO-8859-15'?><Form/>`,
	})
	m := &manifest.Manifest{}
	if err := recordCharacterEncoding(m, staged); err != nil {
		t.Fatalf("recordCharacterEncoding: %v", err)
	}
	if m.CharacterEncoding != "ISO-8859-15" {
		t.Errorf("CharacterEncoding = %q, want ISO-8859-15", m.CharacterEncoding)
	}
}

func TestRecordCharacterEncoding_DisagreeingPrologsFail(t *testing.T) {
	staged := stageFiles(t, map[string]string{
		"forms/A.42f": `<?xml version='1.0' encoding='UTF-8'?><Form/>`,
		"forms/B.42f": `<?xml version='1.0' encoding='ISO-8859-15'?><Form/>`,
	})
	m := &manifest.Manifest{}
	err := recordCharacterEncoding(m, staged)
	if err == nil {
		t.Fatal("two forms declaring different encodings must fail the pack, got nil")
	}
	for _, want := range []string{"UTF-8", "ISO-8859-15", "forms/A.42f", "forms/B.42f"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// A wrong US-ASCII claim is the most damaging one to let through: US-ASCII is
// the tier install skips its check for, so the package would ship mojibake to
// every consumer without a word.
func TestRecordCharacterEncoding_FalseASCIIClaimFails(t *testing.T) {
	staged := stageFiles(t, map[string]string{
		"lib/Util.42m": "DISPLAY \"" + utf8Literal + "\"",
	})
	m := &manifest.Manifest{CharacterEncoding: "US-ASCII"}
	err := recordCharacterEncoding(m, staged)
	if err == nil {
		t.Fatal("declaring US-ASCII with high bytes present must fail, got nil")
	}
	if !strings.Contains(err.Error(), "lib/Util.42m") {
		t.Errorf("error %q does not name the offending file", err)
	}
}

func TestRecordCharacterEncoding_DeclarationContradictingPrologFails(t *testing.T) {
	staged := stageFiles(t, map[string]string{
		"forms/F.42f": `<?xml version='1.0' encoding='UTF-8'?><Form/>`,
	})
	m := &manifest.Manifest{CharacterEncoding: "ISO-8859-15"}
	err := recordCharacterEncoding(m, staged)
	if err == nil {
		t.Fatal("a declaration contradicting a form prolog must fail, got nil")
	}
	if !strings.Contains(err.Error(), "forms/F.42f") {
		t.Errorf("error %q does not name the form", err)
	}
}

// The one thing an author can tell us that the bytes cannot: which 8-bit
// charset it is. ISO-8859-1 and -15 are indistinguishable from bytes, so a
// declaration that merely narrows an undecidable result must be accepted in
// silence, not second-guessed.
func TestRecordCharacterEncoding_DeclarationNarrowsUndecidable(t *testing.T) {
	staged := stageFiles(t, map[string]string{
		"lib/Util.42m": "DISPLAY \"" + latin9Lit + "\"",
	})
	m := &manifest.Manifest{CharacterEncoding: "iso-8859-15"}
	if err := recordCharacterEncoding(m, staged); err != nil {
		t.Fatalf("narrowing an undecidable result must be accepted, got: %v", err)
	}
	if m.CharacterEncoding != "ISO-8859-15" {
		t.Errorf("CharacterEncoding = %q, want the canonical ISO-8859-15", m.CharacterEncoding)
	}
}

// A pure-webcomponent package ships no compiled BDL artifacts. Labelling it
// US-ASCII on the strength of a vacuous "every artifact was ASCII" would state
// something about compiled output that does not exist.
func TestRecordCharacterEncoding_NoArtifactsLeavesFieldAbsent(t *testing.T) {
	staged := stageFiles(t, map[string]string{
		"MyWidget/index.html": "<html></html>",
		"README.md":           "# docs",
	})
	m := &manifest.Manifest{}
	if err := recordCharacterEncoding(m, staged); err != nil {
		t.Fatalf("recordCharacterEncoding: %v", err)
	}
	if m.CharacterEncoding != "" {
		t.Errorf("CharacterEncoding = %q, want empty for a package with no BDL artifacts", m.CharacterEncoding)
	}
}

// Undecidable bytes with no declaration: the build environment is consulted,
// but only to fill the gap the bytes left — and it says so.
func TestRecordCharacterEncoding_UndecidableFallsBackToEnvironment(t *testing.T) {
	t.Setenv("FGLPKG_CHARMAP", "ISO-8859-1")
	t.Setenv("FGLPKG_LENGTH_SEMANTICS", "BYTE")
	staged := stageFiles(t, map[string]string{
		"lib/Util.42m": "DISPLAY \"" + latin9Lit + "\"",
	})
	m := &manifest.Manifest{}
	if err := recordCharacterEncoding(m, staged); err != nil {
		t.Fatalf("recordCharacterEncoding: %v", err)
	}
	if m.CharacterEncoding != "ISO-8859-1" {
		t.Errorf("CharacterEncoding = %q, want the environment's ISO-8859-1", m.CharacterEncoding)
	}
}

// Sources are not scanned. A .4gl may carry UTF-8 comments and still compile
// to pure-ASCII p-code; what a consumer runs is the compiled output.
func TestRecordCharacterEncoding_IgnoresSources(t *testing.T) {
	staged := stageFiles(t, map[string]string{
		"lib/Util.42m": "DISPLAY \"plain\"",
		"lib/Util.4gl": "# comment with " + utf8Literal,
	})
	m := &manifest.Manifest{}
	if err := recordCharacterEncoding(m, staged); err != nil {
		t.Fatalf("recordCharacterEncoding: %v", err)
	}
	if m.CharacterEncoding != charmap.ASCII {
		t.Errorf("CharacterEncoding = %q, want %q — sources must not be scanned",
			m.CharacterEncoding, charmap.ASCII)
	}
}

// The file named in an error must be stable: map iteration order would
// otherwise make two identical packs blame different files.
func TestRecordCharacterEncoding_ErrorNamesAStableFile(t *testing.T) {
	files := map[string]string{
		"a/One.42m":   "DISPLAY \"" + utf8Literal + "\"",
		"b/Two.42m":   "DISPLAY \"" + utf8Literal + "\"",
		"c/Three.42m": "DISPLAY \"" + utf8Literal + "\"",
	}
	var first string
	for i := 0; i < 8; i++ {
		m := &manifest.Manifest{CharacterEncoding: "US-ASCII"}
		err := recordCharacterEncoding(m, stageFiles(t, files))
		if err == nil {
			t.Fatal("expected a false-ASCII failure")
		}
		if i == 0 {
			first = err.Error()
			continue
		}
		if err.Error() != first {
			t.Fatalf("error message varies between runs:\n  %s\n  %s", first, err)
		}
	}
	if !strings.Contains(first, "a/One.42m") {
		t.Errorf("expected the lexically first file to be named, got %q", first)
	}
}
