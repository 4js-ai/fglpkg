package env

import (
	"runtime"
	"strings"
	"testing"
)

// TestLegacyShellFormatsAreUnchanged is the tripwire for this change's
// compatibility promise: for a value made only of inert characters, the emitted
// bytes are exactly what fglpkg emitted before --shell and quoting existed.
//
// These exact strings sit in users' ~/.bashrc and setup-env.bat files and are
// printed verbatim in docs/user-guide.md and README.md. Changing them is a
// breaking change, and this test exists so it can only ever be a deliberate one.
func TestLegacyShellFormatsAreUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name       string
		sh         Shell
		key, value string
		sep        string
		want       string
	}{
		{
			name: "posix single entry",
			sh:   ShellSh, key: "FGLLDPATH", value: "/opt/pkg/merged", sep: ":",
			want: `export FGLLDPATH=/opt/pkg/merged"${FGLLDPATH:+:$FGLLDPATH}"`,
		},
		{
			name: "posix multi entry",
			sh:   ShellSh, key: "FGLRESOURCEPATH", value: "/a/forms:/b/forms", sep: ":",
			want: `export FGLRESOURCEPATH=/a/forms:/b/forms"${FGLRESOURCEPATH:+:$FGLRESOURCEPATH}"`,
		},
		{
			// The alwaysLD case: `fglpkg env` emits FGLLDPATH even when empty.
			// needsQuote("") is false, which is exactly why the empty string must
			// fall on the unquoted branch.
			name: "posix empty value",
			sh:   ShellSh, key: "FGLLDPATH", value: "", sep: ":",
			want: `export FGLLDPATH="${FGLLDPATH:+:$FGLLDPATH}"`,
		},
		{
			// The literal printed at docs/user-guide.md:179.
			name: "cmd single entry",
			sh:   ShellCmd, key: "FGLLDPATH", value: `C:\Users\you\.fglpkg\merged`, sep: ";",
			want: `SET FGLLDPATH=C:\Users\you\.fglpkg\merged;%FGLLDPATH%`,
		},
		{
			name: "cmd multi entry",
			sh:   ShellCmd, key: "CLASSPATH", value: `C:\a\poi.jar;C:\b\log4j.jar`, sep: ";",
			want: `SET CLASSPATH=C:\a\poi.jar;C:\b\log4j.jar;%CLASSPATH%`,
		},
		{
			name: "cmd empty value",
			sh:   ShellCmd, key: "FGLLDPATH", value: "", sep: ";",
			want: `SET FGLLDPATH=;%FGLLDPATH%`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := prependLine(tc.sh, tc.key, tc.value, tc.sep); got != tc.want {
				t.Errorf("prependLine(%s) changed a shipped output format:\n got: %s\nwant: %s",
					tc.sh, got, tc.want)
			}
		})
	}
}

