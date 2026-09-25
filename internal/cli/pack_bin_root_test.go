package cli

import (
	"encoding/json"
	"path"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// shippedManifest decodes the fglpkg.json a pack staged into the archive.
func shippedManifest(t *testing.T, entries map[string]string) *manifest.Manifest {
	t.Helper()
	raw, ok := entries["fglpkg.json"]
	if !ok {
		t.Fatal("archive has no fglpkg.json")
	}
	var m manifest.Manifest
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("decode shipped manifest: %v\n%s", err, raw)
	}
	return &m
}

// assertBinResolves is the invariant GIS-570 is about: a consumer resolves a
// bin script at <shipped root>/<shipped bin>, so that path must be exactly
// where pack put the file in the archive. Whatever `root` and `importRoot` the
// author used, the SHIPPED manifest has to be self-consistent.
func assertBinResolves(t *testing.T, entries map[string]string, cmd string) string {
	t.Helper()
	m := shippedManifest(t, entries)
	scriptPath, ok := m.Bin[cmd]
	if !ok {
		t.Fatalf("shipped manifest declares no bin %q (bin: %v)", cmd, m.Bin)
	}
	resolved := path.Join(m.RootOrDot(), scriptPath)
	if _, inArchive := entries[resolved]; !inArchive {
		t.Fatalf("bin %q resolves to %q (root %q + %q) which is not in the archive; entries: %v",
			cmd, resolved, m.RootOrDot(), scriptPath, sortedKeys(entries))
	}
	return resolved
}

func sortedKeys(m map[string]string) []string {
	return keys(boolKeys(m))
}

// packLayout builds a one-module, one-bin package with the given manifest and
// returns the archive entries.
func packLayout(t *testing.T, files map[string]string) map[string]string {
	t.Helper()
	stagePackTestDir(t, files)
	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	data, _, err := buildPackageZip(m)
	if err != nil {
		t.Fatalf("buildPackageZip: %v", err)
	}
	return zipEntries(t, data)
}

// TestPackBin_ImportRootInsideRoot is the GIS-570 case. Validate deliberately
// allows importRoot to sit INSIDE root (root ".", importRoot "lib" — the
// `fglpkg init` default shape), but PublishCopy cannot rebase root there
// (filepath.Rel("lib", ".") escapes), so it leaves root alone while staging
// strips the lib/ prefix from the file. The shipped manifest then pointed at
// "lib/scripts/greet.sh" while the script sat at "scripts/greet.sh", and the
// install failed outright on the executable-bit pass.
func TestPackBin_ImportRootInsideRoot(t *testing.T) {
	entries := packLayout(t, map[string]string{
		"fglpkg.json": `{
  "name": "fglpkgtest",
  "version": "1.0.0",
  "root": ".",
  "importRoot": "lib",
  "files": ["*.42m"],
  "bin": { "greet": "lib/scripts/greet.sh" },
  "dependencies": { "fgl": {} }
}`,
		"lib/ModuleA.42m":      "MAIN END MAIN\n",
		"lib/scripts/greet.sh": "#!/bin/sh\necho hi\n",
	})

	if got := assertBinResolves(t, entries, "greet"); got != "scripts/greet.sh" {
		t.Fatalf("expected the bin to resolve at the archive root, got %q", got)
	}
	// The author's importRoot prefix must not survive into the shipped path.
	m := shippedManifest(t, entries)
	if strings.HasPrefix(m.Bin["greet"], "lib/") {
		t.Fatalf("shipped bin still carries the importRoot prefix: %q", m.Bin["greet"])
	}
}

