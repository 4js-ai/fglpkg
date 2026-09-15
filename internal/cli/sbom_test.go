package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
	"github.com/4js-mikefolcher/fglpkg/internal/sbom"
)

// writeLockfileForSbom writes a small fglpkg-lock.json at dir containing
// the supplied packages + jars so the cmdSbom call has something to
// read.
func writeLockfileForSbom(t *testing.T, dir string, pkgs []lockfile.LockedPackage, jars []lockfile.LockedJAR) {
	t.Helper()
	lf := &lockfile.LockFile{
		Version:       1,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		GeneroVersion: "4.0.0",
		RootManifest:  lockfile.RootEntry{Name: "demo", Version: "0.1.0"},
		Packages:      pkgs,
		JARs:          jars,
	}
	if err := lf.Save(dir); err != nil {
		t.Fatalf("write lockfile: %v", err)
	}
}

func TestSbomFlagParsing(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		f, err := parseSbomFlags(nil)
		if err != nil {
			t.Fatalf("parseSbomFlags(nil) error: %v", err)
		}
		if f.output != "" || f.format != "" || f.production || f.pretty {
			t.Errorf("default flags should all be zero, got %+v", f)
		}
	})
	t.Run("output_short", func(t *testing.T) {
		f, err := parseSbomFlags([]string{"-o", "out.json"})
		if err != nil {
			t.Fatalf("parseSbomFlags error: %v", err)
		}
		if f.output != "out.json" {
			t.Errorf("output = %q, want out.json", f.output)
		}
	})
	t.Run("output_long_eq", func(t *testing.T) {
		f, err := parseSbomFlags([]string{"--output=sbom.json"})
		if err != nil {
			t.Fatalf("parseSbomFlags error: %v", err)
		}
		if f.output != "sbom.json" {
			t.Errorf("output = %q, want sbom.json", f.output)
		}
	})
	t.Run("output_missing_value", func(t *testing.T) {
		_, err := parseSbomFlags([]string{"-o"})
		if err == nil {
			t.Fatal("expected error when -o has no value")
		}
	})
	t.Run("flags_combined", func(t *testing.T) {
		f, err := parseSbomFlags([]string{"--pretty", "--production", "--format=cyclonedx"})
		if err != nil {
			t.Fatalf("parseSbomFlags error: %v", err)
		}
		if !f.pretty || !f.production || f.format != "cyclonedx" {
			t.Errorf("flags wrong: %+v", f)
		}
	})
	// GIS-540: --format must accept the space form too. It was the last
	// value-flag in the CLI to reject it, so `--format cyclonedx` failed with
	// `unknown argument "--format"` while the -o beside it took both forms.
	// The error assertions below pin the MESSAGE, not just err != nil: the
	// pre-fix parser errored on these same inputs as an unknown argument, so a
	// bare nil-check would pass against the behavior they exist to rule out.
	t.Run("format_space_form", func(t *testing.T) {
		f, err := parseSbomFlags([]string{"--format", "cyclonedx"})
		if err != nil {
			t.Fatalf("parseSbomFlags error: %v", err)
		}
		if f.format != "cyclonedx" {
			t.Errorf("format = %q, want cyclonedx", f.format)
		}
	})
	t.Run("format_space_form_missing_value", func(t *testing.T) {
		_, err := parseSbomFlags([]string{"--format"})
		if err == nil {
			t.Fatal("expected error when --format has no value, got nil")
		}
		if !strings.Contains(err.Error(), "--format requires a value") {
			t.Errorf("err = %v, want one reporting the MISSING VALUE (not an unknown flag)", err)
		}
	})
	// The consumed value must not swallow a following flag.
	t.Run("format_space_form_among_other_flags", func(t *testing.T) {
		f, err := parseSbomFlags([]string{"--pretty", "--format", "cyclonedx", "-o", "out.json"})
		if err != nil {
			t.Fatalf("parseSbomFlags error: %v", err)
		}
		if f.format != "cyclonedx" || f.output != "out.json" || !f.pretty {
			t.Errorf("surrounding flags not parsed: %+v", f)
		}
	})
	t.Run("unknown_arg", func(t *testing.T) {
		_, err := parseSbomFlags([]string{"--what"})
		if err == nil {
			t.Fatal("expected error for unknown arg")
		}
	})
}

func TestCmdSbomMissingLockfile(t *testing.T) {
	dir := t.TempDir()
	chdirTest(t, dir)
	err := cmdSbom(nil)
	if err == nil {
		t.Fatal("expected error when no lockfile present")
	}
	if !strings.Contains(err.Error(), "fglpkg-lock.json") {
		t.Errorf("err = %v, want one mentioning fglpkg-lock.json", err)
	}
}

