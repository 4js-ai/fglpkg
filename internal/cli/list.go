package cli

// `fglpkg list` — the installed dependency tree.
//
// The tree's shape comes from two different places, because the lock file only
// records half of it:
//
//   - BDL packages and webcomponents carry requiredBy (with the "<root>"
//     sentinel for a direct dependency), so their parentage is exact and the
//     tree is a straight inversion of those fields.
//   - JARs do not: LockedJAR has no requiredBy field. Their parentage is
//     reconstructed at display time from the bundled fglpkg.json of every
//     installed package, via Installer.JarDeclarers. A JAR no manifest on disk
//     claims hangs off the root — the same honest fallback the CycloneDX SBOM
//     makes for every JAR (internal/sbom.buildDependencyEdges).
//
// fglpkg does no transitive POM resolution, so a JAR never has children: every
// JAR node is a leaf one level below whoever declared it, and all of the tree's
// depth comes from the Genero package graph.
//
// The global store (`list --global`) has no lock file — that lives beside a
// project's fglpkg.json — so its tree is reconstructed a third way, from the
// bundled fglpkg.json of every installed package (buildGlobalForest). The result
// is a forest whose roots are the packages nothing else depends on, with each
// JAR leaf keeping the version its declaring package requested (the global store
// installs each package's own JARs rather than resolving one shared version).

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/installer"
	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// rootLabel is the lock file's sentinel for "declared by the root project"
// (lockfile.LockedPackage.RequiredBy, and installer's rootPkgLabel).
const rootLabel = "<root>"

// Tree glyphs. Non-ASCII output is already established in this CLI (the ✓ of
// the install progress lines, the ─ divider of `outdated`), so there is no
// ASCII fallback mode.
const (
	glyphTee      = "├─ "
	glyphElbow    = "└─ "
	glyphPipe     = "│  "
	glyphBlank    = "   "
	repeatMarker  = " (*)"
	repeatLegend  = "(*) subtree already shown above"
	noLockNote    = "(no fglpkg.lock — run 'fglpkg install' to see the dependency tree)"
	emptyInstall  = "No packages installed."
	flatHeader    = "Installed packages:"
	flatJarHeader = "Installed JARs:"
)

// ─── flags ────────────────────────────────────────────────────────────────────

// listFlags holds the parsed arguments of `fglpkg list`.
type listFlags struct {
	local  bool
	global bool
	// flat selects the pre-tree output: one line per installed package, plus a
	// JAR section. It is also the automatic fallback when no lock file is
	// available to supply parentage.
	flat bool
	// depth caps how deep the tree recurses; 0 means unlimited.
	depth int
}

// parseListFlags parses `fglpkg list`'s arguments.
//
// list gets its own parser rather than the shared parseFlags for the same
// reason env does (see parseEnvFlags): --depth takes a VALUE, which parseFlags
// cannot express. Unknown arguments are rejected, as in parseAuditFlags — note
// this makes `fglpkg list somejunk` and `fglpkg list --force` errors where they
// were previously accepted and silently ignored.
func parseListFlags(args []string) (listFlags, error) {
	var f listFlags
	setDepth := func(v string) error {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return fmt.Errorf("invalid --depth %q (want a non-negative integer; 0 means unlimited)", v)
		}
		f.depth = n
		return nil
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--local", a == "-l":
			f.local = true
		case a == "--global", a == "-g":
			f.global = true
		case a == "--flat":
			f.flat = true
		case a == "--depth":
			if i+1 >= len(args) {
				return f, fmt.Errorf("--depth requires a value (a non-negative integer; 0 means unlimited)")
			}
			i++
			if err := setDepth(args[i]); err != nil {
				return f, err
			}
		case strings.HasPrefix(a, "--depth="):
			if err := setDepth(strings.TrimPrefix(a, "--depth=")); err != nil {
				return f, err
			}
		default:
			return f, fmt.Errorf("unknown argument %q", a)
		}
	}
	if f.local && f.global {
		return f, fmt.Errorf("--local and --global are mutually exclusive")
	}
	return f, nil
}

// ─── command ──────────────────────────────────────────────────────────────────

