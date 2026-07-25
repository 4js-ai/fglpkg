package env

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/workspace"
)

// Generator builds the environment variable exports needed for Genero BDL.
type Generator struct {
	home             string
	packagesDir      string
	jarsDir          string
	webcomponentsDir string
}

// New creates a Generator rooted at the fglpkg home directory.
func New(home string) *Generator {
	return &Generator{
		home:             home,
		packagesDir:      filepath.Join(home, "packages"),
		jarsDir:          filepath.Join(home, "jars"),
		webcomponentsDir: filepath.Join(home, "webcomponents"),
	}
}

// Generate returns a slice of shell export lines suitable for eval.
// On Unix:  export VAR=value
// On Windows: SET VAR=value
//
// The generated exports prepend fglpkg-managed paths to any existing
// value of FGLLDPATH / CLASSPATH so that user or system entries are
// never lost.
func (g *Generator) Generate() ([]string, error) {
	var lines []string

	fglldpath, err := g.buildFGLLDPATH()
	if err != nil {
		return nil, err
	}
	lines = append(lines, g.prependExportLine("FGLLDPATH", fglldpath))

	javaClasspath, err := g.buildJavaClasspath()
	if err != nil {
		return nil, err
	}
	if javaClasspath != "" {
		lines = append(lines, g.prependExportLine("CLASSPATH", javaClasspath))
	}

	fglimagepath := g.buildFGLIMAGEPATH()
	if fglimagepath != "" {
		lines = append(lines, g.prependExportLine("FGLIMAGEPATH", fglimagepath))
		lines = append(lines, g.gasHintComment(fglimagepath))
	}

	return lines, nil
}

// buildFGLIMAGEPATH returns the directories to prepend to FGLIMAGEPATH so
// Genero's direct-mode webcomponent loader finds installed bundles via
// "<fglimagepath-dir>/webcomponents/<COMPONENTTYPE>/<COMPONENTTYPE>.html".
// We add the *parent* of the webcomponents/ dir (i.e. .fglpkg/), per the
// Genero search rule.
//
// Returns "" when no webcomponents are installed in any of the considered
// scopes, so the export line is skipped entirely.
func (g *Generator) buildFGLIMAGEPATH() string {
	sep := pathSeparator()
	var parts []string
	seen := make(map[string]bool)
	add := func(p string) {
		if p != "" && !seen[p] {
			parts = append(parts, p)
			seen[p] = true
		}
	}
	// Local project webcomponents (.fglpkg/) take precedence.
	localWC := filepath.Join(".", ".fglpkg", "webcomponents")
	if abs, err := filepath.Abs(localWC); err == nil && abs != g.webcomponentsDir {
		if entries, err := os.ReadDir(abs); err == nil && len(entries) > 0 {
			// Add the parent — not the webcomponents/ dir itself — so
			// Genero's "<fglimagepath-dir>/webcomponents/<COMPONENTTYPE>"
			// search rule resolves correctly.
			add(filepath.Dir(abs))
		}
	}
	// Global webcomponents (~/.fglpkg/webcomponents).
	if entries, err := os.ReadDir(g.webcomponentsDir); err == nil && len(entries) > 0 {
		add(filepath.Dir(g.webcomponentsDir))
	}
	return strings.Join(parts, sep)
}

// gasHintComment is a shell-comment line that tells the user what value
// to add to their .xcf's <WEB_COMPONENT_DIRECTORY> for the same set of
// installed webcomponents. fglpkg cannot edit .xcf files (deployment
// concern), so we print the hint for copy/paste.
func (g *Generator) gasHintComment(fglimagepathValue string) string {
	sep := pathSeparator()
	wcDirs := make([]string, 0)
	for _, p := range strings.Split(fglimagepathValue, sep) {
		if p == "" {
			continue
		}
		// fglimagepathValue holds the *parent* of webcomponents/ — for
		// GAS, point at webcomponents/ directly.
		wcDirs = append(wcDirs, filepath.Join(p, "webcomponents"))
	}
	prefix := "# "
	if runtime.GOOS == "windows" {
		prefix = "REM "
	}
	return fmt.Sprintf("%sFor GAS: add to your .xcf's <WEB_COMPONENT_DIRECTORY>: %s",
		prefix, strings.Join(wcDirs, sep))
}

