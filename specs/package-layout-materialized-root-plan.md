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
  [cli.go:1751](../internal/cli/cli.go#L1751)): while staging, for every shipped **library** module
  (a source/`.42m` not in the manifest `programs` set), read the corresponding `.4gl` and collect its
  `PACKAGE` namespace via Phase 0. Set the deduped, sorted namespace set on the manifest copy written
  into the staged zip.
  - If the author declared `generoPackages`, validate the computed set matches (warn on drift) rather
    than silently overriding.
  - **Source-absent fallback** (a project shipping only `.42m`): if no `.4gl` is present to parse, fall
    back to the author-declared `generoPackages`; if that is also empty, warn that the package will be
    treated as flat (no merge) and proceed. (Namespace-from-`.42m` extraction is a possible later
    enhancement; out of scope here.)
- Tests: packing the PackageB shape (namespaced `com.fourjs.db` lib + `programs: ["test/TestConnection"]`)
  yields a staged `fglpkg.json` with `generoPackages: ["com.fourjs.db"]` and the test program excluded.

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

- **Install** ([`installBDL`](../internal/installer/installer.go#L667)): after extraction +
  `manifest.Load(destDir)` ([L717](../internal/installer/installer.go#L717)), read `generoPackages`,
  write them into the package's `LockedPackage.Packages`, then trigger `materialize.Rebuild` for the
  affected scope; record the returned owned file list into `LockedPackage.Files`.
- **Remove**: unlink the package's `Files` from the scope merged root and prune empty namespace dirs
  (fall back to a store-subtree walk if `Files` is absent — old lock), then remove the store dir as
  today. Re-run `Rebuild` is the safety net.
- Materialization failure is non-fatal to the store (store already written) but surfaces a clear error;
  `fglpkg relink` recovers.

### Phase 5 — `internal/env` rewrite

Rewrite [`buildFGLLDPATH`](../internal/env/env.go#L166) (the `addPackagesFrom` closure at
[L178](../internal/env/env.go#L178)); `GenerateGlobal`/`GenerateGST`/`GenerateLocal` inherit it:

- Per scope, **local first**: if the scope's `.fglpkg/merged` exists and is non-empty, emit **it**
  (one entry) instead of one-per-package.
- Additionally emit a per-package entry for each **legacy flat** package (no `generoPackages`, no merge)
  — preserving today's behavior for that shape only.
- Workspace member source dirs unchanged (step 1).
- Consequences: GIS-358 (`name == PACKAGE`) resolves because the merged path is namespace-correct;
  GIS-359 silent shadowing becomes the Phase 3 hard error.

### Phase 6 — `relink` command, gitignore, docs

- **`fglpkg relink`** — rebuild the merged root(s) for the current/both scopes; idempotent; add to the
  command registry + dispatch + completion (and `TestRegistryMatchesDispatch` stays green).
- **`.gitignore` templates** ([internal/cli/templates.go](../internal/cli/templates.go)): add
  `.fglpkg/merged/`.
- **Docs**: README cheat-sheet (`relink`) + user-guide note on the merged root and the
  hard-link/copy/disk trade-off.

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