func cmdList(args []string) error {
	flags, err := parseListFlags(args)
	if err != nil {
		return &ExitError{Code: 2, Err: err}
	}
	home, _, err := resolveHome(flags.local, flags.global)
	if err != nil {
		return err
	}
	inst := newInstaller(home, nil)

	// The global store has no lock file — that lives beside a project's
	// fglpkg.json — so its dependency tree cannot be read from a lock. It is
	// instead reconstructed from the bundled manifest of every installed
	// package (listGlobalForest). --flat still forces the plain listing.
	if flags.global {
		if flags.flat {
			return listFlat(os.Stdout, inst, false)
		}
		return listGlobalForest(os.Stdout, inst, home, flags.depth)
	}

	// The lock lives beside fglpkg.json in the project directory, not inside
	// .fglpkg/, so the tree is only available once a local project has one.
	projectDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("cannot determine working directory: %w", err)
	}
	hasLock := lockfile.Exists(projectDir)

	if flags.flat || !hasLock {
		// Only call out the missing lock when a tree was what the user asked
		// for and could not be given: under --flat, flat output is the right
		// answer, not a fallback.
		return listFlat(os.Stdout, inst, !flags.flat)
	}

	lf, err := lockfile.Load(projectDir)
	if err != nil {
		return fmt.Errorf("failed to load %s: %w", lockfile.Filename, err)
	}
	return listTree(os.Stdout, inst, lf, projectDir, flags.depth)
}

// listFlat prints one line per installed package followed by one line per
// installed JAR — the pre-tree output, kept for --flat and used automatically
// wherever no lock file can supply parentage. noteMissingLock adds the hint
// pointing at the tree.
func listFlat(w io.Writer, inst *installer.Installer, noteMissingLock bool) error {
	pkgs, err := inst.List()
	if err != nil {
		return err
	}
	jars, err := inst.ListJars()
	if err != nil {
		return err
	}
	if len(pkgs) == 0 && len(jars) == 0 {
		fmt.Fprintln(w, emptyInstall)
		return nil
	}
	if len(pkgs) > 0 {
		fmt.Fprintln(w, flatHeader)
		for _, p := range pkgs {
			fmt.Fprintf(w, "  %-30s %s\n", p.Name, p.Version)
		}
	}
	if len(jars) > 0 {
		if len(pkgs) > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w, flatJarHeader)
		for _, j := range jars {
			fmt.Fprintf(w, "  %s\n", j)
		}
	}
	if noteMissingLock {
		fmt.Fprintln(w)
		fmt.Fprintln(w, noLockNote)
	}
	return nil
}

// listTree prints the dependency tree recorded by the lock file. The installer
// is consulted only for JAR parentage, which the lock does not record.
func listTree(w io.Writer, inst *installer.Installer, lf *lockfile.LockFile, projectDir string, maxDepth int) error {
	if len(lf.Packages) == 0 && len(lf.Webcomponents) == 0 && len(lf.JARs) == 0 {
		fmt.Fprintln(w, emptyInstall)
		return nil
	}

	// A missing or unreadable root manifest costs only the root's own JAR
	// attributions, which then fall back to hanging off the root anyway — so it
	// is not worth failing the command over.
	root, err := manifest.Load(projectDir)
	if err != nil {
		root = &manifest.Manifest{}
	}
	names := make([]string, 0, len(lf.Packages))
	for _, p := range lf.Packages {
		names = append(names, p.Name)
	}

	nodes := buildListTree(lf, inst.JarDeclarers(root, names), maxDepth)
	writeTree(w, nodes, treeRootLabel(lf))
	return nil
}

// ─── global forest ──────────────────────────────────────────────────────────

