package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/installer"
	slugutil "github.com/4js-mikefolcher/fglpkg/internal/slug"
)

// docSource is where `fglpkg docs` found a package's documentation: a base
// directory, the doc files relative to it, and a label for the heading.
type docSource struct {
	baseDir string
	files   []string
	label   string // "<name>@<version>", or just "<name>" when unknown
}

// docFileExtensions are the suffixes treated as documentation when a package's
// `docs` globs are not available to consult.
var docFileExtensions = []string{".md", ".txt", ".rst", ".adoc"}

// docFileBasenames are extensionless files conventionally shipped as docs.
var docFileBasenames = []string{"LICENSE", "COPYING", "NOTICE", "README", "CHANGELOG", "USERGUIDE", "AUTHORS"}

// looksLikeDoc reports whether a slash-relative path is documentation.
//
// This heuristic is needed only on the web-component path. A BDL package keeps
// its fglpkg.json on disk, so its own `docs` globs decide; a web-component
// package keeps no manifest (several widgets would collide on it), so the
// ownership sidecar's file list is all there is, and it mixes the widget's
// JS/CSS in with its documentation.
func looksLikeDoc(slashPath string) bool {
	base := filepath.Base(slashPath)
	ext := strings.ToLower(filepath.Ext(base))
	for _, e := range docFileExtensions {
		if ext == e {
			return true
		}
	}
	stem := strings.ToUpper(strings.TrimSuffix(base, filepath.Ext(base)))
	for _, b := range docFileBasenames {
		if stem == b {
			return true
		}
	}
	return false
}

// findWebcomponentDocs locates the documentation of a package installed as the
// `webcomponent` variant, which findInstalledPackage cannot see.
//
// Such a package is extracted into .fglpkg/webcomponents/ rather than
// .fglpkg/packages/<slug>/, and its manifest is not kept, so the lookup by
// manifest never matched and `docs` reported every widget as not installed —
// telling the user to install something that was already installed (GIS-581).
// The ownership sidecar records exactly which files the package wrote, so the
// docs are resolved from there.
//
// Local (.fglpkg/) is checked before the global store, matching
// findInstalledPackage.
func findWebcomponentDocs(name string) (docSource, bool) {
	slug := slugutil.Canonical(name)
	var dirs []string
	if isProjectDir() {
		if wd, err := os.Getwd(); err == nil {
			dirs = append(dirs, filepath.Join(wd, ".fglpkg", "webcomponents"))
		}
	}
	if globalHome, err := fglpkgGlobalDir(); err == nil {
		dirs = append(dirs, filepath.Join(globalHome, "webcomponents"))
	}

	for _, dir := range dirs {
		// The sidecar is keyed by the package name as the registry served it.
		// Try the canonical slug too, so any spelling the user types resolves
		// the way it does for a BDL package (GIS-271).
		for _, key := range dedupeStrings([]string{name, slug}) {
			owned, version, found := installer.WebcomponentPackageFiles(dir, key)
			if !found {
				continue
			}
			var docs []string
			for _, f := range owned {
				if !looksLikeDoc(f) {
					continue
				}
				// Only report what is really on disk: the sidecar records what
				// was written, and a file removed by hand since would
				// otherwise be listed and then fail to open.
				if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil {
					continue
				}
				docs = append(docs, f)
			}
			label := key
			if version != "" {
				label = key + "@" + version
			}
			return docSource{baseDir: dir, files: docs, label: label}, true
		}
	}
	return docSource{}, false
}

// dedupeStrings returns in with empty values and later duplicates removed,
// preserving order.
func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
