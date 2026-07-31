package env

// Shell syntax selection and value quoting for the `fglpkg env` renderers.
//
// Two properties are easy to conflate, and keeping them apart is what makes this
// file correct:
//
//   - The SHELL is a property of whoever will EXECUTE the emitted line. Git Bash
//     on Windows wants ShellSh even though GOOS is windows; pwsh on Linux wants
//     ShellPowerShell even though GOOS is linux. It is chosen by --shell, with
//     DefaultShell() as the fallback.
//   - The path SEPARATOR is a property of the Genero runtime that will PARSE the
//     value: fglrun/fglcomp split FGLLDPATH and FGLPROFILE on ";" on Windows and
//     ":" elsewhere, whatever shell set them. It stays GOOS-derived in
//     pathSeparator() and is threaded into the emitters as an argument.
//
// The combination is real and load-bearing: the functional suite runs a
// GOOS=windows fglpkg.exe under Git Bash, which is ShellSh with a ";" separator.
//
// Two rules govern quoting:
//
//  1. QUOTE ONLY WHEN NEEDED. This output has shipped unquoted since the first
//     release and users have pasted it into ~/.bashrc, .bat files and Genero
//     Studio settings. A value made only of inert characters is emitted
//     byte-for-byte as it always was; quoting engages solely for values that
//     would otherwise be mis-parsed. TestLegacyShellFormatsAreUnchanged locks
//     this down.
//  2. ALLOWLIST, NEVER DENYLIST. A denylist is wrong by default for every
//     character nobody thought of ("|", "\n", "*", "?", U+00A0). An allowlist is
//     at worst over-cautious, and over-cautious means correctly quoted.

import (
	"fmt"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Shell names the syntax the shell renderers emit.
type Shell string

const (
	ShellSh         Shell = "sh"         // POSIX: sh, bash, zsh, dash, Git Bash
	ShellPowerShell Shell = "powershell" // Windows PowerShell 5.1 and pwsh 7+
	ShellCmd        Shell = "cmd"        // cmd.exe and .bat files
)

// shellNames maps every accepted --shell spelling to its Shell. Aliases exist
// because users type the shell they are sitting in, not the family it belongs
// to. The spellings match `fglpkg completion <shell>` (internal/cli/completion.go)
// so the two commands never disagree about what a shell is called.
var shellNames = map[string]Shell{
	"sh":         ShellSh,
	"bash":       ShellSh,
	"zsh":        ShellSh,
	"powershell": ShellPowerShell,
	"pwsh":       ShellPowerShell,
	"cmd":        ShellCmd,
}

// ShellValues lists the accepted --shell values for CLI error messages and help
// text. Kept beside shellNames, and checked against it by
// TestShellValuesCoversEveryAlias, so a hand-maintained message cannot drift
// from the set actually accepted.
const ShellValues = "sh (aliases bash, zsh), powershell (alias pwsh), cmd"

// ParseShell resolves a --shell value, case-insensitively. It reports false
// rather than returning an error so the caller can name the flag in the message
// (the shape parseAuditFlags uses with audit.ValidSeverity).
func ParseShell(s string) (Shell, bool) {
	sh, ok := shellNames[strings.ToLower(strings.TrimSpace(s))]
	return sh, ok
}

// DefaultShell is the shell assumed when --shell is absent: cmd on Windows, sh
// everywhere else.
//
// This stays GOOS-derived on purpose. Every shipped shell profile, .bat file,
// docs sample and test was written against exactly those two shapes, so --shell
// is purely additive — it changes nothing for anyone who does not pass it. The
// broken `fglpkg env --global | Invoke-Expression` advice is fixed by correcting
// the documentation and offering --shell powershell, not by changing what
// Windows emits by default.
func DefaultShell() Shell {
	if runtime.GOOS == "windows" {
		return ShellCmd
	}
	return ShellSh
}

// commentPrefix is the target shell's line-comment marker.
//
// cmd's REM is a command rather than a marker, but it is inert for the hint text
// we emit: a REM line containing "<WEB_COMPONENT_DIRECTORY>" performs no
// redirection and creates no file. A "# " line piped to Invoke-Expression is
// likewise a no-op.
func commentPrefix(sh Shell) string {
	if sh == ShellCmd {
		return "REM "
	}
	return "# "
}

// inertEverywhere lists the ASCII characters no shell we emit for treats
// specially inside an assignment value. Everything absent from the allowlist
// gets quoted.
const inertEverywhere = "0123456789" +
	"ABCDEFGHIJKLMNOPQRSTUVWXYZ" +
	"abcdefghijklmnopqrstuvwxyz" +
	"/._-:+,=@"

// inertInCmd lists the characters cmd.exe leaves alone inside a SET value but a
// POSIX shell does not:
//
//	\  Windows path separator, and sh's escape character.
//	;  Windows PATH separator, so it appears in EVERY multi-entry value — and
//	   sh's command separator. Keeping it inert for cmd is precisely what
//	   preserves byte-identity for today's multi-entry SET lines, since `set`
//	   consumes the raw remainder of the line.
//	~  8.3 short names (PROGRA~1); bash performs tilde expansion after "=".
//	$ # ' [ ] { }  ordinary text to cmd, special or awkward to a POSIX shell.
const inertInCmd = `\;~$#'[]{}`

// needsQuote reports whether value must be quoted to survive sh.
//
// Deliberately NOT inert for any shell, though each occurs in real paths:
// space (word splitting in sh, trailing-space capture in cmd), '"' (quoting in
// both), '%' (cmd expansion), '!' (history expansion in sh, delayed expansion in
// cmd), & | < > ^ ( ) (metacharacters in both), * ? (globbing), '`' (command
// substitution), and CR/LF/TAB.
func needsQuote(sh Shell, value string) bool {
	// PowerShell output is new in this change: there is no shipped unquoted form
	// to stay byte-compatible with, so it is always quoted. One shape is one
	// shape to review, and '…' is total — $, backtick, %, " and \ are all
	// literal inside it.
	if sh == ShellPowerShell {
		return true
	}
	inert := inertEverywhere
	if sh == ShellCmd {
		inert += inertInCmd
	}
	for _, r := range value {
		if r < utf8.RuneSelf {
			if !strings.ContainsRune(inert, r) {
				return true
			}
			continue
		}
		// No shell metacharacter lives outside ASCII, so a printable non-ASCII
		// rune is inert and an i18n path keeps today's bytes. Control and space
		// runes (NBSP, U+2028) are quoted: they are invisible in a diff, and
		// parsing around them varies by shell and locale.
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return true
		}
	}
	return false
}

