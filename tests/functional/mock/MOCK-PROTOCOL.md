# MOCK-PROTOCOL.md — Local mock of the fglpkg registry

A precise, implementable spec for a hermetic local mock (e.g. Python `http.server`)
that satisfies every fglpkg flow: consumer reads (`install`, `search`, `info`,
`outdated`), the artifact download, `publish --ci`, and `deprecate`. fglpkg is
pointed at the mock with a single environment variable.

All behavior below is derived from fglpkg source in
`/Users/mikefolcher/4js-github/fglpkg/internal/{registry,installer,cli,checksum,signing,lockfile,config,manifest}`.

---

## 1. Override & routing

### The one hook: `FGLPKG_REGISTRY`

```
export FGLPKG_REGISTRY=http://127.0.0.1:PORT
```

- `registryBase()` (`registry/registry.go:813-818`) reads `FGLPKG_REGISTRY`
  directly. If set and non-empty it wins over the built-in default
  (`https://service.generointelligence.ai`). **Trailing slashes are stripped**
  (`strings.TrimRight(r,"/")`); scheme/host/port are taken verbatim. So a bare
  `http://127.0.0.1:PORT` from a local server works directly.
- This one value routes **all** GI traffic to the mock:
  - metadata reads / version list — `GET {base}/registry/packages/{slug}`
  - search — `GET {base}/registry/packages?q=…`
  - self-update — `GET {base}/registry/fglpkg/latest`
  - all publish writes — `POST/PUT/PATCH {base}/registry/packages…`
  - deprecate writes — `PATCH {base}/registry/packages…`
  - artifact download — **only if** the metadata's `download_url` is
    site-relative (see §4). `AbsoluteDownloadURL` prepends `registryBase()` to
    relative URLs, so a relative `download_url` routes the download back to the
    mock; an absolute `http(s)://…` URL is used verbatim and would escape the mock.

### Rules to stay hermetic

1. Set `FGLPKG_REGISTRY` to the mock; do **not** configure any extra registries
   (keep the single built-in `gi` path — only the built-in `gi` registry may be
   `type=genero`; the genero provider always derives its base from
   `registryBase()`, never from a per-registry URL).
2. Make every `download_url` **site-relative** so downloads hit the mock.
3. `FGLPKG_TOKEN` — optional for reads/install; **required non-empty** for
   `publish --ci`; recommended set for `deprecate` so the `Authorization` header
   is present. When set, a `Bearer <token>` header arrives on read and download
   requests too; the mock may ignore it.
4. Signatures: serve unsigned artifacts and rely on default `warn` mode, or set
   `FGLPKG_SIGNING=off` (see §4).

### Base URL / path shape

Everything is rooted at `{base}/registry/…`. `{slug}` in every path is the
**canonical slug** (`slugutil.Canonical`): lowercased, and every run of
`-`/`_`/`.` collapsed to a single `-`, then `url.PathEscape`d. Examples:
`foo_bar` → `foo-bar`, `Fgl.AI.SDK` → `fgl-ai-sdk`. The mock must key its
fixtures on the canonical slug.

### Transport invariants

- **Reads** (`authedGet`, `registry.go:916`): always `GET`; always
  `Accept: application/json`; `Authorization: Bearer <token>` only if token
  non-empty. On HTTP 401 the client calls `TryRefresh()` once (default no-op →
  false) and does not retry — so **never return 401 on the happy path**.
- **Status mapping on reads** (`finalise`, `registry.go:937`): `404` →
  `ErrNotFound` (the "missing / first-publish" signal); any other non-2xx → a
  generic `registry returned HTTP <n>: <body>` error; 2xx → body is
  `json.Unmarshal`ed by the caller.
- **JSON writes** (`publishJSON`, `registry.go:858`): `Accept: application/json`;
  `Content-Type: application/json` only when a body is present;
  `Authorization: Bearer <token>` when token non-empty; single 401 retry via
  `TryRefresh`.
- **Artifact PUT** (`putBytes`, `registry.go:897`): `Content-Type: application/zip`,
  `Accept: application/json`, `Authorization: Bearer <token>`.

---

## 2. Endpoint table

