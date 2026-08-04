# Configuring and using JFrog Artifactory with fglpkg

This guide shows how to use a **JFrog Artifactory** repository with `fglpkg` — to
host your own private Genero BDL (FGL) packages, to consume them alongside (or
instead of) the public Genero Intelligence (GI) registry, and to route Java/JAR
dependencies through an Artifactory Maven repository.

It is a task-oriented walkthrough. For the exhaustive manifest-key schema see the
[`fglpkg.json` reference](fglpkg-json-reference.md#610-registries--default-registry);
for the full design and security rationale see the design spec,
[specs/artifactory-secondary-repository.md](../specs/artifactory-secondary-repository.md).

## What this gives you

- **Private FGL packages.** Publish and install BDL packages from your own
  Artifactory instead of (or as well as) the public GI registry.
- **A checked-in registry config.** Declare the repository in your project's
  `fglpkg.json` so a fresh clone resolves from the right place with no per-machine
  setup — only credentials stay per-developer.
- **A dependency-confusion defense.** When a package name exists in more than one
  configured repository, fglpkg refuses to guess: it is a hard error until you pin
  the source. There is no silent "internal wins" / "public wins" fallback.
- **Java/JAR dependencies via Artifactory.** Point JAR downloads at an Artifactory
  Maven repository instead of Maven Central.

> **Two separate mechanisms.** FGL/BDL **packages** live in an Artifactory
> **generic** repository and are declared under `registries`. Java **JARs** are a
> *different* path: they are fetched from a Maven-layout repository and configured
> with the top-level `mavenMirror` field, not `registries`. Don't mix the two —
> see [Routing Java/JAR dependencies](#routing-javajar-dependencies) below.

## How it fits together

fglpkg treats configured repositories as a **cascade**, merged by name in
increasing precedence (a later layer replaces an entry of the same name):

| Layer | File | Scope | Committed? |
|---|---|---|---|
| Built-in | — | The GI registry (`gi`), always present | n/a |
| Global | `~/.fglpkg/config.json` | Per-user / per-machine | No |
| Project | `fglpkg.json` (`registries`) | This repo — team-shared | **Yes** |

Three rules follow from this model and are worth internalising up front:

- **Descriptors carry no secrets.** A `registries` entry has only a URL, a repo
  key, a priority and an auth *scheme* — never a token. Credentials live
  exclusively in `~/.fglpkg/credentials.json` (mode `0600`), keyed by repository
  URL, written only by `fglpkg login`. That's why the descriptor can be committed.
- **`priority` is query order, not precedence.** Lower is tried first and it
  breaks ties in `search` de-duplication. It does **not** decide which repository
  a name resolves from — a name in two repositories is always a hard error, never
  tie-broken by priority.
- **Only the built-in `gi` registry may be `type: "genero"`.** Every repository
  you add points at Artifactory and is `type: "artifactory"`.

## 1. Set up the Artifactory repository

fglpkg imposes its own path layout on a plain Artifactory **generic** repository,
so the server side is minimal — no plugin, no custom repository layout, no index
file.

On the Artifactory side, an administrator needs to:

1. **Create a generic local repository** for FGL packages — for example with the
   repository key `fgl-internal-generic`. It must be a **generic** repo, *not* a
   Maven repo and *not* a virtual repo. (JAR mirroring, covered later, uses a
   *separate* Maven repository.)
2. **Confirm SHA-256 support** — Artifactory computes and returns SHA-256
   checksums at deploy time on v5.5+, which every current release satisfies.
   fglpkg verifies the SHA-256 it reads from Artifactory's File Info against the
   downloaded bytes, so no manual `checksum` field is needed.
3. **Provision credentials** — create an access token (recommended) or a user
   with **deploy** and **read** permission on that repository. Anonymous read is
   only usable if the repository permits unauthenticated reads, which is commonly
   disabled (it is on JFrog Cloud trials); **publishing always requires
   credentials** regardless of read anonymity.

Note two values for the next step: the **base URL** (including Artifactory's
context path, e.g. `https://artifactory.acme.example/artifactory`) and the
**repository key** (e.g. `fgl-internal-generic`).

<details>
<summary>How fglpkg lays packages out in the generic repo (informational)</summary>

You don't configure any of this — it's what fglpkg reads and writes under the
repository — but it helps when browsing the repo or debugging:

```
{url}/{repoKey}/{name}/{version}/{name}-{version}-genero{N}.zip   # one zip per Genero major variant
{url}/{repoKey}/{name}/{version}/fglpkg.json                      # per-version metadata sidecar
```

For example `…/fgl-internal-generic/acme-utils/1.2.0/acme-utils-1.2.0-genero6.zip`.
Version discovery uses the Artifactory Storage REST API
(`GET {url}/api/storage/{repoKey}/{name}` — the child folders are the versions);
the zip bytes and the sidecar `fglpkg.json` are fetched from the direct content
path. The sidecar is the package's own manifest uploaded verbatim and supplies
its dependencies and Genero constraint.
</details>

## 2. Declare the registry

Register the repository with `fglpkg registry add`:

```bash
# Machine-wide (writes ~/.fglpkg/config.json):
fglpkg registry add acme https://artifactory.acme.example/artifactory \
    --repo-key fgl-internal-generic --packages "acme-*"

# Or committed to this project (writes fglpkg.json):
fglpkg registry add acme https://artifactory.acme.example/artifactory \
    --repo-key fgl-internal-generic --packages "acme-*" --project
```

`registry add <name> <url>` defaults to `type=artifactory`, so you rarely pass
`--type`. The flags:

| Flag | Meaning |
|---|---|
| `--repo-key <k>` | The Artifactory generic-repo key. Required for `artifactory` unless the URL already carries it (see below). |
| `--auth <scheme>` | `bearer` \| `basic` \| `apikey` \| `anonymous`. Default `bearer`. |
| `--priority <n>` | Lower is tried first; must be unique. Defaults to `max + 1` when omitted (the built-in `gi` is priority `1`). |
| `--packages <globs>` | Comma-separated name-scope allow-list, e.g. `'acme-*,foo-*'`. Omit to consult the repo for any name. |
| `--project` | Write to the project `fglpkg.json` instead of `~/.fglpkg/config.json`. Requires an `fglpkg.json` in the current directory. |
| `--consume-default` | Also make this the default consume registry (see [Choosing a default](#choosing-a-default-registry)). |
| `--type <t>` | `genero` \| `artifactory` (default `artifactory`). Only the built-in `gi` may be `genero`. |

**Repo-key inference.** If you paste the repository URL exactly as Artifactory
shows it — `https://acme.jfrog.io/artifactory/GeneroBDL` — fglpkg splits the
trailing segment off as the repo key and records the base URL, so `--repo-key`
becomes optional:

```bash
fglpkg registry add acme https://acme.jfrog.io/artifactory/GeneroBDL
#   Inferred repo key "GeneroBDL" from the URL; base URL recorded as https://acme.jfrog.io/artifactory
```

Inference is deliberately narrow — it fires only for a URL shaped exactly
`<scheme>://<host>/artifactory/<key>`. A bare base URL, a deeper path, or a URL
with a query/fragment leaves `--repo-key` required. If you pass `--repo-key` *and*
the URL carries a key, they must agree or the add is rejected.

**Confirm what's configured** with `fglpkg registry list`:

```
NAME   TYPE         PRIO  AUTH    LOGIN  SOURCE   DEFAULT  URL
gi     genero       1     bearer  env    builtin  -        https://service.generointelligence.ai
acme   artifactory  2     bearer  no     project  -        https://artifactory.acme.example/artifactory
```

The three derived columns are worth reading:

- **SOURCE** — which layer defined the row: `builtin`, `global`, or `project`.
- **DEFAULT** — the default role it serves: `consume`, `publish`, `both`, or `-`.
- **LOGIN** — credential state: `yes` (stored), `no` (none), `anon` (anonymous
  scheme), or `env` (the GI registry authenticated by `FGLPKG_TOKEN` — this only
  ever applies to `gi`, never to an Artifactory repo).

Remove a repository with `fglpkg registry remove acme` (add `--project` to remove
one declared in `fglpkg.json`). The built-in `gi` registry cannot be redefined or
removed.

The `registries` descriptor is documented as a schema in the
[`fglpkg.json` reference](fglpkg-json-reference.md#610-registries--default-registry).

## 3. Authenticate

Credentials are stored separately from the descriptor, so log in per developer /
per machine after the repository is declared:

```bash
fglpkg login --registry acme --token <access-token>          # bearer  (recommended)
fglpkg login --registry acme --user <u> --password <p|token> # basic
fglpkg login --registry acme --api-key <key>                 # apikey  (legacy)
fglpkg logout --registry acme
```

The flag you pass must match the repository's configured `auth` scheme:

| Scheme | Login flag | Header fglpkg sends |
|---|---|---|
| `bearer` (default, recommended) | `--token <access-token>` | `Authorization: Bearer <token>` |
| `basic` | `--user <u> --password <p\|token>` | `Authorization: Basic base64(user:secret)` |
| `apikey` | `--api-key <key>` | `X-JFrog-Art-Api: <key>` |
| `anonymous` | *(no login needed)* | *(none)* |

A JFrog access token works either as the `bearer` token or as the `basic`
password (`user:<token>`). JFrog **API keys are deprecated/EOL** in newer
Artifactory — prefer a bearer access token.

Things to know:

- **Credentials never touch `fglpkg.json` or `config.json`.** A successful login
  writes only `~/.fglpkg/credentials.json` (mode `0600`), keyed by the repository
  URL. This is what makes the committed `registries` block safe.
- **You must pass the secret on the command line.** `fglpkg login --registry acme`
  with no secret flag does *not* prompt — it errors telling you which flag the
  repository's scheme needs.
- **GI and each secondary repo are independent.** Logging in to one never affects
  another.
- **`FGLPKG_TOKEN` authenticates the GI registry only.** It does not authenticate
  an Artifactory repository (or the Maven mirror) — those always use stored
  credentials.

## 4. Consume packages

With the repository declared and (if needed) logged in, install and update work
as usual; fglpkg fans out across the configured repositories and tags each result
with its source.

```bash
fglpkg install acme-utils                 # resolve across all repos
fglpkg install acme-utils --registry acme # resolve only from acme, and pin that source
fglpkg search utils --registry acme       # search only acme (results stay source-tagged)
fglpkg update                             # re-resolve the whole graph
```

**Routing precedence.** For each package name, fglpkg picks a repository in this
order (strongest first):

1. an explicit `--registry <name>` on the command;
2. a per-dependency pin in your `fglpkg.json`;
3. a pin declared by a depending package (transitive; warned once);
4. the configured [default consume registry](#choosing-a-default-registry), if any;
5. otherwise, fan out to every admitting repository and apply the
   [collision guard](#the-collision-guard-dependency-confusion).

**Pinning a source in the manifest.** `install --registry` writes the pin for you;
you can also write it by hand as the object form of a dependency:

```json
{
  "dependencies": {
    "fgl": {
      "acme-utils": { "version": "^1.2.0", "registry": "acme" }
    }
  }
}
```

The resolved source is recorded in `fglpkg.lock` (a package's `registry` field),
so a locked install re-fetches each package from exactly that repository and can
never be silently re-routed. An empty value means the default GI registry, so
older locks keep working unchanged.

> `fglpkg install <pkg> --registry <name>` requires a package argument (it pins
> that package). `fglpkg update --registry <name>` needs no argument — it restricts
> the whole re-resolution to one repository.

## 5. Publish packages

Publish to your Artifactory the same way you build for GI — just target the
repository by name:

```bash
fglpkg publish --registry acme            # deploy the built zip + sidecar manifest
fglpkg publish --registry acme --dry-run  # print the exact PUT URLs, no network
fglpkg publish --registry acme --force    # overwrite an existing variant (guarded by default)
```

Publishing to Artifactory is a direct two-step deploy: fglpkg `PUT`s the package
zip (with an `X-Checksum-Sha256` header that Artifactory verifies on receipt) and
then `PUT`s the sidecar `fglpkg.json`. Notes:

- **No approval step and no visibility.** Unlike GI, there is no pending/review
  step, and `--private` / `--public` is ignored (access is governed by Artifactory
  permissions).
- **Overwrites are guarded.** Publishing a *new* variant under an existing version
  is additive and allowed. Re-publishing an *existing* variant is refused unless
  you pass `--force`; a repository configured immutable returns `409` and fglpkg
  surfaces that rather than overwriting.
- **Credentials required.** Publishing always needs a login, even if the repo
  allows anonymous reads.

To stop typing `--registry` on every publish, set a default publish target — see
below.

## Choosing a default registry

fglpkg has two **separate** defaults; keep them distinct.

**Publish default** — where `fglpkg publish` deploys when you omit `--registry`.
Resolved in decreasing precedence: `FGLPKG_PUBLISH_REGISTRY` → the project
`fglpkg.json` `"defaultRegistry"` → the global `config.json` `"defaultRegistry"`
→ GI. It is publish-only and never affects where packages are consumed from.

```json
{ "defaultRegistry": "acme" }
```

**Consume default** — the single repository that `install` / `update` / `search`
/ `info` / `outdated` resolve from when you don't pass `--registry`. Resolved:
`FGLPKG_CONSUME_REGISTRY` → the project `fglpkg.json` `"defaultConsumeRegistry"`
→ the global `config.json` `"defaultConsumeRegistry"` → (unset: consult every
configured repository). Set it with:

```bash
fglpkg registry add acme https://acme.jfrog.io/artifactory/GeneroBDL --project --consume-default
```

The consume default is **exclusion, not precedence**: only that one repository is
consulted for an unpinned name, so it yields 0 or 1 result and never silently
tie-breaks a collision. A per-dependency pin and an explicit `--registry` both
override it. This is the setting for the "our Artifactory proxies everything,
including public packages" case, where consulting GI in parallel would collide on
every public name.

(`info` and `outdated` have no `--registry` flag, so the consume default is their
only scoping input.)

## The collision guard (dependency confusion)

When more than one repository is configured and a package name is **unpinned**,
fglpkg queries every admitting repository and counts hits:

- **0 hits** → not found;
- **1 hit** → resolve and record the source in the lockfile;
- **≥2 hits** → a **hard error**. fglpkg refuses to guess and tells you to pin the
  source or rename.

This is the dependency-confusion defense: a name present in both your Artifactory
and the public GI registry never resolves silently, and there is deliberately no
"internal wins", "public wins", or "higher priority wins" mode. An auth failure
(401/403) during the fan-out is likewise a hard error — never folded into "not
found" — so an expired token can't silently drop a repository from the count and
let a package mis-route.

Ways to keep names unambiguous:

- **Prefix internal packages** (e.g. `acme-*`) and set a `--packages "acme-*"`
  allow-list on the Artifactory descriptor, making the public/internal split
  structural — the repo is only ever queried for names it owns.
- **Pin the source** of any genuinely colliding name in `fglpkg.json` (the object
  form above), or restrict a command with `--registry`.
- **Where your Artifactory proxies everything**, set a
  [consume default](#choosing-a-default-registry) so GI is out of the picture.

## Routing Java/JAR dependencies

Java/JAR dependencies are a **separate mechanism** from FGL packages: they are not
served from the generic `registries` repository. By default JARs come from Maven
Central; to route them through an Artifactory **Maven** repository (remote or
virtual), set the top-level `mavenMirror` field:

```json
{
  "mavenMirror": {
    "url": "https://artifactory.acme.example/artifactory/maven-virtual",
    "auth": "bearer"
  }
}
```

An Artifactory Maven repo serves the standard Maven2 layout, so only the base URL
changes. The mirror carries **no secrets**: authenticate it by declaring the
enclosing Artifactory base as a `registries` entry and logging in with
`fglpkg login --registry <name> --token <access-token>` — the stored credential is
matched onto the mirror by URL prefix. The base URL can also be set via the
`FGLPKG_MAVEN_URL` environment variable (highest precedence), and a per-dependency
`url` on a JAR still wins outright.

Full detail — precedence, credential matching, and lockfile pinning — is in the
[user guide](user-guide.md#routing-jars-through-an-internal-maven-mirror) and the
[`fglpkg.json` reference §6.9](fglpkg-json-reference.md#69-maven-mirror-for-jar-downloads).

## Team & CI setup

**"Clone → login → install just works."** Commit the repository declaration to the
project `fglpkg.json` (with `--project`), so a teammate who clones the repo
inherits the registry with no machine configuration — they only run
`fglpkg login --registry <name>` once to store their own credentials:

```bash
git clone …/our-app && cd our-app
fglpkg login --registry acme --token <their-access-token>
fglpkg install    # resolves acme-* from Artifactory, everything else from GI
```

**CI / non-interactive.** Give the CI job a scoped access token and log in
non-interactively before installing:

```bash
fglpkg login --registry acme --token "$ARTIFACTORY_TOKEN"
fglpkg install --frozen
```

`FGLPKG_TOKEN` covers the **GI** registry only; a secondary Artifactory repo needs
its own `fglpkg login --registry`, so authenticate each secondary explicitly in CI.

## Troubleshooting

| Symptom | Likely cause & fix |
|---|---|
| `HTTP 401` / `403` / "authentication failed" on install or publish | Missing/expired/mis-scoped credential (Artifactory may return either 401 or 403). Run `fglpkg login --registry <name>` with a token that has read (and, for publish, deploy) permission on the repo. |
| `registry "<name>" is not configured` | The name isn't declared in this scope. Check `fglpkg registry list`; add it, or (in a project) ensure you're in the directory with the committed `fglpkg.json`. |
| `package "<x>" is available from more than one repository` | The [collision guard](#the-collision-guard-dependency-confusion) fired. Pin the source in `fglpkg.json`, pass `--registry`, or scope the repo with `--packages`. |
| `registry "<name>" (type=artifactory) needs a generic-repo key` | Pass `--repo-key <k>`, or paste the full `…/artifactory/<key>` URL so it's inferred. |
| `--repo-key "<x>" disagrees with the repo key "<y>" in the URL` | The flag and the URL's trailing segment differ. Pass the base URL with the flag, or drop the flag and let the URL supply the key. |
| `variant already exists … re-run with --force` on publish | The version+variant is already deployed. Bump the version, or pass `--force` to overwrite (unless the repo is immutable, which returns `409`). |
| `mavenMirror requires a non-empty 'url'` / `… unknown auth …` at pack/publish | The `mavenMirror` block has an empty `url` or an `auth` that isn't `bearer\|basic\|apikey\|anonymous`. Both are caught at pack/publish (an omitted `auth` is fine — it defaults to `bearer`). Fix the block. If downloads still 403 *after* the block is valid, the cause is a credential mismatch — see the next row. |
| JAR download 403 from your mirror | The mirror credential isn't resolving. Declare the enclosing Artifactory base under `registries`, `fglpkg login --registry <name> --token …`, and make sure `mavenMirror.auth` matches how you logged in. |

## Reference & further reading

- [Secondary Package Repositories (README)](../README.md#secondary-package-repositories-jfrog-artifactory) — the narrative reference: cascade, routing, collision guard, publishing.
- [Secondary Repositories (user guide)](user-guide.md#secondary-repositories-jfrog-artifactory) — the step-by-step walkthrough.
- [`fglpkg.json` reference §6.10](fglpkg-json-reference.md#610-registries--default-registry) — authoritative schema for `registries`, `defaultRegistry`, and `defaultConsumeRegistry`.
- [`fglpkg.json` reference §6.9](fglpkg-json-reference.md#69-maven-mirror-for-jar-downloads) — the `mavenMirror` schema.
- [specs/artifactory-secondary-repository.md](../specs/artifactory-secondary-repository.md) — the full design and security rationale, including the exact Artifactory REST layout validated against a live JFrog instance.
