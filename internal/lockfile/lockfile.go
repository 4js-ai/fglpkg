// Package lockfile manages fglpkg.lock — the reproducible install record.
//
// The lock file captures the exact resolved state of every dependency in the
// graph: BDL packages (name, version, download URL, SHA256 checksum, which
// packages required it) and Java JARs (Maven coordinates, download URL,
// SHA256 checksum). It also records the Genero runtime version that was active
// when resolution ran, so a mismatch can be detected on subsequent installs.
//
// File format: JSON, human-readable, intended to be committed to VCS.
//
// Workflow:
//
//	fglpkg install          → resolve → write lock → install from lock
//	fglpkg install (again)  → lock exists & valid → install directly from lock
//	fglpkg update           → re-resolve → overwrite lock → install from lock
package lockfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/4js-mikefolcher/fglpkg/internal/config"
	"github.com/4js-mikefolcher/fglpkg/internal/jsonutil"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
)

const (
	// Filename is the lock file name, always written next to fglpkg.json.
	Filename = "fglpkg.lock"

	// lockVersion is bumped when the lock file schema changes incompatibly.
	lockVersion = 1
)

// LockFile is the top-level lock file structure.
type LockFile struct {
	// Version is the schema version of this lock file.
	Version int `json:"lockfileVersion"`

	// GeneratedAt is an RFC3339 timestamp of when this lock was written.
	GeneratedAt string `json:"generatedAt"`

	// GeneroVersion is the Genero BDL runtime version active during resolution.
	// If the detected version differs on a subsequent install, a warning is
	// emitted (but the install is not blocked — the user may be intentional).
	GeneroVersion string `json:"generoVersion"`

	// RootManifest records the name and version of the project that owns
	// this lock file, for human reference.
	RootManifest RootEntry `json:"root"`

	// Packages lists every resolved BDL package, sorted by name for stable diffs.
	Packages []LockedPackage `json:"packages"`

	// JARs lists every resolved Java JAR, sorted by key for stable diffs.
	JARs []LockedJAR `json:"jars"`

	// Webcomponents lists every resolved webcomponent package (variant
	// "webcomponent"), sorted by name for stable diffs. Separate from
	// Packages because webcomponent packages have different install
	// semantics (extracted to .fglpkg/webcomponents/, no genero variant,
	// no bin scripts).
	Webcomponents []LockedWebcomponent `json:"webcomponents,omitempty"`
}

// RootEntry records the identity of the root project and the dependency set it
// declared when the lock was written.
type RootEntry struct {
	Name    string `json:"name"`
	Version string `json:"version"`

	// Declared snapshots the root manifest's dependency declarations as they
	// stood when this lock was resolved. It is what makes a hand-edited
	// fglpkg.json detectable: without it, Validate can only compare the
	// project's name and version, so adding or removing a dependency by hand
	// leaves the lock looking "up to date" and the install becomes a no-op.
	//
	// A nil Declared means the lock predates this field. Validate treats that
	// as stale rather than as "nothing changed", so the next install
	// re-resolves once and records the snapshot. Deliberately NOT omitempty:
	// an empty-but-present snapshot ("this project genuinely declares no
	// dependencies") must stay distinguishable from an absent one.
	Declared *DeclaredDeps `json:"declared"`
}

// DeclaredDeps is a per-scope snapshot of the root manifest's dependency
// declarations — constraints as written, not the versions they resolved to
// (those live in Packages/JARs).
type DeclaredDeps struct {
	Prod     ScopeDeps `json:"dependencies"`
	Dev      ScopeDeps `json:"devDependencies"`
	Optional ScopeDeps `json:"optionalDependencies"`
}

// ScopeDeps is one scope's declarations: FGL constraints by package name, any
// per-dependency repository pins, and canonicalised Java coordinates.
type ScopeDeps struct {
	FGL      map[string]string `json:"fgl,omitempty"`
	Registry map[string]string `json:"fglRegistry,omitempty"`
	Java     []string          `json:"java,omitempty"`
}

