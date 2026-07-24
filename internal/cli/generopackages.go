package cli

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/genpkg"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// scanGeneroPackages computes the Genero PACKAGE namespace set for the staged
// package (see specs/package-layout-materialized-root.md, Decisions §1). staged
// maps archive path -> source disk path, as populated by the pack staging walk;
// programs is the manifest's program list (MAIN/test/example modules).
//
// A shipped library module's namespace is its **archive-path directory**
// (com/fourjs/poiapi/PoiApi.42m -> com.fourjs.poiapi). This is the ground truth
// for both Genero's own module resolution and the consumer's materialize step,
// which key off the directory layout — and fglcomp already guarantees a
// compiled module's directory matches its PACKAGE. It is also robust to any
// source layout, since it never needs the .4gl to sit beside the .42m (real
// projects compile lib/ or src/ into a namespace tree).
//
// A .42m is excluded from the namespace set when it is:
//   - a declared program (matched by basename), or
//   - a flat-root module (no archive directory — not namespaced), or
//   - a module whose source .4gl IS adjacent and declares no PACKAGE (a flat or
//     MAIN module merely organised into a subdirectory).
//
// Returns the sorted, deduped namespace set.
func scanGeneroPackages(staged map[string]string, programs []string) ([]string, error) {
	programBase := make(map[string]bool, len(programs))
	for _, p := range programs {
		programBase[filepath.Base(p)+".42m"] = true
	}

	set := make(map[string]struct{})
	for archivePath, srcDisk := range staged {
		ap := filepath.ToSlash(archivePath)
		if !strings.HasSuffix(ap, ".42m") {
			continue
		}
		if programBase[path.Base(ap)] {
			continue // declared program — out of namespace, never merged
		}
		dir := path.Dir(ap)
		if dir == "." || dir == "" || dir == "/" {
			continue // flat-root module — no namespace, not merged
		}
		if sourceDeclaresNoPackage(srcDisk) {
			continue // adjacent source is a flat/MAIN module in a subdir
		}
		set[strings.ReplaceAll(strings.Trim(dir, "/"), "/", ".")] = struct{}{}
	}

	if len(set) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(set))
	for ns := range set {
		out = append(out, ns)
	}
	sort.Strings(out)
	return out, nil
}

// sourceDeclaresNoPackage reports whether the module's source .4gl is present
// beside its .42m (same on-disk path, .42m -> .4gl) AND declares no PACKAGE —
// i.e. it is a flat/MAIN module and must not be treated as a namespaced library.
// When the source is absent (a common layout: sources under lib/ or src/,
// compiled into a namespace tree), it returns false so the directory-derived
// namespace stands.
func sourceDeclaresNoPackage(src42m string) bool {
	gl := strings.TrimSuffix(src42m, ".42m") + ".4gl"
	data, err := os.ReadFile(gl)
	if err != nil {
		return false // no adjacent source — trust the directory layout
	}
	_, ok := genpkg.ParsePackageDecl(data)
	return !ok
}

// recordGeneroPackages computes the PACKAGE namespace set for the staged
// package and records it on pub.GeneroPackages (the publish-safe manifest
// written into the artifact). staged/programs come from the pack staging walk.
//
// When the author declared generoPackages explicitly, that set is kept (it
// survives on pub via PublishCopy); a computed set that differs triggers a
// drift warning, and the declared set still wins. Otherwise the computed set is
// recorded (empty for a flat package, which omits the field via omitempty).
func recordGeneroPackages(pub *manifest.Manifest, staged map[string]string, programs []string) error {
	computed, err := scanGeneroPackages(staged, programs)
	if err != nil {
		return err
	}

	declared := normalizeNamespaceSet(pub.GeneroPackages)
	if len(declared) > 0 {
		pub.GeneroPackages = declared
		if len(computed) > 0 && !equalNamespaceSet(declared, computed) {
			fmt.Fprintf(os.Stderr,
				"warning: declared generoPackages %v differ from the package layout %v; using the declared set\n",
				declared, computed)
		}
		return nil
	}

	pub.GeneroPackages = computed
	return nil
}

// normalizeNamespaceSet returns a sorted, deduped copy of an author-declared
// generoPackages list, for comparison against the computed set.
func normalizeNamespaceSet(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// equalNamespaceSet reports whether two normalized namespace sets are equal.
func equalNamespaceSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