// GenerateGWA returns one --webcomponent flag per installed component,
// ready to splice into a `gwabuildtool` invocation. Each flag points at a
// COMPONENTTYPE directory, which is the unit gwabuildtool consumes.
//
// Looks in both .fglpkg/webcomponents/ (local project) and ~/.fglpkg/
// webcomponents/ (global) — typical projects only install locally, but
// the global fallback lets `fglpkg env --gwa` work outside a project too.
func (g *Generator) GenerateGWA() ([]string, error) {
	var lines []string
	seen := make(map[string]bool)
	addFrom := func(dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			abs := filepath.Join(dir, e.Name())
			if !seen[e.Name()] {
				lines = append(lines, "--webcomponent "+abs)
				seen[e.Name()] = true
			}
		}
	}
	localWC := filepath.Join(".", ".fglpkg", "webcomponents")
	if abs, err := filepath.Abs(localWC); err == nil {
		if abs != g.webcomponentsDir {
			addFrom(abs)
		}
	}
	addFrom(g.webcomponentsDir)
	return lines, nil
}

// buildFGLLDPATH returns the fglpkg-managed FGLLDPATH entries.
// Order of precedence (highest first):
//  1. Workspace member source directories (local dev, no install needed)
//  2. Local project scope (.fglpkg/ in cwd)
//  3. Global installed scope (~/.fglpkg/)
//
// Within each scope the PACKAGE-correct merged root (.fglpkg/merged) is emitted
// as a single entry, followed by a per-package entry for any package the merged
// root does not cover (legacy/flat — see scopeFGLLDPATHDirs). Emitting the
// merged root — laid out by namespace — rather than one raw store dir per
// package is what makes `IMPORT FGL <ns>.<mod>` resolve correctly regardless of
// the store-dir name (GIS-358) and turns a same-namespace collision into the
// install-time hard error (GIS-359) instead of silent shadowing.
//
// The existing FGLLDPATH value is preserved at eval time via
// prependExportLine, so we do not read it here.
func (g *Generator) buildFGLLDPATH() (string, error) {
	sep := pathSeparator()
	var parts []string
	seen := make(map[string]bool)

	add := func(p string) {
		if p != "" && !seen[p] {
			parts = append(parts, p)
			seen[p] = true
		}
	}

	// 1. Workspace member paths (if we're inside a workspace).
	if wsRoot := workspace.FindRoot("."); wsRoot != "" {
		ws, err := workspace.Load(wsRoot)
		if err == nil {
			for _, entry := range ws.FGLLDPATHEntries() {
				add(entry)
			}
		}
	}

	// 2. Local project scope (higher priority than global).
	localPkgs := filepath.Join(".", ".fglpkg", "packages")
	if abs, err := filepath.Abs(localPkgs); err == nil {
		if abs != g.packagesDir { // avoid duplicating if local == global
			for _, p := range scopeFGLLDPATHDirs(abs) {
				add(p)
			}
		}
	}

	// 3. Global installed scope.
	for _, p := range scopeFGLLDPATHDirs(g.packagesDir) {
		add(p)
	}

	return strings.Join(parts, sep), nil
}

// scopeFGLLDPATHDirs returns the ordered FGLLDPATH directories for one scope
// whose per-package stores live under packagesDir (which must be absolute). The
// merged root — its sibling <home>/merged — is emitted first (one namespace-
// correct entry) when it exists and is non-empty, followed by a per-package
// store dir for every package the merged root does not cover.
//
// A package is "covered" (store dir dropped) only when it is materialized AND
// every importable .42m it ships is present in the merged root (see
// storeDirCovered). Legacy/flat packages keep their historical per-package
// entry, and so does a materialized package that still ships an un-merged
// flat-root / non-namespaced module — so that module keeps resolving.
func scopeFGLLDPATHDirs(packagesDir string) []string {
	var out []string
	mergedDir := filepath.Join(filepath.Dir(packagesDir), "merged")
	mergedActive := isNonEmptyDir(mergedDir)
	if mergedActive {
		out = append(out, mergedDir)
	}
	entries, err := os.ReadDir(packagesDir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pkgDir := filepath.Join(packagesDir, e.Name())
		// Drop the per-package store dir only when the merged root is active
		// AND fully covers this package's importable modules. A materialized
		// package that still ships an un-merged .42m — a flat-root or otherwise
		// non-namespaced library module — keeps its store entry so that module
		// resolves as it did before the merged root existed. The merged root is
		// emitted first, so namespaced modules still resolve namespace-correctly
		// (GIS-358) and the retained store entry only backstops the leftovers.
		if mergedActive && storeDirCovered(pkgDir, mergedDir) {
			continue
		}
		out = append(out, pkgDir)
	}
	return out
}

