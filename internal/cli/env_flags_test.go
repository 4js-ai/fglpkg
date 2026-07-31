package cli

import (
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/env"
)

func TestParseEnvFlagsShell(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want env.Shell
	}{
		// Both spellings, because parseFlags could only ever have supported the
		// first and that limitation is why env has its own parser.
		{"space separated", []string{"--shell", "powershell"}, env.ShellPowerShell},
		{"equals separated", []string{"--shell=powershell"}, env.ShellPowerShell},
		{"alias pwsh", []string{"--shell=pwsh"}, env.ShellPowerShell},
		{"alias bash", []string{"--shell=bash"}, env.ShellSh},
		{"alias zsh", []string{"--shell", "zsh"}, env.ShellSh},
		{"cmd", []string{"--shell=cmd"}, env.ShellCmd},
		{"case insensitive", []string{"--shell=CMD"}, env.ShellCmd},
		{"mixed case", []string{"--shell", "PowerShell"}, env.ShellPowerShell},
		{"with scope flag", []string{"--global", "--shell", "sh"}, env.ShellSh},
		{"scope flag after", []string{"--shell=sh", "--local"}, env.ShellSh},
		{"last one wins", []string{"--shell=cmd", "--shell=sh"}, env.ShellSh},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := parseEnvFlags(tc.args)
			if err != nil {
				t.Fatalf("parseEnvFlags(%v): %v", tc.args, err)
			}
			if f.shell != tc.want {
				t.Errorf("shell = %q, want %q", f.shell, tc.want)
			}
			if !f.shellGiven {
				t.Error("shellGiven should be true when --shell is passed")
			}
		})
	}
}

// TestParseEnvFlagsDefaultShell: absent --shell, the shell is GOOS-derived and
// shellGiven is false. The second half is load-bearing — the --gst conflict
// check gates on it, and testing f.shell instead would make --gst fail always.
func TestParseEnvFlagsDefaultShell(t *testing.T) {
	for _, args := range [][]string{nil, {}, {"--global"}, {"--gst"}, {"--gwa"}} {
		f, err := parseEnvFlags(args)
		if err != nil {
			t.Fatalf("parseEnvFlags(%v): %v", args, err)
		}
		if f.shell != env.DefaultShell() {
			t.Errorf("parseEnvFlags(%v) shell = %q, want the default %q", args, f.shell, env.DefaultShell())
		}
		if f.shellGiven {
			t.Errorf("parseEnvFlags(%v) marked the default shell as explicitly given", args)
		}
	}
}

func TestParseEnvFlagsScopeAndMode(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		args                    []string
		local, global, gst, gwa bool
	}{
		{"long local", []string{"--local"}, true, false, false, false},
		{"short local", []string{"-l"}, true, false, false, false},
		{"long global", []string{"--global"}, false, true, false, false},
		{"short global", []string{"-g"}, false, true, false, false},
		{"gst", []string{"--gst"}, false, false, true, false},
		{"gwa", []string{"--gwa"}, false, false, false, true},
		{"gst with local", []string{"--gst", "--local"}, true, false, true, false},
		{"no flags", nil, false, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := parseEnvFlags(tc.args)
			if err != nil {
				t.Fatalf("parseEnvFlags(%v): %v", tc.args, err)
			}
			if f.local != tc.local || f.global != tc.global || f.gst != tc.gst || f.gwa != tc.gwa {
				t.Errorf("parseEnvFlags(%v) = {local:%v global:%v gst:%v gwa:%v}, want {%v %v %v %v}",
					tc.args, f.local, f.global, f.gst, f.gwa, tc.local, tc.global, tc.gst, tc.gwa)
			}
		})
	}
}

func TestParseEnvFlagsErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string // substring the error must contain
	}{
		{"unknown shell", []string{"--shell=tcsh"}, `invalid --shell "tcsh"`},
		// fish has a completion script but no emitter, so it must be rejected
		// rather than silently treated as POSIX.
		{"fish is not an emitter", []string{"--shell=fish"}, `invalid --shell "fish"`},
		{"empty shell value", []string{"--shell="}, `invalid --shell ""`},
		{"missing shell value", []string{"--shell"}, "--shell requires a value"},
		{"missing value at end", []string{"--global", "--shell"}, "--shell requires a value"},
		{"unknown flag", []string{"--nope"}, `unknown argument "--nope"`},
		// Previously accepted and silently ignored by the shared parseFlags.
		{"positional argument", []string{"somejunk"}, `unknown argument "somejunk"`},
		{"force is not an env flag", []string{"--force"}, `unknown argument "--force"`},
		{"local and global", []string{"--local", "--global"}, "mutually exclusive"},
		{"gst and gwa", []string{"--gst", "--gwa"}, "mutually exclusive"},
		{"shell with gst", []string{"--gst", "--shell=sh"}, "--shell does not apply to --gst"},
		{"shell with gwa", []string{"--gwa", "--shell=sh"}, "--shell does not apply to --gwa"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseEnvFlags(tc.args)
			if err == nil {
				t.Fatalf("parseEnvFlags(%v) should have failed", tc.args)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q should mention %q", err, tc.want)
			}
		})
	}
}

// TestParseEnvFlagsInvalidShellNamesAcceptedValues keeps the error message
// actionable: a user who typed the wrong shell should be able to read the right
// one out of the message rather than going to the docs.
func TestParseEnvFlagsInvalidShellNamesAcceptedValues(t *testing.T) {
	_, err := parseEnvFlags([]string{"--shell=tcsh"})
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"sh", "powershell", "cmd"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name accepted value %q: %s", want, err)
		}
	}
}
