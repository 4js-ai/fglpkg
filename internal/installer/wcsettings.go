package installer

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Genero Studio's Form Designer discovers custom web components through the
// GSTWCDIR environment variable, which names ONE directory holding every
// component's .wcsettings descriptor (plus an optional same-named icon). That
// is a different thing from the directory holding the components themselves:
// the descriptor is design-time metadata, the component bundle is what the
// front-end loads at runtime. Genero ships both, side by side, and every DVM
// from 3.20 to 7.00 uses the same shape:
//
//	$FGLDIR/webcomponents/
//	  fglgallery/fglgallery.html          ← the component
//	  wcsettings/fglgallery.wcsettings    ← its Studio descriptor
//	  wcsettings/fglgallery.png           ← its optional Form Designer icon
//
// fglpkg mirrors that layout so `fglpkg env --gst` can emit a GSTWCDIR entry
// with the same meaning Studio's own default has ($FGLDIR/webcomponents/
// wcsettings). Packages, however, AUTHOR the descriptor inside the component
// directory it describes — webcomponents/<NAME>/<NAME>.wcsettings — because
// that tree is what `pack` already stages (stageWebcomponentFiles walks the
// declared COMPONENTTYPE dirs and nothing else), so shipping a descriptor
// needs no manifest field and no pack change. syncWCSettings is the step that
// turns the authored layout into the installed one. (GIS-536)
const (
	// wcSettingsDirName is the flat directory, under the webcomponents dir,
	// that collects every installed component's Studio descriptor. The name
	// matches Genero's own so a project's .fglpkg/webcomponents/ reads exactly
	// like $FGLDIR/webcomponents/.
	wcSettingsDirName = "wcsettings"

	// wcSettingsExt is the descriptor extension. The COMPONENTTYPE it
	// describes comes from the FILENAME — the XML itself carries only
	// <DynamicProperty> children and no name — which is what makes collecting
	// descriptors into one flat directory lossless.
	wcSettingsExt = ".wcsettings"
)

// wcSettingsIconExts are the image extensions accepted for a descriptor's
// optional Form Designer icon. Genero's docs call for an icon file "the same
// name as that of the wcsettings file"; the shipped components use .png.
//
// An icon is only ever copied for a component that ALSO ships a descriptor, so
// a component's runtime asset that happens to be named <NAME>.png is not
// mistaken for a Form Designer icon unless the package opted in by shipping
// <NAME>.wcsettings next to it.
var wcSettingsIconExts = []string{".png", ".jpg", ".jpeg", ".gif", ".svg"}

// syncWCSettings mirrors the Studio descriptors of the components just
// installed into <webcomponentsDir>/wcsettings/, and returns the slash-relative
// paths it wrote (under webcomponentsDir) so the caller can hand them to
// recordWCOwnership along with the component files themselves. That is what
// makes `fglpkg remove` prune a descriptor with its component, and what lets
// removeEmptyDirs drop the wcsettings dir once the last one goes.
//
// installed is the extraction result: slash-relative paths under
// webcomponentsDir, exactly as extractZipRouted / extractWebcomponentZip
// return them. Deriving the work from that list (rather than from the declared
// COMPONENTTYPE names) keeps both install paths on one rule and means a
// component that ships no descriptor contributes nothing.
//
// Descriptors for every component this package touched are cleared first, so a
// new version that DROPS its descriptor does not leave the old one stranded —
// on reinstall recordWCOwnership replaces the package's file list, and an
// unowned leftover would never be pruned.
func syncWCSettings(webcomponentsDir string, installed []string) ([]string, error) {
	descriptors, touched := classifyWCSettings(installed)
	if len(touched) == 0 {
		return nil, nil
	}

	settingsDir := filepath.Join(webcomponentsDir, wcSettingsDirName)

	// Clear this package's stale descriptors + icons before writing fresh
	// ones. Only names this install actually touched are cleared: a component
	// owned by a different package keeps its descriptor. (Two packages
	// shipping the same COMPONENTTYPE already clobber each other's component
	// dir — the descriptor inherits exactly those semantics, no new ones.)
	for _, name := range touched {
		for _, ext := range append([]string{wcSettingsExt}, wcSettingsIconExts...) {
			stale := filepath.Join(settingsDir, name+ext)
			if err := os.Remove(stale); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("cannot clear stale Studio descriptor %s: %w", stale, err)
			}
		}
	}

	if len(descriptors) == 0 {
		return nil, nil
	}
	if err := os.MkdirAll(settingsDir, 0755); err != nil {
		return nil, fmt.Errorf("cannot create %s: %w", settingsDir, err)
	}

	names := make([]string, 0, len(descriptors))
	for name := range descriptors {
		names = append(names, name)
	}
	sort.Strings(names) // deterministic ownership records

	var written []string
	for _, name := range names {
		for _, rel := range descriptors[name] {
			src := filepath.Join(webcomponentsDir, filepath.FromSlash(rel))
			dstRel := wcSettingsDirName + "/" + filepath.Base(rel)
			dst := filepath.Join(webcomponentsDir, filepath.FromSlash(dstRel))
			if err := copyWCSettingsFile(src, dst); err != nil {
				return nil, err
			}
			written = append(written, dstRel)
		}
	}
	return written, nil
}

// classifyWCSettings splits an extraction result into the descriptor (and
// icon) files to mirror, keyed by COMPONENTTYPE, and the full set of component
// names the install touched.
//
// A descriptor counts only at <NAME>/<NAME>.wcsettings IN A DIRECTORY THAT IS
// ITSELF A REAL COMPONENT (one carrying <NAME>/<NAME>.html) — the same
// name-matches-directory contract Genero applies to the entry point, and the
// one internal/env's isComponentDir enforces for --gwa and FGLIMAGEPATH
// (GIS-248). Requiring the entry point matters because the pure-WC installer
// extracts a package's docs globs into the shared namespace too: without it, a
// stray docs/docs.wcsettings would flatten into wcsettings/docs.wcsettings and
// offer "docs" to Studio as a COMPONENTTYPE that cannot be loaded.
func classifyWCSettings(installed []string) (descriptors map[string][]string, touched []string) {
	descriptors = map[string][]string{}
	seenName := map[string]bool{}
	present := make(map[string]bool, len(installed))
	for _, rel := range installed {
		present[rel] = true
	}

	for _, rel := range installed {
		name, file, ok := strings.Cut(rel, "/")
		if !ok || name == "" || name == wcSettingsDirName {
			// Zip-root files, and the wcsettings dir itself, are not
			// components. (A package that ships its own wcsettings/ tree —
			// only reachable via a docs glob — is left alone rather than
			// folded into its own destination.)
			continue
		}
		if !seenName[name] {
			seenName[name] = true
			touched = append(touched, name)
		}
		if file != name+wcSettingsExt {
			continue
		}
		if !present[name+"/"+name+".html"] {
			continue // not a loadable component — see the doc comment
		}
		files := []string{rel}
		for _, ext := range wcSettingsIconExts {
			if icon := name + "/" + name + ext; present[icon] {
				files = append(files, icon)
			}
		}
		descriptors[name] = files
	}
	sort.Strings(touched)
	return descriptors, touched
}

// copyWCSettingsFile copies one descriptor or icon into the wcsettings dir.
// A plain copy rather than a link or a move: the component dir keeps its own
// authored copy (that is what the package shipped, and what a reinstall
// rewrites), and the two must be independently removable.
func copyWCSettingsFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("cannot read Studio descriptor %s: %w", src, err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("cannot write Studio descriptor %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("cannot write Studio descriptor %s: %w", dst, err)
	}
	return out.Close()
}
