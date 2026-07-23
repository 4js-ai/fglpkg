package cli

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// withStdin swaps the package-level prompt reader for one backed by input, so
// prompt-driven code can be exercised without a real terminal.
func withStdin(t *testing.T, input string) {
	t.Helper()
	orig := reader
	reader = bufio.NewReader(strings.NewReader(input))
	t.Cleanup(func() { reader = orig })
}

// chdir changes into dir for the duration of the test.
func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
}

// gitInitWithOrigin makes dir a git repo with the given origin remote URL.
func gitInitWithOrigin(t *testing.T, dir, url string) {
	t.Helper()
	for _, args := range [][]string{
		{"init"},
		{"remote", "add", "origin", url},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestNormalizeGitRemoteURL(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"scp_github", "git@github.com:owner/repo.git", "https://github.com/owner/repo"},
		{"scp_no_suffix", "git@github.com:owner/repo", "https://github.com/owner/repo"},
		{"scp_nested_path", "git@gitlab.example.com:group/sub/repo.git", "https://gitlab.example.com/group/sub/repo"},
		{"https_with_git", "https://github.com/owner/repo.git", "https://github.com/owner/repo"},
		{"https_plain", "https://github.com/owner/repo", "https://github.com/owner/repo"},
		{"ssh_scheme", "ssh://git@github.com/owner/repo.git", "https://github.com/owner/repo"},
		{"git_scheme", "git://github.com/owner/repo.git", "https://github.com/owner/repo"},
		{"whitespace", "  https://github.com/owner/repo  ", "https://github.com/owner/repo"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeGitRemoteURL(tc.in); got != tc.want {
				t.Errorf("normalizeGitRemoteURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestPromptGeneroConstraint(t *testing.T) {
	t.Setenv("FGLPKG_GENERO_VERSION", "5.01.00") // makes genero.Detect deterministic

	cases := []struct {
		name, input, want string
	}{
		{"skip", "\n", ""},
		{"current_caret_on_major", "current\n", "^5.0.0"},
		{"current_case_insensitive", "Current\n", "^5.0.0"},
		{"custom_constraint", ">=5.0.0 <6.0.0\n", ">=5.0.0 <6.0.0"},
		{"invalid_then_valid", "nonsense\n^5.0.0\n", "^5.0.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withStdin(t, tc.input)
			var result string
			if _, err := captureStdout(t, func() error {
				var e error
				result, e = promptGeneroConstraint()
				return e
			}); err != nil {
				t.Fatalf("promptGeneroConstraint returned error: %v", err)
			}
			if result != tc.want {
				t.Errorf("promptGeneroConstraint() = %q, want %q", result, tc.want)
			}
		})
	}
}

func TestPromptGeneroConstraintUndetectableRejectsCurrent(t *testing.T) {
	// Force detection to fail: no override, no fglcomp, no FGLDIR.
	t.Setenv("FGLPKG_GENERO_VERSION", "")
	t.Setenv("FGLDIR", "")
	t.Setenv("PATH", "") // no fglcomp on PATH

	// "current" is not acceptable without a detected version; the user then skips.
	withStdin(t, "current\n\n")
	var result string
	if _, err := captureStdout(t, func() error {
		var e error
		result, e = promptGeneroConstraint()
		return e
	}); err != nil {
		t.Fatalf("promptGeneroConstraint returned error: %v", err)
	}
	if result != "" {
		t.Errorf("promptGeneroConstraint() = %q, want empty (skip after rejecting current)", result)
	}
}

func TestPromptRepository(t *testing.T) {
	t.Run("accepts detected default on empty", func(t *testing.T) {
		withStdin(t, "\n")
		var got string
		if _, err := captureStdout(t, func() error {
			var e error
			got, e = promptRepository("https://github.com/acme/widget")
			return e
		}); err != nil {
			t.Fatalf("promptRepository returned error: %v", err)
		}
		if got != "https://github.com/acme/widget" {
			t.Errorf("promptRepository = %q, want the detected default", got)
		}
	})

	t.Run("requires value when nothing detected", func(t *testing.T) {
		// First an empty line (rejected as required), then a real value.
		withStdin(t, "\nhttps://example.com/x/y\n")
		var got string
		if _, err := captureStdout(t, func() error {
			var e error
			got, e = promptRepository("")
			return e
		}); err != nil {
			t.Fatalf("promptRepository returned error: %v", err)
		}
		if got != "https://example.com/x/y" {
			t.Errorf("promptRepository = %q, want the typed value", got)
		}
	})
}

func TestInitNonInteractiveWithGitOrigin(t *testing.T) {
	dir := t.TempDir()
	gitInitWithOrigin(t, dir, "git@github.com:acme/widget.git")
	chdir(t, dir)

	if _, err := captureStdout(t, func() error { return cmdInit([]string{"--yes"}) }); err != nil {
		t.Fatalf("cmdInit: %v", err)
	}

	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Repository != "https://github.com/acme/widget" {
		t.Errorf("repository = %q, want it auto-detected from origin", m.Repository)
	}
	if m.Version != "0.1.0" {
		t.Errorf("version = %q, want default 0.1.0", m.Version)
	}
	if m.License != "UNLICENSED" {
		t.Errorf("license = %q, want default UNLICENSED", m.License)
	}
	if m.Name == "" {
		t.Error("name should default to the directory basename, got empty")
	}
	if m.GeneroConstraint != "" {
		t.Errorf("genero = %q, want omitted by default", m.GeneroConstraint)
	}
}

func TestInitNonInteractiveNoGit(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	if _, err := captureStdout(t, func() error { return cmdInit([]string{"--yes"}) }); err != nil {
		t.Fatalf("cmdInit: %v", err)
	}

	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Repository != "" {
		t.Errorf("repository = %q, want empty when there is no git remote", m.Repository)
	}
	if m.Version != "0.1.0" || m.License != "UNLICENSED" {
		t.Errorf("defaults wrong: version=%q license=%q", m.Version, m.License)
	}
}

// TestInitInteractiveProducesPublishableManifest is the acceptance-criteria
// test: answering the prompts (accepting defaults where offered) yields a
// manifest that passes ValidateForPublish with no hand-editing (GIS-287).
func TestInitInteractiveProducesPublishableManifest(t *testing.T) {
	t.Setenv("FGLPKG_GENERO_VERSION", "5.01.00")

	// Force the interactive path even though the test has no TTY.
	origTTY := stdinIsTerminal
	stdinIsTerminal = func() bool { return true }
	t.Cleanup(func() { stdinIsTerminal = origTTY })

	dir := t.TempDir()
	gitInitWithOrigin(t, dir, "git@github.com:acme/widget.git")
	chdir(t, dir)

	// name, version(default), description, author, license, repository(default), genero
	answers := strings.Join([]string{
		"mypkg",              // Package name
		"",                   // Version → default 0.1.0
		"A real description", // Description
		"Jane Dev",           // Author
		"MIT",                // License
		"",                   // Repository → accept detected default
		"current",            // Genero → ^5.0.0
	}, "\n") + "\n"
	withStdin(t, answers)

	if _, err := captureStdout(t, func() error { return cmdInit(nil) }); err != nil {
		t.Fatalf("cmdInit: %v", err)
	}

	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Name != "mypkg" {
		t.Errorf("name = %q, want mypkg", m.Name)
	}
	if m.Version != "0.1.0" {
		t.Errorf("version = %q, want 0.1.0", m.Version)
	}
	if m.Description != "A real description" {
		t.Errorf("description = %q", m.Description)
	}
	if m.Author != "Jane Dev" {
		t.Errorf("author = %q", m.Author)
	}
	if m.License != "MIT" {
		t.Errorf("license = %q, want MIT", m.License)
	}
	if m.Repository != "https://github.com/acme/widget" {
		t.Errorf("repository = %q, want detected default", m.Repository)
	}
	if m.GeneroConstraint != "^5.0.0" {
		t.Errorf("genero = %q, want ^5.0.0 (caret on detected major)", m.GeneroConstraint)
	}
	if err := m.ValidateForPublish(); err != nil {
		t.Errorf("manifest from init should be publishable, but ValidateForPublish failed: %v", err)
	}
}

// TestPromptWithDefaultEOF: with no input, a defaulted prompt returns its
// default paired with errPromptEOF — the signal that stops required-field loops
// (a blank Enter, by contrast, returns the default with no error).
func TestPromptWithDefaultEOF(t *testing.T) {
	withStdin(t, "") // immediate EOF
	var got string
	var gotErr error
	if _, err := captureStdout(t, func() error {
		got, gotErr = promptWithDefault("License", "UNLICENSED")
		return nil
	}); err != nil {
		t.Fatalf("captureStdout: %v", err)
	}
	if got != "UNLICENSED" {
		t.Errorf("value = %q, want the default", got)
	}
	if !errors.Is(gotErr, errPromptEOF) {
		t.Errorf("err = %v, want errPromptEOF on EOF", gotErr)
	}
}

// TestPromptNonEmptyStringEOFDoesNotLoop is the core regression test: a required
// prompt must NOT re-read forever when stdin is at EOF (Ctrl-D at a terminal, or
// /dev/null). It must return promptly with an error. The 5s timeout guards
// against a reintroduced infinite loop.
func TestPromptNonEmptyStringEOFDoesNotLoop(t *testing.T) {
	withStdin(t, "") // immediate EOF — the old code looped here forever

	done := make(chan error, 1)
	go func() {
		_, err := captureStdout(t, func() error {
			_, e := promptNonEmptyString("Description")
			return e
		})
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, errPromptEOF) {
			t.Errorf("err = %v, want errPromptEOF", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("promptNonEmptyString did not return on EOF — infinite loop regression")
	}
}

// TestInitInteractiveAbortsOnEOF: when init takes the interactive path but stdin
// is closed, it aborts with actionable guidance (pointing at --yes) instead of
// hanging, and writes no manifest.
func TestInitInteractiveAbortsOnEOF(t *testing.T) {
	origTTY := stdinIsTerminal
	stdinIsTerminal = func() bool { return true } // force interactive path
	t.Cleanup(func() { stdinIsTerminal = origTTY })

	dir := t.TempDir()
	chdir(t, dir)
	withStdin(t, "") // no input

	var runErr error
	if _, err := captureStdout(t, func() error {
		runErr = cmdInit(nil)
		return nil
	}); err != nil {
		t.Fatalf("captureStdout: %v", err)
	}
	if runErr == nil {
		t.Fatal("expected cmdInit to fail on closed stdin, got nil")
	}
	if !strings.Contains(runErr.Error(), "--yes") {
		t.Errorf("error %q should point the user at --yes", runErr.Error())
	}
	if _, err := os.Stat(manifest.Filename); !os.IsNotExist(err) {
		t.Errorf("no manifest should be written on abort, but %s exists", manifest.Filename)
	}
}

// TestIsTerminalDetectsNonTTY guards the /dev/null misdetection that caused the
// init hang: a pipe and the null device must both report false. (A real tty
// can't be asserted in CI, so only the negative cases are checked — which is
// exactly what the non-interactive guard depends on.)
func TestIsTerminalDetectsNonTTY(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	if isTerminal(r) {
		t.Error("isTerminal(pipe) = true, want false")
	}

	nul, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	t.Cleanup(func() { _ = nul.Close() })
	if isTerminal(nul) {
		t.Errorf("isTerminal(%s) = true, want false (char device but not a tty)", os.DevNull)
	}
}