func TestCmdSbomFormatSpdxRejected(t *testing.T) {
	dir := t.TempDir()
	chdirTest(t, dir)
	writeLockfileForSbom(t, dir, nil, nil)
	for _, args := range [][]string{{"--format=spdx"}, {"--format", "spdx"}} {
		err := cmdSbom(args)
		if err == nil {
			t.Fatalf("%v: expected error for spdx format", args)
		}
		if !strings.Contains(err.Error(), "spdx") {
			t.Errorf("%v: err = %v, want one mentioning spdx", args, err)
		}
	}
}

func TestCmdSbomToFile(t *testing.T) {
	dir := t.TempDir()
	chdirTest(t, dir)
	writeLockfileForSbom(t, dir,
		[]lockfile.LockedPackage{{Name: "poiapi", Version: "1.0.0", RequiredBy: []string{"<root>"}}},
		nil,
	)
	outPath := filepath.Join(dir, "sbom.json")
	if err := cmdSbom([]string{"-o", outPath}); err != nil {
		t.Fatalf("cmdSbom: %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read sbom: %v", err)
	}
	var doc sbom.Document
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	if doc.BomFormat != "CycloneDX" || doc.SpecVersion != "1.5" {
		t.Errorf("bom header wrong: %q/%q", doc.BomFormat, doc.SpecVersion)
	}
	if len(doc.Components) != 1 || doc.Components[0].Name != "poiapi" {
		t.Errorf("components wrong: %+v", doc.Components)
	}
}

func TestCmdSbomProductionFilter(t *testing.T) {
	dir := t.TempDir()
	chdirTest(t, dir)
	writeLockfileForSbom(t, dir, nil, []lockfile.LockedJAR{
		{Key: "g:prod", GroupID: "g", ArtifactID: "prod", Version: "1.0.0"},
		{Key: "g:dev", GroupID: "g", ArtifactID: "dev", Version: "1.0.0", Scope: "dev"},
	})
	outPath := filepath.Join(dir, "sbom.json")
	if err := cmdSbom([]string{"--production", "-o", outPath}); err != nil {
		t.Fatalf("cmdSbom: %v", err)
	}
	data, _ := os.ReadFile(outPath)
	var doc sbom.Document
	_ = json.Unmarshal(data, &doc)
	if len(doc.Components) != 1 || doc.Components[0].Name != "prod" {
		t.Errorf("expected only the prod jar, got %+v", doc.Components)
	}
}

func TestCmdSbomCompactByDefault(t *testing.T) {
	dir := t.TempDir()
	chdirTest(t, dir)
	writeLockfileForSbom(t, dir, []lockfile.LockedPackage{{Name: "a", Version: "1.0.0"}}, nil)
	outPath := filepath.Join(dir, "sbom.json")
	if err := cmdSbom([]string{"-o", outPath}); err != nil {
		t.Fatalf("cmdSbom: %v", err)
	}
	data, _ := os.ReadFile(outPath)
	// json.Encoder writes a trailing newline; everything before it
	// should be on one line in compact mode.
	trimmed := strings.TrimRight(string(data), "\n")
	if strings.Contains(trimmed, "\n") {
		t.Errorf("expected compact (single-line) JSON, got multi-line output:\n%s", data)
	}
}

func TestCmdSbomPrettyHasIndentation(t *testing.T) {
	dir := t.TempDir()
	chdirTest(t, dir)
	writeLockfileForSbom(t, dir, []lockfile.LockedPackage{{Name: "a", Version: "1.0.0"}}, nil)
	outPath := filepath.Join(dir, "sbom.json")
	if err := cmdSbom([]string{"--pretty", "-o", outPath}); err != nil {
		t.Fatalf("cmdSbom: %v", err)
	}
	data, _ := os.ReadFile(outPath)
	if !bytes.Contains(data, []byte("\n  \"")) {
		t.Errorf("pretty output should contain 2-space indented keys: %s", data)
	}
}

func TestWriteSbomMarshalShape(t *testing.T) {
	var buf bytes.Buffer
	doc := &sbom.Document{
		BomFormat:   "CycloneDX",
		SpecVersion: "1.5",
		Version:     1,
		Metadata:    sbom.Metadata{Timestamp: "2026-05-15T00:00:00Z"},
	}
	if err := writeSbom(&buf, doc, false); err != nil {
		t.Fatalf("writeSbom: %v", err)
	}
	if !strings.Contains(buf.String(), `"bomFormat":"CycloneDX"`) {
		t.Errorf("output missing bomFormat: %s", buf.String())
	}
}
