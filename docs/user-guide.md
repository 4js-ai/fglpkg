# fglpkg User Guide

This guide covers the day-to-day usage of fglpkg, the package manager for Genero BDL projects.

## Table of Contents

- [Getting Started](#getting-started)
- [Installing fglpkg](#installing-fglpkg)
- [Keeping fglpkg Up to Date](#keeping-fglpkg-up-to-date)
- [Setting Up Your Environment](#setting-up-your-environment)
- [Creating a New Project](#creating-a-new-project)
- [Managing Dependencies](#managing-dependencies)
- [Publishing a Package](#publishing-a-package)
- [Deprecating & Relocating Packages](#deprecating--relocating-packages)
- [Working with Java JARs](#working-with-java-jars)
- [Webcomponent Packages](#webcomponent-packages)
- [Distributable Scripts](#distributable-scripts)
- [Lifecycle Hooks](#lifecycle-hooks)
- [Package Documentation](#package-documentation)
- [Registry Authentication](#registry-authentication)
- [Secondary Repositories (JFrog Artifactory)](#secondary-repositories-jfrog-artifactory)
- [Workspaces (Monorepos)](#workspaces-monorepos)
- [Lock Files](#lock-files)
- [Package Signature Verification](#package-signature-verification)
- [Package Ownership](#package-ownership)
- [Troubleshooting](#troubleshooting)

---

## Getting Started

fglpkg manages three kinds of assets for your Genero BDL projects:

- **BDL packages** — compiled Genero modules (`.42m`, `.42f`, `.sch` files) published to a registry
- **Java JARs** — Java libraries downloaded from Maven Central (or custom URLs), needed when your BDL code calls into Java
- **Webcomponent packages** — html/css/js bundles published under a `COMPONENTTYPE` name and consumed by `WEBCOMPONENT` form fields. See [Webcomponent Packages](#webcomponent-packages).

## Installing fglpkg

### Download a Pre-built Binary

Visit the [Releases page](https://github.com/4js-mikefolcher/fglpkg/releases) and download the binary for your platform:

| Platform | Binary |
|---|---|
| Linux (Intel) | `fglpkg-linux-amd64` |
| Linux (ARM) | `fglpkg-linux-arm64` |
| macOS (Apple Silicon) | `fglpkg-darwin-arm64` |
| macOS (Intel) | `fglpkg-darwin-amd64` |
| Windows (Intel) | `fglpkg-windows-amd64.exe` |
| Windows (ARM) | `fglpkg-windows-arm64.exe` |

Place the binary in a directory on your `PATH`:

```bash
# macOS / Linux
sudo cp fglpkg-darwin-arm64 /usr/local/bin/fglpkg
sudo chmod +x /usr/local/bin/fglpkg
```

```powershell
# Windows — copy to a directory in your PATH
copy fglpkg-windows-amd64.exe C:\tools\fglpkg.exe
```

Verify the installation:

```bash
fglpkg version
```

### Build from Source

If you have Go installed:

```bash
git clone https://github.com/4js-mikefolcher/fglpkg.git
cd fglpkg
go build -o fglpkg ./cmd/fglpkg
sudo cp fglpkg /usr/local/bin/
```

## Keeping fglpkg Up to Date

fglpkg can update itself, so you don't have to re-download and re-copy the binary by hand.

```bash
fglpkg self-update            # download, verify, and install the latest release
fglpkg self-update --check    # report whether a newer version exists, then exit
fglpkg self-update --yes      # skip the confirmation prompt (for scripts)
fglpkg self-update --force    # reinstall even if already on the latest version
```

**What it does.** `self-update` asks the registry for the latest stable release, downloads the
build for your OS and architecture, and — before installing anything — verifies:

1. an **Ed25519 release signature** chained to a root key pinned inside your fglpkg binary
   (authenticity — the release really came from the fglpkg maintainers), and
2. the **SHA-256 checksum** of the download (integrity — it arrived intact).

Only then does it atomically replace the running executable. If either check fails it aborts
without touching your installed binary and prints a manual-download link with instructions.

Scope is deliberately narrow — **latest stable only**: no version pinning, no pre-release
channel, and no downgrade. The only manual recovery path is the download link shown on failure.

**When it won't run.** Self-update refuses on a `dev` build (one you built from source) and on an
install that looks managed by a package manager such as Homebrew — update those the way you
installed them.

### Update notices

fglpkg also tells you, passively, when a newer version is out. After a command finishes, it
prints a short notice to standard error:

```
─────────────────────────────────────────────
 A new fglpkg is available: 3.8.0 → 3.9.0
 Run 'fglpkg self-update' to upgrade.
─────────────────────────────────────────────
```

The notice appears on every command while a newer release is known, and comes from a remembered
answer — so it is instant and works offline. fglpkg asks the registry at most once every 24 hours,
in the background, and that refresh never blocks your command, changes its exit code, or reports
network errors. The whole feature is automatically silent for `dev` builds, in CI, and when output
is piped or redirected (not an interactive terminal).

To turn it off, set `FGLPKG_NO_UPDATE_CHECK=1`, or configure it in `~/.fglpkg/config.json`:

```json
{
  "updateCheck": false,
  "updateCheckInterval": "24h"
}
```

`updateCheckInterval` is a Go duration string (e.g. `"12h"`, `"48h"`); the default is `24h`.
fglpkg records the last check time and last seen version in `~/.fglpkg/update-check.json`, a
file it manages automatically — separate from your hand-edited `config.json` settings.

## Setting Up Your Environment

fglpkg manages the Genero environment variables so the runtime can find everything an installed package
ships — modules, JARs, forms and styles, schemas, images, and configuration.

### macOS / Linux

Add to your shell profile (`~/.bashrc`, `~/.zshrc`, or equivalent):

```bash
eval "$(fglpkg env --global)"
```

Then reload your shell:

```bash
source ~/.bashrc
```

Use `--global` in your shell profile so it always includes all globally installed packages, regardless of your current directory.

### Windows (cmd.exe)

Run before building, or add to a `setup-env.bat` script:

```cmd
@echo off
FOR /F "tokens=*" %%i IN ('fglpkg env --global') DO %%i
```

To run directly at the command prompt (single `%`):

```cmd
FOR /F "tokens=*" %i IN ('fglpkg env --global') DO %i
```

On Windows, `fglpkg env` outputs `SET` commands:

```
SET FGLLDPATH=C:\Users\you\.fglpkg\merged;%FGLLDPATH%
SET CLASSPATH=C:\Users\you\.fglpkg\jars\poi-5.2.3.jar;%CLASSPATH%
SET FGLRESOURCEPATH=C:\Users\you\.fglpkg\packages\poiapi\com\fourjs\poiapi;%FGLRESOURCEPATH%
```

### Genero Studio

Run `fglpkg env --gst` and paste the output into your project's environment variable settings:

```
FGLLDPATH=$(ProjectDir)/.fglpkg/merged;$(FGLLDPATH)
CLASSPATH=$(ProjectDir)/.fglpkg/jars/poi-5.2.3.jar;$(CLASSPATH)
FGLRESOURCEPATH=$(ProjectDir)/.fglpkg/packages/poiapi/com/fourjs/poiapi;$(FGLRESOURCEPATH)
```

Genero Studio translates `$(ProjectDir)` to the actual project path and `;` to the platform-specific separator automatically.

### Environment Output Modes

`fglpkg env` varies its output depending on context and flags:

| Command | Scope | Format | Use case |
|---|---|---|---|
| `fglpkg env` | Auto (local if in project) | Shell (Unix) or SET (Windows) | Project-specific builds |
| `fglpkg env --global` | All global packages | Shell (Unix) or SET (Windows) | Shell profile setup |
| `fglpkg env --local` | Local `.fglpkg/` only | Shell (Unix) or SET (Windows) | Force local scope |
| `fglpkg env --gst` | Local `.fglpkg/` only | Genero Studio format | Genero Studio projects |

Key points:
- Existing values are preserved — fglpkg **prepends** its paths, never replaces them
- Installed packages are exposed through a single merged `FGLLDPATH` entry (see below)
- All downloaded JARs are added to `CLASSPATH`
- A variable is emitted **only** when a package actually ships matching files, so you never get an
  empty export
- Diagnostics (see [Resource collisions](#resource-collisions)) go to **stderr**, so
  `eval "$(fglpkg env)"` stays safe and `--gst` output stays a strict `VAR=value` list

### Which variables are emitted

| Variable | Resolves | Emitted as | Populated from |
|---|---|---|---|
| `FGLLDPATH` | program modules (`*.42m`, `*.42r`, `*.42x`) | the merged root, plus any uncovered store dir | see [the merged FGLLDPATH root](#the-merged-fglldpath-root) |
| `CLASSPATH` | Java JARs | one entry per `*.jar` | `.fglpkg/jars/` |
| `FGLRESOURCEPATH` | program resources (`*.42f`, `*.42s`, `*.4ad`, `*.4st`, `*.4sm`, `*.4tb`, `*.4tm`, `*.iem`) | directories | every directory in a package that holds one |
| `FGLDBPATH` | database schemas (`*.sch`, `*.val`, `*.att`) | directories | every directory in a package that holds one |
| `FGLIMAGEPATH` | webcomponents, images (`*.png`, `*.jpg`, `*.jpeg`, `*.gif`, `*.svg`, `*.bmp`, `*.ico`, `*.tiff`), icon fonts (`*.ttf`) | directories | `.fglpkg/` (for webcomponents) + every image directory in a package |
| `FGLPROFILE` | configuration | **files**, not directories | each package's declared [`profile`](#profile) entries |

The file kinds above are the complete lists from the Genero reference manual, not a
sample. Three are easy to overlook when packaging:

- **`.iem`** — compiled message files (`fglmkmsg`), used by `OPTIONS HELP FILE`. Neither
  `fglcomp` nor `fglform` produces one, so it is often missing from build scripts.
- **`.val` / `.att`** — a Genero database schema is *three* files, not one. Both are legacy
  (backward compatibility only), but `fgldbsch` still emits them and they resolve through
  `FGLDBPATH`.
- **`.ttf`** — with an image-to-font-glyph mapping file, a bare image name resolves to a
  glyph in a font file, and Genero requires that font's directory on `FGLIMAGEPATH`.

One gap fglpkg cannot close automatically: `FGLIMAGEPATH` accepts *file* entries as well as
directories — an [image-to-font-glyph mapping file](https://4js.com/online_documentation/fjs-fgl-manual-html/fgl-topics/c_fgl_EnvVariables_FGLIMAGEPATH.html)
tells Genero how to turn `"smiley"` into a glyph. Mapping files have no reserved extension
(the manual's own example is `image2font.txt`), so fglpkg cannot recognize one by sight. If
your package ships a custom icon map, its consumers must add it to `FGLIMAGEPATH` by hand.

Two shapes, and the difference matters:

- **`FGLLDPATH` is namespace-addressed.** A module is found by its `PACKAGE` path, which is why
  fglpkg can remap every installed module into one synthetic merged tree.
- **`FGLRESOURCEPATH`, `FGLDBPATH` and `FGLIMAGEPATH` are basename-addressed** and searched
  **non-recursively**. There is no namespace to remap, so fglpkg points them at the real directories
  holding the files — which means the *leaf* directory, not the package's store root. A package whose
  forms sit at `packages/poiapi/com/fourjs/poiapi/Customer.42f` puts exactly that directory on the
  path. It also means a package's store directory stays load-bearing for resources even after the
  merged root has taken it off `FGLLDPATH`.
- **`FGLPROFILE` is not a search path at all.** It is an ordered list of files: Genero loads and
  merges every one of them, and where the same entry key appears twice the **last file loaded wins**.
  That is the reverse of a search path, so fglpkg emits `FGLPROFILE` in the reverse order too — see
  [below](#resource-collisions). fglpkg lists package profiles *before* your existing value, so your
  own settings still override a package's defaults.

<a id="resource-collisions"></a>
### Resource collisions

Because resources are looked up by **basename**, two packages that both ship `Customer.42f` (or the
same `zoom.4st`, or a colliding `.sch`) shadow each other. Unlike a `PACKAGE` namespace clash, there
is nothing to disambiguate them with — so fglpkg cannot prevent it, and instead makes it **loud**:

```
$ eval "$(fglpkg env --local)"
warning: FGLRESOURCEPATH: "Customer.42f" is shipped by both "poiapi" (local) and "reportkit" (global).
  These variables are searched by basename, first match wins: the copy in
  /home/you/proj/.fglpkg/packages/poiapi/com/fourjs/poiapi will be used and reportkit's is shadowed.
  Rename one of the two files, or set FGLRESOURCEPATH yourself to pick a winner.
```

The order is deterministic, so the winner is reproducible rather than an accident:

1. **Local scope before global** — a project's own copy always wins.
2. **Packages in name order** within a scope.
3. **First on the path wins.**

`FGLPROFILE` keeps the same *precedence* but needs the opposite *order*. Because the last file loaded
wins rather than the first, listing local ahead of global would let a globally installed package
override the project's own — silently, since both files load without complaint. So fglpkg emits
profiles back to front: global before local, reverse name order within a scope. The winner is the
same package that would win a resource collision; only the position in the list is flipped. A
package's own `profile` array is *not* reversed — there, last-listed winning is what the author
already means.

Two more rules worth knowing:

- The **same package** installed both locally and globally is *precedence, not a clash*, and is not
  reported — the same rule that governs `PACKAGE` namespaces across scopes.
- One package shipping the same basename in **two of its own directories** is reported too; the second
  copy is unreachable.

Warnings never change the exit status and never appear on stdout.

> **Note** — the default `files` globs are `["*.42m", "*.42f", "*.sch"]`, so a package ships forms and
> schemas out of the box but **not** styles, action defaults or images. To ship those, list them
> explicitly: `"files": ["*.42m", "*.42f", "*.sch", "*.4st", "*.png"]`.

> Workspace member directories are **not** scanned for resources. A member is a *source* tree (`.per`,
> not `.42f`), and where its build drops compiled output is project-specific — so fglpkg would be
> guessing. Members still contribute to `FGLLDPATH` as before.

### The merged FGLLDPATH root

Each namespaced package declares a Genero `PACKAGE` (e.g. `PACKAGE com.fourjs.poiapi`). fglpkg records
those namespaces when the package is published, and at install time it builds a **merged root** —
`.fglpkg/merged` for a local project, `~/.fglpkg/merged` globally — where every namespaced `.42m` is
laid out by its namespace path (`com/fourjs/poiapi/PoiApi.42m`). `fglpkg env` then adds that one
directory to `FGLLDPATH` instead of one entry per installed package, so `IMPORT FGL com.fourjs.poiapi.PoiApi`
resolves to the namespace-correct path no matter what the package or its store directory is named.

- The merged root is a **derived cache**. It is built by hard-linking each module into place (a copy is
  used when hard links aren't available — a different filesystem, or Windows), so it costs almost no
  extra disk, and you can delete it at any time.
- `install` and `remove` keep it up to date automatically. If it is ever missing or stale — you deleted
  it, or edited `.fglpkg/packages/` by hand — rebuild it with **`fglpkg relink`** (add `--local` /
  `--global` to target one scope).
- Two installed packages that declare the **same** `PACKAGE` namespace is a hard error at install time,
  rather than one silently shadowing the other.
- Packages published before namespaces were recorded, and packages with no namespace, keep their
  historical per-package `FGLLDPATH` entry — nothing about those changes.
- The merged root holds **modules only**. Forms, schemas, images and profiles are not remapped into
  it, so a package's store directory keeps feeding `FGLRESOURCEPATH` / `FGLDBPATH` / `FGLIMAGEPATH` /
  `FGLPROFILE` even once the merged root has taken it off `FGLLDPATH`.
- `.fglpkg/merged` is git-ignored by the `init` templates; never commit it (rebuild it instead).

### Home Directory

Everything fglpkg manages lives under `~/.fglpkg` by default. Override this by setting the `FGLPKG_HOME` environment variable:

```bash
# macOS / Linux
export FGLPKG_HOME=/opt/fglpkg
```

```cmd
REM Windows
SET FGLPKG_HOME=C:\fglpkg
```

## Creating a New Project

To start a new Genero BDL project with fglpkg:

```bash
mkdir myproject
cd myproject
fglpkg init
```

This interactively prompts for the fields a publishable package needs — name, version, description, author, license (default `UNLICENSED`), and repository — then writes a `fglpkg.json`. The repository is **auto-detected from the project's git `origin` remote** when present (SCP-style remotes like `git@github.com:acme/myproject.git` are normalised to `https://github.com/acme/myproject`) and offered as the default; if there is no git repo or no `origin`, you are prompted for it. A final prompt offers the optional Genero version constraint, showing the detected runtime version — press Enter to skip it, type `current` to pin the current major (e.g. `^6.0.0`), or enter your own constraint.

Pass `--yes` (or run with a non-interactive stdin, e.g. in CI or scaffolding) to skip the prompts and accept the defaults instead of blocking:

```bash
fglpkg init --yes
```

A freshly created manifest looks like this (with `repository` filled in from the git remote):

```json
{
  "name": "myproject",
  "version": "0.1.0",
  "license": "UNLICENSED",
  "repository": "https://github.com/acme/myproject",
  "dependencies": {}
}
```

## Managing Dependencies

### Local vs Global (Context-Aware)

fglpkg automatically detects whether to install packages locally or globally:

- **Inside a project** (directory has `.fglpkg/` or `fglpkg.json`): packages install to `.fglpkg/` in the project directory
- **Outside a project**: packages install to `~/.fglpkg/` (global)

You can override this with flags:

```bash
fglpkg install --local     # force local .fglpkg/
fglpkg install --global    # force global ~/.fglpkg/
```

These flags work on `install`, `remove`, `update`, `list`, and `env`.

When using local installs, add `.fglpkg/` to your `.gitignore`.

### Installing All Dependencies

If your project already has a `fglpkg.json` with dependencies listed, install them all:

```bash
fglpkg install
```

This resolves the dependency graph, writes a lock file (`fglpkg.lock`), downloads BDL packages from the registry, and downloads Java JARs from Maven Central. Because `fglpkg.json` exists, packages are installed locally to `.fglpkg/` by default.

### Adding a Package

To add a BDL package dependency:

```bash
# Add the latest version
fglpkg install myutils

# Add a specific version
fglpkg install myutils@1.2.0
```

This resolves the version, adds it to your `fglpkg.json`, and installs it.

### Dependency Scopes (prod / dev / optional)

Packages can be recorded under three scopes depending on when they should be installed:

| Scope | When installed | Added to | Transitively pulled? |
|---|---|---|---|
| `dependencies` (prod) | Always | default, or `-P` / `--save-prod` | Yes — consumers get these too |
| `devDependencies` | Developer workflows only, skipped with `--production` | `-D` / `--save-dev` | No — a library's dev deps are private to it |
| `optionalDependencies` | Attempted like prod, but failures warn and continue | `-O` / `--save-optional` | Yes — and the optional tolerance inherits to transitive deps |

```bash
fglpkg install mytester -D         # add to devDependencies
fglpkg install telemetry -O        # add to optionalDependencies
fglpkg install core-lib -P         # explicit prod (same as no flag)
fglpkg install --production        # install everything EXCEPT devDependencies
```

`--production` is intended for CI / deployment builds: it skips the dev scope entirely and still attempts optional packages, warning on failure. It does NOT overwrite `fglpkg.lock`, so a production install cannot accidentally strip dev entries from the lock recorded by the developer.

Peer dependencies are intentionally not supported — they solve a JS/TS singleton problem (React, TypeScript) that has no clean analog in BDL's module layout. Use a version constraint on a prod dep if you need callers to align on a version.

### Removing a Package

```bash
fglpkg remove myutils
```

`remove` drops the package from whichever scope it lives in (`dependencies`, `devDependencies`, or `optionalDependencies`) — telling you which one — then re-resolves the remaining graph and rewrites `fglpkg.lock`, so the removed package and any now-orphaned transitive dependencies do not reappear on the next install.

What happens on disk depends on the install context (see [Local vs Global](#local-vs-global-context-aware)):

- **Local project (`.fglpkg/`)** — the removed package, plus any transitive dependencies the graph no longer needs, are pruned from `.fglpkg/`.
- **Global (`~/.fglpkg/`)** — packages and JARs there are shared across projects, so they are left on disk; only `fglpkg.json` and `fglpkg.lock` are updated.

Removing the **last** dependency empties the graph, so `fglpkg.lock` is deleted rather than left behind as an empty file.

If the registry can't be reached to re-resolve, `remove` still updates the manifest, prints a warning, and leaves the lock untouched — run `fglpkg install` once you're back online to reconcile.

`remove` is a convenience, not the only supported path: it is equivalent to deleting the dependency from `fglpkg.json` and running `fglpkg install`, which reconciles the lock and prunes disk the same way (see [Editing fglpkg.json by Hand](#editing-fglpkgjson-by-hand)). What `remove` adds is the `preuninstall` hook and the "which scope was it in" message.

### Editing fglpkg.json by Hand

You do not have to go through `fglpkg install` / `fglpkg remove` to change your dependencies — editing `fglpkg.json` in an editor is fully supported. The lock records the dependency constraints it was resolved from, so the next `install` notices the edit, re-resolves, and reports what changed:

```
$ fglpkg install
Lock file is stale (dependency "poiapi" was removed from dependencies) — re-resolving...
```

This works in both directions: a dependency you **add** by hand is installed, and one you **delete** is removed from `fglpkg.lock` *and* deleted from `.fglpkg/` (see [Pruning](#pruning-converging-on-the-manifest) below). Constraint changes, scope moves (prod ↔ dev), Java coordinate changes, and `registry` pin changes all count as edits.

> A lock written by fglpkg 4.0.5 or earlier carries no record of the dependencies it was resolved from, so the first `install` after upgrading re-resolves once to record them. That re-resolve can move versions within your existing constraints; run it somewhere you can review the resulting `fglpkg.lock` diff.

### Pruning: Converging on the Manifest

For a **local** (`.fglpkg/`) install, `install` and `update` both delete installed artifacts the dependency graph no longer requires — packages, webcomponent bundles, and orphaned JARs:

```
$ fglpkg install
Lock file is stale (dependency "poiapi" was removed from dependencies) — re-resolving...
  pruned package poiapi
  pruned jar poi-5.3.0.jar
```

This matters because `.fglpkg/packages/` is not an inert cache: `fglpkg env` puts it on `FGLLDPATH`. An orphan left there stays importable, so code that should have stopped compiling keeps compiling on your machine and breaks on a fresh clone. Pruning is also what keeps `fglpkg list` honest — it reports what is on disk.

Two deliberate limits:

- **Global (`~/.fglpkg/`) installs are never pruned.** Those packages and JARs are shared across every project, so pruning them against one project's graph would delete another project's dependencies. Only `fglpkg.lock` is updated.
- **`--production` never prunes.** It resolves a deliberately narrowed graph (no dev scope), so pruning against it would delete a developer's dev packages.

Use `--no-prune` to keep orphans on disk for a single run:

```bash
fglpkg install --no-prune
fglpkg update --no-prune
```

Note that pruning deletes anything under `.fglpkg/packages/`, `.fglpkg/jars/`, and `.fglpkg/webcomponents/` that the graph does not account for — including files you placed there by hand. Those directories are fglpkg-managed; keep local patches somewhere else, or use `--no-prune`.

### Updating Dependencies

Once a `fglpkg.lock` exists, `fglpkg install` will **not** fetch a newer version of a dependency just because one was published — even if your version constraint (e.g. `^1.0.0`) would allow it. `install` re-resolves when `fglpkg.json` has changed (including by hand); otherwise it validates the existing lock against disk and stops there (`Lock file is up to date... Nothing to install`).

To re-resolve all dependencies to their latest compatible versions (ignoring the lock file):

```bash
fglpkg update
```

This rewrites `fglpkg.lock` with whatever versions the registry now resolves to, and re-installs anything that changed — BDL packages, Java JARs, and webcomponent packages alike. Webcomponent bundles are always re-extracted on install (there's no "already installed, skip" fast path for them like there is for BDL packages), so an `update` that picks up a new webcomponent version reliably overwrites the old files in `.fglpkg/webcomponents/<COMPONENTTYPE>/`. See [Publishing an Update](#publishing-an-update) for the publisher side of this flow.

### Reproducible Installs for CI (`--frozen`)

`fglpkg install --frozen` refuses to re-resolve: if `fglpkg.lock` is missing, or disagrees with `fglpkg.json`, it fails instead of quietly resolving to versions nobody reviewed.

```bash
fglpkg install --frozen --production
```

```
$ fglpkg install --frozen
--frozen: fglpkg.lock is out of date with fglpkg.json — dependency "poiapi" was removed from dependencies.
  Run 'fglpkg install' (or 'fglpkg update') and commit the updated lock.
```

It checks the committed lock against the manifest only — nothing needs to be installed yet, which is exactly the fresh-checkout case. `--frozen` cannot be combined with a package argument or `--force` (both require re-resolving), and is not valid for `update`, whose whole purpose is to move the lock.

### Listing Installed Packages

```bash
$ fglpkg list
Installed packages:
  myutils                        1.0.0
  poiapi                         1.0.0
```

### Searching the Registry

`fglpkg search` annotates every result with its compatibility against the Genero version
you are running. The version is detected automatically (honoring `FGLPKG_GENERO_VERSION`)
and can be overridden with `--genero <version>`. The marker is advisory — nothing is hidden
or reordered:

- `✓` — the package's latest version is compatible with your Genero version
- `✗` — the latest version requires a different Genero version
- `?` — unknown: the registry reports no constraint, or no Genero version could be resolved

```bash
$ fglpkg search json
Results for "json" (Genero 4.01):
  NAME                           VERSION      GENERO       ?  DESCRIPTION
  ----                           -------      ------       -  -----------
  jsonutils                      2.0.1        ^4.0.0       ✓  JSON utility functions for BDL
  legacyjson                     1.4.0        ^3.0.0       ✗  JSON helpers for Genero 3
  mystery                        0.9.0        -            ?  registry reports no constraint
```

Grade against a specific version instead of the detected one:

```bash
$ fglpkg search json --genero 3.20
```

If no Genero version can be detected (no `fglcomp`, no `$FGLDIR`, no override), search still
runs — every result shows `?` and the header explains how to set the version. Results from
secondary (non-GI) repositories are not graded and always show `?`.

## Publishing a Package

### Package Structure

A publishable package needs a `fglpkg.json` with at least `name` and `version`. The `root` field tells fglpkg where to find the compiled files.

**Example: Simple package (flat directory)**

```
myutils/
├── fglpkg.json
├── strings.42m
└── dates.42m
```

```json
{
  "name": "myutils",
  "version": "1.0.0",
  "description": "String and date utilities for BDL"
}
```

**Example: Fully-qualified package (Java-style directory structure)**

If your package uses a fully-qualified name like `com.fourjs.poiapi`, Genero expects the `.42m` files to live in a matching directory structure. Set `root` to tell fglpkg where to find them:

```
poiapi/
├── fglpkg.json
└── com/
    └── fourjs/
        └── poiapi/
            ├── PoiApi.42m
            └── PoiHelper.42m
```

```json
{
  "name": "poiapi",
  "version": "1.0.0",
  "description": "POI API for Genero BDL",
  "root": "com/fourjs/poiapi",
  "genero": "^4.0.0",
  "dependencies": {
    "java": [
      {
        "groupId": "org.apache.poi",
        "artifactId": "poi",
        "version": "5.2.3"
      }
    ]
  }
}
```

When published, the zip preserves the full directory structure (`com/fourjs/poiapi/PoiApi.42m`). When installed, it extracts to `~/.fglpkg/packages/poiapi/com/fourjs/poiapi/PoiApi.42m`. Since `~/.fglpkg/packages/poiapi` is on the `FGLLDPATH`, Genero resolves `com.fourjs.poiapi` correctly.

**Example: Compiled output under a build directory (`importRoot`)**

Many projects compile into a build-output directory such as `lib/`, so the package files end up at `lib/com/fourjs/fglpkgtest/…`. Publishing that as-is would ship the `lib/` prefix, and `IMPORT FGL com.fourjs.fglpkgtest.*` would not resolve after install. Set `importRoot` to the directory whose *contents* should become the archive root:

```
fglpkgtest/
├── fglpkg.json
├── dist/
│   └── app.4st
└── lib/
    └── com/
        └── fourjs/
            └── fglpkgtest/
                ├── ModuleA.42m
                └── ModuleB.42m
```

```json
{
  "name": "fglpkgtest",
  "version": "1.0.0",
  "root": "lib/com/fourjs/fglpkgtest",
  "importRoot": "lib",
  "files": ["*.42m"],
  "include": ["dist/app.4st"]
}
```

With `importRoot: "lib"`, packaged files are stored relative to `lib/`, so `lib/com/fourjs/fglpkgtest/ModuleA.42m` ships as `com/fourjs/fglpkgtest/ModuleA.42m` — the `lib/` prefix is stripped and imports resolve after install. Set `root` to the directory that directly holds your program modules (`fglpkg run` relies on it); `importRoot` must be a prefix of `root`.

Use `include` for loose files that live outside `importRoot` but should sit at the archive root: each listed file is copied to the top of the archive under its **basename** (so `dist/app.4st` ships as `app.4st`). A file that must be namespaced (`com/fourjs/…`) belongs under `importRoot` in the source, not in `include`.

### File Selection

By default, fglpkg collects files matching `*.42m`, `*.42f`, and `*.sch`. To customize this, use the `files` field:

```json
{
  "files": ["*.42m", "*.42f", "*.sch", "*.str"]
}
```

Patterns come in two forms:

- **No slash** (e.g. `*.42m`) — matched against each file's **basename**, at any depth under `root`. This is the common case.
- **Contains a slash** (e.g. `tests/*.4gl`, `com/**/*.42m`) — **path-scoped**, matched against the file's path **relative to `root`**. `*` matches within a single directory segment; `**` spans any number of segments. A leading `/` is allowed and means the same thing (anchored at `root`).

So with `"root": "com/fourjs/ai/fgl_ai_sdk"`, a `"files": ["*.42m", "tests/*.4gl"]` ships every compiled module plus only the `.4gl` sources under `tests/` — the library `.4gl` at the package root is left out, with no `.fglpkgignore` needed.

> **Note:** `files` path-patterns are relative to `root` (your BDL source base). This differs from [`.fglpkgignore`](#excluding-files-with-fglpkgignore), whose patterns are relative to the **project root**. Keep that in mind when the same file needs to line up across both.

<a id="profile"></a>
### Shipping Configuration (`profile`)

A package can ship Genero configuration files and have consumers pick them up automatically. List them
in `profile`, as paths relative to `root`:

```json
{
  "name": "poiapi",
  "version": "1.0.0",
  "files": ["*.42m", "*.42f"],
  "profile": ["profiles/poiapi.4gp"]
}
```

After install, `fglpkg env` puts each shipped file on `FGLPROFILE`:

```
export FGLPROFILE=/home/you/proj/.fglpkg/packages/poiapi/profiles/poiapi.4gp"${FGLPROFILE:+:$FGLPROFILE}"
```

Things to know:

- These are **file paths**, not glob patterns and not directories — `FGLPROFILE` is a list of files.
- Declared files are **always packed**, even when your `files` globs don't match them (the defaults
  never would) and even when `.fglpkgignore` would exclude them. Same rule as [`bin`](#distributable-scripts):
  a declared profile that never reaches the archive is a silently broken package. A declared file that
  doesn't exist is a **pack error**, not a silent omission.
- Genero applies `FGLPROFILE` entries **left to right, last one winning**. fglpkg lists package
  profiles *before* any existing value, so a project- or user-level profile still overrides a
  package's defaults.
- The path is rewritten to its archive-relative form when the package is published, so `importRoot`
  stripping is handled for you.
- If a declared profile is missing from an installed package, `fglpkg env` skips it and says so on
  stderr rather than emitting a dangling path.

### Excluding Files with `.fglpkgignore`

Place a `.fglpkgignore` file in the project root to subtract files from the inclusion set computed by `files`/`docs`. The syntax is a small subset of `.gitignore`:

```
# comments start with #
*.bak           # exclude any .bak file at any depth
build/          # trailing slash → directory-only
/scratch        # leading slash → anchored to project root
docs/internal.md
!docs/internal-public.md   # ! re-includes a previously excluded path
```

Notes:
- Patterns are evaluated in file order; the last matching rule wins.
- Files declared in the manifest's `bin` field are always included, even if they match an ignore pattern — dropping a declared script would silently break the package.
- `fglpkg.json` is always included.

### Publishing

Publishing requires a registry account — there is **no GitHub or per-repository setup**. The registry stores package artifacts itself (in R2-backed object storage). Authenticate once:

```bash
fglpkg login                 # opens a browser for OAuth (code + PKCE)
# or, for CI / headless machines:
fglpkg login --token <PAT>   # store a Personal Access Token
```

Credentials are saved to `~/.fglpkg/credentials.json` and refreshed automatically. Then, from the package directory:

```bash
fglpkg publish
fglpkg publish --dry-run     # preview every call without touching the network
```

Publishing is **additive and reviewed**: a freshly published version is marked *pending* and only becomes installable once a registry administrator approves it.

The publish flow:
1. Builds a zip from the directory given by `root` (or `.`), collecting files matching `files` (default `*.42m`, `*.42f`, `*.sch`) plus declared `bin` scripts and `docs`, and SHA256s it.
2. `POST /registry/packages` — creates the package on first publish (a `409` "already exists" is fine). New packages carry the manifest's `visibility` (`public` by default; set `"visibility": "private"` to restrict).
3. `POST /registry/packages/:slug/versions` — creates the version and attaches its changelog (see below).
4. `PUT …/versions/:version/artifacts/:variant` — streams the zip; the registry stores it and records size + checksum.
5. `POST …/versions/:version/submit` — submits the version for admin review.

Authentication uses the OAuth/PAT bearer from `fglpkg login` (or `FGLPKG_TOKEN` in CI). No GitHub token is involved.

### Publishing an Update

To publish a new version of a package you own:

```bash
fglpkg bump patch    # or minor | major | prerelease | <semver>
fglpkg publish
```

`fglpkg bump` bumps the `version` field in `fglpkg.json` (`patch` takes `1.2.3` → `1.2.4`, etc.) and prints a suggested `git tag` command; pass `--git` to have it create the tag for you automatically. (`fglpkg version` is separate — it only prints the fglpkg tool version.) Publishing then works exactly like a first release — the CLI picks up the new version from the manifest. This is the same two-command flow regardless of package kind (BDL, JAR-bearing, or pure webcomponent).

**Consumers do not pick up the new version automatically.** Once a project has a `fglpkg.lock`, plain `fglpkg install` is a no-op if `fglpkg.json` hasn't changed — it validates the lock against what's on disk and prints `Lock file is up to date... Nothing to install`, even when a newer version satisfying the existing constraint (e.g. `^1.0.0`) now exists on the registry. To fetch it, run:

```bash
fglpkg update
```

in the consuming project. See [Updating Dependencies](#updating-dependencies) for what this does.

### Version Changelog

Each published version can carry a changelog that the registry stores and the
portals display. Publish resolves it in this order:

1. `--changelog "<text>"` — inline text, useful in CI.
2. **Automatic** (default): a `CHANGELOG.md` in the project root, in
   [Keep a Changelog](https://keepachangelog.com) format. Publish sends only the
   section whose heading names the version being published:

   ```markdown
   ## [1.2.0] - 2026-07-13

   ### Added
   - The thing you added.

   ## [1.1.0] - 2026-06-01
   - Older entry (not sent when publishing 1.2.0).
   ```

Headings may be bracketed (`## [1.2.0]`) or bare (`## 1.2.0`), with an optional
`v` prefix and a trailing ` - date`. Only the entry for the version being
published is sent — not the whole history.

If `CHANGELOG.md` exists but has no entry for the version, publish prints a
warning and sends an empty changelog (it does not block the publish). Use
`fglpkg publish --dry-run` to preview the resolved changelog size before pushing.

### Genero Version Variants

Genero BDL compiled modules (`.42m` files) are not compatible across major versions — a module compiled with Genero 4.x cannot be loaded by the Genero 6.x runtime. fglpkg handles this with **platform variants**: each package version can have multiple builds, one per Genero major version.

#### Publishing variants

When you run `fglpkg publish`, it automatically detects your local Genero version and uploads the zip as a variant. For example, on a Genero 4.x machine:

```
$ fglpkg publish
Publishing poiapi@1.0.0 (Genero 4 variant) to https://service.generointelligence.ai...
  Package zip: 4096 bytes (SHA256: abc123...)
  Uploaded variant: poiapi-1.0.0-genero4.zip
✓ Published poiapi@1.0.0 (submitted for review)
```

To publish for another Genero version, run the same command on a machine with that version installed:

```bash
# On a Genero 6.x machine
fglpkg publish
```

Both variants live under the same version (`1.0.0`) on the registry as separate artifacts. Publishing a second variant for an existing version is additive and does not require bumping the version.

#### Installing the correct variant

When you run `fglpkg install`, the resolver automatically detects your local Genero version and selects the matching variant. If no variant exists for your Genero version, the install fails with an error listing the available variants.

```
$ fglpkg install
Resolving dependency graph (Genero 4.01.12)...
  → poiapi@1.0.0 (genero4 variant)
✓ poiapi@1.0.0
```

#### Lock file and Genero changes

The lock file records which Genero major version was used during resolution. If you switch to a different Genero major version, run `fglpkg update` to re-resolve and select the correct variants. Plain `fglpkg install` only **warns** about the mismatch and keeps the locked variants — it does not re-resolve for a Genero change.

### Genero Version Constraints

Use the `genero` field to declare which Genero BDL versions your package supports:

```json
{
  "genero": "^4.0.0"
}
```

Supported constraint syntax:
- `^1.0.0` — compatible with 1.x.x (>=1.0.0, <2.0.0)
- `~1.2.0` — patch-level changes (>=1.2.0, <1.3.0)
- `>=3.20.0 <5.0.0` — explicit range
- `^3.20.0 || ^4.0.0` — multiple ranges
- `*` or omit — compatible with any version

## Deprecating & Relocating Packages

`fglpkg deprecate` marks a published version — or a whole package — as deprecated, following the **npm model**: the deprecated version stays **fully installable and listed**; consumers just get a non-fatal warning, optionally pointing at a successor. This is also how a **rename or relocation** is expressed — there is no separate `rename`/`migrate` command.

Deprecation is an **owner-only** action and requires login.

```bash
# Deprecate one version with a message
fglpkg deprecate chart-3d@1.2.3 "security fix in 1.2.4; please upgrade"

# Rename / relocate — message auto-fills to "chart-3d has moved to chart-3d-ng"
fglpkg deprecate chart-3d@1.2.3 --moved-to chart-3d-ng

# Relocate the whole package (all versions), pinning a successor version
fglpkg deprecate chart-3d --moved-to chart-3d-ng@2.0.0

# Lift a deprecation
fglpkg deprecate chart-3d@1.2.3 --undo
```

- A bare `<pkg>` (no `@version`) targets the whole package; `<pkg>@<version>` targets one version.
- A message is required unless `--moved-to` is given (which auto-fills one).
- `--message <text>` is an alternative to the positional message; `--json` prints a machine-readable result.
- Re-running `deprecate` edits the existing message/successor (it is idempotent).

Deprecation is **advisory, not withdrawal**: it never hides or un-lists the package and never renames the slug in place — it records a pointer to a separately-published successor. What consumers see:

- **`install` / `update`** — a `warning:` line for each deprecated resolved dependency (including transitive ones), with a `→ consider: fglpkg install <successor>` hint when a successor is set. The install still succeeds; deprecation never blocks it. (Warnings fire on a fresh resolve, not on a lock-file-only reinstall.)
- **`info`** — a `Deprecated:` / `Moved to:` block under the header.
- **`outdated`** — a `deprecated → <successor>` note for any installed dependency that is deprecated.

## Running BDL Programs

Packages can declare runnable BDL programs — modules that contain a `MAIN` block. These are listed in the `programs` field of `fglpkg.json`:

```json
{
  "name": "poiapi",
  "version": "1.0.0",
  "root": "com/fourjs/poiapi",
  "programs": ["PoiConvert", "PoiMerge"]
}
```

### Running a program

```bash
fglpkg bdl poiapi PoiConvert input.xlsx output.pdf
```

This:
1. Finds the `poiapi` package in installed packages (local first, then global)
2. Verifies `PoiConvert` is declared in the package's `programs` list
3. Derives the working directory from the package's `root` field (`com/fourjs/poiapi/`)
4. Sets up `FGLLDPATH` and `CLASSPATH` with all installed packages
5. Runs `fglrun PoiConvert input.xlsx output.pdf` from that directory

All arguments after the module name are passed through to `fglrun`.

### Listing available programs

```bash
$ fglpkg bdl --list
Available BDL programs:
  PROGRAM                   PACKAGE                   SOURCE
  -------                   -------                   ------
  PoiConvert                poiapi                    local
  PoiMerge                  poiapi                    local
```

### Requirements

- Genero BDL must be installed (`fglrun` on `PATH` or `$FGLDIR` set)
- The package must be installed (`fglpkg install`)
- The module must be declared in the package's `programs` list
- The `.42m` file must exist in the package's `root` directory

## Working with Java JARs

Genero BDL can call Java code, so fglpkg also manages JAR dependencies. Declare them using Maven coordinates:

```json
{
  "dependencies": {
    "java": [
      {
        "groupId": "com.google.code.gson",
        "artifactId": "gson",
        "version": "2.10.1"
      },
      {
        "groupId": "org.apache.poi",
        "artifactId": "poi",
        "version": "5.2.3",
        "checksum": "abc123..."
      }
    ]
  }
}
```

### Optional JAR Fields

| Field | Description |
|---|---|
| `checksum` | SHA256 hex digest for integrity verification (optional, Maven Central is trusted by default) |
| `jar` | Override the JAR filename (default: `artifactId-version.jar`) |
| `url` | Override the download URL entirely for this one JAR (default: the configured Maven mirror, else Maven Central). To route *all* JARs through a mirror, use the top-level `mavenMirror` instead — see below. |

JARs are downloaded to `~/.fglpkg/jars/` and added to `CLASSPATH` by `fglpkg env`.

### Routing JARs through an internal Maven mirror

By default JARs come from public Maven Central. In environments where nothing may
be pulled from a public source, point fglpkg at an internal Maven repository (for
example a JFrog Artifactory Maven *remote* or *virtual* repo) with the top-level
`mavenMirror` block:

```json
{
  "mavenMirror": {
    "url": "https://artifactory.acme.example/artifactory/libs-release",
    "auth": "bearer"
  }
}
```

Because an Artifactory Maven repo serves the standard Maven2 layout, only the base
URL changes — the coordinates resolve to the same per-JAR path. `gson` above is
fetched from
`…/artifactory/libs-release/com/google/code/gson/gson/2.10.1/gson-2.10.1.jar`.

- **Credentials stay separate.** `mavenMirror` holds no secrets, and there is no
  per-URL login. Authenticate the mirror through a [registry](#secondary-repositories-jfrog-artifactory):
  declare the Artifactory base (a URL that is a prefix of the mirror, e.g.
  `https://artifactory.acme.example/artifactory`) in `registries` and run
  `fglpkg login --registry <name> --token <access-token>`. That credential is
  matched onto the mirror by URL prefix and applied to JAR downloads
  automatically. An auth failure (401/403) is a hard error, not a silent miss.
- **Match the schemes.** The mirror's `auth` must match how you logged the
  registry in — `bearer` ↔ `--token`, `basic` ↔ `--user`/`--password`. A
  mismatch yields no headers and falls back to anonymous.
- **Where to declare it** (highest precedence first): a per-dependency `url`
  override → the `FGLPKG_MAVEN_URL` environment variable (URL only) → the
  committed project `fglpkg.json` (team-shared) → the per-user
  `~/.fglpkg/config.json` → Maven Central when none is set. The resolved URL is
  pinned in `fglpkg.lock`, so a change takes effect on the next `fglpkg update`
  (or an `install` that alters the dependency set), not on a locked `install`.
- Committing `mavenMirror` plus its `registries` entry to `fglpkg.json` gives
  teammates a "clone → `login --registry` → install just works" flow without
  re-declaring the mirror.

## Webcomponent Packages

A webcomponent package ships a Genero `COMPONENTTYPE` — an html/css/js bundle that a form references with `WEBCOMPONENT … COMPONENTTYPE = "<name>"`. Webcomponent bundles install to `.fglpkg/webcomponents/<COMPONENTTYPE>/` so they coexist with BDL packages (`.fglpkg/packages/`) and Java JARs (`.fglpkg/jars/`) without colliding.

A manifest can declare `webcomponents` **alone** (a pure-WC package) or **alongside BDL fields** (a mixed package — a BDL wrapper that ships with its companion webcomponent in a single artifact). The variant tag is picked automatically from what the manifest declares:

| Manifest contains | Variant tag | When to use |
|---|---|---|
| Only `webcomponents` | `webcomponent` | A self-contained widget that needs no BDL helper (themes, pure UI bundles) |
| `webcomponents` + BDL fields (`programs`, `files`, `main`, etc.) | `genero<N>` | A widget paired with a BDL convenience wrapper so consumers can just `IMPORT FGL chart_3d` |
| BDL fields only | `genero<N>` | Classic BDL library |

### Creating a webcomponent package

```bash
mkdir mywidget
cd mywidget
fglpkg init --template webcomponent
```

The template scaffolds a pure-WC starter:

```
mywidget/
├── fglpkg.json                 # webcomponents: ["MyWidget"]
├── README.md
├── .gitignore
└── webcomponents/
    └── MyWidget/
        ├── MyWidget.html       # required entry point
        ├── MyWidget.css
        └── MyWidget.js         # demo gICAPI handshake
```

Rename `MyWidget` to your `COMPONENTTYPE`, update the `webcomponents` array in `fglpkg.json` to match, and fill in the HTML/CSS/JS. One package can ship multiple components — add more `webcomponents/<NAME>/` directories and list each name.

### Pairing a webcomponent with a BDL wrapper

A common pattern is to ship a small BDL module that wraps the gICAPI plumbing, so callers just `IMPORT FGL chart_3d` and call a typed function like `chart_3d.show(data STRING)` instead of writing raw `WEBCOMPONENT` form code. Drop the BDL source into the project root and add the BDL fields to the same manifest:

```
mywidget/
├── fglpkg.json                 # webcomponents + programs
├── README.md
├── ChartHelper.4gl             # BDL wrapper module
└── webcomponents/
    └── 3DChart/
        ├── 3DChart.html
        ├── 3DChart.css
        └── 3DChart.js
```

```json
{
  "name": "chart-3d",
  "version": "1.0.0",
  "description": "BDL wrapper + 3D chart webcomponent",
  "license": "MIT",
  "repository": "https://github.com/example/chart-3d",
  "programs": ["ChartHelper"],
  "webcomponents": ["3DChart"],
  "dependencies": { "fgl": {} }
}
```

A `fglpkg publish` of this mixed manifest uploads a single `genero<N>` artifact per Genero major your machine targets (the BDL bits are version-specific; the WC rides along unchanged). On install, the BDL files land under `.fglpkg/packages/chart-3d/` and the `3DChart/` bundle lands under `.fglpkg/webcomponents/3DChart/` — consumers get both halves from one `fglpkg install`.

### Manifest fields

**Pure webcomponent (no BDL helper):**

```json
{
  "name": "chart-3d",
  "version": "1.0.0",
  "description": "3D chart widget for Genero forms",
  "license": "MIT",
  "repository": "https://github.com/4js-mikefolcher/chart-3d",
  "webcomponents": ["3DChart"],
  "dependencies": {
    "fgl": {
      "wc-theme-base": "^1.0.0"
    }
  }
}
```

Notes:
- `webcomponents` is a list of `COMPONENTTYPE` names. Each must match `^[A-Za-z0-9][A-Za-z0-9_-]*$` (digit-leading names like `3DChart` are valid).
- Mix `webcomponents` with `programs`, `main`, `files`, `bin`, `root`, or `dependencies.java` freely — fglpkg derives the package's variant tag from what's actually declared.
- The legacy `"type": "webcomponent"` field is accepted but ignored. New manifests should omit it.

### Publishing

```bash
fglpkg login           # once, OAuth in the browser (or use FGLPKG_TOKEN)
fglpkg pack --list     # preview the zip contents before pushing
fglpkg publish --dry-run
fglpkg publish
```

The publish flow picks the variant automatically — `webcomponent` for pure-WC packages, `genero<N>` for mixed or pure-BDL. In both cases the in-zip layout has the `webcomponents/` prefix stripped — so a source file at `webcomponents/3DChart/3DChart.html` is stored as `3DChart/3DChart.html` in the artifact, ready to drop into the consumer's install directory.

### Consuming a webcomponent package

```bash
# In the consuming project
fglpkg install chart-3d
eval "$(fglpkg env)"
```

`fglpkg install` extracts each `<COMPONENTTYPE>/` directory directly into `.fglpkg/webcomponents/`, so:

```
yourproject/
└── .fglpkg/
    └── webcomponents/
        └── 3DChart/
            ├── 3DChart.html
            ├── 3DChart.css
            └── 3DChart.js
```

Then reference the component from a form just like a built-in one:

```
WEBCOMPONENT wc = FORMONLY.mychart,
    COMPONENTTYPE = "3DChart";
```

### Environment wiring

`fglpkg env` adds `.fglpkg/` to `FGLIMAGEPATH` when webcomponents are installed, so Genero's direct-mode loader resolves `<COMPONENTTYPE>` against `.fglpkg/webcomponents/<COMPONENTTYPE>/<COMPONENTTYPE>.html` automatically:

```bash
$ eval "$(fglpkg env)"
$ env | grep FGLIMAGEPATH
FGLIMAGEPATH=/path/to/project/.fglpkg:...
```

Alongside the export, `fglpkg env` prints a hint comment showing the value to add to your GAS application's `.xcf` (fglpkg cannot edit your `.xcf` for you — that's a deployment concern):

```bash
$ fglpkg env --local
export FGLLDPATH=...
export FGLIMAGEPATH=/path/to/project/.fglpkg"${FGLIMAGEPATH:+:$FGLIMAGEPATH}"
# For GAS: add to your .xcf's <WEB_COMPONENT_DIRECTORY>: /path/to/project/.fglpkg/webcomponents
```

### Packaging for GWA with `gwabuildtool`

For Genero Web Applications, webcomponents must be bundled into the GWA artifact at build time. `fglpkg env --gwa` emits one `--webcomponent` flag per installed `COMPONENTTYPE`, suitable for splicing into a `gwabuildtool` invocation:

```bash
$ fglpkg env --gwa
--webcomponent /path/to/project/.fglpkg/webcomponents/3DChart
--webcomponent /path/to/project/.fglpkg/webcomponents/Heatmap

$ gwabuildtool -p . -o build/ $(fglpkg env --gwa)
```

### Install layout summary

| Asset | Install path | Discovered via |
|---|---|---|
| BDL module | `.fglpkg/packages/<name>/` → merged into `.fglpkg/merged/<namespace>/` | `FGLLDPATH` |
| Form / style / action defaults | `.fglpkg/packages/<name>/…` (wherever the package puts it) | `FGLRESOURCEPATH` |
| Database schema | `.fglpkg/packages/<name>/…` | `FGLDBPATH` |
| Image | `.fglpkg/packages/<name>/…` | `FGLIMAGEPATH` |
| Profile / config | `.fglpkg/packages/<name>/…` (declared via `profile`) | `FGLPROFILE` |
| Java JAR | `.fglpkg/jars/<artifact>-<version>.jar` | `CLASSPATH` |
| Webcomponent | `.fglpkg/webcomponents/<COMPONENTTYPE>/` | `FGLIMAGEPATH` (direct mode), `WEB_COMPONENT_DIRECTORY` (GAS), `--webcomponent` flag (GWA) |

Only modules are relocated at install time (into the merged root). Everything else is discovered
in place, in the package's own store directory — which is why that directory stays on the resource
paths even when the merged root has taken it off `FGLLDPATH`.

The lockfile records webcomponent packages under a separate `webcomponents` array, so a fresh `fglpkg install --frozen` from a committed `fglpkg.lock` reproduces the install byte-for-byte.

## Distributable Scripts

Packages can ship executable scripts (bash, python, etc.) that consumers can run after installation. Declare them using the `bin` field in `fglpkg.json`:

```json
{
  "name": "dbtools",
  "version": "1.0.0",
  "bin": {
    "db-migrate": "scripts/migrate.sh",
    "db-seed": "scripts/seed.py"
  }
}
```

Each key is the command name and each value is the path to the script file (relative to the package `root`). Script files are automatically included in the package zip when publishing, even if they don't match the `files` patterns.

After installation, scripts are automatically made executable (on Unix). Run them with `fglpkg run`:

```bash
# List all available commands from installed packages
$ fglpkg run --list
Available commands:
  COMMAND              PACKAGE              SOURCE     SCRIPT
  -------              -------              ------     ------
  db-migrate           dbtools              global     scripts/migrate.sh
  db-seed              dbtools              global     scripts/seed.py

# Run a command
$ fglpkg run db-migrate

# Pass arguments to the script (use -- to separate)
$ fglpkg run db-migrate -- --up --env production
```

### How It Works

- `fglpkg run` scans installed packages (local first, then global) to find the named command
- If the same command name exists in multiple packages, an error is reported listing the conflicts
- On Unix, scripts are executed directly (relying on the shebang line, e.g., `#!/bin/bash`)
- On Windows, scripts are executed via `cmd.exe` or the appropriate interpreter based on file extension

### Writing Portable Scripts

For maximum compatibility, include a shebang line at the top of your scripts:

```bash
#!/usr/bin/env bash
set -euo pipefail
echo "Running migration..."
```

```python
#!/usr/bin/env python3
import sys
print("Seeding database...")
```

## Lifecycle Hooks

The optional `hooks` field declares steps to run on well-known events. The vocabulary is intentionally a closed set of declarative operations — arbitrary shell commands are not supported, since shell-based hooks are the dominant supply-chain attack vector in mainstream package managers.

```json
{
  "name": "poiapi",
  "version": "1.0.0",
  "hooks": {
    "postinstall": [
      { "op": "mkdir", "path": "var/cache" },
      { "op": "copy-files", "from": "templates/*.tpl", "to": "share/templates" }
    ],
    "prepublish": [
      { "op": "copy-files", "from": "vendor", "to": "dist/vendor" }
    ]
  }
}
```

### Events

Hooks run on the project (consumer) manifest, not on dependency manifests:

| Event          | When it fires                                            | Working directory |
|----------------|----------------------------------------------------------|-------------------|
| `preinstall`   | Before `fglpkg install` starts resolving packages        | project root      |
| `postinstall`  | After every dependency has been installed                | project root      |
| `prepublish`   | Before `fglpkg publish` builds the zip                   | project root      |
| `postpublish`  | After `fglpkg publish` finishes the registry update      | project root      |
| `preuninstall` | Before `fglpkg remove` deletes a package                 | project root      |

A failure in any operation aborts the surrounding command; later operations in the same hook are skipped.

### Operations

Two operations are supported in this release. More can be added without breaking the schema (`fetch-jar` and `compile-bdl` are planned for a later phase).

**`copy-files`** — copy a file, a directory tree, or every match of a glob.

```json
{ "op": "copy-files", "from": "templates/*.tpl", "to": "share/templates" }
```

- `from` is a relative path or a glob (`*`, `?`, `[…]`).
- `to` is a relative directory (created if missing) or a single-file destination.
- Absolute paths and `..` traversal are rejected at manifest load time.

**`mkdir`** — create a directory and its parents. No-op if the directory already exists; fails if the path exists as a file.

```json
{ "op": "mkdir", "path": "var/cache" }
```

### Migrating from `scripts`

The previous `scripts` field was defined but never executed. It has been removed. A manifest that still uses `scripts` fails to load with a hint pointing at `hooks` — convert each entry to one of the operations above.

## Package Documentation

Packages can include documentation files that consumers can browse after installation. Declare them using the `docs` field with glob patterns:

```json
{
  "name": "poiapi",
  "version": "1.0.0",
  "docs": ["README.md", "docs/**/*.md"]
}
```

Documentation is only included when the author explicitly declares the `docs` field — there are no default patterns. The glob patterns support `**` for matching any number of directory levels.

### Browsing Documentation

Use `fglpkg docs` to discover and read documentation for installed packages:

```bash
# List available documentation files
$ fglpkg docs poiapi
Documentation for poiapi@1.0.0:
  README.md
  docs/getting-started.md
  docs/api-reference.md

View a file: fglpkg docs poiapi <file>

# Display a specific file
$ fglpkg docs poiapi README.md

# You can also use just the filename (if unique)
$ fglpkg docs poiapi api-reference.md
```

### Docs Glob Patterns

The `docs` field supports standard glob syntax with `**` for recursive matching:

| Pattern | Matches |
|---|---|
| `README.md` | Only `README.md` in the package root |
| `*.md` | Any `.md` file in the package root |
| `docs/**/*.md` | Any `.md` file anywhere under `docs/` |
| `CHANGELOG.md` | Only `CHANGELOG.md` in the package root |

## Registry Authentication

### Logging In

`fglpkg login` (no arguments) opens a browser and completes an OAuth (authorization code + PKCE) login against the registry:

```bash
$ fglpkg login
Opening browser to complete login…
✓ Logged in to https://service.generointelligence.ai as jdeveloper
```

Credentials are stored in `~/.fglpkg/credentials.json` and refreshed automatically when they expire. For non-interactive machines, store a Personal Access Token instead:

```bash
fglpkg login --token <PAT>
```

Running `fglpkg login --token` switches the GI registry to PAT auth: it replaces any OAuth session from a previous browser login, so the token takes effect immediately (no `fglpkg logout` first). Logging in again with `fglpkg login` (no arguments) switches back to OAuth.

### Checking Your Identity

```bash
$ fglpkg whoami
Logged in to https://service.generointelligence.ai as jdeveloper
```

### Logging Out

```bash
fglpkg logout
```

### Using a Token in CI/CD

For non-interactive environments, provide a Personal Access Token via `FGLPKG_TOKEN` instead of running `fglpkg login`:

```bash
# macOS / Linux
export FGLPKG_TOKEN=<PAT>
fglpkg publish --ci        # --ci is non-interactive and prints a machine-readable status line
```

```cmd
REM Windows
SET FGLPKG_TOKEN=<PAT>
fglpkg publish --ci
```

`FGLPKG_TOKEN` overrides any stored credentials and authenticates every registry command. Installing **public** packages needs no token at all. (For a secondary Artifactory repo in CI, authenticate it with `fglpkg login --registry <name> --token <access-token>` — `FGLPKG_TOKEN` applies only to the GI registry. See [Secondary Repositories](#secondary-repositories-jfrog-artifactory).)

## Secondary Repositories (JFrog Artifactory)

By default fglpkg draws every BDL package from the Genero Intelligence (GI)
registry. If your team hosts **internal** packages in a **JFrog Artifactory**
instance, you can add it as a secondary repository: fglpkg will consume and
publish your internal packages there while still pulling public packages from GI.
This is entirely client-side — nothing changes on the GI side. (Java JARs are not
routed through Artifactory; they stay on Maven Central.)

### 1. Declare the repository

Repositories are listed in a `registries` array. It contains **no secrets** —
credentials are stored separately by `fglpkg login`. Declare it in your project's
`fglpkg.json` (committed, so teammates get the URL on clone):

```json
{
  "name": "myapp",
  "version": "1.0.0",
  "dependencies": { "fgl": { "acme-utils": "^1.0.0" } },
  "registries": [
    {
      "name": "acme",
      "type": "artifactory",
      "url": "https://artifactory.acme.example/artifactory",
      "repoKey": "fgl-internal-generic",
      "priority": 2,
      "auth": "bearer",
      "packages": ["acme-*"]
    }
  ]
}
```

Or provision it once for every project on the machine in `~/.fglpkg/config.json`
(same shape) — useful for an ops team:

```json
{ "registries": [ { "name": "acme", "type": "artifactory", "url": "…", "repoKey": "…", "priority": 2, "auth": "bearer" } ] }
```

There are three layers, mirroring how npm (`.npmrc`) and NuGet (`NuGet.config`)
separate *what to fetch* from *how to authenticate*:

| Layer | File | Scope | Committed? |
|---|---|---|---|
| Project | `fglpkg.json` `registries` | This repo — team-shared | **Yes**, checked in |
| Machine | `~/.fglpkg/config.json` | Per-user default for every project | No (per developer) |
| Secrets | `~/.fglpkg/credentials.json` | Per-user tokens, keyed by URL | **Never** |

Because the registry declarations live in the committed `fglpkg.json`, a clean
clone needs no registry setup: **clone → `fglpkg login --registry <name>` →
`fglpkg install`** just works. Each developer supplies only their own credentials;
nobody re-declares the registries, and no secret is ever committed.

Descriptor fields:

| Field | Required | Meaning |
|---|---|---|
| `name` | Yes | Logical id used in `--registry`, credentials, and dependency pins |
| `type` | Yes | `"genero"` or `"artifactory"` |
| `url` | Yes | Base URL, including any context path (e.g. `…/artifactory`) |
| `repoKey` | For `artifactory` | The Artifactory **generic** repository key |
| `priority` | Yes | Lower is tried first; must be unique. Ordering only — not a precedence tiebreak |
| `auth` | No | `bearer` (default) \| `basic` \| `apikey` \| `anonymous` |
| `packages` | No | Glob allow-list (e.g. `["acme-*"]`); names outside it are never queried against this repo |

Instead of hand-editing JSON, let `fglpkg` manage these entries — it validates
the result before writing and auto-assigns the priority after `gi` when you omit
`--priority`:

```bash
# Paste the repository URL as Artifactory shows it — the repo key is split off
# the end and recorded as "repoKey", so --repo-key is optional:
fglpkg registry add acme https://artifactory.acme.example/artifactory/fgl-internal-generic \
    --packages "acme-*"                                   # writes ~/.fglpkg/config.json

# Equivalent, spelling the key out explicitly:
fglpkg registry add acme https://artifactory.acme.example/artifactory \
    --repo-key fgl-internal-generic --packages "acme-*"
fglpkg registry add acme https://… --repo-key K --project # writes the project fglpkg.json
fglpkg registry remove acme
```

The key is only inferred from the single path segment following `/artifactory` —
a deeper path (`/artifactory/api/storage/…`) or a URL with no `/artifactory`
segment is treated as a plain base URL, so `--repo-key` is still required there.
A `--repo-key` that disagrees with the key in the URL is rejected rather than
guessed at.

`add` defaults `--type` to `artifactory`; pass `--type genero`, `--auth`, or
`--priority` as needed. It refuses to redefine the built-in `gi` or collide on
name or priority.

Check the effective configuration and login status any time:

```bash
fglpkg registry list
# NAME   TYPE         PRIO  AUTH    LOGIN  SOURCE   URL
# gi     genero       1     bearer  env    builtin  https://service.generointelligence.ai
# acme   artifactory  2     bearer  no     project  https://artifactory.acme.example/artifactory
```

The `LOGIN` column shows `yes` (stored credentials), `env` (GI authenticated by
`FGLPKG_TOKEN`), `no` (none), or `anon` (no auth needed). The `SOURCE` column
shows where each entry came from — `builtin` (the always-present GI entry),
`global` (`~/.fglpkg/config.json`), or `project` (the committed `fglpkg.json`) —
so you can confirm which registries a clone inherited from the repo.

### 2. Log in

Credentials are per-repository, so you stay logged into GI and every secondary
repo simultaneously. Use the flag matching the repo's `auth` scheme:

```bash
fglpkg login --registry acme --token <access-token>           # bearer (recommended)
fglpkg login --registry acme --user <u> --password <p|token>  # basic
fglpkg login --registry acme --api-key <key>                  # apikey
fglpkg logout --registry acme
```

A JFrog access token can be used as the `bearer` token or as the `basic`
password. `FGLPKG_TOKEN` authenticates GI only — it has no effect on secondary
repos.

### 3. Consume packages

`fglpkg install` resolves each dependency to the repository that owns its name and
records the source in `fglpkg.lock` (`"registry": "acme"`), so installs are
reproducible. If a name exists in **more than one** repository, fglpkg stops with
a collision error rather than guessing — this is the dependency-confusion
safeguard. Resolve it by pinning the source:

```json
"dependencies": { "fgl": { "utils": { "version": "^1.0.0", "registry": "acme" } } }
```

or add + pin in one step:

```bash
fglpkg install utils --registry acme     # resolves from acme and writes the pin
```

A `packages` allow-list (e.g. `"packages": ["acme-*"]`) makes the split
structural, so those names are only ever looked for in your Artifactory and never
collide with GI.

**Transitive dependencies** of an Artifactory package carry the pins their author
declared, so they resolve from the intended repository automatically. A pin in
your own `fglpkg.json` always overrides a package's declared pin.

`fglpkg search <term>` fans out to every configured repository and tags each
result with its source repo.

### 4. Publish packages

```bash
fglpkg publish --registry acme            # deploy the built zip + sidecar manifest
fglpkg publish --registry acme --dry-run  # preview the PUT URLs, no network
fglpkg publish --registry acme --force    # overwrite an existing variant (refused by default)
```

To stop typing `--registry`, set a default publish target — resolved as
`FGLPKG_PUBLISH_REGISTRY` → project `defaultRegistry` → global `defaultRegistry` →
GI:

```json
{ "defaultRegistry": "acme", "registries": [ … ] }
```

A bare `fglpkg publish` then deploys to `acme`; `fglpkg publish --registry gi`
still targets GI when you need it.

For the complete design, see
[specs/artifactory-secondary-repository.md](../specs/artifactory-secondary-repository.md).

## Workspaces (Monorepos)

Workspaces let you develop multiple related packages in a single repository. Local packages are automatically linked via `FGLLDPATH` without needing to publish and install them.

### Setting Up a Workspace

```bash
# In your monorepo root
fglpkg workspace init packages/myutils packages/dbtools
```

This creates a `fglpkg.workspace.json` file. Each listed path should contain its own `fglpkg.json`.

### Adding Members

```bash
fglpkg workspace add packages/newlib
```

### Listing Members

```bash
$ fglpkg workspace list
Workspace: /path/to/monorepo
  myutils                        v1.0.0
  dbtools                        v2.1.0
  newlib                         v0.1.0
```

### Workspace Info

```bash
fglpkg workspace info
```

### How It Works

When `fglpkg env` detects that you are inside a workspace, it adds each member's source directory to `FGLLDPATH` with higher priority than installed packages. This means you can edit a local package and immediately use it in another member without re-publishing.

## Lock Files

When you run `fglpkg install`, a `fglpkg.lock` file is created alongside your `fglpkg.json`. The lock file pins:

- Exact resolved versions of every BDL package
- Download URLs and SHA256 checksums
- The Genero version used at resolution time
- Under `root.declared`, the dependency constraints it was resolved *from* — this is what lets `install` tell that `fglpkg.json` has been edited (see [Editing fglpkg.json by Hand](#editing-fglpkgjson-by-hand))

This ensures reproducible installs across machines and CI environments. Commit `fglpkg.lock` to version control.

To bypass the lock and re-resolve everything:

```bash
fglpkg update
```

To assert the lock is already correct and fail rather than re-resolve (CI):

```bash
fglpkg install --frozen
```

## Package Signature Verification

Every artifact the GI registry serves is signed with **Ed25519** over a canonical (RFC 8785 / JCS) payload of the package's identity and `sha256`. On install, `fglpkg` reconstructs that payload and verifies the signature — proving the bytes you received are exactly what the registry stored. This sits a layer above the plain SHA-256 integrity check, defending against transport, mirror, and cache tampering.

Trust is anchored in a **root public key pinned inside the fglpkg binary**: the registry's working keys are published in a signed manifest (`GET /registry/.well-known/keys.json`), itself signed by that pinned root — so a rogue registry can't substitute its own keys. The verified manifest is cached at `~/.fglpkg/keys.json`, so reinstalls and `--production` deploys work offline.

This gives integrity **and** authenticity for the registry artifact. It does *not* prove *who built* the package (that is a separate, opt-in provenance layer, not in this release), and Java JARs pulled from Maven Central keep their existing checksum-only trust.

### Enforcement modes

Set `signing.enforce` in `~/.fglpkg/config.json` (or the `FGLPKG_SIGNING` environment variable, which wins):

```json
{ "signing": { "enforce": "warn" } }
```

| Mode | Behaviour |
|---|---|
| `warn` *(default)* | A bad or missing signature warns but the install continues. |
| `require` | A bad or missing signature aborts the install. |
| `off` | Signature verification is skipped entirely. |

`fglpkg install --no-verify-signature` skips verification for a single run (discouraged; for emergencies).

### Auditing signatures

```bash
fglpkg audit signatures
```

Re-verifies every package in `fglpkg.lock` against the current keys manifest, printing one line per package and exiting non-zero if any package is unsigned or fails to verify — suitable as a CI gate.

## Package Ownership

Each package is owned by the partner (tenant) that first published it, and ownership governs who may publish new versions and who can see private or pending versions. Ownership and collaborator management are handled by registry administrators through the Genero Intelligence portal — there is no `fglpkg` CLI command for it.

## Troubleshooting

### "not logged in" when publishing

Make sure you have authenticated:

```bash
fglpkg login
```

Or set the `FGLPKG_TOKEN` environment variable.

### Packages not found by Genero after install

Make sure your environment is set up:

**macOS / Linux** — your shell profile should include:

```bash
eval "$(fglpkg env --global)"
```

Restart your shell or run `source ~/.bashrc` after adding it.

**Windows (cmd.exe)** — run before building:

```cmd
FOR /F "tokens=*" %i IN ('fglpkg env --global') DO %i
```

**Genero Studio** — paste the output of `fglpkg env --gst` into your project's environment settings.

### Stale lock file

If dependencies in `fglpkg.json` have changed and `fglpkg install` says the lock file is stale, it will automatically re-resolve. You can also force it:

```bash
fglpkg update
```

### `fglpkg list` shows a package I removed

`list` reports what is actually installed under `.fglpkg/packages/`, so a package that is gone from `fglpkg.json` but still listed means the store hasn't been reconciled yet. Run `fglpkg install` — it re-resolves and prunes the orphan (see [Pruning](#pruning-converging-on-the-manifest)). If it persists, you are most likely on a **global** install (`~/.fglpkg/`), which is shared across projects and deliberately never pruned; check with `fglpkg list --local` vs `fglpkg list --global`.

### Wrong Genero version detected

Override the detected version:

```bash
# macOS / Linux
export FGLPKG_GENERO_VERSION=4.1.0
fglpkg install
```

```cmd
REM Windows
SET FGLPKG_GENERO_VERSION=4.1.0
fglpkg install
```

### Using a private registry

Point fglpkg at your registry:

```bash
# macOS / Linux
export FGLPKG_REGISTRY=https://registry.example.com
```

```cmd
REM Windows
SET FGLPKG_REGISTRY=https://registry.example.com
```

Add this to your shell profile or batch script for persistence.

### Checking the installed version

```bash
fglpkg version
```

This shows the version and build number embedded at compile time.