// TestPackBin_LayoutsStayConsistent runs the same invariant across every legal
// layout. The three that already worked must be byte-identical to before — the
// rebase is a no-op there, because root and the archive prefix move together.
func TestPackBin_LayoutsStayConsistent(t *testing.T) {
	cases := []struct {
		name        string
		manifest    string
		files       map[string]string
		wantBin     string // shipped bin path
		wantArchive string // where the script must sit in the archive
	}{
		{
			name: "no importRoot, root unset",
			manifest: `{"name":"fglpkgtest","version":"1.0.0","files":["*.42m"],
			            "bin":{"greet":"scripts/greet.sh"},"dependencies":{"fgl":{}}}`,
			files:       map[string]string{"ModuleA.42m": "MAIN END MAIN\n", "scripts/greet.sh": "#!/bin/sh\n"},
			wantBin:     "scripts/greet.sh",
			wantArchive: "scripts/greet.sh",
		},
		{
			name: "no importRoot, root set",
			manifest: `{"name":"fglpkgtest","version":"1.0.0","root":"src","files":["*.42m"],
			            "bin":{"greet":"scripts/greet.sh"},"dependencies":{"fgl":{}}}`,
			files:       map[string]string{"src/ModuleA.42m": "MAIN END MAIN\n", "src/scripts/greet.sh": "#!/bin/sh\n"},
			wantBin:     "scripts/greet.sh",
			wantArchive: "src/scripts/greet.sh",
		},
		{
			name: "root under importRoot",
			manifest: `{"name":"fglpkgtest","version":"1.0.0","root":"lib/com/x","importRoot":"lib",
			            "files":["*.42m"],"bin":{"greet":"scripts/greet.sh"},"dependencies":{"fgl":{}}}`,
			files:       map[string]string{"lib/com/x/ModuleA.42m": "MAIN END MAIN\n", "lib/com/x/scripts/greet.sh": "#!/bin/sh\n"},
			wantBin:     "scripts/greet.sh",
			wantArchive: "com/x/scripts/greet.sh",
		},
		{
			name: "importRoot inside root",
			manifest: `{"name":"fglpkgtest","version":"1.0.0","root":".","importRoot":"lib",
			            "files":["*.42m"],"bin":{"greet":"lib/scripts/greet.sh"},"dependencies":{"fgl":{}}}`,
			files:       map[string]string{"lib/ModuleA.42m": "MAIN END MAIN\n", "lib/scripts/greet.sh": "#!/bin/sh\n"},
			wantBin:     "scripts/greet.sh",
			wantArchive: "scripts/greet.sh",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{"fglpkg.json": tc.manifest}
			for k, v := range tc.files {
				files[k] = v
			}
			entries := packLayout(t, files)

			if _, ok := entries[tc.wantArchive]; !ok {
				t.Fatalf("script not staged at %q; entries: %v", tc.wantArchive, sortedKeys(entries))
			}
			m := shippedManifest(t, entries)
			if m.Bin["greet"] != tc.wantBin {
				t.Errorf("shipped bin = %q, want %q", m.Bin["greet"], tc.wantBin)
			}
			if got := assertBinResolves(t, entries, "greet"); got != tc.wantArchive {
				t.Errorf("bin resolves to %q, want %q", got, tc.wantArchive)
			}
		})
	}
}

// TestPackBin_TwoCommandsOneScript: `bin` may point two commands at the same
// script (BinFiles dedupes), and both must be rewritten.
func TestPackBin_TwoCommandsOneScript(t *testing.T) {
	entries := packLayout(t, map[string]string{
		"fglpkg.json": `{
  "name": "fglpkgtest",
  "version": "1.0.0",
  "root": ".",
  "importRoot": "lib",
  "files": ["*.42m"],
  "bin": { "greet": "lib/scripts/greet.sh", "hello": "lib/scripts/greet.sh" },
  "dependencies": { "fgl": {} }
}`,
		"lib/ModuleA.42m":      "MAIN END MAIN\n",
		"lib/scripts/greet.sh": "#!/bin/sh\n",
	})

	for _, cmd := range []string{"greet", "hello"} {
		if got := assertBinResolves(t, entries, cmd); got != "scripts/greet.sh" {
			t.Errorf("%s resolves to %q, want scripts/greet.sh", cmd, got)
		}
	}
}

// TestRebaseBinToArchive_RejectsScriptOutsideRoot: if a staged script lands
// outside the shipped root, no consumer could resolve it — fail the pack rather
// than ship a manifest that only breaks at the user's install.
func TestRebaseBinToArchive_RejectsScriptOutsideRoot(t *testing.T) {
	pub := &manifest.Manifest{
		Name:    "fglpkgtest",
		Version: "1.0.0",
		Root:    "com/x",
		Bin:     map[string]string{"greet": "scripts/greet.sh"},
	}
	// The staged path is outside root "com/x".
	err := rebaseBinToArchive(pub, map[string]string{"scripts/greet.sh": "elsewhere/greet.sh"})
	if err == nil {
		t.Fatal("expected an error for a script staged outside the shipped root")
	}
	if !strings.Contains(err.Error(), "outside root") {
		t.Fatalf("error should name the problem, got: %v", err)
	}
}

// TestRebaseBinToArchive_LeavesUnstagedPathsAlone: a bin the BDL walk never
// staged keeps the author's path rather than having one invented for it.
func TestRebaseBinToArchive_LeavesUnstagedPathsAlone(t *testing.T) {
	pub := &manifest.Manifest{
		Name:    "fglpkgtest",
		Version: "1.0.0",
		Bin:     map[string]string{"greet": "scripts/greet.sh"},
	}
	if err := rebaseBinToArchive(pub, map[string]string{"other.sh": "other.sh"}); err != nil {
		t.Fatalf("rebaseBinToArchive: %v", err)
	}
	if pub.Bin["greet"] != "scripts/greet.sh" {
		t.Fatalf("an unstaged bin path must be left as written, got %q", pub.Bin["greet"])
	}
}
