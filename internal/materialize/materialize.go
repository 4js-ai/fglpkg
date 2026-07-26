// Package materialize builds a PACKAGE-correct "merged root" for a scope's
// installed packages, so a single FGLLDPATH entry resolves every namespaced
// library module regardless of which package shipped it.
//
// Each installed package lives in its own store dir
// (.fglpkg/packages/<name>/) — the source of truth. Rebuild links (or copies)
// every library module a package declares into a shared, derived tree
// (.fglpkg/merged/) laid out by PACKAGE namespace. The merged tree is a cache:
// it can be deleted and rebuilt at any time from the stores.
//
// A module's merged location is its namespace path (dots→slashes), e.g.
// `PACKAGE com.fourjs.db` module DbConnection → merged/com/fourjs/db/DbConnection.42m,
// so `IMPORT FGL com.fourjs.db.DbConnection` resolves by path. Out-of-namespace
// modules (MAIN programs / tests / examples) are never merged; they stay in the
// store and run via the store directly.
//
// One package per namespace is enforced (strict): two packages declaring the
// same namespace in one scope is a hard error. See
// specs/package-layout-materialized-root.md (Decisions §1, §3).
package materialize

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/genpkg"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// Scope describes one installation scope's directories.
type Scope struct {
	// PackagesDir holds the per-package extracted stores
	// (.fglpkg/packages) — the source of truth.
	PackagesDir string
	// MergedDir is the derived, rebuildable merged FGLLDPATH root
	// (.fglpkg/merged).
	MergedDir string
}

// Result reports what a Rebuild produced.
type Result struct {
	// Owned maps package name -> the merged-root-relative .42m paths it
	// materialized (forward-slash, sorted). Recorded into each
	// LockedPackage.Materialized so removal/rebuild is O(manifest).
	Owned map[string][]string
	// Namespaces maps package name -> the PACKAGE namespace(s) it contributed
	// (sorted). Includes namespaces inferred from the tree for legacy packages.
	Namespaces map[string][]string
	// Inferred lists package names whose namespaces were inferred from the
	// extracted tree because their manifest declared no generoPackages, sorted.
	// Callers should log these (no silent guessing).
	Inferred []string
}

// NamespaceClashError is returned when two distinct packages in one scope
// declare the same PACKAGE namespace. It mirrors the registry's collision
// guard: refuse to guess, name both offenders.
type NamespaceClashError struct {
	Namespace string
	PackageA  string
	PackageB  string
}

func (e *NamespaceClashError) Error() string {
	return fmt.Sprintf(
		"packages %q and %q both declare the Genero PACKAGE namespace %q in this scope.\n"+
			"  Refusing to merge: a namespace can be owned by only one package.\n"+
			"  Remove or rename one of the two so the namespace is unique.",
		e.PackageA, e.PackageB, e.Namespace)
}

// fileMapping is one .42m to place: its absolute source path in the store and
// its merged-root-relative destination (forward-slash).
type fileMapping struct {
	srcAbs    string
	mergedRel string
}

// packagePlan is the materialization plan for a single package.
type packagePlan struct {
	name       string
	namespaces []string
	files      []fileMapping
	inferred   bool
}

// Rebuild (re)materializes scope.MergedDir from every package under
// scope.PackagesDir. It first plans every package (deterministic, sorted) and
// detects namespace clashes; only then does it rebuild the merged root from
// scratch, so a clash leaves the existing merged root untouched.
//
// The merged root is a derived cache, so Rebuild clears and re-links it wholesale
// — this is the simplest guarantee that no stale entry from a since-removed
// package survives, and it never leaves empty namespace directories behind.
func Rebuild(scope Scope) (*Result, error) {
	empty := &Result{Owned: map[string][]string{}, Namespaces: map[string][]string{}}

	entries, err := os.ReadDir(scope.PackagesDir)
	if err != nil {
		if os.IsNotExist(err) {
			// No stores for this scope — ensure no stale merged root lingers.
			if rmErr := os.RemoveAll(scope.MergedDir); rmErr != nil {
				return nil, fmt.Errorf("cannot clear merged root %s: %w", scope.MergedDir, rmErr)
			}
			return empty, nil
		}
		return nil, fmt.Errorf("cannot read packages dir %s: %w", scope.PackagesDir, err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	// Plan first; detect clashes before mutating the filesystem.
	owner := map[string]string{} // namespace -> owning package
	plans := make([]packagePlan, 0, len(names))
	for _, name := range names {
		p, err := planPackage(filepath.Join(scope.PackagesDir, name), name)
		if err != nil {
			return nil, err
		}
		if p == nil {
			continue // not a materializable package
		}
		for _, ns := range p.namespaces {
			if prev, ok := owner[ns]; ok && prev != name {
				return nil, &NamespaceClashError{Namespace: ns, PackageA: prev, PackageB: name}
			}
			owner[ns] = name
		}
		plans = append(plans, *p)
	}

	// Rebuild from scratch.
	if err := os.RemoveAll(scope.MergedDir); err != nil {
		return nil, fmt.Errorf("cannot clear merged root %s: %w", scope.MergedDir, err)
	}

	result := &Result{Owned: map[string][]string{}, Namespaces: map[string][]string{}}
	for _, p := range plans {
		owned := make([]string, 0, len(p.files))
		for _, fm := range p.files {
			dst := filepath.Join(scope.MergedDir, filepath.FromSlash(fm.mergedRel))
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return nil, fmt.Errorf("cannot create merged dir for %s: %w", fm.mergedRel, err)
			}
			if err := linkOrCopy(fm.srcAbs, dst); err != nil {
				return nil, fmt.Errorf("cannot materialize %s: %w", fm.mergedRel, err)
			}
			owned = append(owned, fm.mergedRel)
		}
		sort.Strings(owned)
		result.Owned[p.name] = owned

		nss := append([]string(nil), p.namespaces...)
		sort.Strings(nss)
		result.Namespaces[p.name] = nss

		if p.inferred {
			result.Inferred = append(result.Inferred, p.name)
		}
	}
	sort.Strings(result.Inferred)
	return result, nil
}

// planPackage builds the materialization plan for one store dir. It returns
// nil (no error) when the dir is not a materializable package: no loadable
// manifest, or a package that contributes no namespaced library modules.
func planPackage(pkgDir, name string) (*packagePlan, error) {
	m, err := manifest.Load(pkgDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // not a package (no manifest to classify by)
		}
		return nil, fmt.Errorf("cannot load manifest for package %q: %w", name, err)
	}
	if len(m.GeneroPackages) > 0 {
		return planFromNamespaces(pkgDir, name, m.GeneroPackages)
	}
	return planFromInference(pkgDir, name, m.Programs)
}

