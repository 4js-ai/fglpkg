package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// GIS-593: `fglpkg update` takes no package argument, but parseInstallFlags —
// which it borrows from `install` — collects positionals, and cmdUpdate never
// read them. A name, a nonexistent name and an unknown flag were all accepted
// and exited 0 while every dependency on a range constraint moved.

// TestUpdateRejectsAPackageArgument covers the reported case and the two
// variants that shared its cause. The assertions are on cmdUpdate rather than
// the helper, because the defect was that nothing consulted flags.pkgs at all:
// a helper-only test would pass against the unfixed code.
func TestUpdateRejectsAPackageArgument(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"package name", []string{"fglunit"}, `unknown argument "fglunit"`},
		{"name that does not exist", []string{"no-such-pkg-xyz"}, `unknown argument "no-such-pkg-xyz"`},
		{"unknown flag", []string{"--bogus"}, `unknown argument "--bogus"`},
		{"name among valid flags", []string{"--local", "fglunit"}, `unknown argument "fglunit"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := cmdUpdate(tc.args)
			if err == nil {
				t.Fatalf("cmdUpdate(%q) = nil; want an error", tc.args)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("cmdUpdate(%q) error = %q; want it to contain %q", tc.args, err, tc.want)
			}
			var exitErr *ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("cmdUpdate(%q) error is not an *ExitError, so it exits 1; want the usage code", tc.args)
			}
			if exitErr.Code != 2 {
				t.Errorf("cmdUpdate(%q) exit code = %d; want 2, as `list` uses for a usage error", tc.args, exitErr.Code)
			}
		})
	}
}

// TestUpdateRejectsBeforeDoingAnything pins the half of the acceptance criteria
// that the error message alone does not: the refusal must come before any
// resolution. cmdUpdate loads the manifest after the arguments are checked, so
// running in a directory with no fglpkg.json distinguishes the two — a
// manifest error here would mean the check ran too late.
func TestUpdateRejectsBeforeDoingAnything(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	err := cmdUpdate([]string{"fglunit"})
	if err == nil {
		t.Fatal("cmdUpdate(fglunit) = nil; want an error")
	}
	if strings.Contains(err.Error(), manifest.Filename) {
		t.Errorf("cmdUpdate reported %q, so it got as far as loading the manifest; the argument check must come first", err)
	}
	if !strings.Contains(err.Error(), `unknown argument "fglunit"`) {
		t.Errorf("cmdUpdate error = %q; want the argument refusal", err)
	}
}

// TestUpdateStillAcceptsItsOwnFlags is the control: the guard must not make
// `update` refuse the arguments it documents. Without this, deleting the
// positional collection entirely would still pass the tests above.
func TestUpdateStillAcceptsItsOwnFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--production"},
		{"--no-prune"},
		{"--registry", "gi"},
		{"--local", "--prod"},
	} {
		f, err := parseInstallFlags(args)
		if err != nil {
			t.Fatalf("parseInstallFlags(%q): %v", args, err)
		}
		if len(f.pkgs) != 0 {
			t.Errorf("parseInstallFlags(%q) collected %q as positionals; cmdUpdate would now refuse its own flags", args, f.pkgs)
		}
	}
}

// TestUpdateArgErrorRedirectsOnlyPackageNames: a typo'd flag and a package name
// are different mistakes, and only the second one has an `install` to point at.
func TestUpdateArgErrorRedirectsOnlyPackageNames(t *testing.T) {
	cases := []struct {
		arg         string
		wantInstall string // "" = no redirect expected
	}{
		{"fglunit", "fglpkg install fglunit@<version>"},
		{"fglunit@1.0.1", "fglpkg install fglunit@1.0.1"},
		{"--bogus", ""},
		{"-x", ""},
	}
	for _, tc := range cases {
		got := updateArgError(tc.arg).Error()
		if tc.wantInstall == "" {
			if strings.Contains(got, "fglpkg install") {
				t.Errorf("updateArgError(%q) = %q; a flag typo should not suggest an install", tc.arg, got)
			}
			continue
		}
		if !strings.Contains(got, tc.wantInstall) {
			t.Errorf("updateArgError(%q) = %q; want it to suggest %q", tc.arg, got, tc.wantInstall)
		}
		if strings.Contains(got, "@<version>") && strings.Contains(tc.arg, "@") {
			t.Errorf("updateArgError(%q) = %q; a pinned name must not grow a second @", tc.arg, got)
		}
	}
}