// declaredFrom snapshots a root manifest's three dependency scopes.
func declaredFrom(m *manifest.Manifest) *DeclaredDeps {
	return &DeclaredDeps{
		Prod:     scopeDepsFrom(m.Dependencies),
		Dev:      scopeDepsFrom(m.DevDependencies),
		Optional: scopeDepsFrom(m.OptionalDependencies),
	}
}

// scopeDepsFrom converts one manifest scope into its lock representation.
// Empty maps/slices are left nil so they omit cleanly and two equivalent
// manifests always produce byte-identical snapshots.
func scopeDepsFrom(d manifest.Dependencies) ScopeDeps {
	var s ScopeDeps
	if len(d.FGL) > 0 {
		s.FGL = make(map[string]string, len(d.FGL))
		for name, constraint := range d.FGL {
			s.FGL[name] = constraint
		}
	}
	if len(d.FGLPins) > 0 {
		s.Registry = make(map[string]string, len(d.FGLPins))
		for name, reg := range d.FGLPins {
			s.Registry[name] = reg
		}
	}
	if len(d.Java) > 0 {
		s.Java = make([]string, 0, len(d.Java))
		for _, dep := range d.Java {
			s.Java = append(s.Java, javaDeclKey(dep))
		}
		sort.Strings(s.Java)
	}
	return s
}

// javaDeclKey canonicalises a Java dependency declaration. Coordinates alone
// are not enough: a changed jar name, URL override, or expected checksum all
// change what gets downloaded, so each participates in staleness.
func javaDeclKey(d manifest.JavaDependency) string {
	k := d.GroupID + ":" + d.ArtifactID + ":" + d.Version
	if d.JarFile != "" {
		k += "|jar=" + d.JarFile
	}
	if d.URL != "" {
		k += "|url=" + d.URL
	}
	if d.Checksum != "" {
		k += "|sha256=" + d.Checksum
	}
	return k
}

// LockedPackage is the fully-pinned record of one BDL package.
type LockedPackage struct {
	// Name is the package name, e.g. "myutils".
	Name string `json:"name"`

	// Version is the exact resolved semver string, e.g. "1.2.3".
	Version string `json:"version"`

	// GeneroConstraint is the Genero compatibility range declared by this
	// package version, e.g. "^4.0.0". Stored for auditing.
	GeneroConstraint string `json:"genero,omitempty"`

	// DownloadURL is the exact URL used to download this version.
	DownloadURL string `json:"downloadUrl"`

	// Checksum is the SHA256 hex digest of the downloaded zip file.
	// Empty means the registry provided no checksum (install will skip verify).
	Checksum string `json:"checksum,omitempty"`

	// GeneroMajor is the Genero major version variant that was selected
	// (e.g. "4", "6"). Empty for legacy packages without variants.
	GeneroMajor string `json:"generoMajor,omitempty"`

	// RequiredBy lists every package (or "<root>") that declared a dependency
	// on this package, enabling humans to trace why it was included.
	RequiredBy []string `json:"requiredBy"`

	// Scope is the dependency scope this package was installed under: "dev"
	// or "optional". Empty means production. Used by `fglpkg install
	// --production` to skip dev-scoped entries.
	Scope string `json:"scope,omitempty"`

	// Registry is the logical repository this package resolved from ("gi",
	// "acme-internal"). Empty means the default GI registry, so
	// pre-Artifactory locks parse unchanged (additive, omitempty — no
	// lockfileVersion bump). It is the dependency-confusion pin: a locked
	// package is re-fetched from this repository and can never be silently
	// re-routed. See specs/artifactory-secondary-repository.md §9.
	Registry string `json:"registry,omitempty"`

	// ── PACKAGE-aware materialization (GIS-346) ──
	// GeneroPackages records the Genero PACKAGE namespace(s) this package's
	// library modules declare, mirroring the manifest's generoPackages field
	// (recorded at publish). Materialized records the merged-root-relative
	// .42m paths this package linked/copied into its scope's merged root, so
	// removal and rebuild are O(manifest) rather than a filesystem walk. Both
	// are populated by the installer after extraction/materialization (Phase
	// 4) and are additive/omitempty — pre-existing locks parse unchanged (no
	// lockfileVersion bump). See specs/package-layout-materialized-root.md.
	GeneroPackages []string `json:"generoPackages,omitempty"`
	Materialized   []string `json:"materialized,omitempty"`

	// ── Layer 1 signing material ──
	// Size/UploadedAt/Uploader are the remaining inputs (beyond name,
	// version, variant, and Checksum) needed to reconstruct the canonical
	// signed payload for offline re-verification. Signature/SignatureKeyID
	// are the Ed25519 envelope. All empty for unsigned packages.
	Size           int64  `json:"size,omitempty"`
	UploadedAt     string `json:"uploadedAt,omitempty"`
	Uploader       string `json:"uploader,omitempty"`
	Signature      string `json:"signature,omitempty"` // base64 raw 64-byte Ed25519 signature
	SignatureKeyID string `json:"signatureKeyid,omitempty"`
}

