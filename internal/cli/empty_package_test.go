package cli

import (
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// probeEmpty stages the given project and returns the GIS-276 emptiness verdict
// straight from the staging classifier — no Genero detection or network needed.
// It reuses writeLintProject (lint_test.go) to lay down files and chdir.
func probeEmpty(t *testing.T, files map[string]string) bool {
	t.Helper()
	writeLintProject(t, files)
	m, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	_, _, empty, err := buildPackageZipClassified(m)
	if err != nil {
		t.Fatalf("buildPackageZipClassified: %v", err)
	}
	return empty
}

// TestPackageEmptinessClassification pins the structural, kind-agnostic rule:
// an archive is empty only when nothing but fglpkg.json and files matched
// SOLELY by `docs` was staged. BDL, bin, include, webcomponent, and doubly
// matched files all count as assets (GIS-276).
func TestPackageEmptinessClassification(t *testing.T) {
	// A publishable manifest stem; each case appends its own content fields.
	base := func(extra string) string {
		return `{"name":"pkg.demo","version":"1.0.0","description":"d",` +
			`"license":"MIT","repository":"https://github.com/x/y","author":"a"` + extra + `}`
	}
	cases := []struct {
		name  string
		files map[string]string
		empty bool
	}{
		{
			name: "a README matched only by docs is not an asset",
			files: map[string]string{
				"fglpkg.json": base(`,"docs":["README.md"]`),
				"README.md":   "# hi\n",
			},
			empty: true,
		},
		{
			name: "a README with no docs glob is staged by nothing",
			files: map[string]string{
				"fglpkg.json": base(`,"files":["*.42m"]`),
				"README.md":   "# hi\n",
			},
			empty: true,
		},
		{
			name: "a BDL module is an asset",
			files: map[string]string{
				"fglpkg.json": base(`,"files":["*.42m"],"docs":["README.md"]`),
				"README.md":   "# hi\n",
				"Main.42m":    "MAIN\nEND MAIN\n",
			},
			empty: false,
		},
		{
			// The case the old BDL-only check got wrong: a shell bin script is
			// not "BDL source" but is a genuine asset.
			name: "a non-BDL bin script is an asset",
			files: map[string]string{
				"fglpkg.json": base(`,"bin":{"deploy":"deploy.sh"},"docs":["README.md"]`),
				"README.md":   "# hi\n",
				"deploy.sh":   "#!/bin/sh\necho hi\n",
			},
			empty: false,
		},
		{
			name: "an include file is an asset",
			files: map[string]string{
				"fglpkg.json": base(`,"include":["extra.txt"],"docs":["README.md"]`),
				"README.md":   "# hi\n",
				"extra.txt":   "data\n",
			},
			empty: false,
		},
		{
			name: "a pure-webcomponent tree (html/js) is an asset",
			files: map[string]string{
				"fglpkg.json":                    base(`,"webcomponents":["Chart"],"docs":["README.md"]`),
				"README.md":                      "# hi\n",
				"webcomponents/Chart/Chart.html": "<html></html>\n",
				"webcomponents/Chart/Chart.js":   "console.log(1)\n",
			},
			empty: false,
		},
		{
			// A file matched by BOTH `files` and `docs` is claimed by the asset
			// stager first, so it stays an asset.
			name: "a module matched by both files and docs still counts",
			files: map[string]string{
				"fglpkg.json": base(`,"files":["*.42m"],"docs":["*.42m"]`),
				"Main.42m":    "MAIN\nEND MAIN\n",
			},
			empty: false,
		},
		{
			// Regression: include runs AFTER docs, so before the docs-last
			// ordering an include file that a docs glob also matched was hidden
			// behind the no-op dedup and mis-reported as empty.
			name: "an include file also matched by a docs glob stays an asset",
			files: map[string]string{
				"fglpkg.json": base(`,"include":["extra.txt"],"docs":["*.txt"]`),
				"extra.txt":   "data\n",
			},
			empty: false,
		},
		{
			// Same regression for a profile-only package (profile also stages
			// after docs).
			name: "a profile file also matched by a docs glob stays an asset",
			files: map[string]string{
				"fglpkg.json": base(`,"profile":["app.4gp"],"docs":["*.4gp"]`),
				"app.4gp":     "profile\n",
			},
			empty: false,
		},
		{
			// Regression: a broad `files` glob stages fglpkg.json itself into the
			// asset walk. The manifest must never count as content, so a package
			// whose only staged file is the manifest (+ a docs-only README) is
			// still empty.
			name: "the manifest matched by a broad files glob does not count",
			files: map[string]string{
				"fglpkg.json": base(`,"files":["*.json"],"docs":["README.md"]`),
				"README.md":   "# hi\n",
			},
			empty: true,
		},
		{
			// Same, via an include naming the manifest.
			name: "the manifest named by include does not count",
			files: map[string]string{
				"fglpkg.json": base(`,"include":["fglpkg.json"]`),
			},
			empty: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := probeEmpty(t, tc.files); got != tc.empty {
				t.Errorf("empty = %v, want %v", got, tc.empty)
			}
		})
	}
}

// TestPublishRefusesEmptyWithoutAllowEmpty confirms the guard blocks a
// well-formed but asset-less publish before any network work, and names the
// escape hatch. The manifest carries all publish-required metadata, so the
// refusal is specifically the empty-package guard — not a validation error.
func TestPublishRefusesEmptyWithoutAllowEmpty(t *testing.T) {
	t.Setenv("FGLPKG_HOME", t.TempDir())
	t.Setenv("FGLPKG_GENERO_VERSION", "6.00.01")
	writeLintProject(t, map[string]string{
		"fglpkg.json": `{
  "name": "empty.demo",
  "version": "1.0.0",
  "description": "d",
  "license": "MIT",
  "repository": "https://github.com/x/y",
  "author": "a",
  "files": ["*.42m"]
}`,
		"README.md": "# empty\n",
	})
	err := cmdPublish([]string{"--dry-run"})
	if err == nil {
		t.Fatal("publish should refuse an asset-less package without --allow-empty")
	}
	if !strings.Contains(err.Error(), "no assets") || !strings.Contains(err.Error(), "--allow-empty") {
		t.Errorf("refusal should mention no assets and --allow-empty, got: %v", err)
	}
}

// TestParsePublishFlagsAllowEmpty guards the flag surface.
func TestParsePublishFlagsAllowEmpty(t *testing.T) {
	pf, err := parsePublishFlags([]string{"--allow-empty", "--dry-run"})
	if err != nil {
		t.Fatalf("parsePublishFlags: %v", err)
	}
	if !pf.allowEmpty {
		t.Error("--allow-empty should set allowEmpty")
	}
	if !pf.dryRun {
		t.Error("--dry-run should still parse alongside --allow-empty")
	}
}
