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
		{"unknown flag", []string{"--bogus"}, `unknown flag "--bogus"`},
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

// TestUpdateArgErrorRedirectsToInstall: the redirect names the package, and a
// name that already carries a version does not grow a second @.
func TestUpdateArgErrorRedirectsToInstall(t *testing.T) {
	for _, tc := range []struct{ arg, want string }{
		{"fglunit", "fglpkg install fglunit@<version>"},
		{"fglunit@1.0.1", "fglpkg install fglunit@1.0.1"},
	} {
		got := updateArgError(tc.arg).Error()
		if !strings.Contains(got, tc.want) {
			t.Errorf("updateArgError(%q) = %q; want it to suggest %q", tc.arg, got, tc.want)
		}
		if strings.Contains(tc.arg, "@") && strings.Contains(got, "@<version>") {
			t.Errorf("updateArgError(%q) = %q; a pinned name must not grow a second @", tc.arg, got)
		}
	}
}

// GIS-583, the part GIS-593 did not cover: parseInstallFlags turned any
// unrecognised token into a package name, so `fglpkg install --genero 6.00`
// became a registry lookup for "--genero" — and the error even suggested
// `fglpkg login`. A slug always starts alphanumeric, so a leading "-" is a
// flag. This is asserted on the parser because it fixes `install` too, not
// only `update`.
func TestParseInstallFlagsRejectsUnknownFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--genero", "6.00"},
		{"--bogus-flag"},
		{"-x"},
		{"fglunit", "--genero"},
	} {
		f, err := parseInstallFlags(args)
		if err == nil {
			t.Errorf("parseInstallFlags(%q) = nil error, pkgs=%q; want an unknown-flag error", args, f.pkgs)
			continue
		}
		if !strings.Contains(err.Error(), "unknown flag") {
			t.Errorf("parseInstallFlags(%q) error = %q; want it to name it an unknown flag", args, err)
		}
	}
}

// TestParseInstallFlagsStillAcceptsPackageNames is the control for the check
// above: a version pin and a scoped name must not be mistaken for flags.
func TestParseInstallFlagsStillAcceptsPackageNames(t *testing.T) {
	args := []string{"fglunit@1.0.1", "megamenu", "--save-dev"}
	f, err := parseInstallFlags(args)
	if err != nil {
		t.Fatalf("parseInstallFlags(%q): %v", args, err)
	}
	if len(f.pkgs) != 2 || f.pkgs[0] != "fglunit@1.0.1" || f.pkgs[1] != "megamenu" {
		t.Errorf("parseInstallFlags(%q) pkgs = %q; want both package names", args, f.pkgs)
	}
}

// GIS-583: update parsed install-only flags and dropped them, exit 0. The worst
// of them is --no-verify-signature: a user who passed it believed they had
// turned verification off for that run, and nothing said otherwise.
func TestUpdateRefusesInstallOnlyFlags(t *testing.T) {
	for _, flag := range []string{
		"--force", "-f",
		"--no-verify-signature",
		"--save-dev", "-D",
		"--save-optional", "-O",
		"--save-prod", "-P",
	} {
		t.Run(flag, func(t *testing.T) {
			err := cmdUpdate([]string{flag})
			if err == nil {
				t.Fatalf("cmdUpdate(%q) = nil; want a refusal", flag)
			}
			if !strings.Contains(err.Error(), flag) {
				t.Errorf("cmdUpdate(%q) error = %q; it must name the flag the user typed", flag, err)
			}
			if !strings.Contains(err.Error(), "not valid for 'update'") {
				t.Errorf("cmdUpdate(%q) error = %q; want the same wording --frozen uses", flag, err)
			}
			var exitErr *ExitError
			if !errors.As(err, &exitErr) || exitErr.Code != 2 {
				t.Errorf("cmdUpdate(%q) should exit 2 like the other usage errors; got %v", flag, err)
			}
		})
	}
}

// TestUpdateFrozenStillRefused guards the pre-existing --frozen policy, whose
// message is deliberately more specific than the generic install-only one.
func TestUpdateFrozenStillRefused(t *testing.T) {
	err := cmdUpdate([]string{"--frozen"})
	if err == nil || !strings.Contains(err.Error(), "--frozen is not valid for 'update'") {
		t.Fatalf("cmdUpdate(--frozen) = %v; want the bespoke --frozen refusal", err)
	}
}
