package installer

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/4js-mikefolcher/fglpkg/internal/checksum"
	"github.com/4js-mikefolcher/fglpkg/internal/classpath"
	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	gh "github.com/4js-mikefolcher/fglpkg/internal/github"
	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/materialize"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
	"github.com/4js-mikefolcher/fglpkg/internal/signing"
)

// InstalledPackage is a summary of an installed BDL package.
type InstalledPackage struct {
	Name    string
	Version string
}

// Installer manages package installation into the fglpkg home directory.
type Installer struct {
	home             string // e.g. ~/.fglpkg
	packagesDir      string // ~/.fglpkg/packages
	jarsDir          string // ~/.fglpkg/jars
	webcomponentsDir string // ~/.fglpkg/webcomponents
	githubToken      string // GitHub PAT for downloading from private GitHub Releases
	registryToken    string // bearer for the consumer registry when it serves zips directly
	giOrigin         string // scheme+host of the GI registry; gates where registryToken may be sent
	repoAuth         []RepoAuth
	// mavenBase, when non-empty, replaces public Maven Central as the base for
	// JAR downloads (GIS-365). The mirror is also registered in repoAuth so its
	// downloads carry credentials via matchRepoAuth. "" == Maven Central.
	mavenBase string
	// versionFetcher/infoFetcher, when non-nil, replace the default live GI
	// registry fetchers with a multi-provider routing layer (RepositorySet).
	versionFetcher resolver.VersionFetcher
	infoFetcher    resolver.InfoFetcher
	// pinDeclarer, when non-nil, receives per-dependency registry pins found in
	// resolved packages' manifests so transitive deps route to the author's
	// stated source. Set alongside the multi-provider fetchers.
	pinDeclarer resolver.PinDeclarer
	// configuredRegistries lists the logical names of the currently-configured
	// repositories (including the built-in "gi"). Used to reject a lock file
	// that references a repository since removed from the config (spec §9).
	configuredRegistries []string

	// ── Layer 1 signature verification (opt-in via WithSigning) ──
	signingEnforce  string // "require" | "warn" | "off"; "" == off
	keysHome        string // where the keys manifest is cached (usually the global ~/.fglpkg)
	registryBase    string // consumer registry base URL for fetching the keys manifest
	keysOnce        sync.Once
	keysManifest    *signing.Manifest
	keysManifestErr error
}

// RepoAuth maps a repository URL prefix to the HTTP headers that authenticate
// downloads from it. Used for secondary (Artifactory) repositories, whose auth
// scheme may be bearer/basic/apikey. Matched by longest URL prefix.
type RepoAuth struct {
	URLPrefix string
	Headers   map[string]string
}

// New creates an Installer rooted at home.
//
//   - githubToken: authenticates downloads from private GitHub Releases
//     (used by the legacy fglpkg-registry.fly.dev flow). Pass "" if not needed.
//   - registryToken: bearer for non-GitHub download URLs (the new
//     service.generointelligence.ai flow serves zips itself, possibly
//     behind auth). Pass "" for anonymous fetches.
//   - giOrigin: the GI registry's base URL (e.g. https://service.generointelligence.ai
//     or FGLPKG_REGISTRY's override). registryToken is only ever sent to a
//     download URL whose origin matches this — never to an arbitrary
//     absolute host (e.g. an R2/CDN target) — to avoid leaking the GI
//     bearer to a third party (GIS-267 / GIS-249 S1).
func New(home, githubToken, registryToken, giOrigin string) *Installer {
	return &Installer{
		home:             home,
		packagesDir:      filepath.Join(home, "packages"),
		jarsDir:          filepath.Join(home, "jars"),
		webcomponentsDir: filepath.Join(home, "webcomponents"),
		githubToken:      githubToken,
		registryToken:    registryToken,
		giOrigin:         giOrigin,
	}
}

// WithRepoAuth attaches per-repository download auth (for Artifactory
// secondary repositories) and returns the installer for chaining.
func (i *Installer) WithRepoAuth(ra []RepoAuth) *Installer {
	i.repoAuth = ra
	return i
}

// WithMavenBase sets the Maven mirror base URL used for JAR downloads (GIS-365)
// and returns the installer for chaining. An empty base keeps the default
// (public Maven Central). Register the same base in WithRepoAuth so authenticated
// mirrors receive credentials.
func (i *Installer) WithMavenBase(base string) *Installer {
	i.mavenBase = base
	return i
}

// WithFetchers replaces the default live GI registry fetchers with a
// multi-provider routing layer (e.g. a RepositorySet's Versions/Info). Pass
// nil,nil to keep the default single-registry behaviour.
func (i *Installer) WithFetchers(fv resolver.VersionFetcher, fi resolver.InfoFetcher) *Installer {
	i.versionFetcher = fv
	i.infoFetcher = fi
	return i
}

// WithPinDeclarer attaches a PinDeclarer (typically the same RepositorySet
// backing the fetchers) so declared per-dependency registry pins are honoured
// during resolution. Returns the installer for chaining.
func (i *Installer) WithPinDeclarer(pd resolver.PinDeclarer) *Installer {
	i.pinDeclarer = pd
	return i
}

// WithConfiguredRegistries records the logical names of the currently
// configured repositories so a lock file referencing a removed repository can
// be rejected before install (spec §9). Returns the installer for chaining.
func (i *Installer) WithConfiguredRegistries(names []string) *Installer {
	i.configuredRegistries = names
	return i
}

// newResolver builds the resolver, using injected multi-provider fetchers when
// configured (still honouring any workspace), else the default live resolver.
func (i *Installer) newResolver(gv genero.Version) (*resolver.Resolver, error) {
	if i.versionFetcher != nil && i.infoFetcher != nil {
		r := resolver.NewWithFetchers(gv, i.versionFetcher, i.infoFetcher)
		if i.pinDeclarer != nil {
			r = r.WithPinDeclarer(i.pinDeclarer)
		}
		if err := r.DetectWorkspace(); err != nil {
			return nil, err
		}
		return r, nil
	}
	return resolver.New()
}

// matchRepoAuth returns the auth headers for the configured repository whose
// URL prefix best (longest) matches url, and whether any configured repo
// matched at all. A match with empty headers (an "anonymous" repo) still
// reports matched=true, so the caller knows the host belongs to a configured
// repository and must NOT be sent the GI registry token (which would leak the
// GI bearer to a third-party host — see GIS-267).
//
// Matching is by parsed-URL origin (scheme+host) plus a path prefix on a "/"
// boundary — a raw string prefix would let
// "https://acme.jfrog.io.attacker.com/…" match a repo configured as
// "https://acme.jfrog.io" (GIS-249 S1).
func (i *Installer) matchRepoAuth(downloadURL string) (headers map[string]string, matched bool) {
	var best RepoAuth
	for _, ra := range i.repoAuth {
		if urlHasOriginPrefix(downloadURL, ra.URLPrefix) && len(ra.URLPrefix) > len(best.URLPrefix) {
			best = ra
			matched = true
		}
	}
	return best.Headers, matched
}

// urlHasOriginPrefix reports whether rawURL is served by the same origin
// (scheme+host, case-insensitive) as prefix, and its path starts with
// prefix's path on a "/" boundary (equal, or the next rune is "/"). This is
// stricter than strings.HasPrefix(rawURL, prefix): it rejects a host that
// merely has prefix's host as a string prefix, e.g. a repo configured as
// "https://acme.jfrog.io" must not match "https://acme.jfrog.io.attacker.com/…"
// (GIS-249 S1), nor "https://acme.jfrog.io/repo" match ".../repo-other/x".
func urlHasOriginPrefix(rawURL, prefix string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	p, err := url.Parse(prefix)
	if err != nil {
		return false
	}
	if !strings.EqualFold(u.Scheme, p.Scheme) || !strings.EqualFold(u.Host, p.Host) {
		return false
	}
	pPath := strings.TrimSuffix(p.Path, "/")
	if !strings.HasPrefix(u.Path, pPath) {
		return false
	}
	rest := u.Path[len(pPath):]
	return rest == "" || strings.HasPrefix(rest, "/")
}

