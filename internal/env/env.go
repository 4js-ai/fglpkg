// Package env computes the Genero environment variables that make installed
// fglpkg packages resolvable, and renders them for a shell (`eval`), for
// Genero Studio, or for a child process.
//
// Shell rendering is selected explicitly — sh, PowerShell or cmd — and quotes
// values only when they would otherwise be mis-parsed; see shell.go, which also
// explains why the target shell and the path separator are separate decisions.
// The Genero Studio and child-process paths are never shell-quoted.
//
// Six variables are managed, in two shapes:
//
//   - FGLLDPATH resolves program modules (*.42m/.42r/.42x) by NAMESPACE PATH,
//     so installed packages are exposed through a single synthetic merged root
//     laid out by `PACKAGE` namespace (GIS-346/358/359 — see the merged-root
//     machinery below).
//   - CLASSPATH lists Java jars.
//   - FGLRESOURCEPATH, FGLDBPATH and FGLIMAGEPATH are directory search paths
//     looked up by BASENAME, non-recursively. There is no namespace to remap,
//     so the merged-root trick does not transfer: we hand Genero the real
//     directories that hold the files. A store dir therefore stays load-bearing
//     for resources even after the merged root has taken it off FGLLDPATH.
//   - FGLPROFILE is not a search path at all — it is an ordered list of FILES,
//     merged with later entries overriding earlier ones.
//
// Because the basename-addressed variables have no namespace to disambiguate,
// two packages shipping the same file name shadow each other. That is the
// GIS-359 failure the merged root removed for modules, and it cannot be removed
// here — so it is made loud instead: collisions are recorded as warnings for
// the CLI to print to stderr (never stdout, which must stay `eval`-safe and,
// under --gst, a strict VAR=value list).
package env

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/classpath"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/workspace"
)

// envVar names a Genero environment variable this package manages.
type envVar string

const (
	varLD       envVar = "FGLLDPATH"
	varClass    envVar = "CLASSPATH"
	varResource envVar = "FGLRESOURCEPATH"
	varDB       envVar = "FGLDBPATH"
	varImage    envVar = "FGLIMAGEPATH"
	varProfile  envVar = "FGLPROFILE"
)

// assetExtensions maps a lowercased file extension to the search-path variable
// whose value must contain the file's DIRECTORY. These variables are searched
// non-recursively by basename, which is why the scanner emits every leaf
// directory that DIRECTLY holds a match rather than a package's store root: a
// namespaced package ships its forms at <store>/com/fourjs/poiapi/Customer.42f,
// and the store root alone would never resolve them.
//
// Deliberately NOT a generic "BDL source" list: a build-time source check would
// include .4gl/.per (never installed) and .42m (FGLLDPATH, not a resource),
// whereas this list answers the resource-installation question. Fusing the two
// would force one list to grow .png.
// The three lists below are transcribed from the Genero 6.00 reference manual,
// not from what fglpkg happens to have seen in the wild. Each cites its source
// so a future reader can re-check it against a newer BDL release rather than
// guessing whether an omission was deliberate.
var assetExtensions = map[string]envVar{
	// FGLRESOURCEPATH — the manual's "program resource files" list is closed and
	// has exactly these eight entries. See c_fgl_EnvVariables_FGLRESOURCEPATH.
	".42f": varResource, // compiled form
	".iem": varResource, // compiled message file (fglmkmsg; OPTIONS HELP FILE)
	".4ad": varResource, // action defaults
	".4st": varResource, // presentation style
	".4sm": varResource, // start menu
	".4tb": varResource, // toolbar
	".4tm": varResource, // topmenu
	".42s": varResource, // compiled localized strings
	// FGLDBPATH — a database schema is three files, not one, and fgldbsch emits
	// them side by side. See c_fgl_DatabaseSchema_017. .val/.att are legacy
	// (backward compatibility only) but still resolved through FGLDBPATH, so a
	// package that ships them must get its schema dir on the path.
	".sch": varDB, // column definitions
	".val": varDB, // column validation rules (legacy)
	".att": varDB, // column video attributes (legacy)
	// FGLIMAGEPATH — c_fgl_images_resource_spec, "supported image file formats".
	".png":  varImage,
	".jpg":  varImage,
	".jpeg": varImage, // not in the manual's table, which lists only .jpg
	".gif":  varImage,
	".svg":  varImage,
	".bmp":  varImage,
	".ico":  varImage,
	".tiff": varImage,
	".tif":  varImage, // as .jpeg: the manual lists only the four-letter spelling
	// TrueType fonts are image resources too: with an image-to-font-glyph
	// mapping file, a bare image name resolves to a glyph in a .ttf, and the
	// manual requires the font file's DIRECTORY to be on FGLIMAGEPATH unless it
	// sits beside the mapping file. Note that the mapping file ITSELF is a file
	// entry on FGLIMAGEPATH, not a directory — fglpkg cannot infer one by
	// extension (the manual's own example is a .txt), so shipping a custom icon
	// map still needs the path set by hand.
	".ttf": varImage,
}