| # | Method | Path pattern | Purpose | Called by / when |
|---|--------|--------------|---------|------------------|
| E1 | GET | `/registry/packages/{slug}` | Package detail: full version list + artifacts | `install`, `info`, `outdated`, `search`→resolve, and publish step 0 precheck. `Resolve` issues this **twice** (version list + info-for-genero). |
| E2 | GET | `/registry/packages?q={term}` | Search | `search` command |
| E3 | GET | `/registry/fglpkg/latest` | Self-update latest release info | `fglpkg` self-update check; **optional** — 404 is a silent no-op |
| E4 | GET | `{download_url}` (typically `/registry/packages/{slug}/versions/{version}/artifacts/{variant}`) | Serve the artifact zip bytes | Installer, after resolving metadata (E1) or from lockfile |
| E5 | GET | `/registry/.well-known/keys.json` | Signing keys manifest | **Only** for signed artifacts under `require`; **not fetched** for unsigned/warn |
| P1 | POST | `/registry/packages` | Create package | `publish` step 1 |
| P2 | POST | `/registry/packages/{slug}/versions` | Create version | `publish` step 2 |
| P3 | PUT | `/registry/packages/{slug}/versions/{version}/artifacts/{variant}?filename={filename}` | Upload artifact zip | `publish` step 3 |
| P4 | POST | `/registry/packages/{slug}/versions/{version}/submit` | Submit for review | `publish` step 4 |
| P5 | PATCH | `/registry/packages/{slug}` | Sync search metadata | `publish` step 5 (conditional, best-effort) |
| D1 | PATCH | `/registry/packages/{slug}/versions/{version}` | Deprecate/undo a specific version | `deprecate pkg@version …` |
| D2 | PATCH | `/registry/packages/{slug}` | Deprecate/undo whole package | `deprecate pkg …` (no `@version`) |

Note: E1 backs several client calls — `FetchVersionList` (1 GET), `FetchInfo`/
`FetchInfoForGenero` (1 GET, then find version in-memory), `Resolve` (2 GETs),
`VariantsFor` (1 GET). There is **no** server-side "latest"/resolve endpoint for
packages: constraints (`latest`, `*`, `^1.2.0`, …) are resolved entirely
client-side from the returned `versions[].version` list.

**Unrecognised paths must 404.** Not merely a sensible default — the suite depends
on it. `lib/mock.sh`'s `mock_secondary_url` points a `type=artifactory` descriptor
at `/mirror` on this same server precisely so its `/mirror/api/storage/…` probes
answer 404 → `ErrNotFound`. That gives the functional tests a secondary repository
that is *reachable and empty* rather than unreachable, which is the only way a
multi-repository fan-out can run to completion in a test (a non-not-found error
aborts it). Returning anything else there — 500, a connection reset, a stub 200 —
silently changes what those tests are exercising.

---

## 3. Exact response bodies

**CRITICAL — wire casing.** Reads unmarshal into internal `api*` structs whose
JSON tags are **snake_case** (`download_url`, `sha256`, `size_bytes`,
`latest_version`, `uploaded_at`, `deprecation_message`, `moved_to`, …). Do **not**
emit the client's own camelCase output/lockfile types (`downloadUrl`,
`checksum`, `publishedAt`). Exceptions noted below: `apiBrowseResponse.pageSize`
is camelCase, and `/registry/fglpkg/latest` is entirely camelCase.

### E1 — `GET /registry/packages/{slug}` → `apiPackageDetail`

Top-level = inlined `apiListedPackage` fields **plus** `versions[]`.

```json
{
  "slug": "foo-bar",
  "name": "foo-bar",
  "description": "Example package",
  "visibility": "public",
  "owner": { "partner_id": "partner-123", "name": "Acme Corp" },
  "status": "published",
  "latest_version": "1.2.0",
  "downloads": 0,
  "tags": {},
  "deprecated": false,
  "deprecation_message": "",
  "moved_to": "",
  "genero": "^6.0.0",
  "versions": [
    {
      "version": "1.2.0",
      "status": "published",
      "changelog": "",
      "tags": {},
      "submitted_at": "2026-01-01T00:00:00Z",
      "published_at": "2026-01-02T00:00:00Z",
      "review_comment": "",
      "repository": "",
      "author": "Acme Corp",
      "license": "MIT",
      "genero": "^6.0.0",
      "dependencies": { "fgl": {}, "java": [] },
      "readme": "",
      "userguide": "",
      "deprecated": false,
      "deprecation_message": "",
      "moved_to": "",
      "artifacts": [
        {
          "variant": "genero6",
          "filename": "foo-bar-1.2.0-genero6.zip",
          "size_bytes": 12345,
          "sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
          "download_url": "/registry/packages/foo-bar/versions/1.2.0/artifacts/genero6",
          "uploaded_at": "2026-01-02T00:00:00Z",
          "uploader": "Acme Corp",
          "signature": null
        }
      ]
    }
  ]
}
```

