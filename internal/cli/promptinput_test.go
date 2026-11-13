package cli

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

const (
	esc = "\x1b"
	// What a terminal in normal cursor mode sends for the four arrow keys.
	arrowLeft  = esc + "[D"
	arrowRight = esc + "[C"
	// What it sends in APPLICATION cursor mode — the mode a full-screen program
	// can leave the terminal in, and a shape a CSI-only strip would miss.
	appArrowUp = esc + "OA"
)

func TestStripTerminalControls(t *testing.T) {
	cases := []struct {
		name, in, want, why string
	}{
		{
			"the reported keystroke",
			"A descriptin" + arrowLeft + arrowLeft + "o",
			"A descriptino",
			"GIS-613: arrow keys insert bytes instead of moving; the text is wrong but CLEAN",
		},
		{"clean text is untouched", "A real description", "A real description", ""},
		{"empty stays empty", "", "", ""},
		{"a lone arrow leaves nothing", arrowLeft, "", "an empty line, which prompts already read as 'accept the default'"},
		{"application-mode arrows", "ab" + appArrowUp + "cd", "abcd", "ESC O A is three bytes, not a CSI sequence"},
		{"home and end", esc + "[H" + "x" + esc + "[F", "x", ""},
		{"delete key", "ab" + esc + "[3~" + "cd", "abcd", "a CSI sequence with a parameter and a ~ final byte"},
		{"a parameterised CSI", "a" + esc + "[1;5D" + "b", "ab", "parameters and the ';' separator are part of the sequence"},
		{
			"bracketed paste markers",
			esc + "[200~" + "pasted text" + esc + "[201~",
			"pasted text",
			"a terminal with bracketed paste on wraps what is pasted",
		},
		{"an OSC sequence ended by BEL", "a" + esc + "]0;a title\x07" + "b", "ab", ""},
		{"an OSC sequence ended by ST", "a" + esc + "]0;t" + esc + "\\" + "b", "ab", ""},
		{
			"an UNTERMINATED OSC is a stray Esc, not an OSC",
			"a" + esc + "]b c",
			"a]b c",
			"a prompt reads a whole line at once, so a real OSC always arrives with " +
				"its terminator; an unterminated one is a typed ']'. Consuming to " +
				"end-of-line here deleted everything after it",
		},
		{
			"Esc then a bare bracket at the end of a line",
			esc + "]",
			"]",
			"the ']' is the whole answer; there is nothing for an OSC to terminate",
		},
		{
			"UTF-8 directly after an unterminated OSC introducer",
			"a" + esc + "]\u65e5b",
			"a]\u65e5b",
			"every byte consumed past the ESC is ASCII, so a rune cannot be split",
		},
		{
			"a stray Esc keeps the text that follows it",
			"a" + esc + "b" + "c",
			"abc",
			"cooked mode cannot tell Alt+b from Esc then 'b'; fglpkg binds no Meta " +
				"key, so the reading that keeps what the user typed is the right one",
		},
		{
			"a stray Esc before a capital O",
			"name " + esc + "Overview",
			"name Overview",
			"ESC O is only SS3 when the third byte is one a terminal sends; 'v' is " +
				"not, and reading it as a keypress ate two typed characters",
		},
		{
			"Esc, then a real sequence",
			"a" + esc + arrowLeft + "b",
			"ab",
			"ESC ESC [ D is one shape of Alt+Left; consuming the pair as a two-byte " +
				"escape left a literal '[D' in the answer",
		},
		{
			"an unterminated CSI eats the rest",
			"keep" + esc + "[12",
			"keep",
			"emitting the tail as text is the corruption being prevented",
		},
		{"a trailing lone ESC", "keep" + esc, "keep", ""},
		{"DEL is dropped", "ab\x7fc", "abc", "some terminals send DEL for Backspace"},
		{"a stray control byte", "a\x01b\x02c", "abc", ""},
		{"tab survives", "a\tb", "a\tb", "a tab is text a person can legitimately type"},
		{
			"tab survives the rebuild too",
			"a\tb" + arrowLeft + "c",
			"a\tbc",
			"a line with no control byte returns early, so only a line that ALSO " +
				"trips the scanner proves the loop keeps tabs — without this the " +
				"'tab survives' row above passes even if the loop drops them",
		},
		{
			"UTF-8 survives",
			"café ünïcode 日本語 — ok",
			"café ünïcode 日本語 — ok",
			"bytes >= 0x80 must pass through or every accented author name is mangled",
		},
		{
			"UTF-8 next to an escape",
			"日本" + arrowRight + "語",
			"日本語",
			"the scanner must not resynchronise mid-rune",
		},
		{
			"UTF-8 directly after an ESC",
			"a" + esc + "éb",
			"aéb",
			"consuming a fixed byte count here cut the rune in half and left invalid " +
				"UTF-8, which the JSON encoder writes out as U+FFFD",
		},
		{
			"UTF-8 directly after an SS3 introducer",
			"a" + esc + "O日b",
			"aO日b",
			"the same split one byte further in: 日 is not an SS3 final byte",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stripTerminalControls(tc.in)
			if got != tc.want {
				t.Errorf("stripTerminalControls(%q) = %q, want %q (%s)", tc.in, got, tc.want, tc.why)
			}
			// The acceptance criterion, on every row: whatever comes out is
			// safe to write to a manifest.
			if strings.ContainsAny(got, esc+"\x7f") {
				t.Errorf("result still carries a control byte: %q", got)
			}
			// Valid UTF-8 is part of that criterion: a scan that stops mid-rune
			// leaves orphan bytes that are neither an escape nor readable text,
			// and Go's JSON encoder writes them into the manifest as U+FFFD.
			if !utf8.ValidString(got) {
				t.Errorf("result is not valid UTF-8: %q", got)
			}
		})
	}
}