// sameOrigin reports whether rawURL's scheme+host matches originURL's,
// ignoring path/query/fragment. Used to gate the GI registry bearer: it must
// only be sent to the GI registry's own origin, never to an arbitrary
// absolute download URL (e.g. an R2/CDN target) — GIS-249 S1.
func sameOrigin(rawURL, originURL string) bool {
	if originURL == "" {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	o, err := url.Parse(originURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Scheme, o.Scheme) && strings.EqualFold(u.Host, o.Host)
}

// WithSigning enables Layer 1 signature verification on install.
//
//   - enforce: "require" aborts on a bad/missing signature, "warn" logs and
//     continues, "off" (or "") disables verification entirely.
//   - keysHome: directory the root-verified keys manifest is cached in
//     (typically the global ~/.fglpkg, even for a local install).
//   - registryBase: consumer registry base URL the manifest is fetched from.
func (i *Installer) WithSigning(enforce, keysHome, registryBase string) *Installer {
	i.signingEnforce = enforce
	i.keysHome = keysHome
	i.registryBase = registryBase
	return i
}

// keysManifestFor lazily loads (once) the root-verified keys manifest.
func (i *Installer) keysManifestFor() (*signing.Manifest, error) {
	i.keysOnce.Do(func() {
		home := i.keysHome
		if home == "" {
			home = i.home
		}
		i.keysManifest, i.keysManifestErr = signing.LoadManifest(home, i.registryBase)
	})
	return i.keysManifest, i.keysManifestErr
}

// verifySignature checks info's Layer 1 registry signature, honouring the
// configured enforce mode. variant is the artifact variant that was signed
// (e.g. "genero6" / "webcomponent"); it may differ from info.Variant when the
// record was reconstructed from the lock file.
func (i *Installer) verifySignature(info *registry.PackageInfo, variant string) error {
	mode := i.signingEnforce
	if mode == "" || mode == "off" {
		return nil
	}
	if info.Signature == nil {
		return i.onSigningIssue(mode, fmt.Errorf("%s@%s: %w", info.Name, info.Version, signing.ErrUnsigned))
	}
	m, err := i.keysManifestFor()
	if err != nil {
		return i.onSigningIssue(mode, fmt.Errorf("%s@%s: cannot load keys manifest: %w", info.Name, info.Version, err))
	}
	p := signing.ArtifactFields{
		Name: info.Name, Version: info.Version, Variant: variant,
		SHA256: info.Checksum, Size: info.Size,
		UploadedAt: info.UploadedAt, Uploader: info.Uploader,
	}
	sig := signing.ArtifactSignature{KeyID: info.Signature.KeyID, Alg: info.Signature.Alg, Sig: info.Signature.Sig}
	if err := m.VerifyArtifact(p, sig); err != nil {
		return i.onSigningIssue(mode, err)
	}
	return nil
}

// onSigningIssue applies the enforce policy: "require" surfaces the error;
// "warn" logs it and continues.
func (i *Installer) onSigningIssue(mode string, err error) error {
	if mode == "require" {
		return err
	}
	printSync("  warning: signature check failed: %v\n", err)
	return nil
}

// lockSignature reconstructs a signature envelope from lock-file fields,
// returning nil when the locked package carries no signature.
func lockSignature(keyid, sig string) *registry.Signature {
	if keyid == "" && sig == "" {
		return nil
	}
	return &registry.Signature{KeyID: keyid, Alg: "ed25519", Sig: sig}
}

// The three record builders below are the single source of each install
// candidate's registry metadata. Signature verification and the download both
// read the record a builder returns, so the sha256 that was verified is by
// construction the sha256 the downloaded bytes are checked against — the bind
// that lets verification run before the fetch (GIS-580).

// lockedPackageInfo rebuilds the registry record for a locked BDL package.
func lockedPackageInfo(pkg lockfile.LockedPackage) *registry.PackageInfo {
	return &registry.PackageInfo{
		Name:        pkg.Name,
		Version:     pkg.Version,
		DownloadURL: pkg.DownloadURL,
		Checksum:    pkg.Checksum,
		Size:        pkg.Size,
		UploadedAt:  pkg.UploadedAt,
		Uploader:    pkg.Uploader,
		Signature:   lockSignature(pkg.SignatureKeyID, pkg.Signature),
	}
}

// lockedPackageVariant is the artifact variant that was signed for a locked
// BDL package: "genero<major>", or "" when the lock predates GeneroMajor —
// such an entry cannot be verified until `fglpkg update` re-locks it, since
// the signed payload names a real variant and "" will never match it.
func lockedPackageVariant(pkg lockfile.LockedPackage) string {
	if pkg.GeneroMajor == "" {
		return ""
	}
	return "genero" + pkg.GeneroMajor
}

// lockedWebcomponentInfo rebuilds the registry record for a locked web
// component.
func lockedWebcomponentInfo(wc lockfile.LockedWebcomponent) *registry.PackageInfo {
	return &registry.PackageInfo{
		Name:        wc.Name,
		Version:     wc.Version,
		DownloadURL: wc.DownloadURL,
		Checksum:    wc.Checksum,
		Variant:     "webcomponent",
		Size:        wc.Size,
		UploadedAt:  wc.UploadedAt,
		Uploader:    wc.Uploader,
		Signature:   lockSignature(wc.SignatureKeyID, wc.Signature),
	}
}

// planPackageInfo builds the registry record for a freshly resolved package.
func planPackageInfo(pkg resolver.ResolvedPackage) *registry.PackageInfo {
	return &registry.PackageInfo{
		Name:        pkg.Name,
		Version:     pkg.Version.String(),
		DownloadURL: pkg.DownloadURL,
		Checksum:    pkg.Checksum,
		Variant:     pkg.Variant,
		Size:        pkg.Size,
		UploadedAt:  pkg.UploadedAt,
		Uploader:    pkg.Uploader,
		Signature:   pkg.Signature,
	}
}

// verifyPlanSignatures checks every resolved package's Layer 1 signature under
// the configured enforce mode, before the lock file is written and before
// anything is fetched or extracted.
//
// A signature failure aborts the whole install even for an optional-scoped
// package: an optional dependency may be skipped when it cannot be fetched,
// but "I could not establish that this artifact is what the registry signed"
// is a trust failure, not an availability one.
func (i *Installer) verifyPlanSignatures(plan *resolver.Plan) error {
	for _, pkg := range plan.Packages {
		if err := i.verifySignature(planPackageInfo(pkg), pkg.Variant); err != nil {
			return fmt.Errorf("failed to install %s: %w", pkg.Name, err)
		}
	}
	return nil
}

// lockInstallSet is everything a replay would install, honouring
// opts.Production.
//
// It is deliberately the ONLY place the replay's install set is selected: the
// gate that verifies signatures and the pass that fetches and extracts both
// read it, so they cannot disagree about what is being installed. Two copies of
// this choice would let the gate check one set while installFromLock installs
// another, and under "require" the difference would go in unverified. Same
// reason the record builders above exist — the check and the action read one
// value.
func lockInstallSet(lf *lockfile.LockFile, opts Options) ([]lockfile.LockedPackage, []lockfile.LockedJAR, []lockfile.LockedWebcomponent) {
	if opts.Production {
		return lf.FilterForProduction()
	}
	return lf.ToInstallList()
}

// verifyOnDiskSignatures re-verifies the LOCK RECORDS of entries whose files are
// already present, and applies under "require" only.
//
// Presence on disk is not evidence that the record was ever accepted: those
// files may be exactly what an earlier refused run left behind, and skipping
// them is what let a retry turn exit 1 into exit 0 (GIS-580). Under "warn" they
// are left alone — the warning was emitted when they were installed, repeating
// it on every replay is noise that changes no outcome, and `fglpkg audit
// signatures` is the command for auditing what is in the store.
//
// The entries reaching this function are the ones whose extracted manifest
// names the locked version (lockfile.PackageIsInstalled), so re-verifying the
// record does say something about the files: anything else was classified as
// not installed and is about to be fetched and verified as such (GIS-586).
// Web components are the exception — they carry no manifest on disk, so their
// presence is still version-blind (GIS-579, GIS-581).
func (i *Installer) verifyOnDiskSignatures(pkgs []lockfile.LockedPackage, wcs []lockfile.LockedWebcomponent) error {
	if i.signingEnforce != signing.EnforceRequire {
		return nil
	}
	for _, pkg := range pkgs {
		if err := i.verifySignature(lockedPackageInfo(pkg), lockedPackageVariant(pkg)); err != nil {
			return fmt.Errorf("failed to install %s: %w", pkg.Name, err)
		}
	}
	for _, wc := range wcs {
		if err := i.verifySignature(lockedWebcomponentInfo(wc), "webcomponent"); err != nil {
			return fmt.Errorf("failed to install webcomponent %s: %w", wc.Name, err)
		}
	}
	return nil
}

// verifyLockIsStillTrusted re-verifies everything a clean lock names, for the
// no-op replay that installs nothing at all. Without it, "Lock file is up to
// date. Nothing to install." reports success for a store that may hold an
// artifact whose signature was refused on the run that put it there.
func (i *Installer) verifyLockIsStillTrusted(lf *lockfile.LockFile, opts Options) error {
	pkgs, _, wcs := lockInstallSet(lf, opts)
	return i.verifyOnDiskSignatures(pkgs, wcs)
}

// verifyLockInstallSet verifies every signature a lock replay will rely on. It
// runs before the prune as well as before any fetch or extraction: verification
// is read-only, so doing it first costs nothing and means a refused run leaves
// the store exactly as it found it. Pruning first would delete orphans and only
// then refuse — a store change on a run that installed nothing, which is the
// opposite of what the resolve path promises.
//
// Entries whose files are missing are about to be fetched, so they are checked
// under the configured mode. Entries already present are re-checked under
// "require" only (see verifyOnDiskSignatures). Web components are re-extracted
// on every replay, so they always count as about to be fetched.
func (i *Installer) verifyLockInstallSet(lf *lockfile.LockFile, opts Options) error {
	pkgs, _, wcs := lockInstallSet(lf, opts)
	var onDisk []lockfile.LockedPackage
	for _, pkg := range pkgs {
		if lockfile.PackageIsInstalled(i.packagesDir, pkg) {
			onDisk = append(onDisk, pkg)
			continue
		}
		if err := i.verifySignature(lockedPackageInfo(pkg), lockedPackageVariant(pkg)); err != nil {
			return fmt.Errorf("failed to install %s: %w", pkg.Name, err)
		}
	}
	for _, wc := range wcs {
		if err := i.verifySignature(lockedWebcomponentInfo(wc), "webcomponent"); err != nil {
			return fmt.Errorf("failed to install webcomponent %s: %w", wc.Name, err)
		}
	}
	return i.verifyOnDiskSignatures(onDisk, nil)
}

// Options controls optional install behaviour.
type Options struct {
	// Production skips dev-scoped packages and JARs. Optional entries are
	// still attempted.
	Production bool
	// NoManifestFallback disables the fallback half of the dependency
	// cross-check: when set, the installer still diffs each package's
	// bundled manifest against the install set and warns on divergence, but
	// it does NOT install Java coordinates the manifest declares and the
	// install set omits. Default (false) means fallback is on.
	NoManifestFallback bool

	// Prune deletes installed packages, webcomponent bundles, and JARs that
	// the current dependency graph no longer requires, so the store converges
	// on the manifest instead of accumulating orphans that keep resolving on
	// FGLLDPATH (and keep appearing in `fglpkg list`).
	//
	// It MUST be false for a global (~/.fglpkg) home — those artifacts are
	// shared across every project, so pruning against one project's graph
	// would delete another's dependencies. Only the caller knows which home
	// is in play, hence the opt-in. It is additionally suppressed under
	// Production, which resolves a deliberately narrowed graph (no dev scope)
	// and so must never be allowed to delete a developer's dev packages.
	Prune bool

	// SkipLock suppresses every project lock-file write (and the legacy-lock
	// migration) so an install materializes into the store and builds the merged
	// root but leaves projectDir untouched. Set for a global "tool" install
	// (`fglpkg install <pkg> --global` outside a project): the global store is
	// tracked by scanning, not a project lock, so the current directory must stay
	// clean (GIS-565). Honoured on the resolve path (forceResolve), which is the
	// only path that add-a-package global installs take.
	SkipLock bool
}

// InstallAll resolves or reads from the lock file, then installs every
// BDL package and Java JAR. If a valid lock file exists and matches the
// current environment, it is used directly (no network resolution needed).
// Pass forceResolve=true to bypass the lock and re-resolve from scratch
// (used by `fglpkg update`).
func (i *Installer) InstallAll(m *manifest.Manifest, projectDir string, forceResolve bool) error {
	return i.InstallAllWithOptions(m, projectDir, forceResolve, Options{})
}

// InstallAllWithOptions is InstallAll with caller-controlled options.
func (i *Installer) InstallAllWithOptions(m *manifest.Manifest, projectDir string, forceResolve bool, opts Options) error {
	if err := i.ensureDirs(); err != nil {
		return err
	}

	// GIS-289: one-shot rename of a pre-rename fglpkg.lock to fglpkg-lock.json.
	// fglpkg was internal-only when the lock file was renamed, so instead of a
	// permanent dual-read we migrate it in place the first time install/update
	// runs in a project that still carries the old name. projectDir is the same
	// directory every lock read/write below uses (the workspace root under a
	// workspace), so the rename always lands where the lock is expected.
	if !opts.SkipLock {
		if migrated, err := lockfile.Migrate(projectDir); err != nil {
			return err
		} else if migrated {
			fmt.Printf("Renamed %s to %s — commit the rename.\n", lockfile.LegacyFilename, lockfile.Filename)
		}
	}

	// Detect Genero version once — used for both lock validation and resolution.
	gv, err := genero.Detect()
	if err != nil {
		return fmt.Errorf("cannot detect Genero version: %w", err)
	}

	// Pruning is the install-side counterpart of what `remove` already does:
	// converge the store on the dependency graph. Gated on a project-local home
	// and never applied to a narrowed --production graph (see Options.Prune).
	prune := opts.Prune && !opts.Production

	// ── Try to use an existing lock file ────────────────────────────────────
	if !forceResolve && lockfile.Exists(projectDir) {
		lf, err := lockfile.Load(projectDir)
		if err != nil {
			fmt.Printf("warning: cannot read lock file: %v — re-resolving\n", err)
		} else {
			// A lock referencing a repository that is no longer configured is a
			// hard error — never install from a source the user can't see (§9).
			if err := lf.CheckRegistries(i.configuredRegistries); err != nil {
				return err
			}
			vr := lf.Validate(m, gv.String(), i.packagesDir, i.webcomponentsDir, i.jarsDir, i.installedWebcomponents())
			if vr.NeedsResolve() {
				fmt.Printf("Lock file is stale (%s) — re-resolving...\n", vr.StaleReason())
			} else {
				if vr.GeneroMismatch != nil {
					fmt.Printf("warning: %v\n", vr.GeneroMismatch)
				}
				if vr.IsClean() {
					// Everything the lock names is on disk, so this run fetches
					// nothing — which is precisely the state an earlier refused
					// install leaves behind. Re-verify before reporting success,
					// or "Nothing to install" launders a rejected artifact into
					// an accepted one on the retry (GIS-580).
					if err := i.verifyLockIsStillTrusted(lf, opts); err != nil {
						return err
					}
					// Everything the lock names is on disk — but something the
					// lock does NOT name still can be (a branch switch to an
					// older lock, an interrupted run), so converge first.
					var pruned []string
					if prune {
						pruned, err = i.pruneToLock(lf)
						if err != nil {
							return err
						}
						reportPruned(pruned)
					}
					// Build the merged root when it is MISSING — the migration
					// case (fglpkg upgraded on an already-installed project, or
					// .fglpkg/merged deleted) — or when a prune just removed
					// modules that are still linked into it. Otherwise
					// install/remove have kept it current, so skip the
					// redundant rebuild (and its inference re-scan).
					if !i.mergedRootExists() || len(pruned) > 0 {
						if err := i.syncMergedRoot(projectDir, !opts.Production); err != nil {
							return err
						}
					}
					// Heal a missing/stale classpath anchor even on a no-op
					// install — the migration case (jars installed by a
					// pre-anchor fglpkg, or the anchor deleted by hand). Sync
					// byte-compares and skips the write when already current.
					if err := classpath.Sync(i.jarsDir); err != nil {
						return err
					}
					fmt.Printf("Lock file is up to date (Genero %s). Nothing to install.\n", gv)
					return nil
				}
				// Lock is valid but some packages are missing on disk — install
				// them, dropping anything the lock no longer names first.
				fmt.Printf("Installing from lock file (Genero %s)...\n", gv)
				// Before the prune, so a refused run changes nothing at all
				// (GIS-580).
				if err := i.verifyLockInstallSet(lf, opts); err != nil {
					return err
				}
				if prune {
					pruned, err := i.pruneToLock(lf)
					if err != nil {
						return err
					}
					reportPruned(pruned)
				}
				return i.installFromLock(lf, m, opts, projectDir)
			}
		}
	}

	// ── Resolve the full dependency graph ───────────────────────────────────
	fmt.Printf("Resolving dependency graph (Genero %s)...\n", gv)
	r, err := i.newResolver(gv)
	if err != nil {
		return fmt.Errorf("cannot initialise resolver: %w", err)
	}
	resolveOpts := resolver.DefaultResolveOptions()
	if opts.Production {
		resolveOpts.IncludeDev = false
	}
	plan, err := r.ResolveWithOptions(m, resolveOpts)
	if err != nil {
		return fmt.Errorf("dependency resolution failed:\n%w", err)
	}
	fmt.Printf("Resolved %d package(s), %d JAR(s)\n\n", len(plan.Packages), len(plan.JARs))
	warnDeprecations(plan, os.Stderr)

	// Signatures are checked here — before the lock file is written, before the
	// prune, and before anything is fetched — so that under "require" a failure
	// leaves no trace for a later run to accept: no files in .fglpkg/ and no
	// lock entry claiming the package is installed. Verifying after extraction
	// (as this did until GIS-580) failed the first run but left the artifact on
	// disk and in the lock, so a re-run took the already-installed fast path and
	// exited 0 — a CI retry step defeated "require" entirely.
	if err := i.verifyPlanSignatures(plan); err != nil {
		return err
	}

	// Write the lock file before installing so it's always present even if
	// installation is interrupted partway through.
	// When --production is in effect we do NOT overwrite the lock file,
	// because it would drop dev entries that should remain recorded.
	if !opts.Production && !opts.SkipLock {
		lf := lockfile.FromPlan(plan, m, i.mavenBase)
		if err := lf.Save(projectDir); err != nil {
			// Non-fatal: warn but continue with the install.
			fmt.Printf("warning: could not write lock file: %v\n", err)
		} else {
			fmt.Printf("Wrote %s\n\n", lockfile.Filename)
		}
	}

	// Prune before installing, not after: the plan's keep-set already accounts
	// for everything about to be installed, so anything the sweep removes is
	// genuinely orphaned — and materialization downstream then sees a store
	// with no stale members in it.
	if prune {
		pruned, err := i.pruneToPlan(plan)
		if err != nil {
			return err
		}
		reportPruned(pruned)
	}

	return i.installFromPlan(plan, m, opts, projectDir)
}

// reportPruned prints one line per removed artifact, matching the summary
// `fglpkg remove` prints for the same operation.
func reportPruned(pruned []string) {
	for _, p := range pruned {
		fmt.Printf("  pruned %s\n", p)
	}
}

// lockError carries an actionable, user-facing message while still unwrapping to
// the failure that caused it, so errors.Is/As keep working on the download
// sentinels after translation. fmt.Errorf's %w cannot be used for this: it splices
// the wrapped error's text into the message, and removing that raw HTTP wording is
// the whole point of GIS-283 — so the message and the cause are carried separately.
type lockError struct {
	msg   string
	cause error
}

func (e *lockError) Error() string { return e.msg }
func (e *lockError) Unwrap() error { return e.cause }

// lockInstallError turns a per-artifact failure during a lock-based install into
// an actionable message (GIS-283). A pin the registry no longer serves
// (ErrArtifactGone) names name@version and the ways out; a transient failure says
// so and suggests a retry; any other error keeps its detail with name@version
// context. `fglpkg remove <name>` and `fglpkg update` apply equally to BDL and
// webcomponent packages, so the remedies need no kind distinction — kind is used
// only to keep the fallback message specific ("" for a package).
// Every branch preserves err in the chain, so a caller can still classify.
func lockInstallError(kind, name, version string, err error) error {
	switch {
	case errors.Is(err, ErrArtifactGone):
		// A "not found" is evidence the registry will not serve this pin, NOT
		// proof the version was deleted: a private registry commonly answers 404
		// rather than 403 for an artifact the caller may not see (so as not to
		// leak its existence), and a mistyped FGLPKG_REGISTRY or Artifactory
		// repoKey 404s everything. Stating deletion as fact would send a merely
		// unauthenticated user to `fglpkg remove` — dropping a dependency they
		// still need. So name the observation, list the causes, and offer login
		// alongside update/remove.
		return &lockError{msg: goneMessage(name, version), cause: err}
	case errors.Is(err, ErrDownloadTransient):
		// The cause already ends in the sentinel's "temporary download failure",
		// so the advice line adds only the action — restating the transience here
		// said it twice. ErrDownloadTransient spans a transport error (the user's
		// connection) AND a 429/5xx (server-side throttling or a busy registry),
		// so the advice must not blame the connection outright: lead with a retry
		// and offer the connection only as the fallback cause. (Wording the
		// sentinel itself for a caller-facing read is tracked separately.)
		return &lockError{
			msg: fmt.Sprintf("could not install %s@%s: %v\n"+
				"  this is usually transient — wait a moment and retry (check your connection if it persists)",
				name, version, err),
			cause: err,
		}
	default:
		return fmt.Errorf("failed to install %s%s@%s: %w", kindPrefix(kind), name, version, err)
	}
}

// kindPrefix renders an artifact-kind label for a message, e.g. "webcomponent ".
// Empty for an ordinary BDL package, which needs no qualifier.
func kindPrefix(kind string) string {
	if kind == "" {
		return ""
	}
	return kind + " "
}

// goneMessage renders the ErrArtifactGone advice: what was observed, the causes
// that produce it, then the remedies. The command column is padded to the widest
// entry rather than to a fixed width, so the two-column layout survives a package
// name of any length (a hard-coded gap only lines up for one name length).
func goneMessage(name, version string) string {
	remedies := [][2]string{
		{"fglpkg login", "authenticate, if the package is private"},
		{"fglpkg update", "re-resolve to a still-available version"},
		{"fglpkg remove " + name, "drop this dependency from the project"},
	}
	width := 0
	for _, r := range remedies {
		if n := len(r[0]); n > width {
			width = n
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s@%s is no longer available on the registry, but %s still pins it.\n",
		name, version, lockfile.Filename)
	b.WriteString("  The registry answered \"not found\". Either the version was deleted or\n")
	b.WriteString("  withdrawn, or you do not have access to it (a private package needs a login).\n")
	b.WriteString("  Fix it with one of:\n")
	for _, r := range remedies {
		fmt.Fprintf(&b, "    %-*s   %s\n", width, r[0], r[1])
	}
	return strings.TrimRight(b.String(), "\n")
}

// installFromLock installs every entry in the lock file using its pinned
// URLs and checksums, bypassing the resolver entirely. When opts.Production
// is true, dev-scoped entries are skipped.
func (i *Installer) installFromLock(lf *lockfile.LockFile, root *manifest.Manifest, opts Options, projectDir string) error {
	// Same selection the gate verified (verifyLockInstallSet) — see
	// lockInstallSet on why this must not be a second copy.
	pkgs, jars, wcs := lockInstallSet(lf, opts)

	// Filter packages that are already on disk so the parallel phase only does
	// real work. "On disk" is the locked VERSION being extracted, the same test
	// the gate above used and the same one that decided this replay was not a
	// no-op — see lockfile.PackageIsInstalled.
	var pkgsToInstall, pkgsOnDisk []lockfile.LockedPackage
	for _, pkg := range pkgs {
		if lockfile.PackageIsInstalled(i.packagesDir, pkg) {
			pkgsOnDisk = append(pkgsOnDisk, pkg)
			continue
		}
		pkgsToInstall = append(pkgsToInstall, pkg)
	}

	// Signatures were verified by verifyLockInstallSet before the prune, so
	// this function only fetches and extracts (GIS-580). It must stay that
	// way: its caller is the gate.

	// Already-installed lines are printed synchronously up front for a stable
	// "already there" prelude.
	for _, pkg := range pkgsOnDisk {
		fmt.Printf("  ✓ %s@%s (already installed)\n", pkg.Name, pkg.Version)
	}

	cap := installConcurrency()

	if err := runParallel(pkgsToInstall, cap, func(pkg lockfile.LockedPackage) error {
		// Signature already verified above; this phase only fetches and
		// extracts (GIS-580).
		if err := i.Install(lockedPackageInfo(pkg)); err != nil {
			return lockInstallError("", pkg.Name, pkg.Version, err)
		}
		printSync("  ✓ %s@%s\n", pkg.Name, pkg.Version)
		return nil
	}); err != nil {
		return err
	}

	// Webcomponent packages — install in parallel after the BDL pass.
	// A locked webcomponent entry is considered "already installed" when
	// any of its COMPONENTTYPE dirs are present; on a re-install we always
	// re-extract to refresh the contents anyway, so this gate just keeps
	// the no-op-fast-path from repeating itself.
	if err := runParallel(wcs, cap, func(wc lockfile.LockedWebcomponent) error {
		// Signature already verified above; this phase only fetches and
		// extracts (GIS-580).
		if err := i.Install(lockedWebcomponentInfo(wc)); err != nil {
			return lockInstallError("webcomponent", wc.Name, wc.Version, err)
		}
		printSync("  ✓ %s@%s (webcomponent)\n", wc.Name, wc.Version)
		return nil
	}); err != nil {
		return err
	}

	// ── Dependency cross-check (post-extraction) ────────────────────────────
	// Diff each installed package's bundled manifest against the locked JAR
	// set. Scans ALL locked packages (including those already on disk) so a
	// stale lock is still cross-checked.
	installedPkgs := make(map[string]bool, len(pkgs)+len(wcs))
	bdlPkgNames := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		installedPkgs[p.Name] = true
		bdlPkgNames = append(bdlPkgNames, p.Name)
	}
	for _, w := range wcs {
		installedPkgs[w.Name] = true
	}
	install := make(map[string]manifest.JavaDependency, len(jars))
	for _, jar := range jars {
		install[jar.Key] = manifest.JavaDependency{
			GroupID: jar.GroupID, ArtifactID: jar.ArtifactID, Version: jar.Version,
		}
	}
	supplemental := i.crossCheckJava(root, bdlPkgNames, install, installedPkgs, opts)

	// Filter locked JARs that are already on disk.
	var jarsToInstall []lockfile.LockedJAR
	for _, jar := range jars {
		dep := manifest.JavaDependency{
			GroupID: jar.GroupID, ArtifactID: jar.ArtifactID, Version: jar.Version,
		}
		if _, err := os.Stat(filepath.Join(i.jarsDir, dep.JarFileName())); err == nil {
			fmt.Printf("  ✓ %s (already present)\n", jar.Key)
			continue
		}
		jarsToInstall = append(jarsToInstall, jar)
	}

	if err := runParallel(jarsToInstall, cap, func(jar lockfile.LockedJAR) error {
		dep := manifest.JavaDependency{
			GroupID:    jar.GroupID,
			ArtifactID: jar.ArtifactID,
			Version:    jar.Version,
			Checksum:   jar.Checksum,
			URL:        jar.DownloadURL,
		}
		if err := i.InstallJar(dep); err != nil {
			return fmt.Errorf("failed to install JAR %s: %w", jar.Key, err)
		}
		printSync("  ✓ %s\n", jar.Key)
		return nil
	}); err != nil {
		return err
	}

	// Fallback JARs recovered from bundled manifests — install as full
	// JavaDependency structs so url/jar overrides survive. InstallJar is
	// idempotent (skips JARs already on disk).
	if err := runParallel(supplemental, cap, func(dep manifest.JavaDependency) error {
		if err := i.InstallJar(dep); err != nil {
			return fmt.Errorf("failed to install fallback JAR %s: %w", dep.Key(), err)
		}
		printSync("  ✓ %s (manifest fallback)\n", dep.JarFileName())
		return nil
	}); err != nil {
		return err
	}

	if !opts.Production {
		i.recordManifestJARs(projectDir, supplemental)
	}

	// The jar set is settled: bring the CLASSPATH anchor in line with it
	// (write when jars exist, delete when none remain). install/update/remove
	// are the only commands that change the jar set — env only references the
	// anchor, so it must be kept current here or it goes stale.
	if err := classpath.Sync(i.jarsDir); err != nil {
		return err
	}

	// Materialize the PACKAGE-correct merged FGLLDPATH root from the installed
	// stores. A namespace clash aborts (strict one-package-per-namespace).
	if err := i.syncMergedRoot(projectDir, !opts.Production); err != nil {
		return err
	}
	return nil
}

// warnDeprecations writes a non-fatal stderr warning for each deprecated
// package in the resolved plan, pointing at the successor when --moved-to was
// set. npm-style deprecation is advisory: this NEVER blocks the install. Only
// fires on a fresh resolve — a lock-file install carries no deprecation state
// (the lockfile is deliberately not extended), matching specs/deprecate-cli.md.
// Warnings are de-duplicated per (name, version) so a package reached by
// multiple paths in the transitive graph warns exactly once.
func warnDeprecations(plan *resolver.Plan, w io.Writer) {
	seen := make(map[string]bool)
	for _, p := range plan.Packages {
		if !p.Deprecated {
			continue
		}
		key := p.Name + "@" + p.Version.String()
		if seen[key] {
			continue
		}
		seen[key] = true

		if p.DeprecationMessage != "" {
			fmt.Fprintf(w, "warning: %s@%s is deprecated: %s\n", p.Name, p.Version.String(), p.DeprecationMessage)
		} else {
			fmt.Fprintf(w, "warning: %s@%s is deprecated\n", p.Name, p.Version.String())
		}
		if p.MovedTo != "" {
			fmt.Fprintf(w, "warning: %s has moved to %s\n", p.Name, p.MovedTo)
			fmt.Fprintf(w, "         → consider: fglpkg install %s\n", p.MovedTo)
		}
	}
}

// installFromPlan installs every entry in a freshly resolved Plan.
// Optional-scoped items whose download or extraction fails emit a warning
// and are skipped; hard-scope failures abort the install.
func (i *Installer) installFromPlan(plan *resolver.Plan, root *manifest.Manifest, opts Options, projectDir string) error {
	cap := installConcurrency()

	if err := runParallel(plan.Packages, cap, func(pkg resolver.ResolvedPackage) error {
		// Signatures were verified by verifyPlanSignatures before the lock
		// write; this phase only fetches and extracts (GIS-580).
		if err := i.Install(planPackageInfo(pkg)); err != nil {
			if pkg.Scope == manifest.ScopeOptional {
				printSync("  warning: skipping optional package %s: %v\n", pkg.Name, err)
				return nil
			}
			return fmt.Errorf("failed to install %s: %w", pkg.Name, err)
		}
		// Required-by hint joins the completion line so it doesn't
		// race onto a separate line from a sibling worker.
		kindHint := ""
		if pkg.IsWebcomponent() {
			kindHint = " (webcomponent)"
		}
		if len(pkg.RequiredBy) > 0 {
			printSync("  ✓ %s@%s%s  (required by: %s)\n",
				pkg.Name, pkg.Version.String(), kindHint, strings.Join(pkg.RequiredBy, ", "))
		} else {
			printSync("  ✓ %s@%s%s\n", pkg.Name, pkg.Version.String(), kindHint)
		}
		return nil
	}); err != nil {
		return err
	}

	// ── Dependency cross-check (post-extraction) ────────────────────────────
	var bdlPkgNames []string
	installedPkgs := make(map[string]bool, len(plan.Packages))
	for _, p := range plan.Packages {
		installedPkgs[p.Name] = true
		if !p.IsWebcomponent() {
			bdlPkgNames = append(bdlPkgNames, p.Name)
		}
	}
	install := make(map[string]manifest.JavaDependency, len(plan.JARs))
	for _, dep := range plan.JARs {
		install[dep.Key()] = dep
	}
	supplemental := i.crossCheckJava(root, bdlPkgNames, install, installedPkgs, opts)

	// Install the resolved JARs plus any manifest-fallback JARs. Fallback
	// JARs carry no plan scope, so they never hit the optional-skip path
	// (transitive Java is always production for the consumer).
	jarsToInstall := append(append([]manifest.JavaDependency(nil), plan.JARs...), supplemental...)
	if err := runParallel(jarsToInstall, cap, func(dep manifest.JavaDependency) error {
		if err := i.InstallJar(dep); err != nil {
			if plan.JARScopes[dep.Key()] == manifest.ScopeOptional {
				printSync("  warning: skipping optional JAR %s: %v\n", dep.Key(), err)
				return nil
			}
			return fmt.Errorf("failed to install JAR %s: %w", dep.Key(), err)
		}
		printSync("  ✓ %s\n", dep.JarFileName())
		return nil
	}); err != nil {
		return err
	}

	if !opts.Production && !opts.SkipLock {
		i.recordManifestJARs(projectDir, supplemental)
	}

	// The jar set is settled: bring the CLASSPATH anchor in line with it —
	// see installFromLock for the write/read split rationale.
	if err := classpath.Sync(i.jarsDir); err != nil {
		return err
	}

	// Materialize the PACKAGE-correct merged FGLLDPATH root from the now-installed
	// stores. A namespace clash aborts the install (strict one-package-per-namespace).
	// recordLock is off under SkipLock so the merged root is still built (the global
	// tool must resolve) but nothing is written into projectDir (GIS-565).
	if err := i.syncMergedRoot(projectDir, !opts.Production && !opts.SkipLock); err != nil {
		return err
	}
	return nil
}

// Install downloads, verifies, and unpacks a single package — dispatching
// to the BDL or webcomponent install layout based on info.Variant.
func (i *Installer) Install(info *registry.PackageInfo) error {
	if info.Variant == "webcomponent" {
		return i.installWebcomponent(info)
	}
	return i.installBDL(info)
}

// installBDL is the BDL (or mixed) package install path: extract the zip
// into .fglpkg/packages/<name>/, splitting off any webcomponent bundles
// declared in the in-zip manifest into .fglpkg/webcomponents/<NAME>/, and
// make bin scripts executable.
func (i *Installer) installBDL(info *registry.PackageInfo) error {
	if err := i.ensureDirs(); err != nil {
		return err
	}

	tmp, err := os.CreateTemp("", "fglpkg-*.zip")
	if err != nil {
		return fmt.Errorf("cannot create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	// Normalize the download URL: the registry returns site-relative
	// download URLs (and older lock files persisted them in that form), so
	// resolve against the consumer base before the GET. No-op for URLs that
	// already carry a scheme (GitHub assets, R2/CDN redirects).
	downloadURL := registry.AbsoluteDownloadURL(info.DownloadURL)

	// Download and verify in one streaming pass.
	repoHeaders, repoMatched := i.matchRepoAuth(downloadURL)
	if err := downloadAndVerify(downloadURL, info.Checksum, info.Name, tmp, i.githubToken, i.registryToken, i.giOrigin, repoHeaders, repoMatched); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	// Peek at the in-zip manifest before extracting so we know which
	// top-level directories are COMPONENTTYPE bundles (need to route to
	// .fglpkg/webcomponents/) vs. ordinary BDL paths (extract into
	// .fglpkg/packages/<name>/). Pure-BDL packages return an empty list.
	wcNames, err := readWebcomponentsFromZip(tmpName)
	if err != nil {
		return fmt.Errorf("cannot read manifest from zip: %w", err)
	}

	destDir := filepath.Join(i.packagesDir, info.Name)
	if err := os.RemoveAll(destDir); err != nil {
		return fmt.Errorf("cannot clean existing package dir: %w", err)
	}
	// Claim the directory before a single file lands in it, so a run killed
	// mid-extraction leaves a store that reads as NOT installed. Absence of the
	// stamp cannot carry that meaning on its own: a directory with no stamp is
	// also what a pre-stamp fglpkg left behind, and `fglpkg pack` puts
	// fglpkg.json mid-zip, so a half-extracted package can otherwise present a
	// manifest naming exactly the version the lock wants (GIS-586).
	if err := lockfile.MarkInstalling(i.packagesDir, info.Name, info.Version); err != nil {
		return err
	}
	wcInstalled, err := extractZipRouted(tmpName, destDir, i.webcomponentsDir, wcNames)
	if err != nil {
		return err
	}
	// Mirror any Genero Studio descriptor into webcomponents/wcsettings/ so
	// `env --gst` can point GSTWCDIR at it (GIS-536). The copies are appended
	// to the ownership record below, so `remove` prunes them with the package.
	wcSettings, err := syncWCSettings(i.webcomponentsDir, wcInstalled)
	if err != nil {
		return err
	}
	wcInstalled = append(wcInstalled, wcSettings...)
	// Track any webcomponent bundle this mixed package routed into the shared
	// webcomponents dir so `remove` can prune it too (GIS-372).
	if err := recordWCOwnership(i.webcomponentsDir, info.Name, info.Version, wcInstalled); err != nil {
		return err
	}

	// Make bin scripts executable after extraction.
	pkgManifest, err := manifest.Load(destDir)
	if err == nil && len(pkgManifest.Bin) > 0 {
		if err := makeBinScriptsExecutable(destDir, pkgManifest); err != nil {
			return fmt.Errorf("cannot set bin script permissions: %w", err)
		}
	}

	// LAST, once everything is in place: mark the install complete. Nothing may
	// be added below this line without moving it, or the stamp would start
	// claiming an install that had not finished (GIS-586). info.Version is the
	// authority, not the zip's manifest — the installer accepts zips carrying
	// no manifest, or one naming a different version than the registry
	// published it as, and a replay must not read either as stale forever.
	return lockfile.MarkInstalled(i.packagesDir, info.Name, info.Version)
}

// installWebcomponent downloads, verifies, and unpacks a webcomponent
// package. Unlike BDL packages — which extract to their own subdir under
// .fglpkg/packages/<name>/ — webcomponent bundles drop straight into
// .fglpkg/webcomponents/<COMPONENTTYPE>/ so Genero finds them via
// FGLIMAGEPATH/WEB_COMPONENT_DIRECTORY without an extra path segment. The
// in-zip layout already has the COMPONENTTYPE/ prefix (the pack step
// strips the leading "webcomponents/"), so a direct extraction is correct.
//
// The package's fglpkg.json is intentionally not extracted to disk —
// multiple webcomponent packages would collide on it. The component names
// are discoverable from the directory listing alone.
func (i *Installer) installWebcomponent(info *registry.PackageInfo) error {
	if err := i.ensureDirs(); err != nil {
		return err
	}

	tmp, err := os.CreateTemp("", "fglpkg-*.zip")
	if err != nil {
		return fmt.Errorf("cannot create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	downloadURL := registry.AbsoluteDownloadURL(info.DownloadURL)
	repoHeaders, repoMatched := i.matchRepoAuth(downloadURL)
	if err := downloadAndVerify(downloadURL, info.Checksum, info.Name, tmp, i.githubToken, i.registryToken, i.giOrigin, repoHeaders, repoMatched); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	// The manifest's webcomponents list names this package's own
	// COMPONENTTYPE bundles. Those dirs may be cleanly replaced on
	// reinstall; any other shared top-level tree (com/, examples/, docs/)
	// must be merged rather than clobbered (GIS-298).
	componentTypes, err := readWebcomponentsFromZip(tmpName)
	if err != nil {
		return fmt.Errorf("cannot read manifest from zip: %w", err)
	}
	installed, err := extractWebcomponentZip(tmpName, i.webcomponentsDir, componentTypes,
		wcOwnedBy(i.webcomponentsDir, info.Name))
	if err != nil {
		return err
	}
	// Mirror any Genero Studio descriptor into webcomponents/wcsettings/ so
	// `env --gst` can point GSTWCDIR at it (GIS-536).
	wcSettings, err := syncWCSettings(i.webcomponentsDir, installed)
	if err != nil {
		return err
	}
	installed = append(installed, wcSettings...)
	// Record which files this package installed so `remove` can prune them
	// without deleting files a still-installed package shares (GIS-372).
	return recordWCOwnership(i.webcomponentsDir, info.Name, info.Version, installed)
}

// InstallJar downloads and verifies a Java JAR into the jars directory.
// The JAR checksum field on JavaDependency is optional; if empty the
// integrity check is skipped (Maven Central is trusted by default).
func (i *Installer) InstallJar(dep manifest.JavaDependency) error {
	if err := i.ensureDirs(); err != nil {
		return err
	}

	dest := filepath.Join(i.jarsDir, dep.JarFileName())
	if _, err := os.Stat(dest); err == nil {
		// Already on disk. Callers report progress; this fast path is
		// silent to keep parallel install output clean.
		return nil
	}

	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("cannot create jar file: %w", err)
	}

	url := dep.MavenURL(i.mavenBase)

	// If the JAR is served by a configured repository (a Maven mirror registered
	// in repoAuth, GIS-365), apply that repo's auth headers — the same path FGL
	// packages take via matchRepoAuth. With no mirror configured the URL is
	// Maven Central, matchRepoAuth returns matched=false, and the download stays
	// anonymous — byte-identical to the previous behavior. The GI/GitHub tokens
	// are deliberately left empty: a JAR host must never receive the GI bearer.
	repoHeaders, repoMatched := i.matchRepoAuth(url)
	if err := downloadAndVerify(url, dep.Checksum, dep.JarFileName(), f, "", "", "", repoHeaders, repoMatched); err != nil {
		f.Close()
		os.Remove(dest)
		return err
	}
	return f.Close()
}

// Remove deletes a BDL package directory.
func (i *Installer) Remove(name string) error {
	dir := filepath.Join(i.packagesDir, name)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return fmt.Errorf("package %q is not installed", name)
	}
	return os.RemoveAll(dir)
}

// ReconcileAfterRemove brings the install state back in line with a manifest
// that has just had one or more dependencies removed. It re-resolves the
// remaining graph, rewrites the lock file — or deletes it when the last
// dependency is gone — so the removed package and its now unreferenced JARs no
// longer reappear on the next install, and — when prune is true — deletes
// installed packages and JARs that the resolved graph no longer requires.
//
// prune MUST be false for a global (~/.fglpkg) home: those package and JAR
// directories are shared across every project, so pruning against a single
// project's graph would delete another project's dependencies. It is only
// safe for a project-local (.fglpkg/) install. The lock rewrite is always
// safe — the lock is project-local regardless of where artifacts live.
//
// A resolution failure (e.g. offline, registry unreachable) is returned so the
// caller can fall back to a manifest-only removal; nothing is pruned in that
// case.
func (i *Installer) ReconcileAfterRemove(m *manifest.Manifest, projectDir string, prune bool) ([]string, error) {
	if err := i.ensureDirs(); err != nil {
		return nil, err
	}
	gv, err := genero.Detect()
	if err != nil {
		return nil, fmt.Errorf("cannot detect Genero version: %w", err)
	}
	r, err := i.newResolver(gv)
	if err != nil {
		return nil, fmt.Errorf("cannot initialise resolver: %w", err)
	}
	plan, err := r.ResolveWithOptions(m, resolver.DefaultResolveOptions())
	if err != nil {
		return nil, fmt.Errorf("dependency resolution failed:\n%w", err)
	}

	// Reconcile the lock first, before mutating disk: rewrite it from the
	// re-resolved graph, or delete it when that graph is now empty. Always
	// safe regardless of prune — the lock is project-local wherever artifacts
	// live.
	lockNote, err := reconcileLock(plan, m, projectDir, i.mavenBase)
	if err != nil {
		return nil, err
	}

	var pruned []string
	if prune {
		diskPruned, err := i.pruneToPlan(plan)
		if err != nil {
			return pruned, err
		}
		pruned = append(pruned, diskPruned...)
	}
	if lockNote != "" {
		pruned = append(pruned, lockNote) // reported after the pruned artifacts
	}

	// Refresh the CLASSPATH anchor now that unreferenced JARs are pruned
	// (deleting it if the last jar is gone). Best-effort like the merged-root
	// rebuild below: an anchor write problem must never block a remove — a
	// leftover stale anchor only lists jars that no longer exist, entries the
	// JVM classloader silently skips.
	_ = classpath.Sync(i.jarsDir)

	// Rebuild the merged root so a removed package's modules disappear from it
	// (and record ownership into the rewritten lock). Best-effort on removal: a
	// merged-root issue — even a pre-existing namespace clash — must never block
	// a remove, so any error is swallowed here.
	_ = i.syncMergedRoot(projectDir, true)
	return pruned, nil
}

// reconcileLock brings the project lock file in line with a freshly re-resolved
// plan after a dependency removal. It is a no-op when the project has no lock
// (a `remove` must never conjure one for a project that was never installed).
// When the removal empties the graph — exactly the case where the lock would
// otherwise be rewritten with empty package and JAR lists — the lock is deleted
// instead: a project with no dependencies has nothing to pin, and a leftover
// empty fglpkg-lock.json is confusing (GIS-273). Otherwise the lock is rewritten
// from the plan. Returns a human-readable note when the lock was deleted (for
// the caller's summary), or "" when it was rewritten or absent.
func reconcileLock(plan *resolver.Plan, m *manifest.Manifest, projectDir, mavenBase string) (string, error) {
	if !lockfile.Exists(projectDir) {
		return "", nil
	}
	if len(plan.Packages) == 0 && len(plan.JARs) == 0 {
		if err := lockfile.Remove(projectDir); err != nil {
			return "", fmt.Errorf("cannot remove empty lock file: %w", err)
		}
		return lockfile.Filename + " (no dependencies remain)", nil
	}
	if err := lockfile.FromPlan(plan, m, mavenBase).Save(projectDir); err != nil {
		return "", fmt.Errorf("cannot write lock file: %w", err)
	}
	return "", nil
}

// pruneToPlan deletes installed BDL packages, JARs, and webcomponent artifacts
// that are absent from plan, returning a human-readable list of what it
// removed. Webcomponent bundles are keyed on disk by COMPONENTTYPE, not by
// package name, so their removal is driven by the per-scope ownership sidecar
// written at install time (webcomponent_owners.go); a file still owned by a
// remaining package is kept (GIS-372).
func (i *Installer) pruneToPlan(plan *resolver.Plan) ([]string, error) {
	wantPkg := make(map[string]bool, len(plan.Packages))
	wantWC := make(map[string]bool, len(plan.Packages))
	for _, p := range plan.Packages {
		// Only pure-BDL/mixed packages own a directory under packages/, so a
		// pure webcomponent package is deliberately absent from wantPkg.
		if !p.IsWebcomponent() {
			wantPkg[p.Name] = true
		}
		// wantWC, by contrast, holds EVERY wanted package name: the ownership
		// sidecar is keyed by package name for any webcomponent-bearing
		// package, and a *mixed* package (variant genero<N>, bundle routed into
		// webcomponents/ at install) is recorded there under its own name. Were
		// wantWC limited to IsWebcomponent() entries, every prune would delete
		// a still-required mixed package's bundle. Naming a pure-BDL package
		// here is harmless — it simply has no sidecar entry to keep.
		wantWC[p.Name] = true
	}
	// Plan JARs are full JavaDependency structs, so JarFileName() honours any
	// `jar` / `url` override and the derived name is authoritative.
	wantJar := make(map[string]bool, len(plan.JARs))
	for _, dep := range plan.JARs {
		wantJar[dep.JarFileName()] = true
	}
	return i.pruneTo(wantPkg, wantWC, wantJar)
}

// pruneToLock is the lock-driven counterpart of pruneToPlan, for the install
// paths that never build a resolver plan: the lock is valid, so its own
// contents define what belongs on disk. Used to clear artifacts left behind by
// an earlier lock (a branch switch, an interrupted run) that would otherwise
// keep resolving on FGLLDPATH and show up in `fglpkg list`.
//
// JARs are deliberately left alone here. A LockedJAR records coordinates but
// not the manifest's optional `jar` filename override, so its on-disk name can
// only be derived — and a wrong guess would delete a JAR that is genuinely
// required. An orphaned JAR is instead collected on the next real re-resolve,
// which is the moment one can actually become orphaned.
func (i *Installer) pruneToLock(lf *lockfile.LockFile) ([]string, error) {
	wantPkg := make(map[string]bool, len(lf.Packages))
	// wantWC spans both lock sections — see pruneToPlan for why a mixed
	// package's name must count as a wanted webcomponent owner.
	wantWC := make(map[string]bool, len(lf.Packages)+len(lf.Webcomponents))
	for _, p := range lf.Packages {
		wantPkg[p.Name] = true
		wantWC[p.Name] = true
	}
	for _, w := range lf.Webcomponents {
		wantWC[w.Name] = true
	}
	return i.pruneTo(wantPkg, wantWC, nil)
}

// pruneTo deletes installed artifacts absent from the given keep-sets. A nil
// wantJar skips the JAR sweep entirely (see pruneToLock); an empty non-nil
// wantJar prunes every JAR.
func (i *Installer) pruneTo(wantPkg, wantWC, wantJar map[string]bool) ([]string, error) {
	var pruned []string

	pkgEntries, err := os.ReadDir(i.packagesDir)
	if err != nil && !os.IsNotExist(err) {
		return pruned, err
	}
	for _, e := range pkgEntries {
		if !e.IsDir() || wantPkg[e.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(i.packagesDir, e.Name())); err != nil {
			return pruned, fmt.Errorf("cannot prune package %s: %w", e.Name(), err)
		}
		pruned = append(pruned, "package "+e.Name())
	}

	if wantJar != nil {
		jarEntries, err := os.ReadDir(i.jarsDir)
		if err != nil && !os.IsNotExist(err) {
			return pruned, err
		}
		for _, e := range jarEntries {
			// The classpath anchor is fglpkg-managed metadata, not a
			// dependency jar — never a prune candidate. classpath.Sync
			// (called after every prune) deletes it when it should go.
			if e.IsDir() || wantJar[e.Name()] || e.Name() == classpath.AnchorName {
				continue
			}
			if err := os.Remove(filepath.Join(i.jarsDir, e.Name())); err != nil {
				return pruned, fmt.Errorf("cannot prune jar %s: %w", e.Name(), err)
			}
			pruned = append(pruned, "jar "+e.Name())
		}
	}

	wcPruned, err := i.pruneWebcomponents(wantWC)
	if err != nil {
		return pruned, err
	}
	pruned = append(pruned, wcPruned...)

	return pruned, nil
}

// List returns all currently installed BDL packages by scanning the packages dir.
func (i *Installer) List() ([]InstalledPackage, error) {
	entries, err := os.ReadDir(i.packagesDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var pkgs []InstalledPackage
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		version := "unknown"
		if m, err := manifest.Load(filepath.Join(i.packagesDir, e.Name())); err == nil {
			version = m.Version
		}
		pkgs = append(pkgs, InstalledPackage{Name: e.Name(), Version: version})
	}
	return pkgs, nil
}

// ListJars returns the file names of all installed JARs by scanning the jars
// dir, sorted for stable output. Unlike List, there is no manifest to consult:
// a JAR on disk is just a file, so the name is all there is. Used by
// `fglpkg list` in its flat mode, where no lock file is available to supply
// coordinates.
func (i *Installer) ListJars() ([]string, error) {
	entries, err := os.ReadDir(i.jarsDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var jars []string
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".jar") {
			continue
		}
		jars = append(jars, e.Name())
	}
	sort.Strings(jars)
	return jars, nil
}

// PackagesDir returns the path where BDL packages are installed.
func (i *Installer) PackagesDir() string { return i.packagesDir }

// MergedDir returns this scope's derived, PACKAGE-correct merged FGLLDPATH root
// (home/merged). It is a rebuildable cache materialized from the per-package
// stores; see internal/materialize and specs/package-layout-materialized-root.md.
func (i *Installer) MergedDir() string { return filepath.Join(i.home, "merged") }

// materializeScope pairs this installer's store and merged directories for the
// materialize package.
func (i *Installer) materializeScope() materialize.Scope {
	return materialize.Scope{PackagesDir: i.packagesDir, MergedDir: i.MergedDir()}
}

// syncMergedRoot rebuilds this scope's merged FGLLDPATH root from the installed
// stores. When recordLock is true, it also records each package's namespaces and
// materialized files into the project lock.
//
// A namespace clash is returned so the caller can decide (install aborts on it;
// removal ignores it — a stale merged root must never block a remove). Any other
// (I/O) failure is non-fatal: the stores are intact, so it is reported as a
// warning and `fglpkg relink` can recover.
func (i *Installer) syncMergedRoot(projectDir string, recordLock bool) error {
	if _, err := i.materializeAndRecord(projectDir, recordLock); err != nil {
		var clash *materialize.NamespaceClashError
		if errors.As(err, &clash) {
			return err
		}
		fmt.Fprintf(os.Stderr, "warning: could not build merged FGLLDPATH root: %v\n", err)
		fmt.Fprintf(os.Stderr, "  the packages are installed; run 'fglpkg relink' to retry.\n")
		return nil
	}
	return nil
}

// materializeAndRecord rebuilds this scope's merged root and — when recordLock
// is true — records ownership into the project lock. It returns the result
// (including the list of packages whose namespaces were inferred from layout)
// and any error verbatim (a namespace clash included), leaving each caller to
// decide how to treat a failure and whether to surface the inferred list.
//
// Inference notes are intentionally NOT printed here: inference is correct and
// expected for packages published before namespaces were recorded, so emitting
// a note on every automatic install/remove/env sync would be pure noise. Only
// the explicit `fglpkg relink` surfaces the inferred list (see cmdRelink).
func (i *Installer) materializeAndRecord(projectDir string, recordLock bool) (*materialize.Result, error) {
	res, err := materialize.Rebuild(i.materializeScope())
	if err != nil {
		return nil, err
	}
	if recordLock {
		if err := applyMaterializationToLock(projectDir, res); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not record materialization in %s: %v\n",
				lockfile.Filename, err)
		}
	}
	return res, nil
}