// listGlobalForest reconstructs and prints the global store's dependency forest.
// The global store has no lock file (that lives beside a project's fglpkg.json),
// so parentage is recovered from the bundled fglpkg.json of every installed
// package. Only BDL packages and the JARs they declare appear: a globally-
// installed webcomponent extracts into a shared tree with no per-package
// manifest to attribute, so it is listed by neither the forest nor --flat (both
// see only packages/ and jars/). With no packages installed, this falls back to
// the flat listing so any stray JARs are still reported.
func listGlobalForest(w io.Writer, inst *installer.Installer, globalRoot string, maxDepth int) error {
	installed, err := inst.List()
	if err != nil {
		return err
	}
	if len(installed) == 0 {
		return listFlat(w, inst, false)
	}

	pkgs := make([]globalPkg, 0, len(installed))
	for _, ip := range installed {
		gp := globalPkg{name: ip.Name, version: ip.Version}
		// A package with no readable manifest still shows as a childless node —
		// we know it is installed, we just cannot recover its dependencies.
		// inst.List() already read the version from this same manifest, so there
		// is nothing to re-derive here — only the dependency lists.
		if m, err := manifest.Load(filepath.Join(inst.PackagesDir(), ip.Name)); err == nil {
			gp.fglDeps = installedFGLDeps(m)
			gp.jars = installedJARs(m)
		}
		pkgs = append(pkgs, gp)
	}

	nodes := buildGlobalForest(pkgs, maxDepth)
	writeTree(w, nodes, fmt.Sprintf("Global packages — %s", globalRoot))
	return nil
}

// scopeOptional is the tag rendered for an optionally-declared dependency,
// matching the string the lock stores and the local tree renders (production is
// the empty scope, and so untagged).
const scopeOptional = string(manifest.ScopeOptional)

// fglDep is one declared FGL-package dependency and the scope its declarer put it
// under. scope is per-EDGE: the same package can be a production dep of one
// package and an optional dep of another.
type fglDep struct {
	name  string
	scope string // "" production, "optional"
}

// jarDep is one declared JAR dependency and its declarer's scope.
type jarDep struct {
	dep   manifest.JavaDependency
	scope string // "" production, "optional"
}

// installedFGLDeps returns the de-duplicated FGL dependencies a manifest declares
// across the prod and optional scopes — the scopes a consumer installs
// transitively — each tagged with the scope it was declared under. Production is
// collected first so it wins when a name appears in both buckets. Dev deps are
// excluded: they are stripped at publish and never installed globally.
func installedFGLDeps(m *manifest.Manifest) []fglDep {
	seen := map[string]bool{}
	var out []fglDep
	add := func(fgl map[string]string, scope string) {
		names := make([]string, 0, len(fgl))
		for name := range fgl {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if !seen[name] {
				seen[name] = true
				out = append(out, fglDep{name: name, scope: scope})
			}
		}
	}
	add(m.Dependencies.FGL, "")
	add(m.OptionalDependencies.FGL, scopeOptional)
	return out
}

// installedJARs returns the JAR dependencies a manifest declares across the prod
// and optional scopes, each tagged with the scope it was declared under.
func installedJARs(m *manifest.Manifest) []jarDep {
	out := make([]jarDep, 0, len(m.Dependencies.Java)+len(m.OptionalDependencies.Java))
	for _, j := range m.Dependencies.Java {
		out = append(out, jarDep{dep: j})
	}
	for _, j := range m.OptionalDependencies.Java {
		out = append(out, jarDep{dep: j, scope: scopeOptional})
	}
	return out
}

// globalPkg is one installed package's reconstructed metadata: its version and
// the direct dependencies its bundled manifest declares. It is the pure input to
// buildGlobalForest, so the forest's shape is unit-testable without a filesystem.
type globalPkg struct {
	name    string
	version string
	fglDeps []fglDep // direct FGL-package dependencies (prod+optional), scope-tagged
	jars    []jarDep // direct JAR dependencies (prod+optional), scope-tagged
}