// assetVarOrder fixes the emission order of the scanned directory variables so
// output is byte-stable across runs.
var assetVarOrder = []envVar{varResource, varDB, varImage}

// RawEnvOrder is the order in which callers should apply RawEnv's keys when
// building a child-process environment.
var RawEnvOrder = []string{
	string(varLD), string(varClass), string(varResource),
	string(varDB), string(varImage), string(varProfile),
}

// maxWarnings caps the diagnostics emitted per run. Two badly overlapping
// packages could otherwise print hundreds of lines on every shell startup.
const maxWarnings = 20

// maxValueLen is the point past which a variable's value is worth warning
// about: Windows caps the whole environment block at ~32 KB, and a package with
// hundreds of namespaced resource directories can get there on its own.
const maxValueLen = 8000

// Generator builds the environment variable exports needed for Genero BDL.
type Generator struct {
	home             string
	packagesDir      string
	jarsDir          string
	webcomponentsDir string

	// shell selects the syntax Generate/GenerateLocal/GenerateGlobal render.
	// The zero value means DefaultShell(), so a Generator built the old way —
	// env.New(home) — renders exactly what it rendered before --shell existed.
	// GenerateGST and the raw accessors ignore it entirely; see their comments.
	shell Shell

	// warnings holds the diagnostics from the most recent Generate*/RawEnv
	// call. Reset at the top of each, so Warnings() is never stale or
	// cumulative. This package never prints; the CLI writes these to stderr.
	warnings []string
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

// WithShell selects the shell syntax the Generate* renderers emit, and returns
// g so it composes at the call site: env.New(home).WithShell(f.shell).
//
// A setter rather than a New parameter or per-shell methods: New has ~20 call
// sites that do not care about shells, and most of them are tests whose subject
// is which PATHS get emitted, not how they are wrapped.
func (g *Generator) WithShell(sh Shell) *Generator {
	g.shell = sh
	return g
}

// targetShell resolves the zero value lazily, so New's signature — and every
// existing caller — stays untouched.
func (g *Generator) targetShell() Shell {
	if g.shell == "" {
		return DefaultShell()
	}
	return g.shell
}

// Warnings returns the diagnostics accumulated by the most recent
// Generate*/RawEnv call: basename collisions between installed packages,
// unusable `profile` declarations, and over-long values.
//
// The env package is a library and never prints. The CLI writes these to
// STDERR so `eval "$(fglpkg env)"` and the strict VAR=value --gst list stay
// uncontaminated.
func (g *Generator) Warnings() []string { return g.warnings }

// ─── scanning ─────────────────────────────────────────────────────────────────

// storeScan is everything one walk of a single installed package's store dir
// yields. Four output modes used to want three different answers about the same
// tree; scanning once and caching is the difference between one pass over the
// store and several.
type storeScan struct {
	pkg       string                         // manifest name, else the store dir's basename
	dir       string                         // absolute store dir walked
	assets    map[envVar]map[string][]string // var -> leaf dir -> basenames found there
	assetDirs map[envVar][]string            // var -> leaf dirs, in walk (lexical) order
	profiles  []string                       // absolute, existence-checked profile FILES
	covered   bool                           // materialized AND every importable .42m is merged
	warnings  []string                       // problems found in this package
}

// scanner walks each store dir at most once per command invocation.
type scanner struct {
	cache map[string]*storeScan
	walks int // test seam: asserts one walk per store dir
}

func newScanner() *scanner {
	return &scanner{cache: make(map[string]*storeScan)}
}

// scan walks pkgDir once, classifying every file by extension and recording the
// .42m merged-root coverage verdict FGLLDPATH needs.
//
// The `covered` verdict is byte-for-byte the pre-existing storeDirCovered /
// storeFullyMerged semantics, and the invariants it encodes (GIS-346/358) must
// not drift:
//
//   - A package is covered — its store dir droppable from FGLLDPATH — only when
//     it is materialized (its manifest records ≥1 generoPackages namespace) AND
//     every importable .42m it ships is present in the merged root.
//   - A legacy/flat package (no recorded namespaces), an unreadable manifest, or
//     a package still shipping an un-merged importable module (a flat-root /
//     non-namespaced .42m) is NOT covered — its store dir stays on FGLLDPATH so
//     those modules keep resolving. A manifest read error must never drop a
//     package from FGLLDPATH.
//   - Declared program modules (manifest `programs`) run by path and are never
//     resolved via FGLLDPATH, so they never force the store dir to be kept
//     (mirrors materialize's exclusion).
//
// A scan never fails: a corrupt installed manifest degrades to "no profiles,
// keeps its FGLLDPATH entry, resources still harvested".
func (s *scanner) scan(pkgDir, mergedDir string) *storeScan {
	key := pkgDir + "\x00" + mergedDir
	if cached, ok := s.cache[key]; ok {
		return cached
	}
	s.walks++

	sc := &storeScan{
		pkg:       filepath.Base(pkgDir),
		dir:       pkgDir,
		assets:    make(map[envVar]map[string][]string),
		assetDirs: make(map[envVar][]string),
	}
	s.cache[key] = sc

	m, mErr := manifest.Load(pkgDir)
	hasNamespaces := mErr == nil && len(m.GeneroPackages) > 0
	if mErr == nil && strings.TrimSpace(m.Name) != "" {
		sc.pkg = m.Name
	}

	var isProgram func(string) bool
	if mErr == nil {
		isProgram = programMatcher(m.Programs)
	} else {
		isProgram = func(string) bool { return false }
	}

	sawUncovered := false
	_ = filepath.WalkDir(pkgDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// Skip dot-directories below the store root (VCS metadata, editor
			// state). The root itself lives under .fglpkg, so it must not be
			// caught by its own name.
			if p != pkgDir && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()

		if strings.HasSuffix(name, ".42m") {
			if !hasNamespaces || sawUncovered {
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
				sawUncovered = true
			}
			return nil
		}

		v, ok := assetExtensions[strings.ToLower(filepath.Ext(name))]
		if !ok {
			return nil
		}
		dir := filepath.Dir(p)
		if sc.assets[v] == nil {
			sc.assets[v] = make(map[string][]string)
		}
		if _, seen := sc.assets[v][dir]; !seen {
			sc.assetDirs[v] = append(sc.assetDirs[v], dir)
		}
		sc.assets[v][dir] = append(sc.assets[v][dir], name)
		return nil
	})

	sc.covered = hasNamespaces && !sawUncovered

	if mErr == nil {
		sc.profiles = resolveProfiles(pkgDir, sc.pkg, m.Profile, &sc.warnings)
	}
	return sc
}