// mergedRootExists reports whether this scope's merged root is present and
// non-empty (so a fast-path install can skip a redundant rebuild).
func (i *Installer) mergedRootExists() bool {
	entries, err := os.ReadDir(i.MergedDir())
	return err == nil && len(entries) > 0
}

// RebuildMergedRoot rebuilds this scope's merged FGLLDPATH root from the
// installed stores on a best-effort basis, without touching the lock file. Used
// by the offline remove-fallback; it never fails the caller.
func (i *Installer) RebuildMergedRoot() { _ = i.syncMergedRoot("", false) }

// Relink rebuilds this scope's merged FGLLDPATH root for `fglpkg relink`,
// returning the materialize result and any error (a namespace clash included)
// so the command can report what it linked and fail loudly on a clash. When
// recordLock is true it also records ownership into the project lock.
func (i *Installer) Relink(projectDir string, recordLock bool) (*materialize.Result, error) {
	return i.materializeAndRecord(projectDir, recordLock)
}

// applyMaterializationToLock patches each LockedPackage in the project lock with
// the PACKAGE namespaces it owns and the merged-root files it materialized, then
// saves — only when something changed. A no-op when the project has no lock
// (e.g. a --production or lockless install), so it never conjures one.
func applyMaterializationToLock(projectDir string, res *materialize.Result) error {
	if !lockfile.Exists(projectDir) {
		return nil
	}
	lf, err := lockfile.Load(projectDir)
	if err != nil {
		return err
	}
	changed := false
	for idx := range lf.Packages {
		name := lf.Packages[idx].Name
		if ns := res.Namespaces[name]; !stringSlicesEqual(lf.Packages[idx].GeneroPackages, ns) {
			lf.Packages[idx].GeneroPackages = ns
			changed = true
		}
		if files := res.Owned[name]; !stringSlicesEqual(lf.Packages[idx].Materialized, files) {
			lf.Packages[idx].Materialized = files
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return lf.Save(projectDir)
}

// stringSlicesEqual reports element-wise equality, treating nil and empty as
// equal so a nil↔[] difference never dirties the lock.
func stringSlicesEqual(a, b []string) bool {
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

// WebcomponentsDir is the directory holding installed webcomponent bundles,
// one subdirectory per COMPONENTTYPE.
func (i *Installer) WebcomponentsDir() string { return i.webcomponentsDir }

// JarsDir returns the path where Java JARs are stored.
func (i *Installer) JarsDir() string { return i.jarsDir }

// ─── Download + verify ────────────────────────────────────────────────────────

// Download-failure sentinels let a lock-based install turn an opaque HTTP/network
// error into an actionable message that names the package and a remedy (GIS-283).
var (
	// ErrArtifactGone means the registry no longer has the artifact (HTTP 404 or
	// 410) — a permanent condition, typically a package deleted or withdrawn
	// server-side after the lock was written.
	ErrArtifactGone = errors.New("artifact no longer available on the registry")
	// ErrDownloadTransient means the download failed for a retryable reason: a
	// transport error (DNS, connection refused, timeout), a 408/429 throttle, or
	// a 5xx server response.
	ErrDownloadTransient = errors.New("temporary download failure")
)

// downloadAndVerify fetches url, streams the body through a DigestingReader
// into w, and verifies the SHA256 against expectedChecksum in a single pass.
// name is used only in error messages.
//
// Auth selection (first match wins):
//   - GitHub URL + githubToken non-empty → send githubToken (legacy private
//     GitHub Releases path used by fglpkg-registry.fly.dev).
//   - URL belongs to a configured secondary repository (repoMatched) → send
//     that repo's auth-scheme headers, or NONE for an "anonymous" repo. The GI
//     registry token is never sent here: doing so would leak the GI bearer to
//     a third-party (e.g. Artifactory) host (GIS-267).
//   - Other non-GitHub URL whose origin matches giOrigin + registryToken
//     non-empty → send registryToken (the GI service.generointelligence.ai
//     path where the registry serves zips). registryToken is NEVER sent to a
//     URL outside giOrigin — e.g. an absolute R2/CDN download target a GI
//     package resolves to — since that would leak the GI bearer to a third
//     party (GIS-249 S1).
//   - Otherwise → no auth (anonymous public download).
func downloadAndVerify(url, expectedChecksum, name string, w io.Writer, githubToken, registryToken, giOrigin string, repoHeaders map[string]string, repoMatched bool) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("download failed for %s: %w", name, err)
	}

	isGH := gh.IsGitHubURL(url)
	authToken := ""
	switch {
	case isGH && githubToken != "":
		authToken = githubToken
		req.Header.Set("Authorization", "Bearer "+authToken)
		req.Header.Set("Accept", "application/octet-stream")
	case repoMatched:
		// Configured secondary (Artifactory) repository: apply its auth-scheme
		// headers (bearer / basic / apikey), or none for an anonymous repo.
		// Never fall through to registryToken — that would leak the GI bearer
		// to the secondary host (GIS-267).
		for k, v := range repoHeaders {
			req.Header.Set(k, v)
		}
	case !isGH && registryToken != "" && sameOrigin(url, giOrigin):
		authToken = registryToken
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	// Use a custom client for GitHub API downloads. GitHub redirects asset
	// downloads to a different host (github-releases.githubusercontent.com),
	// and Go's default client strips the Authorization header on cross-host
	// redirects. We need to preserve the token through the redirect chain.
	client := http.DefaultClient
	if isGH && authToken != "" {
		client = &http.Client{
			CheckRedirect: func(r *http.Request, via []*http.Request) error {
				if len(via) > 10 {
					return fmt.Errorf("too many redirects")
				}
				r.Header.Set("Authorization", "Bearer "+authToken)
				return nil
			},
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		// A transport-level failure (DNS, connection, timeout) is retryable.
		return fmt.Errorf("download failed for %s (%v): %w", name, err, ErrDownloadTransient)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("HTTP 401 downloading %s: Not authorised — run 'fglpkg login' or set FGLPKG_TOKEN", name)
	}
	// Artifactory returns 403 (not 401) for a bad or missing credential on a
	// protected repo; surface it as an auth failure rather than the generic
	// message so a mis-scoped Maven mirror token is diagnosable (GIS-365). NOTE:
	// an anonymous 403 is ambiguous — a gone GI R2/CDN object vs an auth-required
	// mirror — and cannot be told apart here without knowing the download's
	// source, so it is intentionally NOT classified as "gone" (see PR #71 review).
	if resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("HTTP 403 downloading %s from %s: Forbidden — check your credentials for this repository (run 'fglpkg login')", name, url)
	}
	// 404/410 mean the artifact is gone (permanent); 408/429 (timeout/rate-limit)
	// and 5xx are retryable. Each carries a sentinel so a lock-based install can
	// classify the failure and print the right remedy (GIS-283).
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return fmt.Errorf("HTTP %d downloading %s from %s: %w", resp.StatusCode, name, url, ErrArtifactGone)
	}
	if resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return fmt.Errorf("HTTP %d downloading %s from %s: %w", resp.StatusCode, name, url, ErrDownloadTransient)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d downloading %s from %s", resp.StatusCode, name, url)
	}

	dr := checksum.NewDigestingReader(resp.Body)
	if _, err := io.Copy(w, dr); err != nil {
		return fmt.Errorf("error writing %s: %w", name, err)
	}

	// Verify after the full body has been streamed — no second read.
	if err := dr.Verify(name, expectedChecksum); err != nil {
		return err // already a descriptive *checksum.ErrMismatch
	}
	return nil
}

