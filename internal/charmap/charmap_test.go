package charmap

import (
	"bytes"
	"io"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want Tier
	}{
		// An empty artifact has no high bytes, so it is trivially portable.
		// It must not land in Undecidable, or a package of empty stubs would
		// be refused by every consumer.
		{"empty", nil, TierASCII},
		{"pure ascii", []byte("DISPLAY \"hello\""), TierASCII},
		{"ascii with control bytes", []byte{0x00, 0x01, 0x7F, 'A'}, TierASCII},
		// The spec's measurement: DISPLAY "café naïve €" compiled under each
		// locale. UTF-8 gives 7 high bytes that decode; ISO-8859-15 gives 3
		// that do not.
		{"utf-8 literal", []byte("caf\xc3\xa9 na\xc3\xafve \xe2\x82\xac"), TierUTF8},
		{"iso-8859-15 literal", []byte("caf\xe9 na\xefve \xa4"), TierUndecidable},
		{"lone continuation byte", []byte{'A', 0x80, 'B'}, TierUndecidable},
		{"truncated sequence at eof", []byte{'A', 0xC3}, TierUndecidable},
		{"overlong encoding", []byte{0xC0, 0xAF}, TierUndecidable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.data); got != tc.want {
				t.Errorf("Classify(%q) = %v, want %v", tc.data, got, tc.want)
			}
		})
	}
}

// TestClassifyReaderSplitSequence is the reason the classifier streams with a
// carry buffer instead of validating each read in isolation. A multi-byte rune
// that straddles a chunk boundary is still valid UTF-8; judging the two halves
// separately would call a good artifact "unknown 8-bit" purely because of
// where the buffer ended, and the package would then be refused by every
// consumer whose charmap is not byte-identical.
func TestClassifyReaderSplitSequence(t *testing.T) {
	// Land the 3-byte € so that it begins one byte before the chunk boundary.
	pad := bytes.Repeat([]byte("a"), chunkSize-1)
	data := append(append([]byte{}, pad...), []byte("\xe2\x82\xac")...)
	data = append(data, bytes.Repeat([]byte("b"), 10)...)

	got, err := ClassifyReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ClassifyReader: %v", err)
	}
	if got != TierUTF8 {
		t.Fatalf("a UTF-8 rune split across the chunk boundary classified as %v, want %v", got, TierUTF8)
	}
}

// oneByteReader hands back a single byte per Read, the pathological case for
// a boundary-sensitive scanner: every multi-byte rune straddles a boundary.
type oneByteReader struct{ data []byte }

func (r *oneByteReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	p[0] = r.data[0]
	r.data = r.data[1:]
	return 1, nil
}

func TestClassifyReaderOneByteAtATime(t *testing.T) {
	got, err := ClassifyReader(&oneByteReader{data: []byte("caf\xc3\xa9 \xe2\x82\xac")})
	if err != nil {
		t.Fatalf("ClassifyReader: %v", err)
	}
	if got != TierUTF8 {
		t.Fatalf("byte-at-a-time UTF-8 classified as %v, want %v", got, TierUTF8)
	}
}