// Idempotence: sanitising already-clean text must be a no-op, or a value read
// back and re-prompted would keep changing.
func TestStripTerminalControlsIsIdempotent(t *testing.T) {
	for _, in := range []string{"plain", "A descriptin" + arrowLeft + "o", "日本語", "", arrowLeft} {
		once := stripTerminalControls(in)
		if twice := stripTerminalControls(once); twice != once {
			t.Errorf("stripTerminalControls(%q) = %q, then %q — not idempotent", in, once, twice)
		}
	}
}

// readPromptLine sanitises a terminal and leaves a pipe alone. The split is the
// contract GIS-613 is bound by: bytes down a pipe were put there deliberately
// by a script or a test, and rewriting those would change what every
// non-interactive caller — the functional suite included — receives.
func TestReadPromptLineSanitisesOnlyATerminal(t *testing.T) {
	const typed = "desc" + arrowLeft + "x"
	cases := []struct {
		name string
		tty  bool
		want string
	}{
		{"terminal input is sanitised", true, "descx"},
		{"piped input is byte-for-byte", false, typed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			origTTY := stdinIsTerminal
			stdinIsTerminal = func() bool { return tc.tty }
			t.Cleanup(func() { stdinIsTerminal = origTTY })
			withStdin(t, typed+"\n")

			got, err := readPromptLine()
			if err != nil {
				t.Fatalf("readPromptLine: %v", err)
			}
			if got != tc.want {
				t.Errorf("readPromptLine() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The EOF contract must not move: a closed stream still reports the error, so
// required prompts abort instead of looping on an empty line.
func TestReadPromptLineKeepsTheEOFContract(t *testing.T) {
	for _, tty := range []bool{true, false} {
		origTTY := stdinIsTerminal
		stdinIsTerminal = func() bool { return tty }
		withStdin(t, "")
		line, err := readPromptLine()
		stdinIsTerminal = origTTY
		if err == nil {
			t.Errorf("tty=%v: closed stdin returned no error", tty)
		}
		if line != "" {
			t.Errorf("tty=%v: closed stdin returned %q, want empty", tty, line)
		}
	}
	// A final line with no trailing newline is still input, not EOF-with-nothing.
	origTTY := stdinIsTerminal
	stdinIsTerminal = func() bool { return true }
	t.Cleanup(func() { stdinIsTerminal = origTTY })
	withStdin(t, "no newline"+arrowLeft)
	line, err := readPromptLine()
	if line != "no newline" {
		t.Errorf("unterminated final line = %q, want %q", line, "no newline")
	}
	if err == nil {
		t.Error("an unterminated final line should still report the read error")
	}
}

// TestInitNeverWritesEscapeBytesIntoTheManifest is the acceptance criterion
// end to end: the customer corrected a field mid-entry with the arrow keys, and
// the escape sequence was saved into fglpkg.json — from where it would travel
// into the published sidecar and the registry record.
//
// The answers are deliberately the ones a person mistypes and fixes: free text,
// where no later validation would have caught the bytes. (promptPackageSlug
// already re-prompts until the name is a valid slug, so the name field was
// never the exposure.)
func TestInitNeverWritesEscapeBytesIntoTheManifest(t *testing.T) {
	t.Setenv("FGLPKG_GENERO_VERSION", "5.01.00")

	origTTY := stdinIsTerminal
	stdinIsTerminal = func() bool { return true }
	t.Cleanup(func() { stdinIsTerminal = origTTY })

	dir := t.TempDir()
	gitInitWithOrigin(t, dir, "git@github.com:acme/widget.git")
	chdir(t, dir)

	answers := strings.Join([]string{
		"mypkg",
		"",
		"A descriptin" + arrowLeft + arrowLeft + "o", // tried to fix a typo
		"Jane Dev" + appArrowUp,                      // a stray keypress
		"MIT",
		"",
		"current",
	}, "\n") + "\n"
	withStdin(t, answers)

	if _, err := captureStdout(t, func() error { return cmdInit(nil) }); err != nil {
		t.Fatalf("cmdInit: %v", err)
	}

	raw, err := os.ReadFile(manifest.Filename)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if strings.ContainsAny(string(raw), esc+"\x7f") {
		t.Errorf("%s contains terminal control bytes:\n%q", manifest.Filename, raw)
	}

	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// The text is still wrong — the arrow keys did not edit the line, which is
	// what GIS-617 has to answer — but it is text, and it round-trips.
	if m.Description != "A descriptino" {
		t.Errorf("description = %q, want %q", m.Description, "A descriptino")
	}
	if m.Author != "Jane Dev" {
		t.Errorf("author = %q, want %q", m.Author, "Jane Dev")
	}
	if err := m.ValidateForPublish(); err != nil {
		t.Errorf("manifest should still be publishable: %v", err)
	}
}

// TestPromptYesNoReadsThroughTheSanitiser pins the OTHER caller of
// readPromptLine. GIS-613 is a claim about every prompt, not about `fglpkg
// init` alone, and a y/n prompt guards things worth not mis-answering
// (overwrite, publish, prune). Restoring a raw reader.ReadString inside
// promptYesNo survives every other test in the repo, so this one is the whole
// of its coverage.
//
// Each row is chosen so the sanitised and unsanitised readings DISAGREE.
// Unsanitised, the answer starts with ESC, which isValidYesNo rejects, so the
// prompt re-reads — and the re-read hits EOF, which promptYesNo deliberately
// treats as "not confirmed" whatever the default is. Both rows therefore come
// back false if the sanitiser is bypassed.
func TestPromptYesNoReadsThroughTheSanitiser(t *testing.T) {
	cases := []struct {
		name       string
		typed      string
		yesDefault bool
		want       bool
		why        string
	}{
		{
			"an arrow key alone accepts the default",
			arrowLeft + "\n",
			true,
			true,
			"the keystroke leaves nothing, and an empty line is a bare enter",
		},
		{
			"an arrow key before a real answer keeps the answer",
			arrowLeft + "y\n",
			false,
			true,
			"the 'y' the user typed must still count against a [y/N] default",
		},
		{
			"an application-mode arrow is discarded too",
			appArrowUp + "n\n",
			true,
			false,
			"ESC O A is three bytes and would otherwise mask the 'n'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			origTTY := stdinIsTerminal
			stdinIsTerminal = func() bool { return true }
			t.Cleanup(func() { stdinIsTerminal = origTTY })
			withStdin(t, tc.typed)

			var got bool
			if _, err := captureStdout(t, func() error {
				got = promptYesNo("Continue?", tc.yesDefault)
				return nil
			}); err != nil {
				t.Fatalf("captureStdout: %v", err)
			}
			if got != tc.want {
				t.Errorf("promptYesNo(%q, default=%v) = %v, want %v (%s)",
					tc.typed, tc.yesDefault, got, tc.want, tc.why)
			}
		})
	}
}

// TestSS3FinalSet pins the MEMBERS of the SS3/SS2 final set, which the rest of
// the suite does not: an always-true isSS3Final and an unconditional three-byte
// SS3 are both killed above, so the shape of the rule is covered, but dropping
// a single member from the set, or adding one, passed everything.
//
// Both directions corrupt something. Drop a member and that key leaks its bytes
// into the answer — application-mode Home would write "OH" into fglpkg.json,
// which is the whole of GIS-613. Add one and a character the user typed after a
// stray Esc disappears instead.
func TestSS3FinalSet(t *testing.T) {
	// Every final a terminal actually sends after ESC O / ESC N, named by the
	// key that sends it so a deletion fails with the key's name in the message.
	finals := map[byte]string{
		'A': "Up", 'B': "Down", 'C': "Right", 'D': "Left",
		'F': "End", 'H': "Home",
		'P': "F1", 'Q': "F2", 'R': "F3", 'S': "F4",
	}
	for b, key := range finals {
		// SS3 (ESC O) and SS2 (ESC N) take the same finals.
		for _, intro := range []string{"O", "N"} {
			in := "a" + esc + intro + string(b) + "z"
			if got := stripTerminalControls(in); got != "az" {
				t.Errorf("%s (ESC %s %c) = %q, want %q — the keystroke leaks into the answer",
					key, intro, b, got, "az")
			}
		}
	}

	// Nothing else is a final. After a stray Esc, "O" and whatever follows it
	// are characters the user typed and watched appear, so both must survive.
	for _, b := range []byte("EGIJKLMNOTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789") {
		if _, isFinal := finals[b]; isFinal {
			t.Fatalf("the non-final list contains %c, which is in the set", b)
		}
		in := "a" + esc + "O" + string(b) + "z"
		want := "aO" + string(b) + "z"
		if got := stripTerminalControls(in); got != want {
			t.Errorf("ESC O %c = %q, want %q — a non-final must not be read as a keypress",
				b, got, want)
		}
	}
}