// ─── Zip extraction ───────────────────────────────────────────────────────────

func (i *Installer) ensureDirs() error {
	for _, d := range []string{i.packagesDir, i.jarsDir, i.webcomponentsDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("cannot create directory %s: %w", d, err)
		}
	}
	return nil
}

// makeBinScriptsExecutable sets the executable bit on all bin scripts
// after extraction. On Windows this is a no-op.
//
// Scripts are resolved under the package's `root` — the base `bin` paths are
// relative to — via manifest.BinScriptPath. Joining them straight onto pkgDir
// missed the script for any package that sets `root`, which failed the whole
// install with "cannot set bin script permissions" (GIS-569).
func makeBinScriptsExecutable(pkgDir string, m *manifest.Manifest) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	for _, scriptPath := range m.BinFiles() {
		fullPath, err := m.BinScriptPath(pkgDir, scriptPath)
		if err != nil {
			return fmt.Errorf("bin script %q in installed package: %w", scriptPath, err)
		}
		info, err := os.Stat(fullPath)
		if err != nil {
			return fmt.Errorf("bin script %q not found in installed package (under root %q): %w", scriptPath, m.RootOrDot(), err)
		}
		if err := os.Chmod(fullPath, info.Mode()|0111); err != nil {
			return fmt.Errorf("cannot chmod %s: %w", fullPath, err)
		}
	}
	return nil
}

