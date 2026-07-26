# Implementation plan: PACKAGE-aware install layout (materialized merged root)

**Ticket:** [GIS-346](https://4js.atlassian.net/browse/GIS-346) (resolves [GIS-358](https://4js.atlassian.net/browse/GIS-358), [GIS-359](https://4js.atlassian.net/browse/GIS-359))
**Design:** [package-layout-materialized-root.md](package-layout-materialized-root.md) — read its **Decisions** section first.
**Date:** 2026-07-24

This is the engineering plan for the design in the spec. It assumes the four resolved decisions:
namespaces recorded at publish (classified per module), automatic materialization, strict
one-package-per-namespace clash, and lockfile-recorded ownership.

## Guiding shape

- **Metadata travels in the artifact's own `fglpkg.json`** (a computed `generoPackages` field), so the
  consumer learns each package's `PACKAGE` namespace(s) with **no GI registry-backend change** and no
  directory-shape guessing. The installer already loads the extracted manifest
  ([installer.go:717](../internal/installer/installer.go#L717)) — that is the read hook.
- **Per-module classification** (spec Decisions §1): `PACKAGE`-declaring library module → materialize;
  `MAIN`/test/example (manifest `programs`) → never merge; legacy flat library module → per-package
  `FGLLDPATH` entry.
- **The per-package store is the source of truth**; `.fglpkg/merged/<scope>` is a derived, rebuildable
  cache. Any step may be re-derived by `fglpkg relink`.

## Phases

Each phase is independently landable and testable. Publish-side (1) and consume-side (3–5) meet at the
lockfile/manifest schema (2); consume-side is developed against hand-crafted fixtures, so it does not
block on republishing real packages.

### Phase 0 — namespace parsing (pure, no behavior change)

New `internal/genpkg` (naming TBD):

```go
// ParsePackageDecl returns the namespace declared by a Genero source module,
// e.g. "com.fourjs.db" for `PACKAGE com.fourjs.db`, and ok=false when the
// module declares no PACKAGE (a MAIN program / flat module).
func ParsePackageDecl(src []byte) (namespace string, ok bool)
```

- Match `^\s*PACKAGE\s+([A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*)` on the first non-comment,
  non-blank lexical line (PackageB: `PACKAGE com.fourjs.db` is line 1).
- Helper `NamespacePath(ns string) string` → dots-to-slashes (`com/fourjs/db`).
- Unit tests: with/without PACKAGE, leading comments, whitespace, nested namespace.

### Phase 1 — publish-side: record `generoPackages` in the staged manifest

- **Manifest** ([internal/manifest/manifest.go](../internal/manifest/manifest.go)): add
  `GeneroPackages []string` with JSON key `generoPackages,omitempty`. Author may declare it explicitly
  (override/escape hatch); normally it is computed.
- **pack/publish** ([`stagePackage`/`stageBDLFiles`](../internal/cli/cli.go#L2149),
  [`buildPackageZip`](../internal/cli/cli.go#L2129), reached from both
  [`cmdPack`](../internal/cli/pack.go#L21) and the publish path
  [cli.go:1751](../internal/cli/cli.go#L1751)): while staging, derive each shipped **library** module's
  namespace from its **staged archive-path directory** (`com/fourjs/poiapi/PoiApi.42m` →
  `com.fourjs.poiapi`). This is the ground truth for both Genero's own resolution and the consumer's
  materialize step — fglcomp already guarantees a compiled module's directory matches its `PACKAGE` — and
  is robust to source layout (real projects compile `lib/`/`src/` into a namespace tree, so the `.4gl`
  rarely sits beside the shipped `.42m`). Set the deduped, sorted namespace set on the manifest copy
  written into the staged zip. Excluded: declared `programs` (by basename), flat-root `.42m` (no
  directory), and a `.42m` whose **adjacent** source `.4gl` declares no `PACKAGE` (a flat/MAIN module
  merely organised in a subdir — this is the one refinement Phase 0's parser still provides).
  - If the author declared `generoPackages`, it wins; the computed set is compared and a drift warning
    is emitted on mismatch (rather than silently overriding).
- Tests: packing the PackageB shape (namespaced `com.fourjs.db` lib + `programs: ["test/TestConnection"]`)
  yields a staged `fglpkg.json` with `generoPackages: ["com.fourjs.db"]` and the test program excluded;
  the poiapi shape (`.42m` under `com/fourjs/poiapi`, source in `lib/`) yields `["com.fourjs.poiapi"]`
  with no spurious "flat" warning.

### Phase 2 — lockfile schema (ownership record)

- **`LockedPackage`** ([internal/lockfile/lockfile.go](../internal/lockfile/lockfile.go)) gains, both
  additive/`omitempty` (no `lockfileVersion` bump — pre-existing locks parse unchanged):
  - `GeneroPackages []string` (`json:"generoPackages,omitempty"`) — the `PACKAGE` namespace(s) this
    package owns. Named to mirror the manifest's `generoPackages` field (clearer than a bare
    `packages`, which would collide with `LockFile.Packages`).
  - `Materialized []string` (`json:"materialized,omitempty"`) — the merged-root-relative `.42m` paths
    this package materialized, for O(manifest) removal/rebuild.
- Populated by the installer (Phase 4).

### Phase 3 — `internal/materialize`

```go
type Scope struct { PackagesDir, MergedDir string } // local or global

type Result struct {
    Owned      map[string][]string // pkg -> merged-relative .42m paths it materialized
    Namespaces map[string][]string // pkg -> namespaces it contributed
    Inferred   []string            // pkgs whose namespaces were inferred (to log)
}

// Rebuild (re)materializes MergedDir from every package under PackagesDir,
// reading each store's own fglpkg.json for its generoPackages (so materialize
// stays decoupled from the lockfile). Returns per-package ownership and a clash
// error (naming both packages) if two declare the same namespace.
func Rebuild(scope Scope) (*Result, error)

func linkOrCopy(src, dst string) error // os.Link; on any link failure → copy
```

- **Signature note:** Rebuild takes no `locked` argument (the earlier sketch's
  `locked []lockfile.LockedPackage` was dropped) — each store carries its own manifest, so materialize
  reads namespaces there and does not import `internal/lockfile`. It returns a richer `*Result` instead
  of a bare `owned` map; the installer maps `Owned`/`Namespaces` onto each `LockedPackage`.
- **Clash detection** at namespace-ownership granularity: build `namespace → package`; a second distinct
  package claiming a namespace → hard error (`*NamespaceClashError`) naming both, mirroring the registry
  guard ([`collisionError`](../internal/provider/repositoryset.go#L285) /
  [`resolver.ErrCollision`](../internal/provider/repositoryset.go#L313)). Planning happens before any
  filesystem mutation, so a clash leaves the existing merged root untouched.
- **Authoritative path:** for each recorded namespace, materialize the `.42m` files sitting **directly**
  in that namespace's directory (non-recursive — a deeper directory is a distinct, separately-recorded
  namespace, which keeps parent/child namespaces owned by different packages from overlapping on disk).
- **Rebuild strategy:** the merged root is a derived cache, so Rebuild clears and re-links it wholesale.
  This makes it idempotent, guarantees no stale entry from a since-removed package survives, and never
  leaves empty namespace directories behind (no separate prune step needed). Hard-link, copy on any link
  failure (cross-FS EXDEV, unsupported FS, restrictive Windows config).
- **Consume-side fallback for pre-`generoPackages` packages:** when a manifest lacks `generoPackages`,
  a `.42m`'s namespace is inferred from its **directory path** (the Genero convention that a module's
  package path mirrors its directory). Flat root `.42m` (legacy, no namespace) and declared `programs`
  are excluded. The package is reported in `Result.Inferred` for the caller to log (no silent guessing).
  The recorded field is authoritative when present.

### Phase 4 — installer integration

Rather than materialize per package (which would race the parallel install and rebuild N times), the
installer materializes **once per scope** after the batch, via a single choke point
`syncMergedRoot(projectDir, recordLock)`:

- Calls `materialize.Rebuild(scope)` for the installer's home
  (`Scope{PackagesDir: home/packages, MergedDir: home/merged}` — `Installer.MergedDir()`), which reads
  each store's own `generoPackages` (so the installer no longer reads namespaces itself).
- When `recordLock` is true, patches every `LockedPackage` with `GeneroPackages` (from
  `Result.Namespaces`) and `Materialized` (from `Result.Owned`) and saves — change-detected, and a no-op
  when the project has no lock (a `--production` or lockless install), so it never conjures one.
- A **namespace clash is returned** so the caller decides; other (I/O) failures are non-fatal (stores are
  intact) — warned, with a pointer to `fglpkg relink`.

Wired into:
- **`installFromPlan` / `installFromLock`** (after the BDL pass): `syncMergedRoot(projectDir, !Production)`
  — a clash **aborts** the install (strict one-package-per-namespace).
- **The "lock up to date / nothing to install" fast path**: calls it **only when the merged root is
  missing** (`mergedRootExists` guard) — the migration case (fglpkg upgraded on an already-installed
  project, or `.fglpkg/merged` deleted). When it already exists, install/remove have kept it current, so
  a no-op install neither rebuilds nor re-scans.
- **`ReconcileAfterRemove`** (after prune/lock reconcile): `syncMergedRoot(projectDir, true)` with the
  clash **ignored** — a merged-root issue must never block a remove.
- **The offline remove-fallback** (CLI): `Installer.RebuildMergedRoot()` (best-effort, no lock write).

**Inference notes are surfaced by `relink` only.** Inference is correct and expected for packages
published before namespaces were recorded, so `materializeAndRecord` does **not** print a note on
automatic syncs (install/remove/env) — that would reprint on every command while any legacy package is
installed. `Result.Inferred` is still returned and recorded into the lock; `fglpkg relink` reports it
(with the inferred namespaces and a "republish to record them" hint) as the one explicit diagnostic.

Removal relies on the wholesale `Rebuild` (the removed store is gone, so its files simply are not
re-linked) rather than `Materialized`-driven unlinking; `Materialized` is kept as the ownership record
and a future fast-path. Materialization failure is non-fatal to the store; `fglpkg relink` (Phase 6)
recovers.

### Phase 5 — `internal/env` rewrite

Rewrite `buildFGLLDPATH` (replacing its one-dir-per-package `addPackagesFrom` closure); `GenerateLocal`,
`GenerateGlobal`, and `GenerateGST` all route through the same per-scope logic:

- A shared `scopeFGLLDPATHDirs(packagesDir)` returns, **per scope**: the scope's `<home>/merged` root
  (one entry) when it exists and is non-empty, followed by a per-package store entry for every package
  the merged root does **not** cover. `GenerateGST` uses a `$(ProjectDir)`-templated twin,
  `gstFGLLDPATHParts`.
- "Covered" is decided by `packageIsMaterialized(pkgDir)` = the installed package's manifest records ≥1
  `generoPackages` namespace. So a **legacy/flat** package (no recorded namespaces) keeps its historical
  per-package entry; a manifest that fails to load falls back to a per-package entry too (never dropped).
- Scope order is unchanged: workspace member source dirs, then local scope (merged + its flat packages),
  then global scope. Within a scope the merged root is emitted first.
- Consequences: GIS-358 (`name == PACKAGE`) resolves because the emitted path is the namespace-correct
  merged tree, not the store-dir name; GIS-359 silent shadowing became the Phase 3 install-time hard
  error. Verified via `fglpkg env` / `env --gst` on a mixed project (materialized + legacy).
- **Known limitation (deferred):** only `.42m` are materialized, so a *namespaced* package's non-`.42m`
  content is not on the merged FGLLDPATH entry. This is not a regression for module resolution
  (FGLLDPATH resolves `.42m`), and non-`.42m` artifacts via the merged root stay parked (see Deferred).

### Phase 6 — `relink` command, gitignore, docs

- **`fglpkg relink`** — rebuilds the merged root(s) from the installed stores; idempotent; local (when
  in a project) + global by default, `--local`/`--global` to target one scope. Wired into dispatch +
  the command registry (`TestRegistryMatchesDispatch` stays green) and, via the registry, completion.
  Backed by a bare `installer.New(home, "", "", "")` and the new `Installer.Relink` (which shares
  `materializeAndRecord` with the install-path `syncMergedRoot`) — so relink is fully offline, surfaces
  a namespace clash as a hard error, and records ownership into the project lock for the local scope.
- **`.gitignore` templates** ([internal/cli/templates.go](../internal/cli/templates.go)): added an
  explicit, commented `.fglpkg/merged/` entry (the default `.fglpkg/` already covers it, but the entry
  documents that the derived cache must never be committed even when `.fglpkg/packages/` is vendored).
- **Docs**: README cheat-sheet (`relink`) + Home-Directory-Layout note on `merged/`; user-guide
  "The merged FGLLDPATH root" subsection (namespace layout, hard-link/copy/disk trade-off, `relink`,
  same-namespace clash, legacy passthrough).

## Test matrix (mirrors the spec's Test plan)

- `genpkg.ParsePackageDecl` unit table.
- Hard-link path **and** forced `EXDEV` copy fallback both yield a resolvable merged root.
- K `PACKAGE` packages → one merged `FGLLDPATH` entry per scope (+ flat entries); every
  `IMPORT FGL <ns>.<mod>` resolves with no manual override.
- `sample-a` (name ≠ PACKAGE) and `hello` (name == PACKAGE root) both resolve from the merged entry.
- PackageB shape: `com.fourjs.db` merged; `test/TestConnection` **not** merged, still runnable.
- Two packages same namespace → error naming both (GIS-359); no silent drop.
- `remove` one package leaves the others resolvable; empty namespace dirs pruned; `Files`-driven and
  walk-fallback removal both correct.
- Local package shadowing a global one of the same namespace → local wins, no false clash.
- Legacy flat package unchanged.
- Functional (black-box) cases under `tests/functional/cases/`: a `PACKAGE`-package install collapses
  `env` to one merged entry; same-namespace second install errors; `relink` rebuilds.
- Cross-platform: Linux/macOS/Windows, no elevated privileges.

## Sequencing & rollout

1. Phases land in order 0 → 6; 3–5 can be built and tested against fixtures before real artifacts carry
   `generoPackages` (Phase 3 fallback covers already-published packages).
2. **Migration:** existing installs re-materialize on the next `install`/`env`, or eagerly via
   `relink`; the store is untouched, so it is non-breaking and reversible (`rm -rf .fglpkg/merged`).
3. **PR breakdown:** (A) Phase 0+1 (publish records namespaces) — small, self-contained, shippable
   alone. (B) Phase 2+3+4 (lockfile + materialize + installer). (C) Phase 5+6 (env switch-over +
   `relink` + docs) — the behavior-visible flip; gate the `env` change so A/B can merge first.

## Deferred (tracked in the spec's open questions)

Non-`.42m` artifacts (`.42f`/`.sch`/C ext) via the merged root; workspace-member scope; namespace
extraction directly from `.42m`; meta-packages; relocating the perf harness in-repo; Windows/NTFS perf
magnitude.