// buildGlobalForest reconstructs the global store's dependency forest from the
// bundled manifests of installed packages. Unlike buildListTree there is no lock
// file: package parentage comes from each manifest's FGL deps, and every JAR leaf
// keeps the version its declaring package requested. That is faithful for the
// global store, which installs each package's own declared JARs rather than
// resolving one shared version — so the same coordinate can legitimately appear
// at two versions under two different packages.
//
// Roots are the installed packages no other installed package depends on. A dep
// on a package that is not installed is ignored (the forest shows what is on
// disk, not what is merely declared). Repeats and cycles collapse to a (*) leaf
// exactly as in the single-root tree; maxDepth caps recursion (0 = unlimited).
//
// Every installed package is guaranteed to appear exactly once: a package caught
// in a dependency cycle with no external entry point (a<->b, or a store that is
// wholly one cycle) is reachable from no root, so after the roots are walked any
// still-unvisited package is promoted to a root of its own. Without that pass
// such packages would silently vanish from the listing and the counts.
func buildGlobalForest(pkgs []globalPkg, maxDepth int) []listNode {
	byName := make(map[string]globalPkg, len(pkgs))
	names := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		if _, dup := byName[p.name]; dup {
			continue // a name is installed once; ignore an accidental duplicate
		}
		byName[p.name] = p
		names = append(names, p.name)
	}
	sort.Strings(names) // deterministic root and leftover ordering

	// requiredByOther marks any installed package named as an FGL dep of another
	// installed package. What remains are the forest's roots.
	requiredByOther := map[string]bool{}
	for _, name := range names {
		for _, dep := range byName[name].fglDeps {
			if _, ok := byName[dep.name]; ok && dep.name != name {
				requiredByOther[dep.name] = true
			}
		}
	}
	var roots []string
	for _, name := range names {
		if !requiredByOther[name] {
			roots = append(roots, name) // names is sorted, so roots is too
		}
	}

	// reachable is computed over the FULL dependency graph, independent of the
	// depth-limited display walk below: a package hidden only because an ancestor
	// was truncated by maxDepth is still reachable and must NOT be promoted to a
	// root. Only a package no root can reach at all — an isolated cycle — is a
	// leftover root, so nothing installed silently vanishes.
	reachable := map[string]bool{}
	var mark func(name string)
	mark = func(name string) {
		if reachable[name] {
			return
		}
		reachable[name] = true
		for _, dep := range byName[name].fglDeps {
			if _, ok := byName[dep.name]; ok {
				mark(dep.name)
			}
		}
	}
	for _, r := range roots {
		mark(r)
	}
	var extraRoots []string
	for _, name := range names {
		if !reachable[name] {
			extraRoots = append(extraRoots, name) // sorted (names is)
		}
	}

	// seen keys on identity (kind|label|version), not name, so two versions of a
	// coordinate both expand while a genuine repeat collapses — and a cycle is
	// bounded because a package is already seen by the time we return to it.
	seen := map[string]bool{}
	pkgKey := func(p globalPkg) string { return fmt.Sprintf("%d|%s@%s", kindPkg, p.name, p.version) }
	jarKey := func(j manifest.JavaDependency) string { return fmt.Sprintf("%d|%s@%s", kindJar, j.Key(), j.Version) }

	var expand func(name string, depth int) listNode
	expand = func(name string, depth int) listNode {
		p := byName[name]
		n := listNode{label: p.name, version: p.version, kind: kindPkg}
		if seen[pkgKey(p)] {
			n.repeat = true
			return n
		}
		seen[pkgKey(p)] = true
		if maxDepth != 0 && depth >= maxDepth {
			return n
		}
		// FGL-package children first (alphabetical), then JAR leaves
		// (alphabetical by coordinate, then version) — the package-before-JAR
		// ordering buildListTree applies at every level. Scope is a property of
		// the EDGE, so it is stamped on the child node here (after expansion),
		// not carried inside the package: the same package can be optional under
		// one parent and production under another.
		kids := append([]fglDep(nil), p.fglDeps...)
		sort.SliceStable(kids, func(a, b int) bool { return kids[a].name < kids[b].name })
		for _, dep := range kids {
			if _, ok := byName[dep.name]; !ok {
				continue // declared but not installed → not shown
			}
			child := expand(dep.name, depth+1)
			child.scope = dep.scope
			n.children = append(n.children, child)
		}
		js := append([]jarDep(nil), p.jars...)
		sort.SliceStable(js, func(a, b int) bool {
			if js[a].dep.Key() != js[b].dep.Key() {
				return js[a].dep.Key() < js[b].dep.Key()
			}
			return js[a].dep.Version < js[b].dep.Version
		})
		for _, j := range js {
			jn := listNode{label: j.dep.Key(), version: j.dep.Version, kind: kindJar, scope: j.scope}
			if seen[jarKey(j.dep)] {
				jn.repeat = true
			} else {
				seen[jarKey(j.dep)] = true
			}
			n.children = append(n.children, jn)
		}
		return n
	}

	out := make([]listNode, 0, len(names))
	// Real roots first, then the isolated-cycle leftovers. Skip any leftover an
	// earlier walk already displayed (expanding one cycle member pulls in the
	// rest), so a cycle surfaces once as a subtree rather than as redundant
	// top-level (*) repeats.
	for _, r := range roots {
		out = append(out, expand(r, 1))
	}
	for _, r := range extraRoots {
		if seen[pkgKey(byName[r])] {
			continue
		}
		out = append(out, expand(r, 1))
	}
	return out
}