// readWebcomponentsFromZip opens the zip at zipPath, reads fglpkg.json
// from its root, and returns the manifest's Webcomponents list. A missing
// manifest or unrecognised JSON yields an empty list and no error — the
// caller treats the install as pure BDL.
func readWebcomponentsFromZip(zipPath string) ([]string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	for _, f := range r.File {
		if filepath.ToSlash(f.Name) != manifest.Filename {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		// Use a partial decode so unknown/new manifest fields don't
		// reject the read here (the resolver and pack flow do strict
		// validation; this is just a routing lookup).
		var partial struct {
			Webcomponents []string `json:"webcomponents"`
		}
		if err := json.Unmarshal(data, &partial); err != nil {
			return nil, fmt.Errorf("manifest in zip is not valid JSON: %w", err)
		}
		return partial.Webcomponents, nil
	}
	return nil, nil
}

// extractZipRouted unpacks a zip into destDir like extractZip, but if
// wcNames is non-empty it diverts any entry whose first path component
// matches one of those names to webcomponentsDir/<COMPONENTTYPE>/...
// instead. Used by mixed packages that ship BDL files alongside one or
// more webcomponent bundles in a single artifact.
//
// Each diverted COMPONENTTYPE directory is cleared at the destination
// before extraction so a re-install does not leave stale files behind.
//
// Returns the slash-relative paths (under webcomponentsDir) it wrote there, so
// the caller can record webcomponent ownership for pruning on remove (GIS-372).
func extractZipRouted(zipPath, destDir, webcomponentsDir string, wcNames []string) ([]string, error) {
	if len(wcNames) == 0 {
		return nil, extractZip(zipPath, destDir)
	}
	wcSet := make(map[string]bool, len(wcNames))
	for _, n := range wcNames {
		wcSet[n] = true
	}

	// Clear any pre-existing install of these webcomponent dirs.
	for _, n := range wcNames {
		if err := os.RemoveAll(filepath.Join(webcomponentsDir, n)); err != nil {
			return nil, fmt.Errorf("cannot clean existing webcomponent dir %s: %w", n, err)
		}
	}

	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("cannot open zip %s: %w", zipPath, err)
	}
	defer r.Close()

	var wcInstalled []string
	budget := int64(maxDecompressedTotal)
	for _, f := range r.File {
		clean := filepath.Clean(f.Name)
		if strings.HasPrefix(clean, "..") {
			return nil, fmt.Errorf("unsafe path in zip: %s", f.Name)
		}
		slashed := filepath.ToSlash(clean)
		if reservedStoreEntry(slashed) {
			warnReservedEntry(filepath.Base(destDir), slashed)
			continue
		}
		top := strings.SplitN(slashed, "/", 2)[0]

		routedToWC := wcSet[top]
		var target string
		if routedToWC {
			// Webcomponent bundle — extract straight into the
			// webcomponents dir, preserving the COMPONENTTYPE prefix.
			target = filepath.Join(webcomponentsDir, clean)
		} else {
			// BDL content (or manifest, root docs) — stays inside
			// the package's own directory.
			target = filepath.Join(destDir, clean)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return nil, err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return nil, err
		}
		if err := writeZipEntry(f, target, &budget); err != nil {
			return nil, err
		}
		if routedToWC {
			wcInstalled = append(wcInstalled, slashed)
		}
	}
	return wcInstalled, nil
}