// planFromNamespaces plans using the manifest's recorded generoPackages — the
// authoritative path. For each namespace it places the .42m files sitting
// directly in that namespace's directory (non-recursive: a deeper directory is
// a distinct, separately-recorded namespace).
func planFromNamespaces(pkgDir, name string, namespaces []string) (*packagePlan, error) {
	p := &packagePlan{name: name}
	seen := map[string]bool{}
	for _, ns := range namespaces {
		ns = strings.TrimSpace(ns)
		if ns == "" || seen[ns] {
			continue
		}
		seen[ns] = true
		p.namespaces = append(p.namespaces, ns)

		nsPath := genpkg.NamespacePath(ns) // dots -> slashes
		nsDir := filepath.Join(pkgDir, filepath.FromSlash(nsPath))
		ents, err := os.ReadDir(nsDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue // declared namespace ships no .42m directory here
			}
			return nil, fmt.Errorf("cannot read namespace dir %s: %w", nsDir, err)
		}
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".42m") {
				continue
			}
			p.files = append(p.files, fileMapping{
				srcAbs:    filepath.Join(nsDir, e.Name()),
				mergedRel: path.Join(nsPath, e.Name()),
			})
		}
	}
	if len(p.namespaces) == 0 {
		return nil, nil
	}
	return p, nil
}

// planFromInference plans a legacy package that recorded no generoPackages
// (published before Phase 1). A .42m's namespace is inferred from its directory
// path — the Genero convention that a module's package path mirrors its
// directory. Flat root .42m (legacy, no namespace) and declared programs are
// never merged.
func planFromInference(pkgDir, name string, programs []string) (*packagePlan, error) {
	programFull, programBase := programSets(programs)
	p := &packagePlan{name: name, inferred: true}
	nsSet := map[string]bool{}

	err := filepath.WalkDir(pkgDir, func(pathStr string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".42m") {
			return nil
		}
		rel, err := filepath.Rel(pkgDir, pathStr)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		dir := path.Dir(relSlash)
		if dir == "." {
			return nil // flat root .42m — legacy flat, not merged
		}
		modFull := strings.TrimSuffix(relSlash, ".42m")
		modBase := strings.TrimSuffix(d.Name(), ".42m")
		if programFull[modFull] || programBase[modBase] {
			return nil // declared program — never merged
		}
		nsSet[strings.ReplaceAll(dir, "/", ".")] = true
		p.files = append(p.files, fileMapping{srcAbs: pathStr, mergedRel: relSlash})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("cannot scan package %q for namespaces: %w", name, err)
	}
	if len(p.files) == 0 {
		return nil, nil // no namespaced library modules (pure program/flat/WC package)
	}
	for ns := range nsSet {
		p.namespaces = append(p.namespaces, ns)
	}
	return p, nil
}

// programSets returns lookup sets of a manifest's program module identifiers in
// both full-relative and basename forms (tolerating a trailing .42m), so a
// staged module can be recognised as a program however it was declared.
func programSets(programs []string) (full, base map[string]bool) {
	full = make(map[string]bool, len(programs))
	base = make(map[string]bool, len(programs))
	for _, pr := range programs {
		pr = strings.TrimSuffix(filepath.ToSlash(strings.TrimSpace(pr)), ".42m")
		if pr == "" {
			continue
		}
		full[pr] = true
		base[path.Base(pr)] = true
	}
	return full, base
}

// linkOrCopy hard-links src to dst, falling back to a byte copy when the link
// fails (cross-filesystem EXDEV, a filesystem without hard-link support, or a
// Windows configuration that disallows it). Any pre-existing dst is removed
// first so the helper is safe to call standalone.
func linkOrCopy(src, dst string) error {
	_ = os.Remove(dst)
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	return copyFile(src, dst)
}

// copyFile copies the contents of src into dst (created/truncated).
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
