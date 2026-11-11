// Package charmap classifies the character encoding of compiled Genero
// artifacts and compares IANA charset names.
//
// It exists because `fglcomp`/`fglform` write string literals into
// .42m/.42f/.42s in the encoding of the build locale, so a package built under
// UTF-8 produces mojibake — or silently wrong string lengths — when consumed
// under ISO-8859-15. See specs/character-encoding.md (GIS-285).
//
// What a byte scan can prove differs sharply by artifact, which the callers
// must respect (measured against Genero BDL 6.00.01):
//
//   - .42s is a clean container: a resource file built from an ASCII .str has
//     no byte >= 0x80, a UTF-8 one decodes as UTF-8, and an 8-bit one does
//     not. Its bytes are reliable evidence, and are identical whatever locale
//     fglmkstr ran under.
//   - .42m carries literal bytes verbatim, but its own STRUCTURE also contains
//     bytes >= 0x80 — opcode operands and the embedded source-path length. Of
//     99 real modules compiled from pure-ASCII sources, only 21 had no high
//     byte. So "no high byte" proves the text is ASCII, and a high byte proves
//     NOTHING. The file is byte-identical across build locales, so it records
//     no charset of its own.
//   - .42f carries an XML prolog, but the prolog names the BUILD LOCALE and
//     never the content. The same UTF-8 .per compiled under en_US.UTF-8,
//     LANG=C and en_US.ISO8859-15 gives identical TEXT bytes and prologs of
//     UTF-8, ASCII and ISO-8859-15 — so a prolog cannot classify a
//     form even when the form really does carry non-ASCII text. A .42f is a
//     clean container like a .42s and is classified by its own bytes. Nothing
//     here reads the prolog: measured, fglrun uses a form's bytes as stored
//     whatever the prolog says, so there is no decision it could inform.
package charmap

import (
	"bytes"
	"io"
	"strings"
	"unicode/utf8"
)

// Tier is the conclusion a byte scan supports about one artifact.
type Tier int

const (
	// TierASCII means no byte >= 0x80 is present, so the artifact is portable
	// to every charset Genero supports (ISO-8859-*, UTF-8 and Big5 are all
	// ASCII-7 compatible).
	TierASCII Tier = iota
	// TierUTF8 means high bytes are present and the whole stream decodes as
	// UTF-8.
	TierUTF8
	// TierUndecidable means high bytes are present but do not decode as UTF-8,
	// so the artifact uses some 8-bit charset. ISO-8859-1 vs -15 cannot be
	// told apart from bytes; only a declaration resolves it.
	TierUndecidable
)

func (t Tier) String() string {
	switch t {
	case TierASCII:
		return "US-ASCII"
	case TierUTF8:
		return "UTF-8"
	default:
		return "unknown 8-bit"
	}
}

// IANA charset names, as the spec's measurement table records them.
const (
	ASCII = "US-ASCII"
	UTF8  = "UTF-8"
)

// canonicalNames maps a lowercased charset name to its IANA preferred form.
// Genero's `fglrun -i` Charmap values are near-IANA, with two wrinkles the
// spec measured: Genero reports "ASCII", a registered alias of IANA's
// preferred "US-ASCII"; and "SHIFT_JIS", which differs from IANA's "Shift_JIS"
// only in case. IANA charset names are case-insensitive, so the case wrinkle
// is absorbed by comparing lowercased; only the alias needs a real mapping.
var canonicalNames = map[string]string{
	"ascii":       ASCII,
	"us-ascii":    ASCII,
	"utf-8":       UTF8,
	"utf8":        UTF8,
	"iso-8859-1":  "ISO-8859-1",
	"iso-8859-15": "ISO-8859-15",
	"big5":        "Big5",
	"big5-hkscs":  "Big5-HKSCS",
	"shift_jis":   "Shift_JIS",
	"sjis":        "Shift_JIS",
	"euc-jp":      "EUC-JP",
	"euc-kr":      "EUC-KR",
	"euc-tw":      "EUC-TW",
	"koi8-r":      "KOI8-R",
}

