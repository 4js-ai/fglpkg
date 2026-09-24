package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRawManifest drops a verbatim fglpkg.json into dir.
func writeRawManifest(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "fglpkg.json"), []byte(body), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

// writeManifest drops a minimal fglpkg.json declaring the given fgl dependency.
func writeManifest(t *testing.T, dir, dep string) {
	t.Helper()
	writeRawManifest(t, dir, `{"name":"testproj","version":"1.0.0","dependencies":{"fgl":{"`+dep+`":"^1.0.0"}}}`+"\n")
}

// TestCmdRemove_CommaFormRejected covers the comma half of GIS-564: a
// comma-joined argument is one token that matches nothing, so it is rejected up
// front (before any manifest/installer work) with a space-separated suggestion
// that keeps every argument and the scope flag.
func TestCmdRemove_CommaFormRejected(t *testing.T) {
	t.Run("single arg", func(t *testing.T) {
		out, err := captureStdout(t, func() error { return cmdRemove([]string{"foo,bar"}) })
		if err == nil {
			t.Fatal("expected an error for the comma form, got nil")
		}
		if !strings.Contains(err.Error(), "spaces") || !strings.Contains(err.Error(), "foo bar") {
			t.Fatalf("error should suggest the space-separated form, got: %v", err)
		}
		if strings.Contains(out, "✓ Removed") {
			t.Fatalf("comma form must not report a removal, got output:\n%s", out)
		}
	})

	// The suggestion must not drop the scope flag or the other arguments.
	t.Run("keeps flag and all args", func(t *testing.T) {
		_, err := captureStdout(t, func() error { return cmdRemove([]string{"--global", "foo,bar", "baz"}) })
		if err == nil {
			t.Fatal("expected an error for the comma form, got nil")
		}
		if !strings.Contains(err.Error(), "fglpkg remove --global foo bar baz") {
			t.Fatalf("suggestion should keep --global and every name, got: %v", err)
		}
	})
}