// LockedWebcomponent is the fully-pinned record of one webcomponent package.
// COMPONENTTYPE names are not persisted here in v1 — they are inferred at
// runtime by listing subdirectories of the install location. Future versions
// may persist them once the registry exposes the manifest's webcomponents
// field server-side.
type LockedWebcomponent struct {
	// Name is the package name, e.g. "chart-3d".
	Name string `json:"name"`

	// Version is the exact resolved semver string.
	Version string `json:"version"`

	// DownloadURL is the exact URL used to download this version (variant
	// "webcomponent").
	DownloadURL string `json:"downloadUrl"`

	// Checksum is the SHA256 hex digest of the downloaded zip file.
	Checksum string `json:"checksum,omitempty"`

	// RequiredBy lists every package (or "<root>") that declared a dependency
	// on this webcomponent package.
	RequiredBy []string `json:"requiredBy"`

	// Scope is the dependency scope: "dev" or "optional". Empty means
	// production.
	Scope string `json:"scope,omitempty"`

	// Registry is the logical repository this package resolved from. Empty
	// means the default GI registry. See LockedPackage.Registry.
	Registry string `json:"registry,omitempty"`

	// ── Layer 1 signing material (see LockedPackage) ──
	Size           int64  `json:"size,omitempty"`
	UploadedAt     string `json:"uploadedAt,omitempty"`
	Uploader       string `json:"uploader,omitempty"`
	Signature      string `json:"signature,omitempty"`
	SignatureKeyID string `json:"signatureKeyid,omitempty"`
}

// LockedJAR is the fully-pinned record of one Java JAR.
type LockedJAR struct {
	// Key is "groupId:artifactId", the deduplication key.
	Key string `json:"key"`

	GroupID    string `json:"groupId"`
	ArtifactID string `json:"artifactId"`
	Version    string `json:"version"`

	// DownloadURL is the resolved Maven Central (or override) URL.
	DownloadURL string `json:"downloadUrl"`

	// Checksum is the SHA256 hex digest of the JAR file.
	Checksum string `json:"checksum,omitempty"`

	// Scope is the dependency scope this JAR was installed under: "dev"
	// or "optional". Empty means production.
	Scope string `json:"scope,omitempty"`

	// Source records where this JAR entry came from: "" / "registry"
	// (resolved from registry metadata) or "manifest" (recovered from a
	// package's bundled manifest via the dependency cross-check fallback).
	// Informational; lets a reader see which JARs bypassed the registry's
	// declared dependency set, and is the audit trail that makes future
	// enforcement of manifest↔registry divergence possible.
	Source string `json:"source,omitempty"`
}

// ─── Construction ─────────────────────────────────────────────────────────────