// resolveProfiles turns a manifest's declared `profile` entries into absolute
// file paths, dropping any that escape the store dir (defence against a hostile
// or hand-edited installed manifest) or that are not present on disk.
func resolveProfiles(pkgDir, pkg string, declared []string, warnings *[]string) []string {
	var out []string
	for _, entry := range declared {
		clean := filepath.Clean(filepath.FromSlash(strings.TrimSpace(entry)))
		if clean == "" || clean == "." {
			continue
		}
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			*warnings = append(*warnings, fmt.Sprintf(
				"FGLPROFILE: package %q declares profile %q, which escapes the package directory; ignoring it.",
				pkg, entry))
			continue
		}
		full := filepath.Join(pkgDir, clean)
		info, err := os.Stat(full)
		if err != nil || info.IsDir() {
			*warnings = append(*warnings, fmt.Sprintf(
				"FGLPROFILE: package %q declares profile %q, but no such file is installed at %s; ignoring it.",
				pkg, entry, full))
			continue
		}
		out = append(out, full)
	}
	return out
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

// ─── scopes ───────────────────────────────────────────────────────────────────

// envScope is one .fglpkg tree to harvest. Scopes are listed in precedence
// order (local before global), which is also the order their paths appear in
// every emitted value — so "first on the path wins" is a documented,
// reproducible rule rather than an accident of directory iteration.
type envScope struct {
	name        string // "local" | "global" — appears verbatim in collision warnings
	root        string // absolute .fglpkg dir
	packagesDir string
	mergedDir   string
	jarsDir     string
	wcDir       string
}