// treeRootLabel renders the project heading, e.g. "myproject@1.0.0". A lock
// written for an unnamed project still needs a root line for the tree to hang
// off, so fall back to the sentinel.
func treeRootLabel(lf *lockfile.LockFile) string {
	name, version := lf.RootManifest.Name, lf.RootManifest.Version
	if name == "" {
		return rootLabel
	}
	if version == "" {
		return name
	}
	return name + "@" + version
}

// ─── tree model ───────────────────────────────────────────────────────────────

type nodeKind int

const (
	kindPkg nodeKind = iota
	kindWebcomponent
	kindJar
)

// listNode is one rendered row of the tree.
type listNode struct {
	// label is a package name ("myutils") or a JAR's "groupId:artifactId".
	label   string
	version string
	kind    nodeKind
	// scope is the lock file's scope for this entry: "dev", "optional", or ""
	// for production.
	scope string
	// repeat marks a node whose subtree was already expanded elsewhere in the
	// tree. It renders with the (*) marker and is never recursed into, which is
	// also what makes a dependency cycle terminate.
	repeat   bool
	children []listNode
}

// buildListTree assembles the root project's children from a loaded lock file
// plus a JAR-key -> declaring-package map (see Installer.JarDeclarers).
// maxDepth caps recursion depth, 0 meaning unlimited. It is pure: every input
// is a value, so the whole tree shape is unit-testable without a filesystem.
func buildListTree(lf *lockfile.LockFile, jarParents map[string][]string, maxDepth int) []listNode {
	// entries indexes every node the tree can contain, by its parent key.
	type entry struct {
		label   string
		version string
		kind    nodeKind
		scope   string
	}
	byParent := map[string][]entry{}
	// known records which parent names actually exist as packages, so an
	// unknown parent can be collapsed onto the root instead of being dropped.
	known := map[string]bool{}
	for _, p := range lf.Packages {
		known[p.Name] = true
	}
	for _, wc := range lf.Webcomponents {
		known[wc.Name] = true
	}

	// attach applies the same three rules as internal/sbom.buildDependencyEdges
	// — keep the two in step: no requiredBy at all means a stray entry, which
	// we assume is direct; the "<root>" sentinel means direct; and an unknown
	// parent collapses onto the root rather than vanishing from the output.
	attach := func(requiredBy []string, e entry) {
		if len(requiredBy) == 0 {
			byParent[rootLabel] = append(byParent[rootLabel], e)
			return
		}
		for _, parent := range requiredBy {
			if parent == rootLabel || !known[parent] {
				parent = rootLabel
			}
			byParent[parent] = append(byParent[parent], e)
		}
	}

	for _, p := range lf.Packages {
		attach(p.RequiredBy, entry{label: p.Name, version: p.Version, kind: kindPkg, scope: p.Scope})
	}
	for _, wc := range lf.Webcomponents {
		attach(wc.RequiredBy, entry{label: wc.Name, version: wc.Version, kind: kindWebcomponent, scope: wc.Scope})
	}
	for _, j := range lf.JARs {
		e := entry{label: j.Key, version: j.Version, kind: kindJar, scope: j.Scope}
		parents := jarParents[j.Key]
		if len(parents) == 0 {
			// No manifest on disk declares it — the root is the only honest
			// place left to put it.
			byParent[rootLabel] = append(byParent[rootLabel], e)
			continue
		}
		for _, parent := range parents {
			if parent == rootLabel || !known[parent] {
				parent = rootLabel
			}
			byParent[parent] = append(byParent[parent], e)
		}
	}

	// Genero packages and webcomponents before JARs at every level, then
	// alphabetical within each group. This is the requested ordering, and it is
	// applied per node rather than only at the top.
	for parent := range byParent {
		kids := byParent[parent]
		sort.SliceStable(kids, func(a, b int) bool {
			ja, jb := kids[a].kind == kindJar, kids[b].kind == kindJar
			if ja != jb {
				return jb
			}
			if kids[a].label != kids[b].label {
				return kids[a].label < kids[b].label
			}
			return kids[a].version < kids[b].version
		})
	}

	// seen is keyed on the identity of a node, not just its name, so two
	// versions of the same package both expand while a genuine repeat does not.
	seen := map[string]bool{}
	key := func(e entry) string { return fmt.Sprintf("%d|%s@%s", e.kind, e.label, e.version) }

	var expand func(parent string, depth int) []listNode
	expand = func(parent string, depth int) []listNode {
		kids := byParent[parent]
		if len(kids) == 0 {
			return nil
		}
		out := make([]listNode, 0, len(kids))
		for _, e := range kids {
			n := listNode{label: e.label, version: e.version, kind: e.kind, scope: e.scope}
			// Duplicate entries collapse to a (*) leaf. Deduping on first
			// *visit* rather than after the fact is what bounds a cycle: a
			// package that (transitively) requires itself is already seen by
			// the time we get back to it.
			if seen[key(e)] {
				n.repeat = true
				out = append(out, n)
				continue
			}
			seen[key(e)] = true
			// JARs never have children (no transitive POM resolution), so the
			// depth cap only ever truncates the Genero graph.
			if maxDepth == 0 || depth < maxDepth {
				n.children = expand(e.label, depth+1)
			}
			out = append(out, n)
		}
		return out
	}
	return expand(rootLabel, 1)
}