// FromPlan builds a LockFile from a resolved Plan and the root manifest.
// Packages with variant "webcomponent" land in the Webcomponents array;
// everything else lands in Packages. mavenBase is the resolved Maven mirror
// base ("" for public Maven Central) recorded into each JAR's DownloadURL so
// the pinned URL replays through the same source (GIS-365).
func FromPlan(plan *resolver.Plan, root *manifest.Manifest, mavenBase string) *LockFile {
	pkgs := make([]LockedPackage, 0, len(plan.Packages))
	wcs := make([]LockedWebcomponent, 0)
	for _, p := range plan.Packages {
		requiredBy := make([]string, len(p.RequiredBy))
		copy(requiredBy, p.RequiredBy)
		sort.Strings(requiredBy)

		keyid, sig := sigFields(p.Signature)

		if p.IsWebcomponent() {
			wcs = append(wcs, LockedWebcomponent{
				Name:           p.Name,
				Version:        p.Version.String(),
				DownloadURL:    p.DownloadURL,
				Checksum:       p.Checksum,
				RequiredBy:     requiredBy,
				Scope:          scopeLockString(p.Scope),
				Registry:       normalizeSource(p.Source),
				Size:           p.Size,
				UploadedAt:     p.UploadedAt,
				Uploader:       p.Uploader,
				Signature:      sig,
				SignatureKeyID: keyid,
			})
			continue
		}

		pkgs = append(pkgs, LockedPackage{
			Name:           p.Name,
			Version:        p.Version.String(),
			DownloadURL:    p.DownloadURL,
			Checksum:       p.Checksum,
			GeneroMajor:    plan.GeneroVersion.MajorString(),
			RequiredBy:     requiredBy,
			Scope:          scopeLockString(p.Scope),
			Registry:       normalizeSource(p.Source),
			Size:           p.Size,
			UploadedAt:     p.UploadedAt,
			Uploader:       p.Uploader,
			Signature:      sig,
			SignatureKeyID: keyid,
		})
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Name < pkgs[j].Name })
	sort.Slice(wcs, func(i, j int) bool { return wcs[i].Name < wcs[j].Name })

	jars := make([]LockedJAR, 0, len(plan.JARs))
	for _, dep := range plan.JARs {
		jars = append(jars, LockedJAR{
			Key:         dep.Key(),
			GroupID:     dep.GroupID,
			ArtifactID:  dep.ArtifactID,
			Version:     dep.Version,
			DownloadURL: dep.MavenURL(mavenBase),
			Checksum:    dep.Checksum,
			Scope:       scopeLockString(plan.JARScopes[dep.Key()]),
		})
	}
	sort.Slice(jars, func(i, j int) bool { return jars[i].Key < jars[j].Key })

	return &LockFile{
		Version:       lockVersion,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		GeneroVersion: plan.GeneroVersion.String(),
		RootManifest:  RootEntry{Name: root.Name, Version: root.Version, Declared: declaredFrom(root)},
		Packages:      pkgs,
		JARs:          jars,
		Webcomponents: wcs,
	}
}

// normalizeSource collapses the explicit GI source name to "" so the lock's
// "empty registry means GI" convention holds regardless of whether GI packages
// were resolved via the single-registry path (Source left "") or through
// GeneroProvider in multi-registry mode (Source stamped "gi"). This keeps
// fglpkg.lock byte-identical — and diffs clean — when a second registry is
// added or removed. (GIS-249 C2)
func normalizeSource(source string) string {
	if source == config.GIName {
		return ""
	}
	return source
}