// scopes returns the ordered scopes for one output mode.
//
// The local scope is dropped when both scopes are requested and the local
// .fglpkg resolves to the same directory as the global home — the local ==
// global guard that used to be duplicated across every builder. A local-only
// mode keeps its scope unconditionally, since there is nothing to duplicate.
func (g *Generator) scopes(includeLocal, includeGlobal bool) []envScope {
	var out []envScope
	if includeLocal {
		if abs, err := filepath.Abs(filepath.Join(".", ".fglpkg")); err == nil {
			if !includeGlobal || filepath.Join(abs, "packages") != g.packagesDir {
				out = append(out, envScope{
					name:        "local",
					root:        abs,
					packagesDir: filepath.Join(abs, "packages"),
					mergedDir:   filepath.Join(abs, "merged"),
					jarsDir:     filepath.Join(abs, "jars"),
					wcDir:       filepath.Join(abs, "webcomponents"),
				})
			}
		}
	}
	if includeGlobal {
		out = append(out, envScope{
			name:        "global",
			root:        g.home,
			packagesDir: g.packagesDir,
			mergedDir:   filepath.Join(g.home, "merged"),
			jarsDir:     g.jarsDir,
			wcDir:       g.webcomponentsDir,
		})
	}
	return out
}

// ─── planning ─────────────────────────────────────────────────────────────────

// owner records which package first put a basename on a variable's search path,
// so a later package shipping the same basename can be reported as shadowed
// rather than silently losing.
type owner struct{ scope, pkg, dir string }

// envPlan is the mode-independent result of scanning: an ordered, deduplicated
// value per variable. Every output mode renders the same plan; only the line
// syntax and the path form differ.
type envPlan struct {
	ldpath    []string
	classpath []string
	assets    map[envVar][]string // varResource / varDB / varImage -> ordered dirs
	profiles  []string            // ordered full FILE paths
	wcParents []string            // parents of webcomponents/ dirs — GAS hint only

	seen     map[envVar]map[string]bool
	owners   map[envVar]map[string]owner
	warnings []string
	dropped  int // warnings suppressed by maxWarnings
}

func newPlan() *envPlan {
	return &envPlan{
		assets: make(map[envVar][]string),
		seen:   make(map[envVar]map[string]bool),
		owners: make(map[envVar]map[string]owner),
	}
}

func (p *envPlan) warn(format string, args ...interface{}) {
	if len(p.warnings) >= maxWarnings {
		p.dropped++
		return
	}
	p.warnings = append(p.warnings, fmt.Sprintf(format, args...))
}

// addPath appends dir to v's value unless it is already there. A path appearing
// twice is not a collision — the first insertion simply keeps its position, and
// since local scopes are visited before global ones, a local directory always
// outranks its global twin.
func (p *envPlan) addPath(v envVar, dir string) {
	if dir == "" {
		return
	}
	dir = filepath.Clean(dir)
	if p.seen[v] == nil {
		p.seen[v] = make(map[string]bool)
	}
	if p.seen[v][dir] {
		return
	}
	p.seen[v][dir] = true
	switch v {
	case varLD:
		p.ldpath = append(p.ldpath, dir)
	case varClass:
		p.classpath = append(p.classpath, dir)
	default:
		p.assets[v] = append(p.assets[v], dir)
	}
}

// claim registers the basenames a package contributes to v from dir, warning
// when a different package already put the same basename on that variable.
//
// Basenames are compared exactly, not case-folded: Genero resolves them
// case-sensitively on Unix, so lowercasing would invent collisions there. The
// cost is under-reporting a Customer.42f / customer.42f pair on Windows, which
// is the rarer and less confusing failure.
func (p *envPlan) claim(v envVar, scope, pkg, dir string, basenames []string) {
	if p.owners[v] == nil {
		p.owners[v] = make(map[string]owner)
	}
	for _, base := range basenames {
		prev, exists := p.owners[v][base]
		if !exists {
			p.owners[v][base] = owner{scope: scope, pkg: pkg, dir: dir}
			continue
		}
		if prev.dir == dir {
			continue // same directory reached twice; addPath already deduped it
		}
		if prev.pkg == pkg && prev.scope != scope {
			// The same package installed both locally and globally is
			// precedence, not a clash — the rule that already governs
			// PACKAGE namespaces across scopes.
			continue
		}
		if prev.pkg == pkg {
			p.warn("%s: package %q ships %q in two directories, %s and %s; the first is searched first and the second is unreachable.",
				v, pkg, base, prev.dir, dir)
			continue
		}
		p.warn("%s: %q is shipped by both %q (%s) and %q (%s).\n"+
			"  These variables are searched by basename, first match wins: the copy in\n"+
			"  %s will be used and %s's is shadowed.\n"+
			"  Rename one of the two files, or set %s yourself to pick a winner.",
			v, base, prev.pkg, prev.scope, pkg, scope, prev.dir, pkg, v)
	}
}