// ─── rendering ────────────────────────────────────────────────────────────────

// writeTree renders the tree under a root heading, followed by a count summary
// and — only when something was collapsed — the (*) legend. It writes to an
// io.Writer so tests can assert the exact output without capturing stdout
// (the pattern writeAuditTable uses).
func writeTree(w io.Writer, nodes []listNode, rootHeading string) {
	fmt.Fprintf(w, "%s\n\n", rootHeading)
	writeNodes(w, nodes, "")

	var pkgs, wcs, jars int
	repeated := false
	var walk func([]listNode)
	walk = func(ns []listNode) {
		for _, n := range ns {
			if n.repeat {
				repeated = true
				continue // counted at its first, expanded occurrence
			}
			switch n.kind {
			case kindWebcomponent:
				wcs++
			case kindJar:
				jars++
			default:
				pkgs++
			}
			walk(n.children)
		}
	}
	walk(nodes)

	parts := []string{fmt.Sprintf("%d package%s", pkgs, pluralS(pkgs))}
	if wcs > 0 {
		parts = append(parts, fmt.Sprintf("%d webcomponent%s", wcs, pluralS(wcs)))
	}
	parts = append(parts, fmt.Sprintf("%d JAR%s", jars, pluralS(jars)))
	fmt.Fprintf(w, "\n%s.\n", strings.Join(parts, ", "))
	if repeated {
		fmt.Fprintln(w, repeatLegend)
	}
}

// writeNodes renders one sibling level, threading the prefix that draws the
// ancestors' continuation lines.
func writeNodes(w io.Writer, nodes []listNode, prefix string) {
	for i, n := range nodes {
		last := i == len(nodes)-1
		glyph, cont := glyphTee, glyphPipe
		if last {
			glyph, cont = glyphElbow, glyphBlank
		}
		fmt.Fprintf(w, "%s%s%s\n", prefix, glyph, nodeText(n))
		writeNodes(w, n.children, prefix+cont)
	}
}

// nodeText renders a single node: "myutils@1.4.2 (dev)" for a package,
// "com.google.code.gson:gson  2.10.1" for a JAR (two spaces, matching the
// coordinate/version layout of `fglpkg audit`).
func nodeText(n listNode) string {
	var b strings.Builder
	if n.kind == kindJar {
		b.WriteString(n.label)
		if n.version != "" {
			b.WriteString("  ")
			b.WriteString(n.version)
		}
	} else {
		b.WriteString(n.label)
		if n.version != "" {
			b.WriteString("@")
			b.WriteString(n.version)
		}
	}
	if n.kind == kindWebcomponent {
		b.WriteString(" (webcomponent)")
	}
	if n.scope != "" {
		fmt.Fprintf(&b, " (%s)", n.scope)
	}
	if n.repeat {
		b.WriteString(repeatMarker)
	}
	return b.String()
}