// AddManifestJARs appends Java dependencies recovered by the manifest
// cross-check fallback to the lock's JAR list, marking each Source
// "manifest". Coordinates already present (by key) are left untouched, so an
// entry the resolver already recorded is never downgraded to manifest-sourced.
// The list is re-sorted by key so diffs stay stable. Returns true if at least
// one new entry was added. mavenBase is the resolved Maven mirror base ("" for
// public Maven Central) baked into each new JAR's DownloadURL (GIS-365).
func (lf *LockFile) AddManifestJARs(deps []manifest.JavaDependency, mavenBase string) bool {
	existing := make(map[string]bool, len(lf.JARs))
	for _, j := range lf.JARs {
		existing[j.Key] = true
	}
	added := false
	for _, dep := range deps {
		if existing[dep.Key()] {
			continue
		}
		lf.JARs = append(lf.JARs, LockedJAR{
			Key:         dep.Key(),
			GroupID:     dep.GroupID,
			ArtifactID:  dep.ArtifactID,
			Version:     dep.Version,
			DownloadURL: dep.MavenURL(mavenBase),
			Checksum:    dep.Checksum,
			Source:      "manifest",
		})
		existing[dep.Key()] = true
		added = true
	}
	if added {
		sort.Slice(lf.JARs, func(i, j int) bool { return lf.JARs[i].Key < lf.JARs[j].Key })
	}
	return added
}

// ─── Persistence ──────────────────────────────────────────────────────────────

// Save writes the lock file as formatted JSON to dir/fglpkg.lock.
func (lf *LockFile) Save(dir string) error {
	// jsonutil (no HTML escaping) so a requiredBy entry like "<root>" keeps its
	// literal angle brackets instead of Unicode escapes (GIS-280).
	data, err := jsonutil.MarshalIndent(lf, "  ")
	if err != nil {
		return fmt.Errorf("cannot serialise lock file: %w", err)
	}
	path := filepath.Join(dir, Filename)
	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("cannot write %s: %w", path, err)
	}
	return nil
}

// Load reads and parses the lock file from dir/fglpkg.lock.
func Load(dir string) (*LockFile, error) {
	path := filepath.Join(dir, Filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lf LockFile
	if err := json.Unmarshal(data, &lf); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", Filename, err)
	}
	return &lf, nil
}

// Exists reports whether a lock file exists in dir.
func Exists(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, Filename))
	return err == nil
}