// buildPlan harvests every scope in precedence order.
func (g *Generator) buildPlan(s *scanner, scopes []envScope, includeWorkspace bool) (*envPlan, error) {
	p := newPlan()

	// Workspace member source dirs come first — local development resolves
	// against the working tree without an install. They contribute to
	// FGLLDPATH only: a member is a SOURCE tree (.per, not .42f), its compiled
	// resources land wherever its build puts them, and walking arbitrary user
	// trees on every `fglpkg env` has no size bound. Declaring resource dirs
	// for workspace members is deliberately left to a follow-up.
	if includeWorkspace {
		if wsRoot := workspace.FindRoot("."); wsRoot != "" {
			if ws, err := workspace.Load(wsRoot); err == nil {
				for _, entry := range ws.FGLLDPATHEntries() {
					p.addPath(varLD, entry)
				}
			}
		}
	}

	for _, sc := range scopes {
		mergedActive := isNonEmptyDir(sc.mergedDir)
		if mergedActive {
			// One namespace-correct entry instead of one raw store dir per
			// package: what makes IMPORT FGL <ns>.<mod> resolve regardless of
			// the store dir's name (GIS-358).
			p.addPath(varLD, sc.mergedDir)
		}

		// CLASSPATH carries one anchor jar per jars dir, never the individual
		// jars — see internal/classpath's package comment for why. The anchor
		// is REFERENCED here, never written: install/update/remove keep it
		// current (classpath.Sync), while env/bdl are read→stdout commands —
		// env is typically eval'd from shell profiles, so a disk write here
		// would race concurrent shells and fail on a read-only global home.
		if anchor := classpath.AnchorPath(sc.jarsDir); anchor != "" {
			p.addPath(varClass, anchor)
		} else if classpath.HasDependencyJars(sc.jarsDir) {
			p.warn("%s holds JAR dependencies but no %s; run 'fglpkg install' to regenerate the classpath anchor.",
				sc.jarsDir, classpath.AnchorName)
		}

		// FGLIMAGEPATH points at the PARENT of webcomponents/ (i.e. .fglpkg/),
		// because Genero's direct-mode loader searches
		// "<fglimagepath-dir>/webcomponents/<COMPONENTTYPE>/<COMPONENTTYPE>.html".
		// Emitting it before any packaged image dir keeps the historical
		// ordering.
		if isNonEmptyDir(sc.wcDir) {
			parent := filepath.Dir(sc.wcDir)
			p.wcParents = append(p.wcParents, parent)
			p.addPath(varImage, parent)
		}

		entries, err := os.ReadDir(sc.packagesDir)
		if err != nil {
			continue // scope not installed yet
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			pkgDir := filepath.Join(sc.packagesDir, e.Name())
			scan := s.scan(pkgDir, sc.mergedDir)

			// Drop the per-package store dir from FGLLDPATH only when the
			// merged root is active AND fully covers this package's importable
			// modules. A materialized package that still ships an un-merged
			// .42m — flat-root or otherwise non-namespaced — keeps its store
			// entry so that module resolves as it did before the merged root
			// existed. The merged root is emitted first, so namespaced modules
			// still resolve namespace-correctly and the retained store entry
			// only backstops the leftovers.
			if !(mergedActive && scan.covered) {
				p.addPath(varLD, pkgDir)
			}

			// Assets are harvested from EVERY store dir, including one just
			// dropped from FGLLDPATH. That drop is a .42m-only judgement: the
			// merged root holds no forms, schemas or images, so the store dir
			// remains the only place they exist. Reusing the FGLLDPATH list
			// here would silently un-resolve every resource in a materialized
			// package.
			for _, v := range assetVarOrder {
				for _, dir := range scan.assetDirs[v] {
					p.addPath(v, dir)
					p.claim(v, sc.name, scan.pkg, dir, scan.assets[v][dir])
				}
			}
			// FGLPROFILE accumulates in the OPPOSITE order to everything above,
			// and the inversion is load-bearing — do not "tidy" this into an
			// append. The search-path variables are first-match-wins, so the
			// highest-precedence entry must come first. FGLPROFILE is not a
			// search path: the runtime loads every listed file in order and
			// merges them, so for a duplicated entry key the LAST file loaded
			// wins. Highest precedence therefore has to come LAST.
			//
			// Prepending each package's block as it is discovered reverses the
			// scope/lexical walk exactly: local-a, local-b, global-a, global-b
			// accumulates to [global-b, global-a, local-b, local-a], so the
			// locally installed copy overrides the global one — the same
			// precedence the search-path variables get by being listed first.
			// The block is prepended whole, preserving the author's intra-package
			// order, where last-wins is likewise what a manifest's `profile`
			// array means.
			p.profiles = append(append([]string(nil), scan.profiles...), p.profiles...)
			for _, w := range scan.warnings {
				p.warn("%s", w)
			}
		}
	}

	return p, nil
}