// TestCmdRemove_UnknownNameNoFalseSuccess covers the core of GIS-564: removing a
// package that is not declared warns and fails instead of printing the old
// "✓ Removed <name> (not declared in manifest)" success line — and it leaves the
// manifest byte-for-byte unchanged (it returns before Save).
func TestCmdRemove_UnknownNameNoFalseSuccess(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "realpkg")
	chdirTest(t, dir)

	before, err := os.ReadFile(filepath.Join(dir, "fglpkg.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	out, err := captureStdout(t, func() error { return cmdRemove([]string{"ghost"}) })
	if err == nil {
		t.Fatal("expected a non-nil error when nothing was removed")
	}
	if strings.Contains(out, "✓ Removed") {
		t.Fatalf("an undeclared name must not report success, got output:\n%s", out)
	}
	if !strings.Contains(out, "warning") || !strings.Contains(out, "ghost") {
		t.Fatalf("expected a warning naming the missing package, got output:\n%s", out)
	}
	// The manifest must not have been rewritten at all — Save was never reached.
	after, err := os.ReadFile(filepath.Join(dir, "fglpkg.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("a no-op remove must not rewrite the manifest\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestCmdRemove_NoMatchSkipsHook confirms the other behaviour this PR changes:
// the preuninstall hook must not run when nothing matched. The hook here would
// create a marker directory if it ran.
func TestCmdRemove_NoMatchSkipsHook(t *testing.T) {
	dir := t.TempDir()
	writeRawManifest(t, dir, `{"name":"testproj","version":"1.0.0",`+
		`"dependencies":{"fgl":{"realpkg":"^1.0.0"}},`+
		`"hooks":{"preuninstall":[{"op":"mkdir","path":"hook-ran"}]}}`+"\n")
	chdirTest(t, dir)

	if _, err := captureStdout(t, func() error { return cmdRemove([]string{"ghost"}) }); err == nil {
		t.Fatal("expected an error when nothing was removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "hook-ran")); !os.IsNotExist(err) {
		t.Fatalf("preuninstall hook must not run when nothing matched (marker exists: %v)", err)
	}
}

// TestCmdRemove_HookFailureNoFalseSuccess covers the deferred-✓ change: the
// "✓ Removed" line must not print before the removal is committed. A failing
// preuninstall hook aborts the command, and the user must not have already been
// told the package was removed (and the manifest must be untouched).
func TestCmdRemove_HookFailureNoFalseSuccess(t *testing.T) {
	dir := t.TempDir()
	writeRawManifest(t, dir, `{"name":"testproj","version":"1.0.0",`+
		`"dependencies":{"fgl":{"realpkg":"^1.0.0"}},`+
		`"hooks":{"preuninstall":[{"op":"copy-files","from":"does-not-exist.txt","to":"dest"}]}}`+"\n")
	chdirTest(t, dir)

	before, err := os.ReadFile(filepath.Join(dir, "fglpkg.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	out, err := captureStdout(t, func() error { return cmdRemove([]string{"realpkg"}) })
	if err == nil {
		t.Fatal("expected the failing preuninstall hook to abort the command")
	}
	if strings.Contains(out, "✓ Removed") {
		t.Fatalf("no ✓ may print when a failing hook aborts the removal, got:\n%s", out)
	}
	after, err := os.ReadFile(filepath.Join(dir, "fglpkg.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("an aborted remove must not rewrite the manifest\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestCmdRemove_OutsideProjectWithoutGlobalExplainsScope: outside a project
// there is no manifest to remove from, and the bare "failed to load fglpkg.json"
// told the user nothing about the only thing `remove` can mean there (GIS-567).
// Deleting from a store shared by every project must not be inferred from an
// empty directory either, so the message names the flag instead.
// The store is deliberately NOT empty: with an empty one the mutated code path
// (no --global requirement) still errors with "no packages matched … --global",
// which satisfies both message assertions — so the safety property has to be
// checked on the store itself, not on the message (PR #88 review).
func TestCmdRemove_OutsideProjectWithoutGlobalExplainsScope(t *testing.T) {
	store := t.TempDir()
	t.Setenv("FGLPKG_GLOBAL_DIR", store)
	pkgDir := writeStorePackage(t, store, "fglunit")

	chdirTest(t, t.TempDir()) // no fglpkg.json, no .fglpkg/

	err := cmdRemove([]string{"fglunit"})
	if err == nil {
		t.Fatal("expected an error outside a project")
	}
	if strings.Contains(err.Error(), "failed to load") {
		t.Fatalf("the manifest load error must not surface here, got: %v", err)
	}
	if !strings.Contains(err.Error(), "--global") {
		t.Fatalf("the error should point at the global scope, got: %v", err)
	}
	// The property this test exists for: a bare remove must leave the shared
	// store alone, even though the package it named IS installed there.
	if _, statErr := os.Stat(pkgDir); statErr != nil {
		t.Fatal("a bare remove outside a project must not delete from the global store")
	}
}

// writeStorePackage puts one installed package in a global store.
func writeStorePackage(t *testing.T, store, name string) string {
	t.Helper()
	pkgDir := filepath.Join(store, "packages", name)
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{"name":"` + name + `","version":"1.0.0"}` + "\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "fglpkg.json"), []byte(body), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return pkgDir
}

// TestCmdRemove_GlobalOutsideProjectDoesNotTouchCwd: the global path must not
// read or write a project manifest — the whole point of GIS-567.
func TestCmdRemove_GlobalOutsideProjectDoesNotTouchCwd(t *testing.T) {
	store := t.TempDir()
	t.Setenv("FGLPKG_GLOBAL_DIR", store)
	pkgDir := writeStorePackage(t, store, "fglunit")

	wd := t.TempDir()
	chdirTest(t, wd)

	if err := cmdRemove([]string{"fglunit", "--global"}); err != nil {
		t.Fatalf("cmdRemove --global: %v", err)
	}
	if _, err := os.Stat(pkgDir); err == nil {
		t.Fatal("the package should be gone from the global store")
	}
	// Nothing was created in the current directory.
	entries, err := os.ReadDir(wd)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("a global remove must leave the cwd untouched, found %d entries", len(entries))
	}
}