// Remove deletes dir/fglpkg.lock. A missing file is not an error, so callers
// may remove unconditionally without racing Exists.
func Remove(dir string) error {
	if err := os.Remove(filepath.Join(dir, Filename)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ─── Validation ───────────────────────────────────────────────────────────────

// ValidationResult is returned by Validate, describing any problems found.
type ValidationResult struct {
	// SchemaError is set when the lock file's schema version is unrecognised.
	SchemaError error

	// GeneroMismatch is set when the current Genero version differs from the
	// one recorded in the lock file.
	GeneroMismatch *GeneroMismatchError

	// ManifestMismatch is set when the root manifest's name or version has
	// changed since the lock was written (lock is stale).
	ManifestMismatch *ManifestMismatchError

	// MissingPackages lists packages in the lock that are not yet installed
	// (install directory absent).
	MissingPackages []string

	// MissingWebcomponents lists webcomponent packages in the lock whose
	// install does not appear under the webcomponents directory.
	MissingWebcomponents []string

	// MissingJARs lists the keys ("groupId:artifactId") of locked Java JARs
	// whose file is absent from the jars directory.
	MissingJARs []string
}

// IsClean returns true when there are no errors or mismatches at all.
func (vr *ValidationResult) IsClean() bool {
	return vr.SchemaError == nil &&
		vr.GeneroMismatch == nil &&
		vr.ManifestMismatch == nil &&
		len(vr.MissingPackages) == 0 &&
		len(vr.MissingWebcomponents) == 0 &&
		len(vr.MissingJARs) == 0
}

// NeedsResolve returns true when a full re-resolution is required before
// installing (schema incompatible or manifest has changed).
func (vr *ValidationResult) NeedsResolve() bool {
	return vr.SchemaError != nil || vr.ManifestMismatch != nil
}

// StaleReason is a one-line description of why NeedsResolve is true, for
// inlining in a progress message. Empty when no re-resolution is needed.
func (vr *ValidationResult) StaleReason() string {
	switch {
	case vr.ManifestMismatch != nil:
		return vr.ManifestMismatch.Summary()
	case vr.SchemaError != nil:
		return vr.SchemaError.Error()
	}
	return ""
}

// GeneroMismatchError describes a Genero version difference.
type GeneroMismatchError struct {
	Locked  string // version in lock file
	Current string // version detected now
}

func (e *GeneroMismatchError) Error() string {
	return fmt.Sprintf(
		"lock file was generated with Genero %s but current runtime is %s.\n"+
			"Run 'fglpkg update' to re-resolve for the current Genero version.",
		e.Locked, e.Current,
	)
}

// ManifestMismatchError describes a stale lock file (manifest changed).
type ManifestMismatchError struct {
	Field      string
	InLock     string
	InManifest string

	// Reason, when set, replaces the generic "<field> changed from X to Y"
	// message. Dependency-set changes (a package added, removed, or
	// re-constrained) don't read as a field edit, so they describe themselves.
	Reason string
}

// Summary is the one-line form of the message, for inlining in a progress line.
func (e *ManifestMismatchError) Summary() string {
	if e.Reason != "" {
		return e.Reason
	}
	return fmt.Sprintf("%s changed from %q (lock) to %q (manifest)",
		e.Field, e.InLock, e.InManifest)
}

func (e *ManifestMismatchError) Error() string {
	return "lock file is stale: " + e.Summary()
}

// diffDeclared compares the dependency snapshot recorded in the lock against
// the root manifest's current declarations, returning the first difference
// found (or nil when they agree). A nil lock snapshot means the lock predates
// dependency tracking: reported as stale so one re-resolve records it.
//
// Scopes are checked prod → dev → optional, and keys within a scope in sorted
// order, so the reported difference is deterministic for a given pair of
// inputs rather than dependent on map iteration order.
func diffDeclared(inLock, inManifest *DeclaredDeps) *ManifestMismatchError {
	if inLock == nil {
		return &ManifestMismatchError{Reason: "it predates dependency-set tracking, " +
			"so changes to fglpkg.json cannot be detected — re-resolving once to record it"}
	}
	scopes := []struct {
		label  string
		lock   ScopeDeps
		latest ScopeDeps
	}{
		{"dependencies", inLock.Prod, inManifest.Prod},
		{"devDependencies", inLock.Dev, inManifest.Dev},
		{"optionalDependencies", inLock.Optional, inManifest.Optional},
	}
	for _, s := range scopes {
		if e := diffScope(s.label, s.lock, s.latest); e != nil {
			return e
		}
	}
	return nil
}

// diffScope reports the first difference between one scope's locked and current
// declarations.
func diffScope(label string, inLock, inManifest ScopeDeps) *ManifestMismatchError {
	for _, name := range sortedKeys(inLock.FGL, inManifest.FGL) {
		locked, wasLocked := inLock.FGL[name]
		current, isDeclared := inManifest.FGL[name]
		switch {
		case wasLocked && !isDeclared:
			return &ManifestMismatchError{Reason: fmt.Sprintf(
				"dependency %q was removed from %s", name, label)}
		case !wasLocked && isDeclared:
			return &ManifestMismatchError{Reason: fmt.Sprintf(
				"dependency %q was added to %s", name, label)}
		case locked != current:
			return &ManifestMismatchError{Reason: fmt.Sprintf(
				"constraint for %q in %s changed from %q (lock) to %q (manifest)",
				name, label, locked, current)}
		}
	}
	for _, name := range sortedKeys(inLock.Registry, inManifest.Registry) {
		if inLock.Registry[name] != inManifest.Registry[name] {
			return &ManifestMismatchError{Reason: fmt.Sprintf(
				"repository pin for %q in %s changed from %q (lock) to %q (manifest)",
				name, label, inLock.Registry[name], inManifest.Registry[name])}
		}
	}
	// Java coordinates are canonicalised and sorted by scopeDepsFrom, so a
	// straight positional walk finds the first divergence.
	lockedJava, currentJava := inLock.Java, inManifest.Java
	for idx := 0; idx < len(lockedJava) || idx < len(currentJava); idx++ {
		switch {
		case idx >= len(currentJava):
			return &ManifestMismatchError{Reason: fmt.Sprintf(
				"java dependency %q was removed from %s", lockedJava[idx], label)}
		case idx >= len(lockedJava):
			return &ManifestMismatchError{Reason: fmt.Sprintf(
				"java dependency %q was added to %s", currentJava[idx], label)}
		case lockedJava[idx] != currentJava[idx]:
			return &ManifestMismatchError{Reason: fmt.Sprintf(
				"java dependency %q in %s changed to %q",
				lockedJava[idx], label, currentJava[idx])}
		}
	}
	return nil
}

// sortedKeys returns the union of two maps' keys in sorted order.
func sortedKeys(a, b map[string]string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	keys := make([]string, 0, len(a)+len(b))
	for _, m := range []map[string]string{a, b} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	sort.Strings(keys)
	return keys
}

// Validate checks whether the lock file is consistent with the current
// environment and manifest. currentGenero may be "" to skip that check.
// packagesDir / webcomponentsDir / jarsDir are used to check which BDL
// installs, webcomponent installs and Java JARs are actually present on disk;
// pass "" to skip any of those checks.
func (lf *LockFile) Validate(root *manifest.Manifest, currentGenero, packagesDir, webcomponentsDir, jarsDir string) *ValidationResult {
	result := &ValidationResult{}

	// Schema version check.
	if lf.Version != lockVersion {
		result.SchemaError = fmt.Errorf(
			"lock file schema version %d is not supported (expected %d)",
			lf.Version, lockVersion,
		)
		return result // nothing else makes sense to check
	}

	// Genero version check. Treated as a warning only — the user decides
	// whether to run `fglpkg update` to re-resolve for a different runtime.
	if currentGenero != "" && lf.GeneroVersion != currentGenero {
		result.GeneroMismatch = &GeneroMismatchError{
			Locked:  lf.GeneroVersion,
			Current: currentGenero,
		}
	}

	// Root manifest identity check, then its dependency set. The dependency
	// check is what catches a hand-edited fglpkg.json — the common case being a
	// dependency deleted from the manifest, which must re-resolve (and prune)
	// rather than report the lock as up to date.
	if lf.RootManifest.Name != root.Name {
		result.ManifestMismatch = &ManifestMismatchError{
			Field: "project name", InLock: lf.RootManifest.Name, InManifest: root.Name,
		}
	} else if lf.RootManifest.Version != root.Version {
		result.ManifestMismatch = &ManifestMismatchError{
			Field: "project version", InLock: lf.RootManifest.Version, InManifest: root.Version,
		}
	} else if e := diffDeclared(lf.RootManifest.Declared, declaredFrom(root)); e != nil {
		result.ManifestMismatch = e
	}

	// On-disk presence check.
	if packagesDir != "" {
		for _, pkg := range lf.Packages {
			dir := filepath.Join(packagesDir, pkg.Name)
			if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
				result.MissingPackages = append(result.MissingPackages, pkg.Name)
			}
		}
	}

	// Webcomponent presence check — a locked webcomponent package is
	// considered installed if the webcomponents directory exists and is
	// non-empty (the publisher controls the COMPONENTTYPE subdir names,
	// not the package name, so we can only verify that *some* extraction
	// happened — Phase 5 may persist the per-package component list).
	if webcomponentsDir != "" {
		for _, wc := range lf.Webcomponents {
			// Treat a totally empty webcomponents directory as "all WC
			// packages missing" so a fresh checkout triggers re-install.
			entries, err := os.ReadDir(webcomponentsDir)
			if err != nil || len(entries) == 0 {
				result.MissingWebcomponents = append(result.MissingWebcomponents, wc.Name)
			}
		}
	}

	// JAR presence check — a locked JAR lands in jarsDir under the same
	// artifactId-version.jar name the installer writes, so a deleted or
	// never-fetched JAR is detectable by a plain stat.
	if jarsDir != "" {
		for _, jar := range lf.JARs {
			dep := manifest.JavaDependency{
				GroupID: jar.GroupID, ArtifactID: jar.ArtifactID, Version: jar.Version,
			}
			path := filepath.Join(jarsDir, dep.JarFileName())
			if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
				result.MissingJARs = append(result.MissingJARs, jar.Key)
			}
		}
	}

	return result
}

