# Artifactory quickstart

Use your own **JFrog Artifactory** to host and share your organization's private
Genero BDL (FGL) packages with `fglpkg`, alongside the public Genero Intelligence
(GI) registry. This is the short, get-going path; for the complete reference —
authentication schemes, the dependency-confusion guard, Maven/JAR routing,
default registries, and troubleshooting — see the
[full Artifactory guide](artifactory.md).

**You need:** a **generic** Artifactory repository for your packages (for example
with the repo key `fgl-internal-generic`) and an access token with **read**
permission — plus **deploy** if you'll publish. Everything else is client-side;
there's no server plugin to install.

## 1. Declare your repository

```bash
fglpkg registry add acme https://artifactory.acme.example/artifactory \
    --repo-key fgl-internal-generic --packages "acme-*" --local
```

- `acme` is the short name you'll use in later commands — substitute your own URL
  and repository key.
- `--packages "acme-*"` scopes the repository to your naming prefix, so public
  package names are never looked up there (recommended — see step 3).
- `--local` writes the entry to your project's `fglpkg.json`, so a teammate who
  clones the repo inherits it automatically. Omit it to configure only your
  machine (`~/.fglpkg/config.json`).

Confirm it with `fglpkg registry list`.

## 2. Log in (once per developer / machine)

```bash
fglpkg login --registry acme --token <access-token>
```

Credentials are stored in `~/.fglpkg/credentials.json` (mode `0600`) — **never**
in `fglpkg.json` — which is exactly why the declaration from step 1 is safe to
commit and share.

## 3. Install packages

```bash
fglpkg install acme-utils     # from your Artifactory; public deps still resolve from GI
fglpkg install                # restore everything a project already declares
```

If a package name exists in **both** your Artifactory and GI, fglpkg refuses to
guess and asks you to disambiguate — this is the dependency-confusion guard.
Prefixing your internal packages (`acme-*`) and setting the `--packages`
allow-list in step 1 keeps the public/internal split clean. You can also pin a
single dependency's source explicitly:

```bash
fglpkg install acme-utils --registry acme
```

## 4. Publish a package

From the package directory:

```bash
fglpkg bump patch                # or: minor | major
fglpkg publish --registry acme   # add --dry-run first to preview the upload, no network
```

Publishing to Artifactory is immediate — there is no review/approval step (that
is GI-only). Re-publishing an already-deployed version + Genero variant is refused
unless you pass `--force`.

## In CI

Commit the `--local` declaration from step 1, give the job a scoped access token,
and log in before a frozen install:

```bash
fglpkg login --registry acme --token "$ARTIFACTORY_TOKEN"
fglpkg install --frozen
```

`FGLPKG_TOKEN` authenticates the **GI** registry only; each Artifactory repository
needs its own `fglpkg login --registry`.

## Also: Java / JAR dependencies

JARs are a **separate** mechanism from FGL packages — route them through an
Artifactory **Maven** repository with the top-level `mavenMirror` field, not
`registries`. See
[Routing Java/JAR dependencies](artifactory.md#routing-javajar-dependencies) in
the full guide.

## Next steps

- [Full Artifactory guide](artifactory.md) — authentication schemes, default
  registries, the collision guard in depth, Maven routing, and a troubleshooting
  table.
- [`fglpkg.json` reference](fglpkg-json-reference.md#610-registries--default-registry)
  — the authoritative `registries` schema.