// reservedStoreEntry reports whether a zip entry is one of fglpkg's own store
// artifacts — files the installer writes into an installed package directory,
// which are never package content and must never be written by an extraction.
//
// Today that is the install stamp. The two-phase write of it is what makes an
// interrupted extraction detectable (GIS-586): a marker goes down before the
// first file lands and is completed after the last one. A zip carrying its own
// .fglpkg-installed at the root would overwrite that marker mid-extraction, so
// if the extraction then failed, the ZIP's stamp is what the next replay would
// trust — and a failed install would read as a finished one, which is precisely
// what the two-phase write exists to prevent.
//
// It needs no hostile publisher. `fglpkg pack` with a broad glob ("*", "**/*")
// picks up a stray .fglpkg-installed from a directory that was itself installed
// — re-publishing a vendored package is the plausible route — and because "."
// sorts first it becomes the archive's FIRST entry. `pack` now excludes it so
// fglpkg cannot produce such an artifact; this is the half that defends against
// one that already exists.
//
// Skipping, rather than refusing the install: the package is otherwise fine,
// and failing it outright would break a package that installs today. The
// warning says what to do about it.
//
// The comparison folds case because the filesystem usually does. On APFS and
// NTFS — the defaults on macOS and Windows — an entry named .FGLPKG-INSTALLED
// opens the SAME file as the marker MarkInstalling wrote, so a case-sensitive
// check would wave it through and let it overwrite exactly what it is meant to
// protect. Nobody arrives at an upper-case copy by accident, so this is a
// deliberately-crafted zip rather than the vendoring mishap above, but the
// filesystem makes it work and the comparison costs nothing.
func reservedStoreEntry(cleanName string) bool {
	return strings.EqualFold(filepath.ToSlash(cleanName), lockfile.InstalledStampFilename)
}