**Load-bearing fields** (all on `versions[].artifacts[]` except the genero
constraint):

- `download_url` — the artifact URL. Site-relative → resolved against
  `registryBase()` (routes to mock). Must be present.
- `sha256` — lowercase hex SHA-256 of the exact zip bytes served. Empty string
  (or omitted) **skips** checksum verification (see §4).
- `size_bytes`, `uploaded_at`, `uploader` — recorded into the lockfile; not
  verification-critical.
- `signature` — object `{ "keyid": …, "alg": …, "sig": … }` or `null`. Use
  `null` (unsigned) for hermetic offline success.
- `versions[].genero` — **per-version** genero constraint string (e.g. `"^6.0.0"`).
  Top-level `genero` is the package "latest" constraint.
- `variant` — drives artifact selection: `"webcomponent"`, `"genero<major>"`
  (e.g. `"genero6"`), or `"default"`.

**Minimal accepted shape:** only `slug` + `versions[]`, each version with
`version` and `artifacts[]`, each artifact with at least `variant`, `sha256`,
`download_url`. Everything else defaults to zero values. A missing top-level
`slug` is backfilled by the client to the requested canonical slug.

**Client-side behaviors to be aware of:**
- Author fallback: empty `versions[].author` → client uses top-level `owner.name`.
- Deprecation fold: a version reads as deprecated if `versions[].deprecated`
  OR top-level `deprecated` is true; version-level message/`moved_to` win, else
  package-level fill in.
- Artifact selection (`pickArtifact`, given `generoMajor` e.g. `"6"`):
  1. any `variant=="webcomponent"`; 2. exact `variant=="genero"+generoMajor`
  (only if `generoMajor!=""`); 3. `variant=="default"`; 4. first artifact.

### E2 — `GET /registry/packages?q={term}` → `apiBrowseResponse`

```json
{
  "packages": [
    {
      "slug": "foo-bar",
      "name": "foo-bar",
      "description": "Example package",
      "visibility": "public",
      "owner": { "partner_id": "partner-123", "name": "Acme Corp" },
      "status": "published",
      "latest_version": "1.2.0",
      "downloads": 0,
      "tags": {},
      "deprecated": false,
      "deprecation_message": "",
      "moved_to": "",
      "genero": "^6.0.0"
    }
  ],
  "page": 1,
  "pageSize": 20,
  "total": 1
}
```

Each element is the same `apiListedPackage` as E1's inlined fields.
`pageSize` is **camelCase** here (unlike the snake_case fields inside each
package). The client reads only `.packages[]` per element: `slug`→Name,
`latest_version`, `description`, `owner.name`→Author, `genero`, `deprecated`,
`moved_to`. `page`/`pageSize`/`total` are parsed but unused. Client sends only
`q=` (URL-query-escaped); no paging params.

### E3 — `GET /registry/fglpkg/latest` → `LatestRelease` (camelCase)

```json
{
  "version": "1.5.0",
  "notes": "",
  "checksumsUrl": "",
  "checksumsSigUrl": "",
  "keysUrl": "",
  "manualUrl": "",
  "instructions": "",
  "assets": [
    { "os": "darwin", "arch": "arm64", "url": "" }
  ]
}
```

`version` must be non-empty or the client errors. `os`/`arch` use
`runtime.GOOS`/`runtime.GOARCH` spellings. **Optional endpoint** — returning 404
is a silent no-op (no update info). Omit entirely for phase 2a.

### E5 — `GET /registry/.well-known/keys.json`

Not needed for unsigned/warn. Only fetched when verifying real signatures under
`require`. Out of scope for the hermetic mock.

---

## 4. Artifact download

### How the URL is formed and fetched

1. Metadata's per-artifact `download_url` (E1) is stored into
   `PackageInfo.DownloadURL` via `AbsoluteDownloadURL`.
2. `AbsoluteDownloadURL(raw)`:
   - `""` → `""`;
   - starts with `http://`/`https://` → returned verbatim (would escape mock);
   - otherwise → `registryBase() + "/" + strings.TrimPrefix(raw,"/")`.