// singleByteNames lists every charset that encodes each character in exactly
// one byte, keyed by lowercased name. It is the complete single-byte half of
// the charset table Genero itself ships, $FGLDIR/etc/charmap.alias, whose 70
// canonical names are enumerable; the multibyte half is listed below for
// documentation, and everything absent from BOTH is simply unknown.
//
// Three states matter, not two. Its one caller, Validate, rejects
// lengthSemantics "char" against a charset where the runtime would silently
// downgrade it to BYTE — so it may only reject a charset known to be
// single-byte. An earlier version treated every UNKNOWN charset as
// single-byte "conservatively", which rejected satisfiable declarations:
// "GBK" with "char" failed the pack, though fglrun -i under zh_CN.GBK reports
// Multibyte: yes (verified).
var singleByteNames = map[string]bool{
	"us-ascii": true, "ascii": true,
	"iso-8859-1": true, "iso-8859-2": true, "iso-8859-3": true,
	"iso-8859-4": true, "iso-8859-5": true, "iso-8859-6": true,
	"iso-8859-7": true, "iso-8859-8": true, "iso-8859-9": true,
	"iso-8859-13": true, "iso-8859-15": true,
	"koi8-r": true, "tis-620": true,
	"hp-roman8": true, "hp-arabic8": true, "hp-greek8": true,
	"hp-hebrew8": true, "hp-turkish8": true, "hp-kana8": true,
	"cp437": true, "cp737": true, "cp775": true, "cp850": true,
	"cp852": true, "cp855": true, "cp856": true, "cp857": true,
	"cp860": true, "cp861": true, "cp862": true, "cp863": true,
	"cp864": true, "cp865": true, "cp866": true, "cp869": true,
	"cp874": true, "cp922": true, "cp1046": true, "cp1124": true,
	"cp1129": true, "cp1251": true, "cp1252": true,
	"windows-1250": true, "windows-1251": true, "windows-1252": true,
	"windows-1253": true, "windows-1254": true, "windows-1255": true,
	"windows-1256": true, "windows-1257": true, "windows-1258": true,
}

// multibyteNames is the other half of the same table. Nothing consults it to
// decide anything — an unknown charset and a known multibyte one are both
// simply "not known to be single-byte" — but it records what the table covers
// so the two lists can be checked against charmap.alias when Genero adds a
// charset.
var multibyteNames = map[string]bool{
	"utf-8": true, "big5": true, "big5-hkscs": true,
	"shift_jis": true, "cp932": true, "cp936": true, "cp943": true,
	"cp949": true, "cp950": true, "johab": true,
	"euc-jp": true, "euc-kr": true, "euc-tw": true,
	"gb2312": true, "gbk": true, "gb18030": true,
	"dec-hanyu": true, "dec-kanji": true,
}

// Canonical returns the IANA preferred spelling of a charset name, or the
// trimmed input unchanged when the name is not one fglpkg knows. It never
// returns an error: an unrecognised charset is still a usable declaration, it
// simply compares by its own spelling.
func Canonical(name string) string {
	s := strings.TrimSpace(name)
	if s == "" {
		return ""
	}
	if c, ok := canonicalNames[strings.ToLower(s)]; ok {
		return c
	}
	return s
}

// Equal reports whether two charset names denote the same charset. IANA
// charset names are case-insensitive, and "ASCII" is an alias of "US-ASCII",
// so both are folded before comparison. Two empty names are not equal — an
// absent declaration means "unknown", never "matches".
func Equal(a, b string) bool {
	ca, cb := Canonical(a), Canonical(b)
	if ca == "" || cb == "" {
		return false
	}
	return strings.EqualFold(ca, cb)
}

// IsKnownSingleByte reports whether fglpkg POSITIVELY knows this charset
// encodes every character in one byte. An unknown charset answers false,
// because "we have never heard of it" is not evidence that it is single-byte,
// and the only caller uses this to reject a declaration.
func IsKnownSingleByte(name string) bool {
	c := Canonical(name)
	if c == "" {
		return false
	}
	return singleByteNames[strings.ToLower(c)]
}