// warnReservedEntry reports a skipped store artifact once per archive.
func warnReservedEntry(pkgDirName, entry string) {
	fmt.Fprintf(os.Stderr,
		"warning: %s ships %s, which is fglpkg's own install record — not extracting it.\n"+
			"  The package was most likely built by packing an installed copy; re-publish it from source.\n",
		pkgDirName, entry)
}

// extractZip unpacks a zip archive into destDir, sanitising all paths.
func extractZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("cannot open zip %s: %w", zipPath, err)
	}
	defer r.Close()

	budget := int64(maxDecompressedTotal)
	for _, f := range r.File {
		cleanName := filepath.Clean(f.Name)
		if strings.HasPrefix(cleanName, "..") {
			return fmt.Errorf("unsafe path in zip: %s", f.Name)
		}
		if reservedStoreEntry(cleanName) {
			warnReservedEntry(filepath.Base(destDir), filepath.ToSlash(cleanName))
			continue
		}

		target := filepath.Join(destDir, cleanName)

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		if err := writeZipEntry(f, target, &budget); err != nil {
			return err
		}
	}
	return nil
}

// extractWebcomponentZip unpacks a webcomponent zip into destDir
// (typically .fglpkg/webcomponents/). Entries at the zip root that are
// not inside a subdirectory are skipped — most importantly the publisher's
// fglpkg.json, which would otherwise collide between multiple installed
// webcomponent packages.
//
// componentTypes names this package's own COMPONENTTYPE bundles (from the
// manifest's "webcomponents" list). Those top-level dirs are the package's
// own and are cleared before extraction so a reinstall/upgrade replaces
// stale files cleanly. Any OTHER top-level tree in the zip (e.g. a shared
// com/, examples/ or docs/ directory) may already hold files installed by a
// different package, so it is MERGED rather than removed:
//
//   - a file absent on disk is written;
//   - a file already present with byte-identical content is left as-is (dedup);
//   - a file already present with DIFFERENT content is a hard conflict — the
//     install aborts naming every clash and touches nothing, instead of
//     silently clobbering or dropping it (GIS-298).
//
// ownedPaths is the set of slash-relative paths under destDir that the package
// being installed already owns, from the ownership sidecar. Those are its own
// previous files — the ones a widget reinstalls on every version — so they are
// never mistaken for a different package's and never reported as a clash
// (GIS-579). Pass nil for a package that owns nothing yet.
func extractWebcomponentZip(zipPath, destDir string, componentTypes []string, ownedPaths map[string]bool) ([]string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("cannot open zip %s: %w", zipPath, err)
	}
	defer r.Close()

	// ownedTypes = the COMPONENTTYPE dirs this package declares. Files under
	// them belong to this package and are safe to clear-and-replace; files
	// under any other top-level dir are potentially shared and must be merged.
	ownedTypes := make(map[string]bool, len(componentTypes))
	for _, c := range componentTypes {
		if c != "" {
			ownedTypes[c] = true
		}
	}

	type zipEntry struct {
		f     *zip.File
		clean string
		top   string
	}
	var entries []zipEntry
	var installed []string // slash-relative paths this package installs
	budget := int64(maxDecompressedTotal)
	for _, f := range r.File {
		clean := filepath.Clean(f.Name)
		if strings.HasPrefix(clean, "..") {
			return nil, fmt.Errorf("unsafe path in zip: %s", f.Name)
		}
		slashed := filepath.ToSlash(clean)
		// Zip-root files (manifest, stray root docs) are not extracted —
		// only files inside a subdirectory install to disk.
		if !strings.Contains(slashed, "/") {
			continue
		}
		entries = append(entries, zipEntry{f: f, clean: clean, top: strings.SplitN(slashed, "/", 2)[0]})
		if !f.FileInfo().IsDir() {
			installed = append(installed, slashed)
		}
	}

	// Pass 1 — detect conflicts on shared (non-owned) files BEFORE touching
	// disk, so a clash aborts without leaving a partial install. Files under
	// an owned COMPONENTTYPE dir are excluded: that dir is cleared in pass 2,
	// so its current contents are this package's own stale files, not a clash.
	//
	// So are files the ownership sidecar attributes to THIS package. A widget
	// that ships anything outside its COMPONENTTYPE dirs — a BDL wrapper, docs,
	// examples — reinstalls those paths on every version, and without this the
	// check could not tell its own previous files from another package's: every
	// such widget refused to move to another version, reporting a clash whose
	// only named package was itself (GIS-579). The sidecar is exactly the
	// record needed, and recordWCOwnership has been writing it all along.
	var conflicts []string
	for _, e := range entries {
		if e.f.FileInfo().IsDir() || ownedTypes[e.top] {
			continue
		}
		if ownedPaths[filepath.ToSlash(e.clean)] {
			continue
		}
		target := filepath.Join(destDir, e.clean)
		same, exists, err := fileMatchesZipEntry(target, e.f)
		if err != nil {
			return nil, err
		}
		if exists && !same {
			conflicts = append(conflicts, filepath.ToSlash(e.clean))
		}
	}
	if len(conflicts) > 0 {
		sort.Strings(conflicts)
		return nil, fmt.Errorf("webcomponent install would overwrite %d file(s) already installed by another package with different content:\n    %s\nrefusing to clobber — remove the conflicting package first, or have the packages namespace their shared files (e.g. examples/<pkg>/, docs/<pkg>/)",
			len(conflicts), strings.Join(conflicts, "\n    "))
	}

	// Pass 2 — clear this package's own COMPONENTTYPE dirs, then extract.
	for c := range ownedTypes {
		if err := os.RemoveAll(filepath.Join(destDir, c)); err != nil {
			return nil, fmt.Errorf("cannot clean existing component dir %s: %w", c, err)
		}
	}
	for _, e := range entries {
		target := filepath.Join(destDir, e.clean)
		if e.f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return nil, err
			}
			continue
		}
		// A shared file already present with identical bytes is a dedup —
		// leave it in place (its owning package still accounts for it).
		if !ownedTypes[e.top] {
			same, exists, err := fileMatchesZipEntry(target, e.f)
			if err != nil {
				return nil, err
			}
			if exists && same {
				continue
			}
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return nil, err
		}
		if err := writeZipEntry(e.f, target, &budget); err != nil {
			return nil, err
		}
	}
	return installed, nil
}