3. **Serve a site-relative `download_url`** (e.g.
   `/registry/packages/foo-bar/versions/1.2.0/artifacts/genero6`) so it resolves
   back to the mock.
4. The installer does `GET <download_url>` (method GET only). Auth: the GI bearer
   is attached only when the URL is same-origin with `FGLPKG_REGISTRY` and a token
   exists; anonymous otherwise. The mock may ignore any `Authorization` header.
5. **The mock must respond `200` with the raw zip bytes.** `401` → "Not
   authorised"; any non-`200` → error. Only `200` proceeds.
6. Content-Type is not checked; set `application/zip` for cleanliness.

### Checksum (SHA-256)

- Streamed one-pass verify: body flows through a digesting reader; compared
  against `sha256` from E1.
- Expected value normalized `ToLower(TrimSpace(...))` before compare — mock may
  send upper- or lower-case hex.
- **Empty `sha256` ⇒ verification skipped.** For a real hermetic check, serve the
  correct lowercase hex SHA-256 of the exact bytes returned.
- Mismatch is fatal (aborts). Carve-out: on a fresh resolve a failure for an
  **optional-scoped** package is downgraded to a warning; required-scope and all
  lockfile installs are fatal.

### Signatures (offline pass in warn mode)

- Default enforce mode is **`warn`** (`signing/config.go`). Resolution order:
  `FGLPKG_SIGNING` env → `signing.enforce` in config.json → `warn`.
- With `signature: null` (unsigned) under `warn`: the client prints one cosmetic
  `warning: signature check failed: ...` line and **install continues**. The keys
  manifest (E5) is **not fetched** (short-circuits before it).
- To suppress even the warning: `export FGLPKG_SIGNING=off` (or pass
  `--no-verify-signature`), which makes signature verification a total no-op.
- `FGLPKG_SIGNING=require` would make unsigned/invalid fatal — do not use for the
  mock.

**Net offline recipe:** unsigned artifact (`signature: null`) + correct lowercase
`sha256` + `200` zip body ⇒ checksum passes, signature warns-but-succeeds. Add
`FGLPKG_SIGNING=off` to silence the warning.

### Zip contents

Include `fglpkg.json` at the zip root (peeked for `webcomponents` routing list and
`bin` scripts). A missing/partial manifest just yields a pure-BDL install. Declare
**no Java dependencies** — JARs are fetched from Maven Central (`dep.MavenURL()`),
**not** the mock; a truly offline run must have zero Java deps (or pre-seed
`home/jars/`). Genero must also be locally installed (`genero.Detect()` runs
before resolution; not a network call).

---

## 5. Publish + deprecate sequence

### `fglpkg publish --ci` — 6 requests, in order

Precondition: `FGLPKG_TOKEN` set and non-empty, else the CLI aborts before any
network call. Never return 401 on the happy path. Bodies of write responses are
ignored by the client.

| # | Method | Path | Request body (summary) | Success status the mock returns |
|---|--------|------|------------------------|---------------------------------|
| 0 | GET | `/registry/packages/{slug}` | — | **404** (first publish, simplest) — or 200 with a `versions[]` that does NOT contain `{version, variant="genero<major>"}` |
| 1 | POST | `/registry/packages` | `{"slug","name","description","visibility":"public"}` | **201** (409 also accepted) |
| 2 | POST | `/registry/packages/{slug}/versions` | `{"version","changelog", …optional: repository/author/license/genero/dependencies/readme/userguide}` | **201** (409 → `ErrVersionExists`, non-fatal add-variant path) |
| 3 | PUT | `/registry/packages/{slug}/versions/{version}/artifacts/{variant}?filename={filename}` | raw zip bytes, `Content-Type: application/zip` | **any 2xx** (e.g. 200/201) |
| 4 | POST | `/registry/packages/{slug}/versions/{version}/submit` | none (no body) | **any 2xx** (e.g. 200) |
| 5 | PATCH | `/registry/packages/{slug}` | `{"description", "keywords":[…]}` | **200** — conditional & best-effort |

