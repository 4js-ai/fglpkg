# Spec: webcomponent install & remove — ownership-tracked artifacts (GIS-298, GIS-372)

**Status:** ✅ Implemented — branch `fix/gis-298-webcomponent-collisions` (this repo). Two commits:
GIS-298 (install-side clobber) then GIS-372 (remove-side orphan).
**Date:** 2026-07-23
**Author:** Mike Folcher
**Tickets:** [GIS-298](https://4js.atlassian.net/browse/GIS-298) (install clobber) and
[GIS-372](https://4js.atlassian.net/browse/GIS-372) (remove leaves artifacts) — both Bug, Severity
High. Same subsystem (`internal/installer` webcomponent extraction), same underlying gap:
webcomponent artifacts under `.fglpkg/webcomponents/` were not owned by any package.
**Related:** [package-layout-materialized-root.md](package-layout-materialized-root.md) (both are
called out there as *separate* failure modes — webcomponents are not on `FGLLDPATH`, so the
merged-root mechanism does not apply).

---

## Problem

Installing a second webcomponent package that shares a top-level tree with the first silently
**drops** the first package's files, while every install still reports success (exit 0, green ✓,
listed in `fglpkg.lock`). Reproduced on Win 11 / Genero 6.00.03: after `fjs-map` then
`fjs-data-grid`, only one widget's `com/fourjs/…` module and `examples/` survive, so
`IMPORT FGL com.fourjs.datagrid.DataGrid` won't compile — with no visible cause. Worst in the
AI-agent flow, where the agent chains installs, sees ✓ each time, and reports success.

## Root cause

`installWebcomponent` → `extractWebcomponentZip` (`internal/installer/installer.go`). The old code
inferred "component dirs" as **every** top-level directory in the zip and ran
`os.RemoveAll(destDir/<top>)` on each before extracting. Webcomponent widgets ship, alongside their
`COMPONENTTYPE/` bundle, shared support trees (`com/`, `examples/`, `docs/`). So installing the
second widget `RemoveAll`'d the shared `com/`, `examples/`, `docs/` — wiping the first widget's
content under them — then wrote only its own. The per-widget `COMPONENTTYPE/` dirs (`Map/`,
`DataGrid/`) are distinct, so they survived; only the shared trees were clobbered. Exactly the
report.

The mixed-package path (`extractZipRouted`, used by BDL packages that also ship a webcomponent) is
**not affected**: it diverts only the manifest-declared COMPONENTTYPE dirs into
`.fglpkg/webcomponents/` and routes all other content (incl. `com/`, `examples/`) into the
package's own `.fglpkg/packages/<name>/`, so there is no cross-package overlap there.

## Why not "install each package in its own subdirectory"

The ticket floats per-package subdirs as the cleaner fix, and
[package-layout-materialized-root.md](package-layout-materialized-root.md) echoes it. But it
collides with webcomponent **discovery**: `internal/env/env.go` points `FGLIMAGEPATH` at the
**parent** of `.fglpkg/webcomponents/` so Genero's direct-mode rule
`<fglimagepath-dir>/webcomponents/<COMPONENTTYPE>/<COMPONENTTYPE>.html` resolves
(`buildFGLIMAGEPATH`, env.go:72-97), and `GenerateGWA` emits one `--webcomponent` flag per
`COMPONENTTYPE` dir found **directly** under `webcomponents/` (env.go:129-156). Moving components to
`.fglpkg/webcomponents/<pkg>/<COMPONENTTYPE>/` breaks both unless `env` also changes to emit one
entry per package dir — a wider change than this High-severity bug warrants. So this fix keeps the
flat `webcomponents/<COMPONENTTYPE>/` layout and makes extraction **collision-aware** instead.

## Fix

`extractWebcomponentZip(zipPath, destDir, componentTypes)` now takes the package's declared
COMPONENTTYPE list (`componentTypes`, from the manifest `webcomponents` field via
`readWebcomponentsFromZip`, already used by the mixed path). Two classes of top-level dir:

- **Owned** — the declared COMPONENTTYPE dirs. These are the package's own bundles; they are
  `RemoveAll`'d before extraction so a reinstall/upgrade replaces stale files cleanly (unchanged
  behavior, but now scoped to *owned* dirs only).
- **Shared** — any other top-level tree (`com/`, `examples/`, `docs/`, …). Merged, never removed:
  - absent on disk → written;
  - present with **byte-identical** content → left as-is (**dedup** — a legitimately shared,
    identical support file);
  - present with **different** content → **hard conflict**.

Conflicts are detected in a **first pass before any disk write**, so a clash aborts the whole
install (non-zero exit) naming every offending path and **touches nothing** — no silent clobber, no
silent drop, no partial write. Zip-root files (the manifest, stray root docs) are still skipped.

Implementation: `internal/installer/installer.go` — `installWebcomponent` reads and passes
`componentTypes`; `extractWebcomponentZip` rewritten (two-pass: detect-then-write); new
`fileMatchesZipEntry` helper (byte compare vs on-disk).

## Tests (`internal/installer/webcomponent_test.go`)

- **SharedTreeNoClobber** (the regression): install `fjs-map` then `fjs-data-grid`; all files of
  both survive.