// storeDirCovered reports whether an installed package's per-package store dir
// is fully represented in the merged root, so it can be dropped from FGLLDPATH.
// It is covered only when the package is materialized (its manifest records ≥1
// generoPackages namespace) AND every importable .42m it ships is present in
// the merged root. A legacy/flat package (no recorded namespaces), an
// unreadable manifest, or a package that still ships an un-merged importable
// module (a flat-root / non-namespaced .42m) is NOT covered — its store dir
// stays on FGLLDPATH so those modules keep resolving (the historical
// behaviour), never dropping a package from FGLLDPATH on a manifest read error.
func storeDirCovered(pkgDir, mergedDir string) bool {
	m, err := manifest.Load(pkgDir)
	if err != nil || len(m.GeneroPackages) == 0 {
		return false
	}
	return storeFullyMerged(pkgDir, mergedDir, m.Programs)
}

// storeFullyMerged reports whether every importable .42m under pkgDir is
// present in the merged root at the same relative path. It returns false as
// soon as it finds an importable module the merged root does not cover — a
// flat-root or otherwise non-namespaced .42m — so the caller keeps the store
// dir on FGLLDPATH and that module keeps resolving. Declared program modules
// (manifest `programs`) run by path and are never resolved via FGLLDPATH, so
// they never force the store dir to be kept (mirrors materialize's exclusion).
func storeFullyMerged(pkgDir, mergedDir string, programs []string) bool {
	isProgram := programMatcher(programs)
	fullyMerged := true
	_ = filepath.WalkDir(pkgDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".42m") {
			return nil
		}
		rel, relErr := filepath.Rel(pkgDir, p)
		if relErr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if isProgram(relSlash) {
			return nil // out-of-namespace program — never on FGLLDPATH
		}
		if _, statErr := os.Stat(filepath.Join(mergedDir, filepath.FromSlash(relSlash))); statErr != nil {
			fullyMerged = false
			return fs.SkipAll // one uncovered module is enough
		}
		return nil
	})
	return fullyMerged
}

// programMatcher returns a predicate that reports whether a store-relative .42m
// path (slash form) is a declared program module — matched by full path or by
// basename, tolerating a trailing .42m — mirroring materialize's programSets so
// env and materialize agree on which modules are out-of-namespace.
func programMatcher(programs []string) func(relSlash string) bool {
	full := make(map[string]bool, len(programs))
	base := make(map[string]bool, len(programs))
	for _, pr := range programs {
		pr = strings.TrimSuffix(filepath.ToSlash(strings.TrimSpace(pr)), ".42m")
		if pr == "" {
			continue
		}
		full[pr] = true
		base[path.Base(pr)] = true
	}
	return func(relSlash string) bool {
		modFull := strings.TrimSuffix(relSlash, ".42m")
		return full[modFull] || base[path.Base(modFull)]
	}
}

// isNonEmptyDir reports whether dir exists and contains at least one entry.
func isNonEmptyDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}

// buildJavaClasspath returns the fglpkg-managed CLASSPATH entries by
// scanning the jars directory for all .jar files.  Local project jars
// (.fglpkg/jars/) are included with higher priority than global jars.
// The existing CLASSPATH value is preserved at eval time via prependExportLine.
func (g *Generator) buildJavaClasspath() (string, error) {
	sep := pathSeparator()
	seen := make(map[string]bool)
	var jars []string

	addJarsFrom := func(dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".jar") {
				p := filepath.Join(dir, e.Name())
				if !seen[p] {
					jars = append(jars, p)
					seen[p] = true
				}
			}
		}
	}

	// Local project jars (higher priority).
	localJars := filepath.Join(".", ".fglpkg", "jars")
	if abs, err := filepath.Abs(localJars); err == nil {
		if abs != g.jarsDir {
			addJarsFrom(abs)
		}
	}

	// Global jars.
	addJarsFrom(g.jarsDir)

	return strings.Join(jars, sep), nil
}

