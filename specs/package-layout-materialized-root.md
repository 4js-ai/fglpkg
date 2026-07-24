# Spec: PACKAGE-aware install layout — slug store + materialized merged root

**Status:** 📋 Not started — spec ready, key decisions resolved 2026-07-24 (see Decisions); engineering plan in [package-layout-materialized-root-plan.md](package-layout-materialized-root-plan.md) (ticket: [GIS-346](https://4js.atlassian.net/browse/GIS-346))
**Date:** 2026-07-22
**Author:** Mike Folcher
**Motivation:** `fglpkg` installs each package into its own `.fglpkg/packages/<name>/` directory and
[`fglpkg env`](../internal/env/env.go#L166) puts **one `FGLLDPATH` entry per package** on the path.
For packages that use the Genero `PACKAGE` instruction this defeats the point of `PACKAGE` ("one
top-dir serves all"): N packages → N `FGLLDPATH` entries, which (a) is slow to resolve at scale,
(b) mis-resolves in the `name == PACKAGE-root` case, and (c) turns a shared-namespace mistake into
a silent, order-dependent wrong-module load. This spec keeps the per-package store **and** presents
a single **materialized merged root** (per scope) on `FGLLDPATH`.
**Credit:** Leo Schubert (GIS-346, and the MS Teams thread 2026-07-21). Leo's GIS-346 comment
proposes a dedicated `.fglpkg/PACKAGEs_G/` merged root for `PACKAGE`-based packages — this spec is
the concrete, cross-platform realization of that idea.
**Related:** [GIS-358](https://4js.atlassian.net/browse/GIS-358) (`env` emits non-resolving path),
[GIS-359](https://4js.atlassian.net/browse/GIS-359) (PACKAGE-clash detection),
[GIS-298](https://4js.atlassian.net/browse/GIS-298) (webcomponent shared-tree clobber — related
failure mode, already hit in production, addressed separately below);
[import-root.md](import-root.md), [name-normalization.md](name-normalization.md),
[webcomponent-packages.md](webcomponent-packages.md).
**Evidence:** reproducible benchmark + materialization test in `~/tmp/fglpkg-fglldpath-perf/`
(`README.md`, `RESULTS.md`, `results/raw_m100.csv`, `results/raw_m200.csv`, `results/materialize.txt`).
*This harness should be relocated into a durable location (in-repo `perf/` or a named repo) and
re-cited — see Open questions.*

---

## Summary

Split the install model into two layers:

1. **Per-package store** (`.fglpkg/packages/<name>/`, unchanged on disk) — the pristine, per-package,
   source-of-truth tree. Keeps clean `add`/`remove`/`upgrade` (delete a directory), per-package
   metadata (`README.md`, `fglpkg.json`), and collision *containment*. Retained as-is.
2. **Materialized merged root** (`.fglpkg/merged/`, new — a derived, rebuildable cache, one **per
   scope**: local project and global) — the `PACKAGE` namespace trees (`com/fourjs/…`) of every
   `PACKAGE`-declaring package, assembled into one directory tree.
   [`fglpkg env`](../internal/env/env.go#L166) emits the merged root(s) — at most one per scope,
   local before global — instead of one entry per package.

The merged root is built with an **OS-independent** primitive: **hard-link each module, fall back to
copy** when hard-links are unavailable (cross-volume, FAT/exFAT, some network FS). **No symbolic
links** — they need admin/Developer-Mode on Windows (Leo's environment) and were rejected in the
Teams thread. The materialization pass is also where `PACKAGE`-namespace **clashes** are detected and
refused — resolving GIS-359.

This is a pure superset of today's behavior for the common case and a strict improvement at scale; it
is the "best of both" between today's isolated dirs (safe, clean removal) and Leo's single-top-dir
goal (fast, short `FGLLDPATH`).

## Decisions (resolved 2026-07-24)

The open decisions previously flagged below are now resolved (Mike Folcher; Leo's GIS-346 items
folded in). The rest of the document should be read in light of these:

1. **Namespace determination — recorded at publish, classified per module.** `fglpkg pack`/`publish`
   derives each package's `PACKAGE` namespace set by parsing the `PACKAGE` declaration from the
   shipped source modules, and records it (dots→slashes, e.g. `com/fourjs/db`) so the consumer knows
   the namespaces **without** re-reading source or guessing from directory shape. Classification is
   **per module, not per package**: a module declaring `PACKAGE x.y.z` is a namespaced library module
   (materialized into the merged root); a module with **no** `PACKAGE` — a `MAIN` program, test, or
   example (the manifest `programs` set) — is out-of-namespace and is **never merged** (it stays in
   the per-package store, runnable via `run`/`bdl`, which read the store directly, not `FGLLDPATH`).
   A single package may contain both (see the PackageB example under "Worked example"). Because
   materialization is driven by the recorded namespace **path** rather than the store-dir name, the
   GIS-358 `name == PACKAGE root` case resolves correctly with no special-casing.
2. **Automatic.** Materialization is default-on and transparent — no opt-in flag. The per-package
   store is untouched, so the change stays non-breaking and reversible (delete `.fglpkg/merged/`).
3. **One package per namespace (strict).** Within a scope, two distinct packages that declare the
   same `PACKAGE` namespace is a hard error naming both — no disjoint-file co-ownership.
4. **Lockfile records ownership.** Each `LockedPackage` records its `PACKAGE` namespace(s) and its
   materialized `.42m` file list, so removal and rebuild are O(manifest) rather than a filesystem walk.

Also settled by "do what's in the file": keep the per-package store and **materialize over it** — not
the delete-the-slug-layer alternative.

The concrete engineering plan for these decisions lives in
[package-layout-materialized-root-plan.md](package-layout-materialized-root-plan.md).

## Background — how it works today

### Install layout and `FGLLDPATH`

The installer routes each package's extracted tree into `.fglpkg/packages/<name>/`
([`destDir` at installer.go#L700](../internal/installer/installer.go#L700), using the package
`info.Name`; field [`installer.packagesDir` at L35](../internal/installer/installer.go#L35)). For
registry installs that name equals the registry slug, but canonicalization via
[`internal/slug`](../internal/slug/slug.go) happens in the resolver/registry/manifest/CLI layers —
**not** in the installer, which uses the raw name.

[`buildFGLLDPATH`](../internal/env/env.go#L166) then scans that directory and adds **every** package
subdirectory as its own entry ([`addPackagesFrom`](../internal/env/env.go#L178)):

```go
// internal/env/env.go — verbatim
addPackagesFrom := func(dir string) {
    if entries, err := os.ReadDir(dir); err == nil {
        for _, e := range entries {
            if e.IsDir() {
                add(filepath.Join(dir, e.Name()))
            }
        }
    }
}
```

So `N` installed packages produce an `FGLLDPATH` of `N` entries (local packages dir before global —
that ordering is a deliberate local-override feature, see the precedence note under Design). `fglpkg`
has **no `PACKAGE` awareness** — it never reads the `PACKAGE` instruction; it just lists directories.

### Genero module resolution (per the 6.00 manual)

`IMPORT FGL <package-path>.<module>` resolves to `root-path/package-path/module.42m` — a **loose
`.42m` file** under a `FGLLDPATH` root. `FGLLDPATH` is searched in listed order; the manual's full
precedence chain is cwd → program dir → `FGLLDPATH` entries → `$FGLDIR/lib` (see the
[FGLLDPATH](https://4js.com/online_documentation/fjs-fgl-manual-html/fgl-topics/c_fgl_EnvVariables_FGLLDPATH.html)
and [PACKAGE](https://4js.com/online_documentation/fjs-fgl-manual-html/fgl-topics/c_fgl_programs_PACKAGE.html)
manual pages). **There is no single-archive shortcut:** `.42x` libraries are legacy (deprecated in
favor of `IMPORT FGL`), the manual states the `.42x` file "does not contain the 42m p-code — you must
provide all compiled 42m modules," and `IMPORT FGL` PACKAGE-path resolution targets loose `.42m`
files, not `.42x`. So collapsing to one `FGLLDPATH` entry **requires a physical merged directory of
`.42m` files**.

### The problems this creates

| Problem | Ticket | What happens |
|---|---|---|
| **Length / performance** | GIS-346 | N packages → N `FGLLDPATH` entries; every lookup scans them in order. Leo's Tyler case: ~16k old-school modules ⇒ enormous path. |
| **`env` correctness** | GIS-358 | When install-dir name == `PACKAGE` root (`hello`/`PACKAGE hello`), `env` emits `.fglpkg/packages/hello` but the module only resolves from the parent `.fglpkg/packages`. |
| **Silent clash** | GIS-359 | Two packages sharing a namespace both land on `FGLLDPATH`; only the first resolves, silently and order-dependently. |
| **Shared-tree clobber** | GIS-298 | Webcomponents install into `.fglpkg/webcomponents/` with shared `com/`, `examples/`, `docs/` trees; a 2nd install silently drops one. Related failure mode; addressed separately (webcomponents are not on `FGLLDPATH`). |

## Evidence

Three reproducible experiments back this design (harness + docs in `~/tmp/fglpkg-fglldpath-perf/`;
Genero 6.00.01, macOS/APFS warm SSD, medians):

1. **`FGLLDPATH` length has a real, linear cost** (`RESULTS.md`, `results/raw_m100.csv`): module
   lookup grows at **~1.2 µs per entry per module**, at both `fglcomp` (compile) and `fglrun`
   (startup) — one-time per process, not per call.

   | FGLLDPATH entries | many-dirs (A) | merged 1-entry (B) |
   |--:|--:|--:|
   | 1   | 16.1 ms | 15.6 ms |
   | 25  | 18.1 ms | 16.2 ms |
   | 100 | **26.2 ms** | **16.1 ms** |

   The merged (1-entry) layout is **flat regardless of package count**; the growth in A is entirely
   `FGLLDPATH`-entry scanning. Doubling the module count doubles the delta (`raw_m200.csv`), so
   cost ≈ `entries × modules × ~1.2 µs`. *Mechanism inference (not directly swept):* since
   resolution is a direct constructed-path `stat` (`root/package-path/module.42m`), it does not
   linearly scan a directory's file list — so **directory count on the path**, not files-per-dir,
   is what scales. (A dedicated files-per-dir sweep would confirm this empirically.)

2. **`PACKAGE` prevents collisions across *distinct* namespaces, not same-namespace** (collision
   experiment, `~/4js-github/fglpkg` package-layout memo): distinct-namespace packages coexist and
   resolve **order-independently**, even in today's per-package-dir layout. Two packages claiming the
   **same** namespace resolve by `FGLLDPATH` order, silently — GIS-359, not fixed by `PACKAGE` alone.

3. **A hard-linked merged root works and collapses the N-entry cost** (`results/materialize.txt`):

   | case | entries | `fglrun` startup (median) |
   |---|--:|--:|
   | per-package store (today) | 100 | ~26 ms |
   | merged, hard-linked | 1 | ~18 ms |
   | merged, copied | 1 | ~17.5 ms |

   Both merged variants collapse the ~26 ms N-entry cost to a single flat entry (~17–18 ms, a
   >8 ms win). Hard-link vs copy differ by ~0.5–1.5 ms — within run noise at 25 iterations (copy
   marginally faster) — i.e. the win is the single entry, and the primitive choice is a disk-space,
   not a speed, decision. (This is a separate, slightly higher-floor run than #1, so treat it as
   "collapses to the flat curve," not a same-run comparison against 16.1 ms.)

## Design

### Which packages are mergeable

Classification is **per module** (see Decisions §1), not per package:

- **Namespaced library modules** — a module declaring `PACKAGE x.y.z` ships under its namespace
  subtree (`com/fourjs/<vendor>/<pkg>/…`). The namespace makes it safe to merge into one shared root;
  these are materialized.
- **Out-of-namespace modules** — `MAIN` programs, tests, and examples (the manifest `programs` set)
  declare no `PACKAGE`. They are **never merged**; they stay in the per-package store and remain
  runnable via `run`/`bdl` (which read the store directly, not `FGLLDPATH`).
- **Legacy flat library modules** — a bare-`IMPORT FGL foo` library module with no namespace and no
  `MAIN`. These have nowhere collision-free to merge, so they **keep a per-package `FGLLDPATH` entry**
  (unchanged). This is the discouraged shape; packaging validation (GIS-358) nudges publishers toward
  `PACKAGE`.

A single package legitimately mixes these (PackageB below: a namespaced `com.fourjs.db` library plus
out-of-namespace `MAIN` test/example programs). `fglpkg` gains `PACKAGE` awareness (see Implementation)
and records, per package, the set of `PACKAGE` namespaces its modules declare, so `env`,
materialization, and clash detection agree without rescanning. This matches Leo's GIS-346 comment: a
merged root for `PACKAGE` modules, plus per-dir entries for old-school module packages.

### Materialization primitive (OS-independent)

```
link_or_copy(src, dst):
    try:    os.link(src, dst)      # hard link — Linux, macOS, Windows/NTFS; no privilege
    except OSError:                # cross-volume (EXDEV), FAT/exFAT, some network FS
            copy(src, dst)
```

- **No symlinks** (Windows admin/Dev-Mode requirement; Leo's constraint).
- Hard-links require the **same volume**; place a scope's `merged/` next to its `packages/`. **Cross-
  volume is expected** when a project on one volume pulls a `-g` global package from `~/.fglpkg` on
  another (e.g. project on an external drive, home on the internal disk) — the per-file copy fallback
  handles it transparently (correct, same speed, more disk).
- **Scope materialization to `.42m`** (what `FGLLDPATH` resolves). Other artifact types a package may
  ship — forms `.42f`, schemas `.sch`, C extensions, images — are located through their own runtime
  search paths (e.g. `FGLIMAGEPATH`, which `env` already emits), **not** `FGLLDPATH`; materializing
  them into an `FGLLDPATH`-only root would not make them resolvable. Handling those through the merged
  root (and whether `env` must point their path vars at it) is follow-up (see Open questions).

### Per-scope merged roots & precedence

A single global merged root cannot express the current **local-over-global** override
([`buildFGLLDPATH`](../internal/env/env.go#L166) lists local `.fglpkg/packages` before the global
one, so a locally-installed package legitimately shadows a global one). Therefore build **one merged
root per scope** — local `.fglpkg/merged` and global `~/.fglpkg/merged` — and emit them **local
first** (at most two entries + any flat-package entries). A local package shadowing a global one of
the same namespace is **precedence, not a clash**; clash detection runs **within** each scope, not
across scopes.

### Clash detection = GIS-359 (namespace granularity)

GIS-346 asks to "detect PACKAGE clashes and bail out"; GIS-359 asks to refuse "like the registry
name-collision guard, naming both packages." So detect at **`PACKAGE`-namespace ownership**
granularity, not merely identical file paths: within a scope, if **two distinct packages declare the
same `PACKAGE` namespace**, that is a clash → **hard error naming both packages**, in the spirit of
the registry guard ([`collisionError`](../internal/provider/repositoryset.go#L285), wrapping
[`resolver.ErrCollision`](../internal/provider/repositoryset.go#L313)). File-path overlap is a subset
and is caught too.

> **Resolved (Decisions §3): strict "one package per `PACKAGE` namespace."** Two distinct packages
> declaring the same namespace is a hard error naming both — even if their modules are disjoint.
> Disjoint-file co-ownership was rejected as fragile: a later version of one package adding a module
> the other already provides would turn a benign shared namespace into a hard error at an upgrade far
> from the original install.

### Clean removal (requires explicit file ownership — not free today)

Today removal is clean only because a package's files live under its own `.fglpkg/packages/<name>/`
dir (`rm -rf`). **The lockfile does *not* record extracted files** — `LockedPackage`
([internal/lockfile/lockfile.go](../internal/lockfile/lockfile.go)) stores
name/version/downloadUrl/checksum/requiredBy/scope only. So the merged model needs explicit ownership:

- **Primary:** at `remove`, derive the owned set by walking the package's per-package store subtree
  and unlink the corresponding paths from the scope's merged root (then prune now-empty namespace
  dirs). The store *is* the ownership record; no new lock field strictly required.
- **Optimization:** record the materialized file list per package (in the lockfile or a sidecar) to
  make removal and rebuild O(manifest) instead of a filesystem walk. **This is mandatory to the
  design, not optional** if the merged root is to be maintained incrementally.

## Worked example

**`sample-a` (GIS-346's headline: fglpkg name ≠ PACKAGE name).** Package name `sample-a`, its module
declares `PACKAGE a`.

*Today:* installs to `.fglpkg/packages/sample-a/a/…`; because the namespace root `a/` sits **inside**
`sample-a/`, `FGLLDPATH` must contain `.fglpkg/packages/sample-a` — one entry per package. Ten such
packages ⇒ ten entries.

*After:* the `a/…` namespace tree is materialized into the scope merged root
(`.fglpkg/merged/a/…`); `env` emits the single `.fglpkg/merged` entry that serves `sample-a`,
`sample-b`, … together. `IMPORT FGL a.<module>` resolves against `.fglpkg/merged/a/<module>.42m`.

**`hello` (GIS-358: fglpkg name == PACKAGE root).** Name `hello`, `PACKAGE hello`, module `hello`.
*Today:* `env` emits `.fglpkg/packages/hello`, but `IMPORT FGL hello.hello` needs
`.fglpkg/packages/hello/hello/hello.42m` → fails; only the parent `.fglpkg/packages` resolves.
*After:* materialized as `.fglpkg/merged/hello/hello.42m`; the single merged entry resolves it. The
flat, non-`PACKAGE` case (module directly under the package dir) keeps its own per-package entry.

## Implementation sketch

- **PACKAGE awareness** (Decisions §1): at `pack`/`publish`, parse the `PACKAGE` declaration from each
  shipped source module to derive the package's namespace set, and persist it in the artifact's own
  `fglpkg.json` (a computed `packages` field) so it travels with the package — the consumer reads it
  post-extract and records it in the lockfile, needing **no** registry-backend change and no source or
  directory-shape guessing. Modules with no `PACKAGE` (the `programs`/MAIN set) are recorded as
  out-of-namespace and excluded from the merged root.
- **`internal/env`**: replace [`addPackagesFrom`](../internal/env/env.go#L178) with logic emitting the
  per-scope merged root(s) (local before global) + one entry per non-`PACKAGE` flat package.
- **New materialize step** (in `installer` or a new `internal/materialize`): build/refresh a scope's
  `merged/` via `link_or_copy`; detect namespace clashes and return an error naming both packages;
  idempotent; exposed as a `fglpkg relink` command and invoked after install/remove/update.
- **`internal/installer`**: after extraction, trigger materialization; on `remove`, unlink the
  package's owned files (see Clean removal) and prune empty namespace dirs.
- **`fglpkg.lock`**: record each package's `PACKAGE` namespace(s) and (optimization) its materialized
  file list.
- **Webcomponents (GIS-298) — separate fix, not a side effect.** Webcomponents don't use `PACKAGE`,
  aren't on `FGLLDPATH`, and are served to GBC/GAS from `.fglpkg/webcomponents/`, so the merged-root
  mechanism does not apply. Adopt GIS-298's own remedy: install each webcomponent package in its own
  subdirectory; where a support tree (`com/`, `examples/`, `docs/`) is *legitimately* shared, treat
  byte-identical files as **dedup**, and only **non-identical** collisions as an error (never silently
  drop). Reuse only the per-package-store + collision-detection principle.

## Test plan

- Hard-link path **and** a forced cross-volume copy fallback (`EXDEV`) both produce a resolvable
  merged root.
- Installing K `PACKAGE` packages yields one merged `FGLLDPATH` entry per scope (+ flat entries), and
  every `IMPORT FGL <pkg>.<mod>` resolves with no manual override.
- `sample-a` (name ≠ PACKAGE) and `hello` (name == PACKAGE root) both resolve from the merged entry.
- Two packages declaring the same `PACKAGE` namespace → error naming both (GIS-359); no silent drop.
- `remove` one package leaves the others resolvable; empty namespace dirs pruned.
- Local package shadowing a global one of the same namespace → local wins (precedence), no false clash.
- Non-`PACKAGE` flat packages unchanged.
- Runs on Linux, macOS, and Windows with no elevated privileges (hard-link where possible, copy
  otherwise). Re-run the perf harness on Windows/NTFS to record the (expected larger) magnitude.

## Acceptance criteria

- `FGLLDPATH` for `PACKAGE` packages collapses to one entry per scope; `fglcomp`/`fglrun` lookup cost
  no longer grows per package (GIS-346).
- `hello` and `sample-a` resolve out of the box (GIS-358).
- Same-namespace install produces a clear error naming both packages, not silent shadowing (GIS-359).
- Removing one package leaves the others intact and resolvable.
- No elevated privileges on any platform; verified with hard-link and copy fallback.
- Non-`PACKAGE` flat packages behave exactly as today.

## Risks & rollout

- **Migration:** existing installs re-materialize lazily on the next `install`/`env`, or eagerly via
  `fglpkg relink`. The per-package store is untouched, so the change is non-breaking and reversible
  (delete `.fglpkg/merged/` to fall back).
- **Disk cost:** the copy fallback (cross-volume globals) roughly doubles on-disk size of materialized
  modules for those packages; hard-links cost nothing. Call this out for users with many global
  `PACKAGE` packages on a separate volume.
- **`.gitignore`:** `.fglpkg/merged/` is a derived cache — add it to the ignore templates.
- **Automatic (Decisions §2):** materialization is default-on and transparent (the store is
  untouched); no opt-in flag. `fglpkg relink` is available to force an eager rebuild.

## Out of scope / open questions

*(Resolved and moved to Decisions: delete-the-slug-layer alternative → keep store + materialize;
clash granularity → strict one-per-namespace; automatic vs opt-in → automatic.)*

- **Non-`.42m` artifact types** (`.42f`, `.sch`, C extensions): confirm each type's runtime search
  path and whether `env` should point it at the merged root.
- **Merged-root scoping** beyond local/global (e.g. workspace members).
- **Meta-packages** (`com.fourjs` parent with installable children) — Leo's larger idea; a registry +
  manifest modeling change, tracked separately.
- **Evidence durability:** relocate the `~/tmp/fglpkg-fglldpath-perf/` harness in-repo (`perf/`) or to
  a named repo and re-cite.
- **Windows magnitude:** the perf numbers are macOS/APFS warm-cache; the *shape* (linear; flat when
  merged) is platform-independent, but the per-entry cost is expected larger on Windows/NTFS +
  Defender and networked FS.