- **DedupIdentical**: two packages ship an identical `docs/LICENSE.txt`; second install succeeds,
  file intact.
- **ConflictErrors**: two packages ship `docs/README.md` with different content; second install
  errors, the first's file is untouched, and the second's own bundle is not partially written.
- **ReinstallCleansOwned**: reinstalling a package drops a file removed in the new version (owned
  COMPONENTTYPE dir still cleaned).

## Acceptance criteria (met)

- Installing two widgets that share top-level trees keeps both widgets' files; `IMPORT FGL` for
  each resolves.
- A real (different-content) collision fails loudly with a non-zero exit and names the files;
  nothing is silently dropped or overwritten.
- Byte-identical shared files dedup without error.
- Reinstall/upgrade of a package still cleans its own stale component files.

## Removal (GIS-372)

`fglpkg remove` deleted a package's `.fglpkg/packages/<name>/` dir and its JARs but **never
touched `.fglpkg/webcomponents/`**, so a removed widget's bundle was orphaned there
(`pruneToPlan`, installer.go — its old comment even said webcomponents "are not pruned … there is
no reliable way to know which bundle belonged to a removed package"). Root cause is the same as
GIS-298's: the webcomponents layout is keyed by `COMPONENTTYPE`, not package name, and the resolver
`Plan` does not carry the `COMPONENTTYPE` list (it lives in the package manifest, read only at
install time).

**Fix — a per-scope ownership sidecar** (`internal/installer/webcomponent_owners.go`):

- `webcomponent-owners.json` sits **next to** each scope's `webcomponents/` dir (local
  `.fglpkg/` and global `~/.fglpkg/`), *not inside* it — so it is never mistaken for a
  `COMPONENTTYPE` dir by `FGLIMAGEPATH`/GWA discovery. It maps `package → [slash-relative files it
  installed under webcomponents/]`.
- **Written at install:** both extraction paths now return the files they wrote under
  `webcomponents/` — `extractWebcomponentZip` (pure webcomponent packages) and `extractZipRouted`
  (mixed BDL+webcomponent packages) — and `install*` calls `recordWCOwnership`. A file deduped at
  install (GIS-298) is still recorded as owned by the deduping package, so shared files are
  **co-owned**.
- **Consumed at remove:** `pruneToPlan` computes the set of packages whose webcomponent artifacts
  should remain — `wantWC` is **every** name in `plan.Packages`, not just the ones with a
  `"webcomponent"` variant — and calls `pruneWebcomponents`, which deletes every file owned *only*
  by now-absent packages, keeps any file a remaining package still owns, prunes emptied directories,
  and rewrites (or removes) the sidecar. Listing pure-BDL packages in `wantWC` is harmless (they own
  no sidecar entries), and it is what makes mixed packages safe: a mixed package resolves to a
  `genero<N>` variant, so gating `wantWC` on `IsWebcomponent()` silently deleted the bundle of a
  still-installed mixed package on any unrelated `remove` (#41).

This is the file-level ownership index that GIS-298 and
[package-layout-materialized-root.md](package-layout-materialized-root.md) flagged as needed;
building it for removal also narrows GIS-298's same-`COMPONENTTYPE` edge (ownership is now recorded,
even if the install-time clash guard doesn't yet consult it).

**Tests** (`internal/installer/webcomponent_owners_test.go`): remove-prunes-owned (the GIS-372
regression, incl. emptied namespace dirs), co-owned file survives until its last owner is removed,
and no-sidecar (pre-fix install) prunes as a safe no-op. Those pass a `wantWC` in directly, so
`TestPruneToPlanKeepsMixedPackageWebcomponents` (`reconcile_test.go`) covers the construction of it:
a mixed package installed through `extractZipRouted` + `recordWCOwnership` keeps its bundle when an
unrelated package is removed (#41).

## Known limitations / out of scope

- **Same COMPONENTTYPE name from two packages (install-time).** If two packages each *declare* the
  same COMPONENTTYPE (e.g. both ship `Map/`), the second still clears-and-replaces it at install —
  last-writer-wins. The ownership index now added for removal (GIS-372) *records* the clash but the
  install-time guard does not yet consult it to refuse the second install; wiring that in is a small
  follow-up. It is a genuine "same component from two sources" case, distinct from the
  shared-support-tree clobber GIS-298 reported.
- **In-place upgrade of a *shared* file for a package that declares no COMPONENTTYPEs.** With an
  empty `componentTypes`, every tree is treated as shared, so a shared file whose content changed
  between versions would report a conflict on reinstall rather than upgrading in place; the remedy
  is `fglpkg remove` then install. Packages that declare their `webcomponents` (the norm) are
  unaffected. A full fix would have the install guard consult the ownership index (now written for
  GIS-372) to treat "a file I already own" as an upgrade rather than a conflict.
- **Publisher-side guidance (ticket option b):** widgets namespacing their shared trees per package
  (`examples/<pkg>/`, `docs/<pkg>/`; `com/fourjs/<pkg>/` is already collision-free) removes the
  overlap at the source. Complementary to this client-side guard; tracked with the widget packages,
  not here.