// GenerateLocal returns export lines using only the local project's
// .fglpkg/packages, .fglpkg/jars, and .fglpkg/webcomponents directories.
func (g *Generator) GenerateLocal() ([]string, error) {
	var lines []string

	localPkgs := filepath.Join(".", ".fglpkg", "packages")
	if abs, err := filepath.Abs(localPkgs); err == nil {
		if dirs := scopeFGLLDPATHDirs(abs); len(dirs) > 0 {
			lines = append(lines, g.prependExportLine("FGLLDPATH", strings.Join(dirs, pathSeparator())))
		}
	}

	localJars := filepath.Join(".", ".fglpkg", "jars")
	classpath, err := g.buildPathsFrom(localJars, false)
	if err != nil {
		return nil, err
	}
	if classpath != "" {
		lines = append(lines, g.prependExportLine("CLASSPATH", classpath))
	}

	// FGLIMAGEPATH for direct-mode webcomponent discovery — point at the
	// parent of .fglpkg/webcomponents/ so Genero's
	// "<fglimagepath-dir>/webcomponents/<COMPONENTTYPE>/" search resolves.
	localWC := filepath.Join(".", ".fglpkg", "webcomponents")
	if abs, err := filepath.Abs(localWC); err == nil {
		if entries, err := os.ReadDir(abs); err == nil && len(entries) > 0 {
			fglimagepath := filepath.Dir(abs)
			lines = append(lines, g.prependExportLine("FGLIMAGEPATH", fglimagepath))
			lines = append(lines, g.gasHintComment(fglimagepath))
		}
	}

	return lines, nil
}

// GenerateGlobal returns export lines using only the global fglpkg home
// directories (~/.fglpkg/packages, jars, webcomponents), ignoring any local
// project .fglpkg/. This backs `fglpkg env --global`, which must emit the
// global scope only — not merge in the current project's packages the way
// Generate() does.
func (g *Generator) GenerateGlobal() ([]string, error) {
	var lines []string

	if dirs := scopeFGLLDPATHDirs(g.packagesDir); len(dirs) > 0 {
		lines = append(lines, g.prependExportLine("FGLLDPATH", strings.Join(dirs, pathSeparator())))
	}

	classpath, err := g.buildPathsFrom(g.jarsDir, false)
	if err != nil {
		return nil, err
	}
	if classpath != "" {
		lines = append(lines, g.prependExportLine("CLASSPATH", classpath))
	}

	// FGLIMAGEPATH for direct-mode webcomponent discovery — parent of the
	// global webcomponents/ dir.
	if entries, err := os.ReadDir(g.webcomponentsDir); err == nil && len(entries) > 0 {
		fglimagepath := filepath.Dir(g.webcomponentsDir)
		lines = append(lines, g.prependExportLine("FGLIMAGEPATH", fglimagepath))
		lines = append(lines, g.gasHintComment(fglimagepath))
	}

	return lines, nil
}

// GenerateGST returns environment variable assignments in Genero Studio
// format. Genero Studio uses:
//   - $(ProjectDir) for the base project directory
//   - $(VARIABLE) to reference environment variables
//   - ; as the path separator (always, regardless of OS)
func (g *Generator) GenerateGST() ([]string, error) {
	var lines []string

	if parts := g.gstFGLLDPATHParts(); len(parts) > 0 {
		lines = append(lines, fmt.Sprintf("FGLLDPATH=%s;$(FGLLDPATH)", strings.Join(parts, ";")))
	}

	localJars := filepath.Join(".", ".fglpkg", "jars")
	classpath, err := g.buildGSTPaths(localJars, false)
	if err != nil {
		return nil, err
	}
	if classpath != "" {
		lines = append(lines, fmt.Sprintf("CLASSPATH=%s;$(CLASSPATH)", classpath))
	}

	// FGLIMAGEPATH for direct-mode webcomponent discovery — point at the
	// parent of .fglpkg/webcomponents/ ($(ProjectDir)/.fglpkg) so Genero's
	// "<fglimagepath-dir>/webcomponents/<COMPONENTTYPE>/" search resolves.
	// (No GAS hint here: GST output is a strict VAR=value list Genero Studio
	// consumes, so a shell-comment line would be invalid.)
	localWC := filepath.Join(".", ".fglpkg", "webcomponents")
	if entries, err := os.ReadDir(localWC); err == nil && len(entries) > 0 {
		lines = append(lines, "FGLIMAGEPATH=$(ProjectDir)/.fglpkg;$(FGLIMAGEPATH)")
	}

	return lines, nil
}