// quotePOSIX wraps value in single quotes — the only POSIX quoting that is fully
// literal (no expansion, no escape processing, no word splitting). A single
// quote cannot appear inside such a string, so each one is spliced out of the
// quoted run and back in escaped (shown as a code block because gofmt rewrites
// a bare doubled quote in prose into a curly one):
//
//	/opt/it's  ->  '/opt/it'\''s'
func quotePOSIX(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// quotePowerShell wraps value in a single-quoted (literal) PowerShell string,
// where $, backtick, %, " and \ are all inert, doubling any embedded single
// quote — PowerShell's own escape for it:
//
//	C:\it's  ->  'C:\it''s'
func quotePowerShell(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// prependLine emits the one statement that prepends value to key's existing
// value, so user and system entries are never lost.
//
//	sh:         export VAR=value"${VAR:+<sep>$VAR}"
//	powershell: $env:VAR = 'value' + $(if ($env:VAR) { '<sep>' + $env:VAR })
//	cmd:        SET "VAR=value<sep>%VAR%"   (unquoted when value is inert)
//
// sep is the platform path separator — the same string renderShell joined value
// with, passed in rather than looked up so the two can never disagree. See this
// file's header on why the separator and the shell are independent.
func prependLine(sh Shell, key, value, sep string) string {
	switch sh {
	case ShellPowerShell:
		// PowerShell has no ${VAR:+…}, so the conditional suffix is an
		// if-EXPRESSION; the $( ) is required because `if` is a statement and
		// only a subexpression can be used as a value.
		//
		// Unset vs empty needs no special case: $env:X is $null when unset,
		// PowerShell DELETES an environment variable assigned "", and
		// `if ($env:X)` is false for both — exactly the unset-or-empty semantics
		// of sh's ${X:+…}.
		//
		// Only PowerShell 2.0+ syntax is used, since Windows PowerShell 5.1 is
		// the floor: no ??, no ternary, no -join.
		return fmt.Sprintf("$env:%s = %s + $(if ($env:%s) { %s + $env:%s })",
			key, quotePowerShell(value), key, quotePowerShell(sep), key)

	case ShellCmd:
		// SET "VAR=value;%VAR%" — the quotes are consumed by SET rather than
		// stored, and they neutralise space, & | < > ^ ( ) and a trailing space.
		// Without them, `set V=C:\d&e` runs "e" as a command and stores a
		// truncated value.
		//
		// Two things quoting cannot fix, both warned about by
		// shellLimitWarnings: a literal "%" (cmd expands it, and "%%" is an
		// escape only inside a .bat file, not at the prompt) and a literal '"'.
		if needsQuote(ShellCmd, value) {
			return fmt.Sprintf("SET \"%s=%s%s%%%s%%\"", key, value, sep, key)
		}
		return fmt.Sprintf("SET %s=%s%s%%%s%%", key, value, sep, key)

	default: // ShellSh
		// ${VAR:+…} expands to the separator plus the old value ONLY when VAR is
		// non-empty, so an unset variable gains no leading separator (a leading
		// ":" means "the current directory" to fglrun).
		v := value
		if needsQuote(ShellSh, value) {
			v = quotePOSIX(value)
		}
		return fmt.Sprintf("export %s=%s\"${%s:+%s$%s}\"", key, v, key, sep, key)
	}
}

// shellLimitWarnings reports what the target shell cannot carry, so a mangled
// environment is at least a loud one. Returned as strings rather than written to
// a plan, keeping the rule unit-testable; renderShell feeds them to p.warn and
// the CLI prints them to STDERR (never stdout, which is eval'd).
//
// It never suppresses the line. The emitted text is still the user's best
// copy/paste source, and silently dropping a variable is worse than emitting one
// the shell will misread and complain about.
func shellLimitWarnings(sh Shell, key, value string) []string {
	var out []string
	if strings.ContainsAny(value, "\r\n") {
		out = append(out, fmt.Sprintf("%s: a path contains a line break, so the emitted statement spans lines and any line-oriented consumer (`| Invoke-Expression`, `FOR /F`) will mis-read it. Rename the directory.", key))
	}
	if sh == ShellCmd {
		// %% is a .bat-file escape, not a command-prompt one, so there is no
		// single emitted form that is correct in both. Say so rather than
		// picking one and being silently wrong in the other.
		if strings.Contains(value, "%") {
			out = append(out, fmt.Sprintf("%s: a path contains '%%', which cmd.exe expands and cannot escape at the prompt, so the variable will be set wrong. Use --shell powershell, or rename the directory.", key))
		}
		if strings.Contains(value, `"`) {
			out = append(out, fmt.Sprintf("%s: a path contains '\"', which SET \"VAR=...\" cannot carry, so the variable will be set wrong. Use --shell powershell, or rename the directory.", key))
		}
	}
	return out
}