// IsKnownMultibyte reports whether fglpkg positively knows this charset
// encodes some character in more than one byte. Unknown answers false too, so
// IsKnownSingleByte and IsKnownMultibyte are both false for a charset outside
// the table — that is the third state, and callers must not treat the absence
// of one as the other.
func IsKnownMultibyte(name string) bool {
	c := Canonical(name)
	if c == "" {
		return false
	}
	return multibyteNames[strings.ToLower(c)]
}

// chunkSize is the read granularity of ClassifyReader. A UTF-8 sequence can
// straddle any boundary, which is why the classifier carries an incomplete
// trailing sequence over to the next chunk rather than judging it in place.
const chunkSize = 32 * 1024

// ClassifyReader reads r to EOF and reports which tier its bytes support.
//
// The scan is streaming and boundary-safe: a multi-byte UTF-8 sequence split
// across two reads is carried over rather than being misjudged as invalid,
// which would otherwise classify a perfectly good UTF-8 artifact as an unknown
// 8-bit one purely because of where a buffer happened to end.
func ClassifyReader(r io.Reader) (Tier, error) {
	var (
		buf      = make([]byte, chunkSize)
		carry    []byte
		highSeen bool
		utf8OK   = true
	)
	for {
		n, err := r.Read(buf[:])
		if n > 0 {
			chunk := buf[:n]
			if len(carry) > 0 {
				chunk = append(carry, chunk...)
				carry = nil
			}
			for _, b := range chunk {
				if b >= 0x80 {
					highSeen = true
					break
				}
			}
			if utf8OK {
				// Hold back an incomplete trailing sequence (at most 3 bytes)
				// so it can be judged once its continuation bytes arrive.
				keep := incompleteTail(chunk)
				head := chunk[:len(chunk)-keep]
				if !utf8.Valid(head) {
					utf8OK = false
				}
				if keep > 0 {
					carry = append(carry, chunk[len(chunk)-keep:]...)
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return TierUndecidable, err
		}
	}
	// Anything still held back at EOF was genuinely truncated, not straddling.
	if utf8OK && len(carry) > 0 && !utf8.Valid(carry) {
		utf8OK = false
	}
	switch {
	case !highSeen:
		return TierASCII, nil
	case utf8OK:
		return TierUTF8, nil
	default:
		return TierUndecidable, nil
	}
}

// Classify is ClassifyReader over a byte slice already in memory.
func Classify(data []byte) Tier {
	t, _ := ClassifyReader(bytes.NewReader(data))
	return t
}

// incompleteTail returns how many bytes at the end of b begin a UTF-8 sequence
// that is not yet complete, and so must wait for the next chunk. It returns 0
// when the slice ends on a sequence boundary or on something that can never
// become valid — those are judged now, not deferred.
func incompleteTail(b []byte) int {
	// A UTF-8 sequence is at most 4 bytes, so only the last 3 can be partial.
	max := 3
	if len(b) < max {
		max = len(b)
	}
	for back := 1; back <= max; back++ {
		c := b[len(b)-back]
		if c < 0x80 {
			return 0 // ASCII byte: nothing straddles here.
		}
		if c&0xC0 == 0xC0 { // a lead byte
			if seqLen(c) > back {
				return back // genuinely incomplete
			}
			return 0 // complete (or over-long) sequence: judge it now
		}
		// continuation byte: keep walking back toward its lead byte
	}
	return 0
}

// seqLen returns the total length of the UTF-8 sequence a lead byte starts,
// or 0 when the byte is not a lead byte.
func seqLen(lead byte) int {
	switch {
	case lead&0xE0 == 0xC0:
		return 2
	case lead&0xF0 == 0xE0:
		return 3
	case lead&0xF8 == 0xF0:
		return 4
	}
	return 0
}
