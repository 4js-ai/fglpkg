package cli

import "strings"

// This file holds the single place fglpkg reads a line from a person, and the
// sanitiser that keeps terminal control bytes out of what they typed.
//
// fglpkg has no line editor. Prompts read a whole line from a terminal left in
// cooked mode, so the line discipline handles backspace but nothing else: an
// arrow key sends an escape sequence (ESC [ D and friends) that the tty hands
// straight to the reader. Pressing left-arrow to correct a typo in `fglpkg
// init` therefore INSERTED bytes instead of moving the cursor, and the answer
// — a description, an author — was written into fglpkg.json with them in it,
// where it goes on to be published in the sidecar manifest and the registry
// record (GIS-613).
//
// Stripping them is the mitigation, not the fix: arrow keys still do not edit
// the line, they now simply do nothing. The line editor that would make them
// work needs either fglpkg's first third-party dependency or in-repo raw-mode
// code per OS, which is GIS-617's question to answer. What this file settles is
// narrower and worth settling now — a mis-keyed prompt can no longer corrupt a
// manifest.

// readPromptLine reads one line of user input, with the trailing CR/LF removed
// (so both Unix and Windows line endings answer alike), and returns whatever
// error the read produced so callers can tell a value from end-of-input.
//
// On a terminal the line is sanitised; on anything else it is returned byte for
// byte. That split is deliberate. Escape sequences in an interactive answer are
// keystrokes the terminal failed to interpret, and are never content. Bytes
// arriving down a pipe were put there by a script or a test that meant to send
// them, and silently rewriting those would change what every non-interactive
// caller receives — including the functional suite, which drives these prompts
// through a pipe.
func readPromptLine() (string, error) {
	line, err := reader.ReadString('\n')
	line = strings.TrimRight(line, "\r\n")
	if stdinIsTerminal() {
		line = stripTerminalControls(line)
	}
	return line, err
}

// stripTerminalControls removes ANSI escape sequences and stray control bytes
// from one line of terminal input, leaving ordinary text — including non-ASCII
// text — untouched.
//
// It never returns an error and never rejects a line: an unusable keystroke
// becomes nothing at all, so a user who presses an arrow key and then Enter has
// simply entered an empty line, which the prompts already handle as "accept the
// default". Refusing the input instead would turn a stray keypress into a
// failed command.
func stripTerminalControls(s string) string {
	// The overwhelmingly common case is a clean line; don't rebuild it.
	// (IndexFunc, not ContainsFunc: the latter is Go 1.21 and go.mod says 1.20.)
	if strings.IndexFunc(s, isControlByte) < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case c == 0x1b: // ESC: drop the whole sequence, not just the ESC
			i += escapeSequenceLen(s[i:])
		case c == 0x7f: // DEL, which some terminals send for Backspace
			i++
		case c < 0x20 && c != '\t': // any other C0 control; a tab is real text
			i++
		default:
			b.WriteByte(c) // bytes >= 0x80 pass through, so UTF-8 survives
			i++
		}
	}
	return b.String()
}

// isControlByte reports whether r is a byte stripTerminalControls would drop.
// It is a rune predicate because it feeds strings.IndexFunc, but every value it
// answers true for is single-byte, so a multi-byte rune can never match.
//
// It guards an optimisation, not the behaviour: the scan loop decides what is
// kept. Being WIDER than the loop only costs a needless rebuild of a line that
// did not change. Being NARROWER is a bug — the line returns early with a byte
// in it the loop would have stripped — which is why the cases below pin DEL and
// ESC specifically rather than trusting the two to stay in step by inspection.
func isControlByte(r rune) bool {
	return r == 0x7f || (r < 0x20 && r != '\t')
}

// escapeSequenceLen returns the number of bytes to discard for the escape
// starting at s[0], which the caller guarantees is ESC.
//
// Cooked mode is ambiguous: the terminal sends the same bytes for Alt+f as for
// Esc then "f", and fglpkg cannot tell them apart. The rule here resolves that
// in favour of the reading that loses no typed text — ESC is dropped alone
// unless what follows is unmistakably a sequence (a CSI or OSC introducer, or
// an SS3/SS2 with a final byte terminals actually send). fglpkg binds no Meta
// key, so an Alt chord carries no meaning to discard, while a stray Esc — a vim
// reflex, or an attempt to cancel — is a real thing people do at a prompt.
// Consuming a fixed two or three bytes instead ate the characters they then
// typed ("\x1bOverview" became "erview") and, worse, cut a multi-byte rune in
// half when the next character was not ASCII, leaving invalid UTF-8 for the
// JSON encoder to replace with U+FFFD. Dropping only the ESC cannot split a
// rune, because every byte this function consumes past the ESC is ASCII.
//
// An unterminated CSI or OSC still consumes the rest of the line: there the
// introducer settles what it is, and the alternative is to emit its tail as
// text, which is the corruption being prevented.
func escapeSequenceLen(s string) int {
	if len(s) < 2 {
		return 1 // a lone trailing ESC
	}
	switch s[1] {
	case '[':
		// CSI — arrows, Home/End, Delete, bracketed paste. Parameter bytes
		// 0x30-0x3F, then intermediates 0x20-0x2F, then one final 0x40-0x7E.
		i := 2
		for i < len(s) && s[i] >= 0x30 && s[i] <= 0x3f {
			i++
		}
		for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
			i++
		}
		if i < len(s) && s[i] >= 0x40 && s[i] <= 0x7e {
			i++
		}
		return i
	case ']':
		// OSC — runs to BEL or to the string terminator ESC \.
		for i := 2; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1
			}
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
		return len(s)
	case 'O', 'N':
		// SS3/SS2 — what an arrow or function key sends in application cursor
		// mode, which is the mode a terminal is often left in by a previous
		// full-screen program. Three bytes: ESC O A.
		//
		// Only when the third byte is one a terminal actually sends. "O" is
		// also a letter, so ESC followed by a typed "Overview" must not be read
		// as a keypress and swallow two characters.
		if len(s) >= 3 && isSS3Final(s[2]) {
			return 3
		}
		return 1
	default:
		// Anything else, including a second ESC: a stray Esc keypress. Drop it
		// alone and let the next byte be judged on its own — ESC ESC [ D (one
		// shape of Alt+Left) then strips as the CSI it is, where consuming two
		// bytes left a literal "[D" in the answer.
		return 1
	}
}

// isSS3Final reports whether c is a final byte a terminal actually sends after
// ESC O / ESC N: the four cursor keys (A-D), End (F), Home (H) and F1-F4 (P-S).
// Keypad finals in application mode are deliberately left out — they are
// ordinary lower-case letters, and treating a typed one as a keypress would
// delete text to suppress a key fglpkg does nothing with.
func isSS3Final(c byte) bool {
	switch c {
	case 'A', 'B', 'C', 'D', 'F', 'H', 'P', 'Q', 'R', 'S':
		return true
	}
	return false
}