// fileMatchesZipEntry reports whether the file at target has byte-identical
// content to the zip entry f. exists is false (and same false) when target
// is absent; both callers treat "absent" as "safe to write".
func fileMatchesZipEntry(target string, f *zip.File) (same, exists bool, err error) {
	onDisk, err := os.ReadFile(target)
	if err != nil {
		if os.IsNotExist(err) {
			return false, false, nil
		}
		return false, false, err
	}
	rc, err := f.Open()
	if err != nil {
		return false, true, err
	}
	defer rc.Close()
	inZip, err := io.ReadAll(rc)
	if err != nil {
		return false, true, err
	}
	return bytes.Equal(onDisk, inZip), true, nil
}

// Decompressed-size caps guard against zip bombs — archives that are tiny
// compressed but expand to fill the disk. The limits are far above any real
// fglpkg package (BDL source + docs), so legitimate installs never hit them.
// They are vars, not consts, only so tests can shrink them.
var (
	maxDecompressedPerEntry int64 = 512 << 20 // 512 MiB per file
	maxDecompressedTotal    int64 = 2 << 30   // 2 GiB per archive
)

// writeZipEntry writes one zip entry to target, enforcing a per-entry and a
// per-archive decompressed-size cap. budget points at the archive's remaining
// byte allowance and is decremented by the bytes written; the caller seeds it
// with maxDecompressedTotal. Both the declared header size and the actual
// stream are checked, so a lying or absent header cannot bypass the limit.
func writeZipEntry(f *zip.File, target string, budget *int64) error {
	if f.UncompressedSize64 > uint64(maxDecompressedPerEntry) {
		return fmt.Errorf("zip entry %s is too large: %d bytes decompressed exceeds the %d-byte limit",
			f.Name, f.UncompressedSize64, maxDecompressedPerEntry)
	}

	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.Create(target)
	if err != nil {
		return err
	}
	defer out.Close()

	// Allow up to the smaller of the per-entry cap and the archive's remaining
	// budget; the extra byte lets io.Copy detect an overflow past the cap.
	limit := int64(maxDecompressedPerEntry)
	if *budget < limit {
		limit = *budget
	}
	n, err := io.Copy(out, io.LimitReader(rc, limit+1))
	if err != nil {
		return err
	}
	if n > limit {
		return fmt.Errorf("zip entry %s exceeds the decompressed-size limit (archive too large)", f.Name)
	}
	*budget -= n
	return nil
}