func TestPrependLine(t *testing.T) {
	const key = "FGLIMAGEPATH"
	for _, tc := range []struct {
		name  string
		sh    Shell
		value string
		sep   string
		want  string
	}{
		// ── sh ────────────────────────────────────────────────────────────────
		{
			// Git Bash on Windows: a GOOS=windows binary emitting POSIX syntax
			// with a ";" separator. Proves the separator is threaded rather than
			// hardcoded, and that "\" and ";" are not inert for sh.
			name: "sh windows path forces quoting and semicolon separator",
			sh:   ShellSh, value: `C:\a\b;C:\c\d`, sep: ";",
			want: `export FGLIMAGEPATH='C:\a\b;C:\c\d'"${FGLIMAGEPATH:+;$FGLIMAGEPATH}"`,
		},
		{
			name: "sh space",
			sh:   ShellSh, value: "/Users/jane doe/p/.fglpkg", sep: ":",
			want: `export FGLIMAGEPATH='/Users/jane doe/p/.fglpkg'"${FGLIMAGEPATH:+:$FGLIMAGEPATH}"`,
		},
		{
			name: "sh trailing space",
			sh:   ShellSh, value: "/opt/a ", sep: ":",
			want: `export FGLIMAGEPATH='/opt/a '"${FGLIMAGEPATH:+:$FGLIMAGEPATH}"`,
		},
		{
			// The '\'' splice: close the quoted run, escape one quote, reopen.
			name: "sh single quote",
			sh:   ShellSh, value: "/opt/it's", sep: ":",
			want: `export FGLIMAGEPATH='/opt/it'\''s'"${FGLIMAGEPATH:+:$FGLIMAGEPATH}"`,
		},
		{
			name: "sh double quote",
			sh:   ShellSh, value: `/opt/a"b`, sep: ":",
			want: `export FGLIMAGEPATH='/opt/a"b'"${FGLIMAGEPATH:+:$FGLIMAGEPATH}"`,
		},
		{
			name: "sh dollar is not expanded",
			sh:   ShellSh, value: "/opt/$HOME", sep: ":",
			want: `export FGLIMAGEPATH='/opt/$HOME'"${FGLIMAGEPATH:+:$FGLIMAGEPATH}"`,
		},
		{
			name: "sh backtick",
			sh:   ShellSh, value: "/opt/a`b", sep: ":",
			want: "export FGLIMAGEPATH='/opt/a`b'\"${FGLIMAGEPATH:+:$FGLIMAGEPATH}\"",
		},
		{
			name: "sh glob and bang",
			sh:   ShellSh, value: "/opt/a*b?c!d", sep: ":",
			want: `export FGLIMAGEPATH='/opt/a*b?c!d'"${FGLIMAGEPATH:+:$FGLIMAGEPATH}"`,
		},
		{
			// Percent is inert to a POSIX shell, but it is not on the allowlist,
			// so it quotes. Over-cautious is the intended failure direction.
			name: "sh percent quotes conservatively",
			sh:   ShellSh, value: "/opt/a%b", sep: ":",
			want: `export FGLIMAGEPATH='/opt/a%b'"${FGLIMAGEPATH:+:$FGLIMAGEPATH}"`,
		},
		{
			// Printable non-ASCII is inert: an i18n path keeps today's bytes.
			name: "sh unicode stays unquoted",
			sh:   ShellSh, value: "/opt/café/日本", sep: ":",
			want: `export FGLIMAGEPATH=/opt/café/日本"${FGLIMAGEPATH:+:$FGLIMAGEPATH}"`,
		},
		{
			// U+00A0 NO-BREAK SPACE: invisible in a diff, and a space to some
			// locales. Quoted despite being non-ASCII.
			name: "sh non-breaking space is quoted",
			sh:   ShellSh, value: "/opt/a\u00a0b", sep: ":",
			want: "export FGLIMAGEPATH='/opt/a\u00a0b'\"${FGLIMAGEPATH:+:$FGLIMAGEPATH}\"",
		},

		// ── powershell ────────────────────────────────────────────────────────
		{
			name: "powershell plain value is still quoted",
			sh:   ShellPowerShell, value: `C:\a\b`, sep: ";",
			want: `$env:FGLIMAGEPATH = 'C:\a\b' + $(if ($env:FGLIMAGEPATH) { ';' + $env:FGLIMAGEPATH })`,
		},
		{
			name: "powershell space",
			sh:   ShellPowerShell, value: `C:\Users\Jane Doe\p`, sep: ";",
			want: `$env:FGLIMAGEPATH = 'C:\Users\Jane Doe\p' + $(if ($env:FGLIMAGEPATH) { ';' + $env:FGLIMAGEPATH })`,
		},
		{
			// '' is PowerShell's escape for a single quote inside a literal string.
			name: "powershell single quote is doubled",
			sh:   ShellPowerShell, value: `C:\it's`, sep: ";",
			want: `$env:FGLIMAGEPATH = 'C:\it''s' + $(if ($env:FGLIMAGEPATH) { ';' + $env:FGLIMAGEPATH })`,
		},
		{
			// Inside '…' none of $, backtick, %, " or \ are special.
			name: "powershell metacharacters stay literal",
			sh:   ShellPowerShell, value: "C:\\a$x`t%PATH%\"b", sep: ";",
			want: "$env:FGLIMAGEPATH = 'C:\\a$x`t%PATH%\"b' + $(if ($env:FGLIMAGEPATH) { ';' + $env:FGLIMAGEPATH })",
		},
		{
			// PowerShell deletes an environment variable assigned "", so this
			// leaves an unset variable unset — the same leading-separator quirk
			// sh has always had. Uniform across shells on purpose.
			name: "powershell empty value",
			sh:   ShellPowerShell, value: "", sep: ";",
			want: `$env:FGLIMAGEPATH = '' + $(if ($env:FGLIMAGEPATH) { ';' + $env:FGLIMAGEPATH })`,
		},
		{
			name: "powershell posix separator",
			sh:   ShellPowerShell, value: "/opt/a", sep: ":",
			want: `$env:FGLIMAGEPATH = '/opt/a' + $(if ($env:FGLIMAGEPATH) { ':' + $env:FGLIMAGEPATH })`,
		},

		// ── cmd ───────────────────────────────────────────────────────────────
		{
			name: "cmd space",
			sh:   ShellCmd, value: `C:\Users\Jane Doe\p`, sep: ";",
			want: `SET "FGLIMAGEPATH=C:\Users\Jane Doe\p;%FGLIMAGEPATH%"`,
		},
		{
			// Unquoted, `set V=C:\d&e` runs "e" as a command and truncates.
			name: "cmd metacharacters",
			sh:   ShellCmd, value: `C:\a&b(c)^d<e>f|g`, sep: ";",
			want: `SET "FGLIMAGEPATH=C:\a&b(c)^d<e>f|g;%FGLIMAGEPATH%"`,
		},
		{
			name: "cmd trailing space",
			sh:   ShellCmd, value: `C:\a `, sep: ";",
			want: `SET "FGLIMAGEPATH=C:\a ;%FGLIMAGEPATH%"`,
		},
		{
			// Quoting cannot stop delayed expansion in a .bat that enabled it;
			// documented rather than warned, since the normal case works.
			name: "cmd bang",
			sh:   ShellCmd, value: `C:\a!b`, sep: ";",
			want: `SET "FGLIMAGEPATH=C:\a!b;%FGLIMAGEPATH%"`,
		},
		{
			// Emitted anyway, with a warning from shellLimitWarnings: silently
			// dropping the variable would be worse.
			name: "cmd percent is emitted with a warning",
			sh:   ShellCmd, value: `C:\a%X%b`, sep: ";",
			want: `SET "FGLIMAGEPATH=C:\a%X%b;%FGLIMAGEPATH%"`,
		},
		{
			name: "cmd unicode stays unquoted",
			sh:   ShellCmd, value: `C:\café\日本`, sep: ";",
			want: `SET FGLIMAGEPATH=C:\café\日本;%FGLIMAGEPATH%`,
		},
		{
			name: "cmd tilde short name stays unquoted",
			sh:   ShellCmd, value: `C:\PROGRA~1\fglpkg`, sep: ";",
			want: `SET FGLIMAGEPATH=C:\PROGRA~1\fglpkg;%FGLIMAGEPATH%`,
		},
	} {
		t.Run(string(tc.sh)+"/"+tc.name, func(t *testing.T) {
			if got := prependLine(tc.sh, key, tc.value, tc.sep); got != tc.want {
				t.Errorf("prependLine:\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}

// TestPrependLineEmbedsValueExactlyOnce guards the property the test helpers and
// users' greps rely on, and rules out the if/else PowerShell form that would
// double an already length-warned value.
func TestPrependLineEmbedsValueExactlyOnce(t *testing.T) {
	const sentinel = "FGLPKGSENTINEL"
	for _, sh := range []Shell{ShellSh, ShellPowerShell, ShellCmd} {
		line := prependLine(sh, "FGLLDPATH", sentinel, ";")
		if n := strings.Count(line, sentinel); n != 1 {
			t.Errorf("%s: value appears %d times, want exactly 1: %s", sh, n, line)
		}
		if strings.ContainsAny(line, "\r\n") {
			t.Errorf("%s: emitted more than one line: %q", sh, line)
		}
	}
}

func TestNeedsQuote(t *testing.T) {
	for _, tc := range []struct {
		sh    Shell
		value string
		want  bool
	}{
		{ShellSh, "/opt/pkg/merged", false},
		{ShellSh, "/a:/b", false},
		{ShellSh, "", false},
		{ShellSh, "/opt/café", false},
		{ShellSh, `C:\a`, true},  // backslash is sh's escape
		{ShellSh, "/a;/b", true}, // semicolon is sh's command separator
		{ShellSh, "/opt/a b", true},
		{ShellSh, "/opt/a\tb", true},
		{ShellSh, "/opt/a\nb", true},

		{ShellCmd, `C:\a\b`, false},
		{ShellCmd, `C:\a;C:\b`, false},
		{ShellCmd, `C:\PROGRA~1`, false},
		{ShellCmd, "", false},
		{ShellCmd, `C:\a b`, true},
		{ShellCmd, `C:\a&b`, true},
		{ShellCmd, `C:\a%b`, true},

		// Always quoted: no shipped unquoted form to preserve.
		{ShellPowerShell, "/opt/pkg", true},
		{ShellPowerShell, "", true},
	} {
		if got := needsQuote(tc.sh, tc.value); got != tc.want {
			t.Errorf("needsQuote(%s, %q) = %v, want %v", tc.sh, tc.value, got, tc.want)
		}
	}
}

func TestParseShell(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Shell
		ok   bool
	}{
		{"sh", ShellSh, true},
		{"bash", ShellSh, true},
		{"zsh", ShellSh, true},
		{"powershell", ShellPowerShell, true},
		{"pwsh", ShellPowerShell, true},
		{"cmd", ShellCmd, true},
		{"CMD", ShellCmd, true},
		{"PowerShell", ShellPowerShell, true},
		{"  sh  ", ShellSh, true},
		{"tcsh", "", false},
		{"fish", "", false}, // completion supports fish; the emitter does not
		{"", "", false},
	} {
		got, ok := ParseShell(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("ParseShell(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestDefaultShellIsGOOSDerived(t *testing.T) {
	want := ShellSh
	if runtime.GOOS == "windows" {
		want = ShellCmd
	}
	if got := DefaultShell(); got != want {
		t.Errorf("DefaultShell() = %q, want %q on GOOS=%s", got, want, runtime.GOOS)
	}
}

// TestShellValuesCoversEveryAlias stops the CLI's "(want: ...)" error message
// drifting from the set shellNames actually accepts.
func TestShellValuesCoversEveryAlias(t *testing.T) {
	for name := range shellNames {
		if !strings.Contains(ShellValues, name) {
			t.Errorf("ShellValues does not mention accepted value %q: %s", name, ShellValues)
		}
	}
}

func TestCommentPrefix(t *testing.T) {
	for sh, want := range map[Shell]string{
		ShellSh:         "# ",
		ShellPowerShell: "# ",
		ShellCmd:        "REM ",
	} {
		if got := commentPrefix(sh); got != want {
			t.Errorf("commentPrefix(%s) = %q, want %q", sh, got, want)
		}
	}
}

func TestShellLimitWarnings(t *testing.T) {
	for _, tc := range []struct {
		name  string
		sh    Shell
		value string
		want  []string // substrings each expected warning must contain
	}{
		{"sh clean", ShellSh, "/opt/a b", nil},
		{"sh quote is representable", ShellSh, "/opt/it's", nil},
		{"powershell quote is representable", ShellPowerShell, `C:\it's`, nil},
		{"cmd clean", ShellCmd, `C:\a b`, nil},
		{"newline warns for every shell", ShellSh, "/opt/a\nb", []string{"line break"}},
		{"carriage return warns", ShellCmd, "C:\\a\rb", []string{"line break"}},
		{"cmd percent", ShellCmd, `C:\a%X%b`, []string{"'%'"}},
		{"cmd double quote", ShellCmd, `C:\a"b`, []string{`'"'`}},
		{"cmd both", ShellCmd, `C:\a%X%"b`, []string{"'%'", `'"'`}},
		{"powershell percent is fine", ShellPowerShell, `C:\a%X%b`, nil},
		{"powershell double quote is fine", ShellPowerShell, `C:\a"b`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := shellLimitWarnings(tc.sh, "FGLIMAGEPATH", tc.value)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d warnings %q, want %d", len(got), got, len(tc.want))
			}
			for i, sub := range tc.want {
				if !strings.Contains(got[i], sub) {
					t.Errorf("warning %d does not mention %s: %s", i, sub, got[i])
				}
				if !strings.HasPrefix(got[i], "FGLIMAGEPATH: ") {
					t.Errorf("warning %d should name the variable: %s", i, got[i])
				}
			}
		})
	}
}

// TestShellChangesWrapperNotValues renders one fixture through every shell and
// asserts the extracted values are identical. It is the guard on this change's
// central constraint: quoting happens at RENDER time only, so it must never
// reach envPlan accumulation, RawEnv, or anything feeding cmd.Env in
// `fglpkg bdl`. If quoting ever leaks into a path as it is collected, the
// extracted values diverge and this fails.
func TestShellChangesWrapperNotValues(t *testing.T) {
	chdirTemp(t)
	envTestWrite(t, ".fglpkg/packages/poiapi/fglpkg.json",
		`{ "name": "poiapi", "version": "1.0.0", "profile": ["profiles/a.4gp"], "dependencies": { "fgl": {} } }`)
	envTestWrite(t, ".fglpkg/packages/poiapi/com/fourjs/poiapi/Customer.42f", "FORM")
	envTestWrite(t, ".fglpkg/packages/poiapi/schema/stores.sch", "SCH")
	envTestWrite(t, ".fglpkg/packages/poiapi/img/logo.png", "PNG")
	envTestWrite(t, ".fglpkg/packages/poiapi/profiles/a.4gp", "A")

	keys := []string{"FGLRESOURCEPATH", "FGLDBPATH", "FGLIMAGEPATH", "FGLPROFILE"}
	want := map[string]string{}
	for _, sh := range []Shell{ShellSh, ShellPowerShell, ShellCmd} {
		lines := mustGenerateLocal(t, New(t.TempDir()).WithShell(sh))
		for _, key := range keys {
			line, ok := envLine(t, sh, lines, key)
			if !ok {
				t.Fatalf("%s: no %s statement in %v", sh, key, lines)
			}
			if first, seen := want[key]; !seen {
				want[key] = line.value
			} else if line.value != first {
				t.Errorf("%s: %s value differs across shells:\n got: %q\nwant: %q",
					sh, key, line.value, first)
			}
			// A rendered value must never contain the quote characters the
			// wrapper uses; those belong to the wrapper, which envLine strips.
			if strings.Contains(line.value, `"`) {
				t.Errorf("%s: %s value carries a quote character: %q", sh, key, line.value)
			}
		}
	}
}

// TestRawEnvIsNeverQuoted pins the hard constraint at its most dangerous point:
// RawEnv feeds cmd.Env in `fglpkg bdl` via exec.Command, where no shell is
// involved, so a quote character would land literally inside the child process's
// variable. Selecting a shell must not change these values at all.
func TestRawEnvIsNeverQuoted(t *testing.T) {
	chdirTemp(t)
	envTestWrite(t, ".fglpkg/packages/poiapi/fglpkg.json",
		`{ "name": "poiapi", "version": "1.0.0", "dependencies": { "fgl": {} } }`)
	envTestWrite(t, ".fglpkg/packages/poiapi/com/fourjs/poiapi/Customer.42f", "FORM")

	var want map[string]string
	for _, sh := range []Shell{ShellSh, ShellPowerShell, ShellCmd} {
		got, err := New(t.TempDir()).WithShell(sh).RawEnv()
		if err != nil {
			t.Fatalf("%s: RawEnv: %v", sh, err)
		}
		for k, v := range got {
			if strings.ContainsAny(v, `"'`) {
				t.Errorf("%s: RawEnv[%s] contains a quote character: %q", sh, k, v)
			}
		}
		if want == nil {
			want = got
			continue
		}
		if len(got) != len(want) {
			t.Fatalf("%s: RawEnv has %d keys, want %d", sh, len(got), len(want))
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("--shell %s changed RawEnv[%s]:\n got: %q\nwant: %q", sh, k, got[k], v)
			}
		}
	}
}