// CheckRegistries reports the first locked package or webcomponent whose
// recorded Registry is not among configured (the logical names of the
// currently-configured repositories, which must include the built-in "gi").
// An empty Registry means the default GI registry and is always valid, so
// pre-Artifactory locks pass unchanged. This is the spec §9 guarantee that a
// lock referencing a repository since removed from the config fails clearly
// instead of installing silently. configured empty ⇒ the check is skipped
// (the caller could not determine the configured set).
func (lf *LockFile) CheckRegistries(configured []string) error {
	if len(configured) == 0 {
		return nil
	}
	known := make(map[string]bool, len(configured))
	for _, n := range configured {
		known[n] = true
	}
	check := func(name, reg string) error {
		if reg == "" || known[reg] {
			return nil
		}
		return fmt.Errorf(
			"locked package %q came from repository %q, which is not configured.\n"+
				"  Configured repositories: %s\n"+
				"  Re-add %q to fglpkg.json / ~/.fglpkg/config.json, or run 'fglpkg update' to re-resolve.",
			name, reg, strings.Join(configured, ", "), reg)
	}
	for _, p := range lf.Packages {
		if err := check(p.Name, p.Registry); err != nil {
			return err
		}
	}
	for _, w := range lf.Webcomponents {
		if err := check(w.Name, w.Registry); err != nil {
			return err
		}
	}
	return nil
}