// finalWarnings returns the plan's diagnostics with the overflow tail appended.
func (p *envPlan) finalWarnings() []string {
	if p.dropped == 0 {
		return p.warnings
	}
	return append(p.warnings, fmt.Sprintf("… and %d more environment warning(s) suppressed.", p.dropped))
}

// ─── rendering ────────────────────────────────────────────────────────────────

// renderShell emits prepending assignment statements plus the GAS hint comment,
// in the canonical variable order, in the syntax of the target shell. A variable
// with no value is skipped entirely, except FGLLDPATH when alwaysLD is set (the
// historical behaviour of the default `fglpkg env` mode).
//
// The shell and the path separator are independent: the shell is whoever will
// execute these lines, the separator is what the Genero runtime that parses the
// values expects. See internal/env/shell.go's header.
func (g *Generator) renderShell(p *envPlan, alwaysLD bool) []string {
	sh, sep := g.targetShell(), pathSeparator()
	var lines []string

	// emit records any shell-specific representation problem before appending
	// the statement. The statement is emitted either way: it is still the user's
	// best copy/paste source, and a variable silently missing is worse than one
	// the shell complains about.
	emit := func(v envVar, value string) {
		for _, w := range shellLimitWarnings(sh, string(v), value) {
			p.warn("%s", w)
		}
		lines = append(lines, prependLine(sh, string(v), value, sep))
	}

	ld := strings.Join(p.ldpath, sep)
	if ld != "" || alwaysLD {
		emit(varLD, ld)
	}
	if cp := strings.Join(p.classpath, sep); cp != "" {
		emit(varClass, cp)
	}
	for _, v := range assetVarOrder {
		value := strings.Join(p.assets[v], sep)
		if value == "" {
			continue
		}
		emit(v, value)
		// The GAS hint describes webcomponent directories, so it belongs to
		// FGLIMAGEPATH only when webcomponents are what put it there. Once
		// packaged images can populate the same variable, gating on a non-empty
		// FGLIMAGEPATH would print a hint pointing at <imagedir>/webcomponents.
		if v == varImage && len(p.wcParents) > 0 {
			lines = append(lines, gasHintComment(sh, sep, p.wcParents))
		}
	}
	if prof := strings.Join(p.profiles, sep); prof != "" {
		emit(varProfile, prof)
	}

	// Deliberately the UNQUOTED join: the platform's environment-block limit
	// applies to the value the OS ends up storing, not to the statement text.
	p.checkValueLengths(sep)
	return lines
}

// renderGST emits Genero Studio "VAR=a;b;$(VAR)" assignments: ";" always,
// forward slashes, and $(ProjectDir)-relative paths. No comment lines are
// produced — GST output is a strict VAR=value list Genero Studio parses, so
// diagnostics go to stderr instead.
//
// This is a FILE FORMAT, not a shell: it is never quoted, never shell-dependent
// (--shell does not apply, and the CLI rejects the combination), and always uses
// ";" regardless of platform. Quote characters here would be stored verbatim in
// the user's project environment settings.
//
// Paths outside the local scope have no $(ProjectDir) representation and are
// dropped, which is exactly today's local-only GST behaviour.
func renderGST(p *envPlan, localRoot string) []string {
	var lines []string
	emit := func(v envVar, paths []string) {
		var parts []string
		for _, abs := range paths {
			if tpl, ok := gstPath(localRoot, abs); ok {
				parts = append(parts, tpl)
			}
		}
		if len(parts) > 0 {
			lines = append(lines, fmt.Sprintf("%s=%s;$(%s)", v, strings.Join(parts, ";"), v))
		}
	}

	emit(varLD, p.ldpath)
	emit(varClass, p.classpath)
	for _, v := range assetVarOrder {
		emit(v, p.assets[v])
	}
	emit(varProfile, p.profiles)

	p.checkValueLengths(";")
	return lines
}

// gstPath rewrites an absolute path under the local scope root into its
// $(ProjectDir)/.fglpkg/... template form, reporting false when the path lies
// outside that root.
func gstPath(localRoot, abs string) (string, bool) {
	const prefix = "$(ProjectDir)/.fglpkg"
	rel, err := filepath.Rel(localRoot, abs)
	if err != nil {
		return "", false
	}
	relSlash := filepath.ToSlash(rel)
	if relSlash == ".." || strings.HasPrefix(relSlash, "../") {
		return "", false
	}
	if relSlash == "." {
		return prefix, true
	}
	return prefix + "/" + relSlash, true
}

