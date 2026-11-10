package genero

import (
	"strings"
	"testing"
)

// The exact shape `fglrun -i` prints, as measured for GIS-285.
const fglrunIOutput = `Genero BDL Version 6.00.01
Charmap      : UTF-8
Multibyte    : yes
Stateless    : yes
Length Semantics : CHAR
`

func TestParseFglrunInfoLines(t *testing.T) {
	cases := []struct {
		name           string
		out            string
		wantCharmap    string
		wantLengthSema string
	}{
		{"measured output", fglrunIOutput, "UTF-8", "CHAR"},
		// The label-to-colon spacing is not fixed, so neither line may be
		// matched by column.
		{"tight spacing", "Charmap: ASCII\nLength Semantics: BYTE\n", "ASCII", "BYTE"},
		{"wide spacing", "Charmap        :   ISO-8859-15\nLength Semantics   :   BYTE\n", "ISO-8859-15", "BYTE"},
		{"leading indent", "  Charmap : Big5\n  Length Semantics : BYTE\n", "Big5", "BYTE"},
		// fglcomp -i reports Charmap but NOT Length Semantics — the whole
		// reason this plumbing runs fglrun instead.
		{"charmap only", "Charmap      : UTF-8\nMultibyte    : yes\n", "UTF-8", ""},
		{"neither", "Genero BDL Version 6.00.01\n", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotCharmap, gotLength string
			if m := charmapLineRe.FindStringSubmatch(tc.out); m != nil {
				gotCharmap = m[1]
			}
			if m := lengthSemanticsLineRe.FindStringSubmatch(tc.out); m != nil {
				gotLength = m[1]
			}
			if gotCharmap != tc.wantCharmap {
				t.Errorf("charmap = %q, want %q", gotCharmap, tc.wantCharmap)
			}
			if gotLength != tc.wantLengthSema {
				t.Errorf("length semantics = %q, want %q", gotLength, tc.wantLengthSema)
			}
		})
	}
}

// With both overrides set, fglrun is never executed — which is what lets the
// functional suite run on a host with no Genero toolchain, and keeps detection
// from depending on the machine's locale.
func TestDetectRuntimeInfoBothOverrides(t *testing.T) {
	t.Setenv("FGLPKG_CHARMAP", "ISO-8859-15")
	t.Setenv("FGLPKG_LENGTH_SEMANTICS", "byte")
	t.Setenv("PATH", t.TempDir()) // no fglrun reachable
	t.Setenv("FGLDIR", "")

	info, err := DetectRuntimeInfo()
	if err != nil {
		t.Fatalf("DetectRuntimeInfo: %v", err)
	}
	if info.Charmap != "ISO-8859-15" {
		t.Errorf("Charmap = %q, want ISO-8859-15", info.Charmap)
	}
	// Normalized to upper case so it compares with what fglrun reports.
	if info.LengthSemantics != "BYTE" {
		t.Errorf("LengthSemantics = %q, want BYTE", info.LengthSemantics)
	}
}

// A partial override still answers when fglrun is unreachable, rather than
// discarding the half the caller did supply.
func TestDetectRuntimeInfoPartialOverrideWithoutFglrun(t *testing.T) {
	t.Setenv("FGLPKG_CHARMAP", "UTF-8")
	t.Setenv("FGLPKG_LENGTH_SEMANTICS", "")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("FGLDIR", "")

	info, err := DetectRuntimeInfo()
	if err != nil {
		t.Fatalf("a partial override must still answer: %v", err)
	}
	if info.Charmap != "UTF-8" {
		t.Errorf("Charmap = %q, want UTF-8", info.Charmap)
	}
}

// With no overrides and no fglrun, detection must fail loudly and name the
// escape hatch rather than returning a silent empty charmap that every
// comparison would then quietly mismatch against.
func TestDetectRuntimeInfoNoOverridesNoFglrun(t *testing.T) {
	t.Setenv("FGLPKG_CHARMAP", "")
	t.Setenv("FGLPKG_LENGTH_SEMANTICS", "")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("FGLDIR", "")

	_, err := DetectRuntimeInfo()
	if err == nil {
		t.Fatal("DetectRuntimeInfo() = nil error with no fglrun and no overrides")
	}
	if !strings.Contains(err.Error(), "FGLPKG_CHARMAP") {
		t.Errorf("error %q does not name the FGLPKG_CHARMAP override", err)
	}
}