// ─── Plan extraction ──────────────────────────────────────────────────────────

// generoMajor extracts the major version from a version string like "4.01.12".
func generoMajor(v string) string {
	for i, c := range v {
		if c == '.' {
			return v[:i]
		}
	}
	return v
}

// sigFields extracts the keyid and base64 signature from a resolved
// signature envelope, returning empty strings when the package is unsigned.
func sigFields(s *registry.Signature) (keyid, sig string) {
	if s == nil {
		return "", ""
	}
	return s.KeyID, s.Sig
}

// scopeLockString converts a manifest.Scope into the string value stored in
// the lock file. Production is stored as "" (omitempty) so the lock file
// stays compact and backwards-compatible.
func scopeLockString(s manifest.Scope) string {
	if s == manifest.ScopeDev {
		return "dev"
	}
	if s == manifest.ScopeOptional {
		return "optional"
	}
	return ""
}

// ToInstallList converts the lock file back into the flat lists needed by
// the installer, so a locked install never touches the resolver or registry
// for version negotiation — it uses exact URLs and checksums directly.
func (lf *LockFile) ToInstallList() ([]LockedPackage, []LockedJAR, []LockedWebcomponent) {
	return lf.Packages, lf.JARs, lf.Webcomponents
}

// FilterForProduction returns the subset of packages, JARs, and webcomponent
// packages that should be installed when `fglpkg install --production` is
// used: everything except dev-scoped entries. Optional entries are retained
// — they are still attempted (a bad optional dep was already dropped at
// resolve time).
func (lf *LockFile) FilterForProduction() ([]LockedPackage, []LockedJAR, []LockedWebcomponent) {
	pkgs := make([]LockedPackage, 0, len(lf.Packages))
	for _, p := range lf.Packages {
		if p.Scope == "dev" {
			continue
		}
		pkgs = append(pkgs, p)
	}
	jars := make([]LockedJAR, 0, len(lf.JARs))
	for _, j := range lf.JARs {
		if j.Scope == "dev" {
			continue
		}
		jars = append(jars, j)
	}
	wcs := make([]LockedWebcomponent, 0, len(lf.Webcomponents))
	for _, w := range lf.Webcomponents {
		if w.Scope == "dev" {
			continue
		}
		wcs = append(wcs, w)
	}
	return pkgs, jars, wcs
}