// checkValueLengths warns once per variable whose rendered value is long enough
// to risk the platform environment-block limit (Windows caps the whole block at
// roughly 32 KB).
func (p *envPlan) checkValueLengths(sep string) {
	check := func(v envVar, parts []string) {
		if n := len(strings.Join(parts, sep)); n > maxValueLen {
			p.warn("%s: the generated value is %d characters across %d entries, which may exceed the platform's environment size limit.",
				v, n, len(parts))
		}
	}
	check(varLD, p.ldpath)
	check(varClass, p.classpath)
	for _, v := range assetVarOrder {
		check(v, p.assets[v])
	}
	check(varProfile, p.profiles)
}

// gasHintComment is a shell-comment line that tells the user what value to add
// to their .xcf's <WEB_COMPONENT_DIRECTORY> for the installed webcomponents.
// fglpkg cannot edit .xcf files (a deployment concern), so we print the hint for
// copy/paste.
//
// It is derived from the webcomponents parents specifically, NOT from the whole
// FGLIMAGEPATH value: that variable now also carries packaged image
// directories, which GAS must not be told about.
//
// The value is NOT quoted: this is a comment for a human to copy into an .xcf,
// not a statement any shell will execute, so shell quoting would only put stray
// characters into what the user pastes.
func gasHintComment(sh Shell, sep string, wcParents []string) string {
	wcDirs := make([]string, 0, len(wcParents))
	for _, p := range wcParents {
		// wcParents holds the PARENT of webcomponents/ — for GAS, point at
		// webcomponents/ directly.
		wcDirs = append(wcDirs, filepath.Join(p, "webcomponents"))
	}
	return fmt.Sprintf("%sFor GAS: add to your .xcf's <WEB_COMPONENT_DIRECTORY>: %s",
		commentPrefix(sh), strings.Join(wcDirs, sep))
}

// ─── output modes ─────────────────────────────────────────────────────────────

// run is the single pipeline behind every output mode: scan the requested
// scopes once, plan the values, render them.
func (g *Generator) run(includeLocal, includeGlobal, includeWorkspace bool) (*envPlan, []envScope, error) {
	g.warnings = nil
	scopes := g.scopes(includeLocal, includeGlobal)
	p, err := g.buildPlan(newScanner(), scopes, includeWorkspace)
	if err != nil {
		return nil, nil, err
	}
	return p, scopes, nil
}

// Generate returns a slice of shell statements suitable for eval, covering
// workspace members plus the local and global scopes.
//
// The syntax is the Generator's target shell (WithShell, defaulting to
// DefaultShell()):
//
//	sh:         export VAR=value"${VAR:+:$VAR}"
//	powershell: $env:VAR = 'value' + $(if ($env:VAR) { ';' + $env:VAR })
//	cmd:        SET "VAR=value;%VAR%"
//
// Values are quoted only when they contain a character the shell would otherwise
// split on or interpret, so a path of ordinary characters is emitted exactly as
// it was before --shell existed. See internal/env/shell.go.
//
// The generated statements PREPEND fglpkg-managed paths to any existing value,
// so user or system entries are never lost. For the search-path variables that
// means fglpkg's entries win; for FGLPROFILE, whose entries are applied
// left-to-right with the last winning, it means the user's own profile still
// overrides a package's.
func (g *Generator) Generate() ([]string, error) {
	p, _, err := g.run(true, true, true)
	if err != nil {
		return nil, err
	}
	// FGLLDPATH is emitted even when empty here, preserving the historical
	// behaviour of the default mode.
	lines := g.renderShell(p, true)
	g.warnings = p.finalWarnings()
	return lines, nil
}

// GenerateLocal returns export lines using only the local project's .fglpkg/.
func (g *Generator) GenerateLocal() ([]string, error) {
	p, _, err := g.run(true, false, false)
	if err != nil {
		return nil, err
	}
	lines := g.renderShell(p, false)
	g.warnings = p.finalWarnings()
	return lines, nil
}

// GenerateGlobal returns export lines using only the global fglpkg home,
// ignoring any local project .fglpkg/. This backs `fglpkg env --global`, which
// must emit the global scope only — not merge in the current project's packages
// the way Generate() does (GIS-290).
func (g *Generator) GenerateGlobal() ([]string, error) {
	p, _, err := g.run(false, true, false)
	if err != nil {
		return nil, err
	}
	lines := g.renderShell(p, false)
	g.warnings = p.finalWarnings()
	return lines, nil
}

