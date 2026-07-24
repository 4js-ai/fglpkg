package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/genpkg"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// generoPackageScan is the result of inspecting the staged library modules for
// their Genero PACKAGE namespaces (see specs/package-layout-materialized-root.md,
// Decisions §1). It is computed at pack/publish time and recorded in the shipped
// manifest's generoPackages field.
type generoPackageScan struct {
	// namespaces is the sorted, deduped set of PACKAGE namespaces declared by
	// the shipped library modules (e.g. ["com.fourjs.db"]).
	namespaces []string
	// libModules counts staged .42m modules that are not declared programs —
	// i.e. candidate library modules.
	libModules int
	// parsedSource counts those library modules whose sibling .4gl source was
	// found and parsed. When libModules > 0 but parsedSource == 0, no namespace
	// could be determined from source (a .42m-only package).
	parsedSource int
}

// scanGeneroPackages inspects the staged tree for the PACKAGE namespaces its
// library modules declare. staged maps archive path -> source disk path, as
// populated by the pack staging walk. programs is the manifest's program list
// (MAIN/test/example modules); those are out-of-namespace and never contribute.
//
// For each staged .42m that is not a declared program, it reads the sibling
// .4gl source (same on-disk path, .42m -> .4gl) and parses its PACKAGE
// declaration via genpkg. A module with no sibling source is counted as a
// library module but not parsed, so callers can fall back to an author-declared
// generoPackages for .42m-only packages.
func scanGeneroPackages(staged map[string]string, programs []string) (generoPackageScan, error) {
	programBase := make(map[string]bool, len(programs))
	for _, p := range programs {
		programBase[filepath.Base(p)+".42m"] = true
	}

	set := make(map[string]struct{})
	var scan generoPackageScan
	for archivePath, srcDisk := range staged {
		if !strings.HasSuffix(archivePath, ".42m") {
			continue
		}
		if programBase[filepath.Base(archivePath)] {
			continue // declared program — out of namespace, never merged
		}
		scan.libModules++
		// The compiled module ships as .42m; its namespace is declared in the
		// sibling .4gl source that sits next to it in the project tree.
		srcPath := strings.TrimSuffix(srcDisk, ".42m") + ".4gl"
		data, err := os.ReadFile(srcPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue // .42m-only module; handled by the caller's fallback
			}
			return generoPackageScan{}, fmt.Errorf(
				"cannot read %s to determine PACKAGE namespace: %w", srcPath, err)
		}
		scan.parsedSource++
		if ns, ok := genpkg.ParsePackageDecl(data); ok {
			set[ns] = struct{}{}
		}
	}

	if len(set) > 0 {
		scan.namespaces = make([]string, 0, len(set))
		for ns := range set {
			scan.namespaces = append(scan.namespaces, ns)
		}
		sort.Strings(scan.namespaces)
	}
	return scan, nil
}

// recordGeneroPackages computes the PACKAGE namespace set for the staged
// package and records it on pub.GeneroPackages (the publish-safe manifest
// written into the artifact). staged/programs come from the pack staging walk.
//
// Precedence:
//   - When the author declared generoPackages explicitly, that set is kept
//     (it survives on pub via PublishCopy). If source is available and the
//     computed set differs, a drift warning is emitted — the declared set wins.
//   - Otherwise the computed set is recorded. A package with library modules
//     but no parseable .4gl source (a .42m-only package) records nothing and is
//     warned that it will be treated as flat; declare generoPackages to override.
func recordGeneroPackages(pub *manifest.Manifest, staged map[string]string, programs []string) error {
	scan, err := scanGeneroPackages(staged, programs)
	if err != nil {
		return err
	}

	declared := normalizeNamespaceSet(pub.GeneroPackages)
	if len(declared) > 0 {
		pub.GeneroPackages = declared
		if scan.parsedSource > 0 && !equalNamespaceSet(declared, scan.namespaces) {
			fmt.Fprintf(os.Stderr,
				"warning: declared generoPackages %v differ from those parsed from source %v; using the declared set\n",
				declared, scan.namespaces)
		}
		return nil
	}

	pub.GeneroPackages = scan.namespaces
	if scan.libModules > 0 && scan.parsedSource == 0 {
		fmt.Fprintf(os.Stderr,
			"warning: no .4gl source found to determine PACKAGE namespaces; %s will be treated as flat "+
				"(no merged FGLLDPATH root). Declare \"generoPackages\" in %s to override.\n",
			pub.Name, manifest.Filename)
	}
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
