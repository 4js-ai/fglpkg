# Phase 2 — mock services (scaffold)

The offline suite (`cases/`) covers commands that need no network. The
registry/network-dependent commands are validated in phase 2 against **local
mocks**, pointed at via fglpkg's own override env vars — no real network, fully
deterministic.

## What to mock and how fglpkg is redirected

| Commands | Override env var | Mock to build here |
|---|---|---|
| `install`, `search`, `info`, `outdated`, `update`, `publish`, `deprecate`, `whoami` | `FGLPKG_REGISTRY=http://127.0.0.1:PORT` (and `FGLPKG_TOKEN` for auth) | `registry_server.py` — implements the `/registry/*` protocol + serves artifact zips |
| `publish` (Artifactory path) | `FGLPKG_ARTIFACTORY_URL`, `FGLPKG_ARTIFACTORY_REPO`, `FGLPKG_ARTIFACTORY_TOKEN` | a static file server (artifact PUT/GET) |
| `audit` | `FGLPKG_AUDIT_URL=http://127.0.0.1:PORT` | `osv_server.py` — canned OSV.dev-style responses |
| `self-update` | (points at GitHub Releases) | out of scope / or a stub release endpoint |

## Registry protocol surface to implement (from `internal/registry`)

Consumer:
- `GET /registry/packages/<slug>` → package metadata (versions, per-version
  variants, checksums, signing fields, deprecation)
- artifact download URL (served from this mock or a sibling file server)

Publisher (for `publish`, non-dry-run):
- `POST /registry/packages` (201 / 409-exists both OK)
- `POST /registry/packages/<slug>/versions` (409 = version exists → add variant)
- `PUT  /registry/packages/<slug>/versions/<v>/artifacts/<variant>`
- `POST /registry/packages/<slug>/versions/<v>/submit` (→ pending admin review)
- `PATCH` metadata (best-effort)

Deprecate:
- `POST`/`PATCH` deprecation endpoint (`--moved-to`, `--undo`)

## Test harness integration

Add a `lib/mock.sh` with `mock_registry_start` / `mock_registry_stop` helpers
(launch the python server on an ephemeral port, export `FGLPKG_REGISTRY`, wait
for readiness, register teardown). Phase-2 cases (`cases/1xx-*.sh`) start the
mock in their `it` body and drive real `install`/`search`/`publish` against it.

Fixtures: canned package metadata + a reproducible artifact zip built by
`pack` (dogfooding) so `install` verifies real checksums.
