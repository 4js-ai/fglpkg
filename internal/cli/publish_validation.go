package cli

import (
	"errors"
	"fmt"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
)

// ErrVariantPublished is the sentinel checkVariantNotPublished wraps when the
// target version+variant already exists on the registry. It lets a caller tell
// this verdict — which `--force` may overwrite in place — apart from an
// inconclusive check (a network/server failure), which must always abort even
// under `--force`. Detect it with errors.Is(err, ErrVariantPublished).
var ErrVariantPublished = errors.New("variant already published")

// checkVariantNotPublished returns nil if (m.Name, m.Version, generoMajor)
// is safe to publish against the new registry. It returns:
//
//   - nil when the package is unknown (first publish), when the version
//     exists but the specific variant does not, or when the version is
//     entirely new for an existing package.
//   - a guidance error wrapping ErrVariantPublished (pointing at `fglpkg bump`)
//     if the same version AND the same variant are already published.
//   - a wrapped network/server error (NOT ErrVariantPublished) if the check
//     itself failed — callers must treat this as "we cannot tell whether
//     re-publish would clobber" and abort, not silently allow, even under
//     `--force`.
//
// Talks to the consumer endpoint /registry/packages/<slug>; the variant
// list comes from that response. New registry only — the legacy fly.dev
// publish path was removed in the Genero Intelligence cutover.
func checkVariantNotPublished(m *manifest.Manifest, generoMajor string) error {
	variants, err := registry.VariantsFor(m.Name, m.Version)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			// Package or version not found — nothing to clobber.
			return nil
		}
		return fmt.Errorf("cannot check whether version %s is already published: %w",
			m.Version, err)
	}
	want := "genero" + generoMajor
	for _, v := range variants {
		if v == want {
			return fmt.Errorf(
				"%w: version %s of %s, Genero %s\n"+
					"bump the version before publishing again:\n"+
					"    fglpkg bump patch     # %s -> next patch\n"+
					"    fglpkg bump minor     # next minor\n"+
					"    fglpkg bump major     # next major",
				ErrVariantPublished, m.Version, m.Name, generoMajor, m.Version)
		}
	}
	return nil
}