func TestCanonical(t *testing.T) {
	cases := []struct{ in, want string }{
		// Genero reports "ASCII"; IANA prefers "US-ASCII".
		{"ASCII", ASCII},
		{"ascii", ASCII},
		{"US-ASCII", ASCII},
		{"us-ascii", ASCII},
		{"UTF-8", UTF8},
		{"utf8", UTF8},
		// Genero reports "SHIFT_JIS"; IANA prefers "Shift_JIS".
		{"SHIFT_JIS", "Shift_JIS"},
		{"Shift_JIS", "Shift_JIS"},
		{"ISO-8859-15", "ISO-8859-15"},
		{"  UTF-8  ", UTF8},
		{"", ""},
		// An unknown charset is still a usable declaration; it compares by its
		// own spelling rather than being rejected or rewritten.
		{"X-SOMETHING", "X-SOMETHING"},
	}
	for _, tc := range cases {
		if got := Canonical(tc.in); got != tc.want {
			t.Errorf("Canonical(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEqual(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"ASCII", "US-ASCII", true},
		{"ascii", "US-ASCII", true},
		{"us-ascii", "ASCII", true},
		{"SHIFT_JIS", "Shift_JIS", true},
		{"UTF-8", "utf-8", true},
		{"UTF-8", "ISO-8859-15", false},
		{"US-ASCII", "UTF-8", false},
		// An absent declaration means "unknown", never "matches" — otherwise
		// a package with no encoding would silently pass every consumer check
		// by comparing equal to an unset environment.
		{"", "", false},
		{"UTF-8", "", false},
		{"", "UTF-8", false},
	}
	for _, tc := range cases {
		if got := Equal(tc.a, tc.b); got != tc.want {
			t.Errorf("Equal(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// Three states, not two. An earlier IsSingleByte answered true for every
// charset it did not recognise, "conservatively" — which rejected satisfiable
// declarations: "GBK" with lengthSemantics "char" failed the pack, though
// fglrun -i under zh_CN.GBK reports Multibyte: yes (verified against the real
// runtime). The tables are the two halves of $FGLDIR/etc/charmap.alias.
func TestIsKnownSingleByte(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"US-ASCII", true},
		{"ASCII", true},
		{"ISO-8859-1", true},
		{"ISO-8859-15", true},
		{"KOI8-R", true},
		{"TIS-620", true},
		{"windows-1252", true},
		{"HP-ROMAN8", true},
		{"cp850", true},
		// Known multibyte: never single-byte.
		{"UTF-8", false},
		{"Big5", false},
		{"Big5-HKSCS", false},
		{"SHIFT_JIS", false},
		{"EUC-JP", false},
		{"EUC-KR", false},
		{"EUC-TW", false},
		{"GBK", false},
		{"GB18030", false},
		{"GB2312", false},
		{"CP932", false},
		{"JOHAB", false},
		// Unknown: not evidence of anything, so not "single-byte".
		{"X-SOMETHING", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsKnownSingleByte(tc.in); got != tc.want {
			t.Errorf("IsKnownSingleByte(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// The two tables must not overlap, and must not both answer for an unknown
// charset — that third state is what the design depends on.
func TestSingleByteAndMultibyteAreDisjoint(t *testing.T) {
	for name := range singleByteNames {
		if multibyteNames[name] {
			t.Errorf("%q is listed as both single-byte and multibyte", name)
		}
	}
	for _, unknown := range []string{"X-SOMETHING", "ISO-8859-42", ""} {
		if IsKnownSingleByte(unknown) || IsKnownMultibyte(unknown) {
			t.Errorf("%q is unknown and must answer false to both predicates", unknown)
		}
	}
}

func TestIsKnownMultibyte(t *testing.T) {
	for _, in := range []string{"UTF-8", "GBK", "EUC-KR", "Shift_JIS", "Big5"} {
		if !IsKnownMultibyte(in) {
			t.Errorf("IsKnownMultibyte(%q) = false, want true", in)
		}
	}
	for _, in := range []string{"ISO-8859-15", "US-ASCII", "X-SOMETHING", ""} {
		if IsKnownMultibyte(in) {
			t.Errorf("IsKnownMultibyte(%q) = true, want false", in)
		}
	}
}

func TestTierString(t *testing.T) {
	if got := TierASCII.String(); got != ASCII {
		t.Errorf("TierASCII.String() = %q, want %q", got, ASCII)
	}
	if got := TierUTF8.String(); got != UTF8 {
		t.Errorf("TierUTF8.String() = %q, want %q", got, UTF8)
	}
	if got := TierUndecidable.String(); got == ASCII || got == UTF8 {
		t.Errorf("TierUndecidable.String() = %q, must not name a concrete charset", got)
	}
}