Details:
- Step 0 uses `finalise`: **404** → "nothing to clobber", proceeds. **200** with
  the exact `{version, variant}` already present → CLI aborts ("bump the
  version"). Any other non-2xx → aborts.
- Step 1: 201 **or** 409 both count as success (no differentiation).
- Step 2: 201 success; 409 → `ErrVersionExists`, treated non-fatal, continues to
  upload. `dependencies` uses `manifest.Dependencies` tags: `fgl` (map
  name→constraint) and `java` (array of `{groupId,artifactId,version,checksum?,
  jar?,url?}`). Optional keys included only when non-empty.
- Step 3: `variant` = `"webcomponent"` for WC-only packages, else
  `"genero"+generoMajor`. `filename` = `"{slug}-{version}-{variant}.zip"`
  (`url.QueryEscape`d in the query). Any 2xx succeeds; non-2xx fatal.
- Step 4: no body ⇒ no `Content-Type` sent. Any 2xx succeeds (idempotent).
- Step 5: sent **only if** `description != "" || len(keywords) > 0`. 200 → ok; any
  error is non-fatal (prints a warning, publish still succeeds). Mock may even
  omit handling it.

Success marker printed under `--ci` (stable/greppable):
```
fglpkg-published name=<name> version=<version> variant=<variant> status=pending
```

### `fglpkg deprecate` — 1 request

No `--ci` flag. `FGLPKG_TOKEN` should be set so the `Authorization` header is
present. Route depends on whether a `@version` was supplied:

| Case | Method | Path |
|------|--------|------|
| `deprecate pkg@version …` | PATCH | `/registry/packages/{slug}/versions/{version}` |
| `deprecate pkg …` (no version) | PATCH | `/registry/packages/{slug}` |

Request bodies (from `deprecationBody`):
- with successor (`--moved-to X`): `{"deprecated":true,"deprecationMessage":"<msg>","movedTo":"<X>"}`
- plain deprecate (no successor): `{"deprecated":true,"deprecationMessage":"<msg>"}` (`movedTo` omitted)
- undo (`--undo`): `{"deprecated":false}`

Status handling (`deprecateResultError`): **any 2xx** (200 or 204) → success, no
response body required or read. 401→`ErrUnauthorized`, 403→`ErrForbidden`,
404→`ErrNotFound`, 400→`BadRequestError`. **Return 200 (empty body) on the happy
path; never 401/403/404/400.**

---

## 6. Minimal-viable mock

### Phase 2a — green `install`, `search`, `info`, `outdated`

Implement exactly **three** handlers (E3 optional):

1. **`GET /registry/packages/{slug}`** → E1 JSON (§3). Canonicalize the slug;
   key fixtures on the canonical form. Each artifact: `variant` matching the
   detected Genero major (e.g. `genero6`, or `default`, or `webcomponent`), a
   **site-relative** `download_url`, correct lowercase `sha256` (or `""` to skip),
   `size_bytes`, `uploaded_at`, `uploader`, and **`signature: null`**. Unknown slug
   → **404**.
2. **`GET /registry/packages?q={term}`** → E2 JSON. Needed for `search` (and for
   `search`→resolve flows). Can return `{"packages":[…],"page":1,"pageSize":20,"total":N}`.
3. **`GET {download_url}`** → **200** + raw zip bytes. This is what `install`
   actually fetches; the zip should contain `fglpkg.json` at root and no Java deps.

Environment for 2a:
- `FGLPKG_REGISTRY=http://127.0.0.1:PORT`
- `FGLPKG_SIGNING=off` (optional, silences the signature warning)
- No extra registries; Genero installed locally; test package has no Java deps.

Notes: `info`/`outdated` read the same E1 endpoint (no separate endpoint).
`outdated` compares installed versions against the E1 `versions[]` list.
`E3 /registry/fglpkg/latest` is optional — return 404 or omit it entirely.
`E5 keys.json` is not needed for unsigned/warn.

### Phase 2b — add `publish` and `deprecate`

Add the write handlers, each returning the fixed status above and an empty (or
ignored) body:

- `POST /registry/packages` → **201**
- `POST /registry/packages/{slug}/versions` → **201**
- `PUT /registry/packages/{slug}/versions/{version}/artifacts/{variant}` → **200**
- `POST /registry/packages/{slug}/versions/{version}/submit` → **200**
- `PATCH /registry/packages/{slug}` → **200** (also serves deprecate D2)
- `PATCH /registry/packages/{slug}/versions/{version}` → **200** (deprecate D1)

Plus the publish step-0 precheck: `GET /registry/packages/{slug}` → **404** for a
clean first publish (already implemented in 2a; just ensure unknown slug → 404).
Set `FGLPKG_TOKEN` to any non-empty value. Never return 401/403/404/400 on happy
paths (except the intentional 404 on the step-0 precheck for a brand-new slug).