// gstFGLLDPATHParts returns the local-scope FGLLDPATH parts in Genero Studio
// ($(ProjectDir)) form: the merged root first when present and non-empty, then a
// per-package path for every package the merged root does not cover (mirroring
// scopeFGLLDPATHDirs, but emitted as $(ProjectDir)-relative templates).
func (g *Generator) gstFGLLDPATHParts() []string {
	var parts []string
	mergedDir := filepath.Join(".", ".fglpkg", "merged")
	mergedActive := isNonEmptyDir(mergedDir)
	if mergedActive {
		parts = append(parts, "$(ProjectDir)/.fglpkg/merged")
	}
	localPkgs := filepath.Join(".", ".fglpkg", "packages")
	entries, err := os.ReadDir(localPkgs)
	if err != nil {
		return parts
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pkgDir := filepath.Join(localPkgs, e.Name())
		if mergedActive && storeDirCovered(pkgDir, mergedDir) {
			continue
		}
		parts = append(parts, "$(ProjectDir)/.fglpkg/packages/"+e.Name())
	}
	return parts
}

// buildPathsFrom scans a directory and returns paths joined by the OS separator.
// If isDirs is true, it collects subdirectories; otherwise, it collects .jar files.
func (g *Generator) buildPathsFrom(dir string, isDirs bool) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", nil
	}
	entries, err := os.ReadDir(abs)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("cannot read directory %s: %w", dir, err)
	}

	sep := pathSeparator()
	var parts []string
	for _, e := range entries {
		if isDirs && e.IsDir() {
			parts = append(parts, filepath.Join(abs, e.Name()))
		} else if !isDirs && !e.IsDir() && strings.HasSuffix(e.Name(), ".jar") {
			parts = append(parts, filepath.Join(abs, e.Name()))
		}
	}
	return strings.Join(parts, sep), nil
}

// buildGSTPaths scans a directory and returns paths in Genero Studio format,
// using $(ProjectDir) as the base and ; as the separator.
func (g *Generator) buildGSTPaths(dir string, isDirs bool) (string, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("cannot read directory %s: %w", dir, err)
	}

	var parts []string
	for _, e := range entries {
		if isDirs && e.IsDir() {
			parts = append(parts, "$(ProjectDir)/.fglpkg/packages/"+e.Name())
		} else if !isDirs && !e.IsDir() && strings.HasSuffix(e.Name(), ".jar") {
			parts = append(parts, "$(ProjectDir)/.fglpkg/jars/"+e.Name())
		}
	}
	return strings.Join(parts, ";"), nil
}

// BuildFGLLDPATH returns the raw FGLLDPATH value (no export prefix).
// Useful for programmatically setting the environment (e.g., fglpkg bdl).
func (g *Generator) BuildFGLLDPATH() (string, error) {
	return g.buildFGLLDPATH()
}

// BuildJavaClasspath returns the raw CLASSPATH value (no export prefix).
func (g *Generator) BuildJavaClasspath() (string, error) {
	return g.buildJavaClasspath()
}

// MergeEnvVar prepends fglpkgValue to existingValue using the OS path
// separator. Returns just fglpkgValue if existingValue is empty, and
// vice versa.
func MergeEnvVar(fglpkgValue, existingValue string) string {
	if fglpkgValue == "" {
		return existingValue
	}
	if existingValue == "" {
		return fglpkgValue
	}
	return fglpkgValue + pathSeparator() + existingValue
}

// prependExportLine emits a shell statement that prepends value to the
// existing variable, so that user/system entries are never lost.
//
// Unix output:  export VAR='/new/paths'"${VAR:+:$VAR}"
// Windows output: SET VAR=/new/paths;%VAR%
func (g *Generator) prependExportLine(key, value string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("SET %s=%s;%%%s%%", key, value, key)
	}
	// The ${VAR:+:$VAR} construct expands to ":$VAR" only when VAR is
	// non-empty, avoiding a trailing colon when the variable is unset.
	return fmt.Sprintf("export %s=%s\"${%s:+:%s}\"", key, value, key, "$"+key)
}

func pathSeparator() string {
	if runtime.GOOS == "windows" {
		return ";"
	}
	return ":"
}