// GenerateGST returns environment variable assignments in Genero Studio format,
// for the local scope only. Genero Studio uses $(ProjectDir) for the project
// base, $(VARIABLE) to reference an existing value, and ";" as the separator
// regardless of platform.
func (g *Generator) GenerateGST() ([]string, error) {
	p, scopes, err := g.run(true, false, false)
	if err != nil {
		return nil, err
	}
	localRoot := ""
	if len(scopes) > 0 {
		localRoot = scopes[0].root
	}
	lines := renderGST(p, localRoot)
	g.warnings = p.finalWarnings()
	return lines, nil
}

// GenerateGWA returns one --webcomponent flag per installed component, ready to
// splice into a `gwabuildtool` invocation. Each flag points at a COMPONENTTYPE
// directory, which is the unit gwabuildtool consumes.
//
// Looks in both .fglpkg/webcomponents/ (local project) and
// ~/.fglpkg/webcomponents/ (global) — typical projects only install locally,
// but the global fallback lets `fglpkg env --gwa` work outside a project too.
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

// ─── raw values (for building a child process environment) ────────────────────

// RawEnv returns the fglpkg-managed value for every variable env manages, keyed
// by variable name, over the same scopes `fglpkg env` uses by default. Values
// are raw — no export prefix, and not merged with the inherited value; absent
// variables are omitted. Callers merge with MergeEnvVar and should apply the
// keys in RawEnvOrder.
//
// Values are NEVER SHELL-QUOTED, and must not be: these feed cmd.Env in
// `fglpkg bdl`, which hands them to exec.Command. No shell is involved there, so
// a quote character would land literally inside the child process's variable and
// break Genero's path resolution. Shell quoting belongs to renderShell alone.
func (g *Generator) RawEnv() (map[string]string, error) {
	p, _, err := g.run(true, true, true)
	if err != nil {
		return nil, err
	}
	sep := pathSeparator()
	out := make(map[string]string, len(RawEnvOrder))
	set := func(v envVar, parts []string) {
		if joined := strings.Join(parts, sep); joined != "" {
			out[string(v)] = joined
		}
	}
	set(varLD, p.ldpath)
	set(varClass, p.classpath)
	for _, v := range assetVarOrder {
		set(v, p.assets[v])
	}
	set(varProfile, p.profiles)

	p.checkValueLengths(sep)
	g.warnings = p.finalWarnings()
	return out, nil
}

// BuildFGLLDPATH returns the raw FGLLDPATH value (no export prefix, never
// shell-quoted — see RawEnv). Useful for programmatically setting the
// environment (e.g., fglpkg bdl).
func (g *Generator) BuildFGLLDPATH() (string, error) {
	p, _, err := g.run(true, true, true)
	if err != nil {
		return "", err
	}
	g.warnings = p.finalWarnings()
	return strings.Join(p.ldpath, pathSeparator()), nil
}

// BuildJavaClasspath returns the raw CLASSPATH value (no export prefix, never
// shell-quoted — see RawEnv).
func (g *Generator) BuildJavaClasspath() (string, error) {
	p, _, err := g.run(true, true, true)
	if err != nil {
		return "", err
	}
	g.warnings = p.finalWarnings()
	return strings.Join(p.classpath, pathSeparator()), nil
}

// MergeEnvVar prepends fglpkgValue to existingValue using the OS path
// separator. Returns just fglpkgValue if existingValue is empty, and
// vice versa.
//
// Pure joining, never shell-quoted — see RawEnv: the result is destined for
// cmd.Env, not for a shell.
func MergeEnvVar(fglpkgValue, existingValue string) string {
	if fglpkgValue == "" {
		return existingValue
	}
	if existingValue == "" {
		return fglpkgValue
	}
	return fglpkgValue + pathSeparator() + existingValue
}

// pathSeparator is the separator the GENERO RUNTIME expects inside a value:
// fglrun and fglcomp split FGLLDPATH and FGLPROFILE on ";" on Windows and ":"
// elsewhere, whichever shell set them. It is therefore a platform property and
// stays GOOS-derived even under `--shell sh` — Git Bash on Windows configures a
// Windows fglrun and still needs ";". The shell only decides the syntax that
// wraps the value; see internal/env/shell.go.
func pathSeparator() string {
	if runtime.GOOS == "windows" {
		return ";"
	}
	return ":"
}
