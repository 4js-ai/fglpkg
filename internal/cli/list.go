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

import (
	"fmt"
	"io"
	"os"
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

	// The lock lives beside fglpkg.json in the project directory, not inside
	// .fglpkg/, and there is none for the global store — so the tree is only
	// available for a local project.
	projectDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("cannot determine working directory: %w", err)
	}
	hasLock := !flags.global && lockfile.Exists(projectDir)

	if flags.flat || !hasLock {
		// Only call out the missing lock when a tree was what the user asked
		// for and could not be given: under --flat or --global, flat output is
		// the right answer, not a fallback.
		return listFlat(os.Stdout, inst, !flags.flat && !flags.global)
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
