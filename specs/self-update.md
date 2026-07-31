# Spec: `fglpkg self-update` — self-updating binary + periodic update notices

**Status:** ✅ Implemented — GIS-255. The client (`self-update` + `upgrade` alias, passive update check, R1 Ed25519 signature verify against the pinned root, R2 GI-served recovery info) merged to `main` via PR #17. The companion GI endpoint `GET /registry/fglpkg/latest` (GIS-256) shipped and is Closed, so self-update is wired end-to-end. Remaining: macOS delivery is gated on Developer-ID-notarized releases (GIS-257).
**Date:** 2026-07-14
**Author:** Mike Folcher
**Motivation:** fglpkg ships as a standalone binary users copy into `PATH` by hand
([README install section](../README.md)). There is no upgrade path: to move from v3.3.0 to v3.4.0
a user must re-download the right asset for their OS/arch, clear the macOS quarantine flag, and copy
it over the old one. Most never do, so field installs drift far behind. fglpkg should be able to
update *itself*, and — like `npm`, `gh`, and `brew` — passively let users know when a newer version
is available.
**Related:** [macos-release-signing.md](macos-release-signing.md) (a self-updated macOS binary must
be signed/notarized or Gatekeeper blocks it — see [§ Platform notes](#platform-notes));
[gi-fglpkg-self-update-endpoint.md](gi-fglpkg-self-update-endpoint.md) (the service-side spec for the
registry endpoint this consumes).

---

## Summary

Two related capabilities:

1. **`fglpkg self-update`** — a new command that downloads the latest stable release binary for the
   current OS/arch, verifies its **Ed25519 release signature and** SHA-256 checksum, and atomically
   replaces the running executable.
2. **A passive update check** — piggybacked on ordinary command runs (no daemon). At most once per
   check interval, fglpkg asks the registry for the latest version in the background and, if a newer
   one exists, prints a one-line notice **after** the command's own output. On by default; disabled
   in CI, for `dev` builds, and whenever the user opts out.

Both learn the latest version from the **Genero Intelligence registry** (a new
`GET /registry/fglpkg/latest` endpoint), not from GitHub directly, keeping all client network
traffic on the registry the user already trusts and authenticates against. The registry response
carries the download URL and checksum per platform asset, so the client stays agnostic about where
binaries are actually hosted.

Scope is deliberately narrow: **latest stable only** — no version pinning, no pre-releases, no
downgrade.

## Background — how it works today

### The binary knows its own version but can do nothing with it

`cli.Version` / `cli.Build` are stamped at build time via `-ldflags`
([cmd/build.sh:5](../cmd/build.sh#L5)); a plain `go build` leaves the sentinel values:

```go
// internal/cli/cli.go:58-61
Version = "dev"
Build   = "unknown"
```

[`cmdVersion`](../internal/cli/version.go#L26) prints them and nothing else. There is no notion of a
"latest available" version anywhere in the client.

### Releases are GitHub assets with a checksum manifest

[release.yml](../.github/workflows/release.yml) fires on a `v*` tag, runs `cmd/build.sh` to produce
six binaries named `fglpkg-<os>-<arch>[.exe]`, generates `checksums.txt`
(`sha256sum * > checksums.txt`), and uploads all of them to a GitHub Release. So each release already
has both the binaries and a `checksums.txt` at predictable URLs; the registry endpoint
([§ Registry contract](#registry-contract-new)) surfaces it to clients.

### There is a proven pattern for JSON state under `~/.fglpkg`

[credentials.go](../internal/credentials/credentials.go) stores a forward-compatible JSON file
(`credentials.json`, mode 0600) in the fglpkg home resolved by
[`fglpkgHome()`](../internal/cli/cli.go#L2589) (honors `FGLPKG_HOME`, else `~/.fglpkg`). Update-check
state reuses this exact pattern via a new `config.json` ([§ Config & state](#config--state-configjson)).

### The registry client already centralizes base URL + auth

[`registryBase()`](../internal/registry/registry.go#L533) resolves `FGLPKG_REGISTRY` (default
`https://service.generointelligence.ai`) and [`httpGetAuthed`](../internal/registry/registry.go#L543)
performs authenticated GETs. The update check is one more call through this same client.

## Registry contract (new)

A new endpoint the client depends on. **Additive and unauthenticated-friendly** — the latest fglpkg
version is public information; auth, if present, is only used to lift rate limits. The service side
of this endpoint is specified in full in
[gi-fglpkg-self-update-endpoint.md](gi-fglpkg-self-update-endpoint.md); this section is the client's
view of the same contract.

```
GET /registry/fglpkg/latest
200 →
{
  "version": "3.4.0",
  "notes": "https://github.com/4js-mikefolcher/fglpkg/releases/tag/v3.4.0",
  "checksumsUrl": "https://github.com/4js-mikefolcher/fglpkg/releases/download/v3.4.0/checksums.txt",
  "checksumsSigUrl": "https://github.com/4js-mikefolcher/fglpkg/releases/download/v3.4.0/checksums.txt.sig",
  "manualUrl": "https://github.com/4js-mikefolcher/fglpkg/releases/tag/v3.4.0",
  "instructions": "Download the binary for your platform, verify its SHA-256, and replace your fglpkg executable. See the release page.",
  "assets": [
    { "os": "darwin", "arch": "arm64", "url": "https://github.com/.../v3.4.0/fglpkg-darwin-arm64" },
    { "os": "linux",  "arch": "amd64", "url": "https://github.com/.../v3.4.0/fglpkg-linux-amd64" },
    { "os": "windows","arch": "amd64", "url": "https://github.com/.../v3.4.0/fglpkg-windows-amd64.exe" }
    // … all six
  ]
}
```

- `version` is the latest **stable** release (no pre-release/`-rc` tags).
- `os`/`arch` use Go's `runtime.GOOS`/`runtime.GOARCH` spellings, so the client matches directly.
- The `url`s point at **GitHub Releases** — GI stores only the version and derives URLs from it; it
  does not host or proxy binaries.
- **Checksums come via `checksumsUrl`, not inline.** GI returns the URL of the release's
  `checksums.txt`; the client fetches and parses it to get the expected SHA-256 for its asset (the
  file is `sha256sum` output: `<hex>  <filename>` lines). This keeps GI a pure URL provider while
  preserving the integrity gate below. If `checksumsUrl` is absent or the fetch fails, self-update
  **aborts** rather than installing an unverified binary.
- **`checksumsSigUrl` — a detached Ed25519 signature over `checksums.txt`.** This is the *authenticity*
  gate; the SHA-256 above is only integrity / anti-corruption. The client verifies this signature back
  to the **pinned root key** before trusting `checksums.txt` — see
  [§ Release signing & verification](#release-signing--verification). If `checksumsSigUrl` is absent or
  the signature fails, self-update **aborts**.
- **`manualUrl` + `instructions` — the operator-configurable recovery path.** GI returns where to
  download by hand and human-readable steps; the client prints them verbatim whenever an update is
  blocked or fails (bad signature, no asset for this platform, permission error, etc.). Nothing about
  the download location or instructions is hardcoded in the binary, so distribution can move (e.g. off
  GitHub) without a client release.
- A registry that predates this endpoint returns `404`; the client treats that as "no update info"
  (silent no-op for the passive check; a clear message for explicit `self-update`).

A new `registry.FetchLatestFGLPkg() (*LatestRelease, error)` wraps this call in
[internal/registry/registry.go](../internal/registry/registry.go), returning a typed struct.

## Release signing & verification

Self-update is gated on **authenticity, not just integrity** (GIS-255 R1): a SHA-256 check proves a
download wasn't corrupted in transit, but not that the release itself is genuine — a compromised GitHub
release or a hijacked version pointer would still checksum-match. So `self-update` **must verify an
Ed25519 signature over the release before it installs anything**, on every platform. Verification is
**fully offline** (no Rekor / network dependency) — which is why Ed25519 was chosen over Sigstore here
(GIS-255, 2026-07-16 design review).

This reuses Layer 1's **two-tier key model** ([`gen-signing-key.mjs`](reference-genero-intelligence)),
not just its verify code:

- The **offline root key** never touches CI or a repo. It stays offline and *certifies* a dedicated
  **release-signing working key** via a signed key manifest — exactly as for package signing.
- The **working key** lives as a CI secret and, in [release.yml](../.github/workflows/release.yml),
  signs `checksums.txt` → `checksums.txt.sig` (detached Ed25519). Signing therefore happens **inside
  the build pipeline** without ever exposing the root. (Signing the single `checksums.txt` transitively
  covers every binary, since each asset's SHA-256 is listed there.)
- The **client pins the root public key** (it already does for Layer 1). On update it fetches the
  working-key manifest + `checksums.txt.sig`, verifies the manifest against the pinned root, then
  verifies `checksums.txt.sig` with the working key — establishing a chain to the offline root. Only
  then does it trust the SHA-256 values in `checksums.txt`.
- If the signature or working-key manifest is missing or fails to verify, self-update **aborts** and
  prints the `manualUrl` / `instructions` recovery path — it never installs an unverified binary.

> **One detail to finalize in implementation:** how the client obtains the release-signing working-key
> manifest. Preferred: publish it as a release asset (e.g. `keys.json` alongside `checksums.txt.sig`)
> so verification needs only the release + the pinned root and stays fully offline; the GI endpoint can
> also surface its URL. (Reusing the package-signing `/.well-known/keys.json` manifest is possible but
> couples release trust to the registry being reachable, so the release-asset form is preferred.)

## `fglpkg self-update`

New command wired into the dispatch switch ([cli.go:140+](../internal/cli/cli.go#L140)) as
`self-update` (alias `upgrade`), backed by a new `internal/selfupdate` package.

### Flags

| Flag | Effect |
|---|---|
| `--check` | Report whether an update exists and exit 0 (newer available) / 0 (up to date). Never writes. |
| `--yes`, `-y` | Skip the confirmation prompt (for scripts). |
| `--force` | Re-install even if already on the latest version (repair a corrupt/quarantined binary). |

No `--version` / `--pre` / downgrade — latest stable only, per scope.

### Flow

1. **Guard managed installs.** If `Version == "dev"`, refuse: this is a source build with no release
   to update to. If the running executable lives under a package-manager prefix (e.g. a Homebrew
   Cellar path, or a path not writable by the user), refuse with a hint to use that manager instead.
   Detection is best-effort and conservative — when unsure, proceed and let the atomic-write step
   fail cleanly.
2. **Resolve latest** via `registry.FetchLatestFGLPkg()`. Compare to `cli.Version` using
   [`internal/semver`](../internal/semver). If not newer and not `--force`, print
   `fglpkg is up to date (vX.Y.Z)` and exit. The contract says `version` is the latest *stable*
   release; enforce that **client-side** too — if it carries a pre-release segment, say so and exit
   without installing. Checked before `--check` and `--force`, neither of which may widen the scope,
   and mirrored in the passive notice so it never advertises an rc that self-update would then refuse.
3. **Select the asset** matching `runtime.GOOS`/`runtime.GOARCH`. If none, print the GI-provided
   `manualUrl` + `instructions` and exit non-zero.
   All network steps are **bounded**: the small signed-metadata GETs carry a 30s deadline and a 1 MiB
   read cap (an oversized body is rejected, not truncated), the binary download a generous overall
   deadline plus a size cap, and every request per-phase dial/TLS/response-header timeouts. The
   registry client shares the same transport-level timeouts but no overall deadline, so long
   legitimate uploads and downloads still stream.
4. **Fetch + authenticate `checksums.txt`.** GET `checksumsUrl`, then GET `checksumsSigUrl` and the
   release-signing working-key manifest, and verify the Ed25519 signature chain back to the **pinned
   root** (see [§ Release signing & verification](#release-signing--verification)). **Only after the
   signature verifies**, parse the `sha256sum`-format lines (`<hex>  <filename>`) and look up the
   selected asset's filename. If `checksumsUrl`/`checksumsSigUrl` is missing, the signature fails, or
   the entry can't be found, **abort and print `manualUrl` + `instructions`** — self-update never
   installs an unverified binary.
5. **Confirm** (unless `--yes`): `Update fglpkg vCUR → vNEW? [Y/n]` via the existing
   [`promptYesNo`](../internal/cli/cli.go#L740).
6. **Download** the asset to a temp file **in the same directory as the target executable** (so the
   final rename is same-filesystem and atomic — a cross-device `os.Rename` fails). Stream to disk.
7. **Verify** the computed SHA-256 against the (now signature-authenticated) expected value from
   step 4 using the existing [checksum](../internal/checksum) streaming verifier. Mismatch → delete
   temp, print `manualUrl` + `instructions`, abort, exit non-zero. This is the integrity gate on top of
   step 4's authenticity gate — never install an unverified binary.
8. **Swap atomically** (see below), preserving the original file mode; `chmod +x` on Unix.
9. Print `Updated fglpkg vCUR → vNEW`. Refresh the cached latest in `update-check.json` so the passive
   notice goes quiet immediately.

### Atomic swap

- **Unix:** write temp in the target dir, `chmod`, then `os.Rename(temp, exe)` — atomic on the same
  filesystem, and replacing a running binary's inode is safe (the running process keeps the old
  open file).
- **Windows:** a running `.exe` cannot be overwritten. Rename the running exe to `fglpkg.old` (in
  place), `os.Rename` the new binary into the real path, and best-effort delete `fglpkg.old` on the
  next run. Document that a leftover `fglpkg.old` is harmless.
- `os.Executable()` gives the path; resolve symlinks with `filepath.EvalSymlinks` so we replace the
  real file, not a symlink into it.

## Passive update check

### Model

Piggybacked on command runs — **no daemon, no cron**. **Notifying and refreshing are separate
concerns** (the npm/brew pattern):

- **Notify** — every allowed run prints the notice from the cached `latestKnownVersion`, after the
  invoked command's output. No network involved, so the notice is instant, works offline, and appears
  on throttled runs too.
- **Refresh** — when `updateCheckInterval` has elapsed, a goroutine kicked off at command start
  fetches the latest version and updates the cache. The attempt time is persisted **synchronously,
  before the fetch starts**: a fast local command (`list`, `env`) exits in tens of milliseconds and
  Go does not wait for the goroutine, so a `lastUpdateCheck` written only on completion would never
  land and every invocation would fire a fresh registry request. If the refresh does come back before
  the command finishes, its fresher answer supersedes the cached one for this run's notice — never
  two notices.

The check never blocks, never changes exit codes, and never emits errors to the user (network
failures are swallowed silently; a failed fetch keeps the previously cached version and still counts
as an attempt, so a flaky endpoint is not hammered).

### When it does *not* run

- `Version == "dev"` (source build).
- Any of these is set: `CI`, `FGLPKG_NO_UPDATE_CHECK=1`, or `updateCheck: false` in `config.json`.
- The command is itself `self-update` or `version` (they surface version info directly).
- stdout is not a TTY — the whole feature is off, network included. stdout is the command's data
  stream, so a redirected stdout is the signal that this run is scripted, and a scripted run should
  neither reach the network nor emit advisory chatter.
- Less than `updateCheckInterval` (default **24h**) since `lastUpdateCheck`: this throttles the
  **refresh only**. A cached known-newer version is still announced.

### The notice

Printed to **stderr** (keeps stdout clean for piping), one block, after the command's output. It is
suppressed when stderr is not a TTY — that is where it would be written, so a redirected stderr means
nobody would see it. (Both streams are consulted, and they answer different questions: stdout decides
whether the feature acts at all, stderr whether the notice can be seen.)

```
─────────────────────────────────────────────
 A new fglpkg is available: 3.3.0 → 3.4.0
 Run 'fglpkg self-update' to upgrade.
─────────────────────────────────────────────
```

## Config & state

**User settings** live in `~/.fglpkg/config.json` ([internal/config](../internal/config)), which the
user also hand-edits to declare registries. They are **read-only** to this feature:

```json
{
  "updateCheck": true,
  "updateCheckInterval": "24h"
}
```

- `updateCheck` (default `true`) — master opt-out; `FGLPKG_NO_UPDATE_CHECK=1` overrides to off.
- `updateCheckInterval` (default `24h`) — Go duration string; throttles the refresh.

Both are `omitempty`, so the read-modify-write cycle in `registry add`/`remove` can never inject them
into a config that did not set them. An explicit `false` still round-trips.

**Mutable state** lives in a separate tool-managed `~/.fglpkg/update-check.json`
([internal/updatecheck](../internal/updatecheck)) — mode 0600, written atomically via a per-writer
temp file + rename, so concurrent invocations cannot corrupt it and an advisory feature never
reformats the user's hand-edited config:

```json
{
  "lastUpdateCheck": "2026-07-14T09:00:00Z",
  "latestKnownVersion": "3.4.0"
}
```

- `lastUpdateCheck` — the last refresh *attempt* (written before the fetch, and on failure too), so
  the throttle holds regardless of how the run ends.
- `latestKnownVersion` — the last result, so a notice can be shown from cache with no network call.
  Refreshed on a successful `self-update` so the notice goes quiet immediately. Only ever a **stable**
  version: a pre-release is discarded before it reaches the cache, because storing one would displace a
  genuinely newer stable release and silence the notice for the rest of the interval.

A `fglpkg config` surface (get/set) is **out of scope**; for now the settings are edited by hand or via
the env var, and the tool maintains the state file. (A future `config` command can wrap it.)

## Platform notes

- **macOS Gatekeeper.** A binary written by fglpkg's own HTTP download does **not** get the
  `com.apple.quarantine` attribute (that flag is applied by browsers, not by API/curl-style
  fetches), so a self-updated binary avoids the quarantine prompt the README documents for browser
  downloads. It must still be **signed and notarized** or Gatekeeper rejects it on some
  configurations — this depends on [macos-release-signing.md](macos-release-signing.md) landing. Note
  the ordering dependency; do not ship self-update on macOS ahead of signed releases.
- **Windows** — the running-exe swap trick above; leftover `fglpkg.old` cleanup is best-effort.
- **Permissions** — if the executable sits in a root-owned dir (`/usr/local/bin`), the rename fails
  with EACCES; catch it and print a clear "re-run with sufficient privileges, or update manually:
  <url>" message rather than a raw error.

## Non-goals

- No **in-tool** version pinning, pre-release channel, or downgrade (`--version`/`--pre` excluded).
  The only recovery/downgrade path is the GI-served `manualUrl` + `instructions`, printed when an
  update is blocked or fails (GIS-255 R2).
- **No OS package-manager distribution** (Homebrew / Scoop / winget) — out of scope. Self-update is the
  upgrade path for the hand-copied binary; managed installs are only *detected* and deferred to, never
  produced. (Design review 2026-07-16.)
- No background daemon / scheduled task — checks only piggyback on invocations.
- No `fglpkg config` command in this spec (state file is edited by hand / env for now).
- No auto-apply — the passive check only *notifies*; it never updates without the user running
  `self-update`.
- Registry-side implementation of `/registry/fglpkg/latest` is specified separately in
  [gi-fglpkg-self-update-endpoint.md](gi-fglpkg-self-update-endpoint.md).

## Testing

- **semver comparison**: current vs latest → newer / equal / older, including `dev`.
- **`registry.FetchLatestFGLPkg`**: maps the JSON contract; asset selection by GOOS/GOARCH; missing
  asset → clear error. (Table-driven, mocked HTTP as in
  [registry_test.go](../internal/registry/registry_test.go).)
- **signature gate**: a `checksums.txt` whose Ed25519 signature is missing, doesn't verify against the
  pinned root, or is signed by a working key not certified by the root → self-update aborts *before*
  downloading the binary and prints `manualUrl`/`instructions`. The critical authenticity test.
- **checksum gate**: a tampered download (wrong sha256, signature valid) aborts and leaves the original
  binary untouched — the critical integrity test.
- **recovery output**: every abort path (no asset for platform, missing/invalid signature, checksum
  mismatch, permission error) prints the GI-served `manualUrl` + `instructions` verbatim.
- **atomic swap**: temp written in target dir; original mode preserved; Windows `.old` path
  exercised behind a GOOS guard.
- **gating logic**: the feature is off entirely for `CI`/env/`updateCheck:false`/`dev`/non-TTY stdout;
  the refresh is additionally skipped within the interval, while a cached notice is not. Pure
  functions over (now, config, env) — no network.
- **throttle durability**: the attempt time is on disk as soon as the check begins, so a command that
  exits before the fetch returns still throttles the next invocation.
- **notice source**: printed from cache on a throttled run; a fresh result supersedes the cached one;
  never printed twice; suppressed when stderr is not a TTY.
- **pre-release guard**: a `latest` carrying a pre-release segment is never installed, not even with
  `--force`, and is never advertised by the notice.
- **bounded network**: an unresponsive server times out rather than hanging; an oversized metadata
  body or download is rejected at the cap.
- **config.json**: defaults when absent; unknown fields rejected; the two update fields are never
  injected by `registry add`/`remove`, and an explicit `false` round-trips.
- **update-check.json**: absent/corrupt treated as zero; concurrent writers cannot corrupt it or
  leave temp files behind; mode 0600.

## Rollout

1. Land `internal/config` + the passive check (safe no-op until the registry endpoint exists — a 404
   is swallowed, so nothing regresses).
2. Land `internal/selfupdate` + the `self-update` command.
3. Gate the macOS path on signed/notarized releases ([macos-release-signing.md](macos-release-signing.md)).
4. Document `self-update`, the notice, `FGLPKG_NO_UPDATE_CHECK`, and `config.json` in
   [README.md](../README.md) and [docs/user-guide.md](../docs/user-guide.md).
