# GIS-368 — Local (repo) vs global (user) config precedence and merge behavior

**Status:** Draft for review
**Component:** fglpkg
**Ticket:** [GIS-368](https://4js.atlassian.net/browse/GIS-368) — *fglpkg: define local (repo) vs global (user) config precedence and merge behavior*
**Raised by:** Tyler Technologies (Daeghan Elkin), demo 2026-07-20
**Related:** GIS-363/364/365 (secondary repositories, default consume registry, Maven mirror), [artifactory-secondary-repository.md](artifactory-secondary-repository.md)

---

## 1. Problem

fglpkg reads configuration from three places, in increasing precedence:

1. **Built-in** defaults (the GI registry, Maven Central, `signing.enforce=warn`, update-check on).
2. **Global** user config — `~/.fglpkg/config.json` (+ `credentials.json`).
3. **Local** project config — fields checked into the project's `fglpkg.json`.
   Environment variables sit above all three.

The *behavior* already exists for routing config, but it was never **defined** as a single model: each key implements its own precedence in its own function, some keys have no project layer at all, and nothing is documented for a user. On the 2026-07-20 call the open question was put plainly: *"is it always local first … or does it merge the two?"* A predictable, documented resolution is a prerequisite for teams checking a repo-level registry config into source control — they must be able to reason about which registry/setting wins.

This spec **defines** that model, **consolidates** the implementation into one path, **guards** the security boundary, and **documents** it.

## 2. Decision

**Precedence (decreasing, first match wins):**

```
environment  >  local (project fglpkg.json)  >  global (~/.fglpkg/config.json)  >  built-in default
```

**Merge semantics (npm/NuGet-style):**

- **Collections** (`registries[]`) **merge by key** (registry `name`). A registry declared in a higher layer **replaces the same-named entry wholesale** (not field-merged) — a project redefining a registry must fully specify it. Entries new to a layer are added. The effective set is priority-sorted; the ≥2-repo collision guard runs on the merged set.
- **Scalars** (`defaultRegistry`, `defaultConsumeRegistry`, `mavenMirror`) **replace** — the first non-empty value in precedence order wins.

**Scope — what a *checked-in* project config may set (the GIS-368 decision):**

| Class | Keys | Project-settable? |
|---|---|---|
| **Routing** | `registries`, `defaultRegistry`, `defaultConsumeRegistry`, `mavenMirror` | **Yes** — formalized here |
| **Policy** | `signing.enforce`, `updateCheck`, `updateCheckInterval` | **No** — global/env only |
| **Secrets** | credentials (tokens) | **No** — global only, never in a repo |

**Rationale for the split (supply-chain safety).** A checked-in config *can* steer where dependencies come from (registries, Maven mirror) — this is useful and already the behavior, and it only takes effect for someone who chooses to run `fglpkg` inside that repo (the same trust boundary as running the repo's build). But signature enforcement must **not** be repo-controlled: if a hostile `fglpkg.json` could both redirect installs to an attacker's mirror **and** set `signing.enforce=off`, it could swap a dependency undetected. Keeping `signing.enforce` global/env-only means the user's own enforcement always guards every install, regardless of the repo they are in. Update-check preferences are a personal/CI concern, not a repo's to dictate. Credentials never belong in a checked-in file.

Project config continues to live **inside the checked-in `fglpkg.json`** — no new project config file is introduced.

## 3. Effective per-key resolution (authoritative)

| Key | Env override | Local (`fglpkg.json`) | Global (`config.json`) | Built-in | Rule |
|---|---|---|---|---|---|
| `registries[]` | `FGLPKG_REGISTRY` retargets built-in GI URL | `registries` | `registries` | GI | **merge by name**, local wins per-name, priority-sorted |
| `defaultRegistry` (publish) | `FGLPKG_PUBLISH_REGISTRY` | `defaultRegistry` | `defaultRegistry` | — | **replace** |
| `defaultConsumeRegistry` | `FGLPKG_CONSUME_REGISTRY` | `defaultConsumeRegistry` | `defaultConsumeRegistry` | "" (all) | **replace** |
| `mavenMirror` | `FGLPKG_MAVEN_URL` | `mavenMirror` | `mavenMirror` | Maven Central | **replace** |
| `signing.enforce` | `FGLPKG_SIGNING` | *(not a field)* | `signing.enforce` | `warn` | global/env only |
| `updateCheck` / `updateCheckInterval` | `FGLPKG_NO_UPDATE_CHECK` (disable) | *(not a field)* | `updateCheck`, `updateCheckInterval` | on / 24h | global/env only |
| credentials | `FGLPKG_TOKEN` | *(not a field)* | `credentials.json` | — | global/env only |

This table matches today's behavior for the routing keys; GIS-368's change is to **name it, centralize it, guard it, and document it** — plus close the consistency gaps in §6.

## 4. Current implementation (as-is)

- **Registries** — already consolidated: `config.Load(home, FGLPKG_REGISTRY, projectRegistries)` → `config.Resolve(builtin, global, project)` merges by name (later layer overwrites), then normalises, validates, priority-sorts, and runs the collision guard. ✅ This is the model; it becomes the reference pattern.
- **Scalars** — three independent resolvers in `internal/cli/cli.go`, each re-implementing `env → project → global`:
  - publish default (`FGLPKG_PUBLISH_REGISTRY` → `m.DefaultRegistry` → `config.GlobalDefaultRegistry`)
  - consume default (`FGLPKG_CONSUME_REGISTRY` → `m.DefaultConsumeRegistry` → `config.GlobalConsumeRegistry`)
  - `resolveMavenMirror` (`m.MavenMirror` → `config.GlobalMavenMirror`, with `FGLPKG_MAVEN_URL` applied last)
- **Policy** — `signing.EnforceMode(globalHome)` and `config.LoadUpdateSettings(home)` read the global home only; no project layer. Credentials load from the global home only.

The three scalar resolvers are individually correct but **duplicate the precedence rule**. That is the same drift risk that produced GIS-367's missed call sites: a fourth routing key added later could easily get the order wrong or skip a layer.

## 5. Implementation plan

Scope is deliberately contained — no routing behavior changes; the value is definition, consistency, guardrails, and closing the CLI/lint/doc gaps.

1. **Centralize scalar precedence.** Add one small, documented, unit-tested helper (proposed `config` package) that expresses the rule once, e.g.:
   ```go
   // ResolveScalar returns the first non-empty of env, local, global (that
   // precedence), or "" — the single source of truth for a replace-semantics
   // config value. Callers pass the already-read values so config stays free of
   // the manifest import.
   func ResolveScalar(env, local, global string) string
   ```
   Rework the three CLI resolvers to call it (mavenMirror adapts via its `URL` string). Keep the existing public resolver functions as thin wrappers so call sites don't move (avoids a GIS-367-style migration).
2. **Confirm/consolidate the registry path** as the collection reference — no logic change; add doc comments cross-referencing this spec.
3. **Guardrails for policy keys.** The manifest struct has no `signing` / `updateCheck` fields and the manifest decoder uses `DisallowUnknownFields`, so a project `fglpkg.json` that declares one is already **rejected at parse time**. Add a test pinning that (a repo cannot smuggle in `signing.enforce`), a friendlier hint when the rejected unknown key is a known policy key (`signing`/`updateCheck` → "this is a user/global setting, not a project one — see this spec"), and a comment on the manifest type recording the deliberate omission.
4. **No change** to credentials or update-check loading.

### 5.1 `registry add` / `registry remove` scope flag — `--local` (GIS-368 follow-up #3)

Managing the *checked-in* repo config from the CLI is the whole point of the driver, and both subcommands already write it via a `--project` flag ([cli.go:3479](../internal/cli/cli.go#L3479)), with **global as the default**. The gap is **naming consistency**: every other scoped command uses `--local`/`-l` (project) and `--global`/`-g` (user), but registry uses `--project`.

- Make **`--local` / `-l`** the primary spelling for "write the project `fglpkg.json`", matching `install`/`env`/`list`/`relink`.
- Accept **`--global` / `-g`** explicitly (it is the default, so it is a no-op selector — but symmetric and self-documenting).
- Keep **`--project`** as a hidden, still-working alias for `--local` (cheap; avoids breaking the handoff specs/docs that reference it). Not advertised in usage.
- **Default stays global.** Unlike `install`'s context auto-detect, `registry add` defaults to the user config regardless of cwd — adding a registry is usually a machine-wide action, and silently editing a checked-in manifest would surprise. `--local` is the deliberate opt-in for the repo config. Document this difference.
- `registry list` already labels each entry's source (`builtin` / `global` / `project`), so the result of a `--local` add is visible without extra work — keep that.
- Update the usage strings, `commands.go` help, README, and user-guide accordingly.

### 5.2 `lint` — validate the project's routing config (GIS-368 follow-up #1)

`manifest.LintInto` validates none of the config/routing fields today, and `config.Resolve` never checks that a default names a real registry. Since a checked-in `fglpkg.json` must be valid on any machine, lint validates the **project layer in isolation** (it must not depend on the user's global config):

- **Project `registries[]` well-formedness** — mirror `config.Resolve`'s checks as lint diagnostics: valid `type`/`auth`, `type=artifactory` requires `repoKey`, `priority ≥ 1`, and priorities unique *within the project layer*. (Errors, matching the load-time rejection, but surfaced early and field-named.)
- **Dangling default** — `defaultRegistry` / `defaultConsumeRegistry` must name a registry declared in the **project's own `registries[]`** or the built-in `gi`. If it names something only a machine's global config could supply, **warn**: the manifest resolves on the author's machine but would break on a fresh clone. (Warning, not error — it can be legitimate for a solo/global setup.)
- **`mavenMirror`** — validate the URL is well-formed and, for an authenticated scheme, that `auth` is a recognised value.
- These run inside `lintProject`, so `pack`/`publish` enforce them too (they already call the same pass).

### 5.3 `docs/fglpkg-json-reference.md` (GIS-368 follow-up #2)

Give the routing fields (`registries`, `defaultRegistry`, `defaultConsumeRegistry`, `mavenMirror`) first-class reference entries that state, per field: the type/shape, that it participates in the local→global cascade (with a pointer to the precedence rule), and the merge-vs-replace behaviour. Add an explicit note that **policy and credentials are intentionally not manifest fields** (`signing.enforce`, update-check, tokens are user/global-only) with the reason, so a reader who looks for them in the manifest finds the boundary documented rather than absent.

## 6. Consistency gaps closed

- The precedence rule is stated once in code (the helper) and once for users (docs), instead of four times implicitly.
- The security boundary is **tested**, not just implied by the absence of a field.
- New routing keys added later have an obvious, correct pattern to follow.

Explicitly **not** closed (deferred, see §9): giving the project layer the ability to set policy keys — rejected by the GIS-368 decision.

## 7. Edge cases

- **Same-named registry in two layers** → higher layer replaces the whole entry (document: redefining a registry locally must repeat its `url`/`auth`/`repoKey`).
- **Empty vs absent** → `omitempty`/blank is "unset", so it never overrides a lower layer with emptiness (matches today; e.g. `defaultConsumeRegistry: ""` in global is treated as unset).
- **Env `FGLPKG_REGISTRY`** retargets the built-in GI URL rather than adding a registry, unchanged.
- **Collision guard** (≥2 repos ⇒ hard error) runs on the *merged* set, so a project registry can trigger it — correct and documented.
- **`--registry <name>` flag** is a per-invocation selector, above env, and orthogonal to this precedence (it picks among the resolved set).

## 8. Testing — complete unit + functional suite

A full matrix, not a smoke test. All new code lands with tests; the implementation closes with an adversarial review pass (per the GIS-367 cadence).

**Unit (`internal/config`, `internal/manifest`)**
- `ResolveScalar`: env > local > global > "" for every combination, including empty-string-is-unset (a blank higher layer does not shadow a set lower one).
- Registry `Resolve` (extend existing): same-name entry in a higher layer **replaces wholesale**; new names **merge**; first-seen order preserved; priority sort; duplicate-priority error; unknown type/auth error; artifactory-without-repoKey error; collision guard fires on the *merged* set.
- Manifest lint (`LintInto`/`lintProject`): well-formedness errors for a malformed project registry; **dangling-default warning** when `defaultRegistry`/`defaultConsumeRegistry` names neither a project registry nor `gi`; no warning when it does; `mavenMirror` URL/auth validation.
- **Policy guardrail:** a manifest JSON with a `signing` or `updateCheck` key fails to decode (`DisallowUnknownFields`), and the friendly hint names the key as a global-only setting.

**Functional (`tests/functional/cases`)**
- `registry add --local` writes the project `fglpkg.json` (not `~/.fglpkg/config.json`); `--project` still works as an alias; default (no flag) and `--global` write the user config; `-l`/`-g` short forms.
- `registry remove --local` removes from the project manifest only.
- Precedence end-to-end: a project registry overrides a same-named global one at install/resolve time; project `defaultConsumeRegistry`/`mavenMirror` beats global; `FGLPKG_*` env beats project.
- `registry list` labels sources `builtin`/`global`/`project` after a `--local` add.
- `lint` errors on a malformed project registry and warns on a dangling default; `lint` inside a repo whose `fglpkg.json` declares `signing` fails with the friendly message.
- Guardrail: with global `signing.enforce=require`, an install inside a repo (whose manifest cannot lower it) still enforces signatures.

**Gates:** full Go suite + full functional suite green; `gofmt`/`vet` clean on all touched files; adversarial review workflow over the new resolution/lint code before marking done.

## 9. Documentation

- **README** — a "Configuration precedence" subsection with the §2 rule and the §3 table; update the `registry add` cheat-sheet line to show `--local`.
- **docs/user-guide.md** — a "Local vs global configuration" section: precedence, merge-vs-replace, the routing/policy split with the security rationale, a worked example (project registry overriding global), and the `registry add --local` workflow for checking a registry into a repo (noting global is the default).
- **docs/fglpkg-json-reference.md** — per §5.3: first-class entries for `registries`/`defaultRegistry`/`defaultConsumeRegistry`/`mavenMirror` describing shape + cascade participation + merge/replace, and the explicit note that policy/credentials are intentionally not manifest fields.
- **`fglpkg registry` / `lint` help** (`commands.go`) — reflect `--local`/`--global` and the new lint checks.

## 10. Out of scope / future

- A separate project config file distinct from `fglpkg.json` — not needed; config stays in the manifest.
- Field-level merge of same-named registries — replace is simpler and current.
- Project-settable policy (`signing.enforce` etc.), including a monotonic "raise-only" variant — rejected by the GIS-368 decision; revisit only if a concrete need appears.
