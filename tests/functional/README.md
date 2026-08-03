# fglpkg functional test suite

Black-box functional tests that drive the real `fglpkg` binary end to end and
assert on its behavior. Complements the in-repo Go unit tests (`*_test.go`) —
this validates the shipped CLI as a user experiences it.

## Running

```bash
./run.sh                 # all cases
./run.sh pack            # only cases whose filename contains "pack"
FGLPKG_BIN=/path/to/fglpkg ./run.sh
KEEP=1 ./run.sh          # keep the per-run sandbox for debugging
```

Exit code: `0` all passed, `1` failures, `2` setup error. Requires `bash` and
`python3` (JSON assertions). `fglcomp` is used when present to produce real
`.42m` fixtures, but tests fall back to stubs without it.

## How it stays hermetic

Every test runs in its own subshell with a fresh temp workdir and a fresh
sandboxed home, so tests never touch your real `~/.fglpkg` (credentials/config)
and never hit the network:

| Lever | Effect |
|---|---|
| `FGLPKG_HOME=<tmp>` | sandbox the global `~/.fglpkg` (config, credentials, installed packages) |
| `FGLPKG_NO_UPDATE_CHECK=1` | disable the background "new version" check (no GitHub call) |
| `FGLPKG_GENERO_VERSION=6.00.01` | deterministic Genero version, independent of the installed `fglrun` (override with `FGLPKG_FT_GENERO`) |
| env scrub | clears inherited `FGLPKG_TOKEN`/`FGLPKG_REGISTRY`/… so the host env can't leak in |

## Layout

```
run.sh                 # runner: resolves the binary, runs cases, prints a summary
lib/harness.sh         # binary resolution, sandbox, run()/run_in(), mkpkg(), it()/suite()
lib/assert.sh          # assert_success/failure/exit/contains/match/eq/file/json/json_field
cases/NN-*.sh          # one file per command group; each calls suite() then it()
mock/                  # phase-2 mock registry/OSV/Artifactory scaffold (see mock/README.md)
```

## Coverage

**Phase 1 — offline / hermetic (implemented):**

| Area | Cases |
|---|---|
| `version` | prints version; contains a semver |
| `help` | lists commands; unknown command fails; `<cmd> --help` |
| `completion` | bash/zsh/fish/powershell; rejects bad shell |
| `init` | writes valid manifest from prompts; `--template library` |
| `pack` | `--list`; writes zip; **byte-reproducible**; `.fglpkgignore` excludes |
| `env` | `--global`/`--local` include installed dirs; empty home |
| `workspace` | `init` (+ `ws` alias); `list` |
| `registry` | lists built-in `gi`; add/remove Artifactory repo; `--repo-key` required |
| `publish --dry-run` | reports validation errors; valid+token succeeds offline |
| `sbom` | CycloneDX 1.5 from lockfile; `-o` writes valid JSON |
| manifest validation | malformed / unknown-field / bad-version rejected |
| `whoami`/`logout`/`list` | not-logged-in; graceful logout; empty install |

**Phase 2 — mock-backed (implemented, 56 tests total):** local mocks
(`mock/registry_server.py` + `mock/osv_server.py`, protocol in
`mock/MOCK-PROTOCOL.md`) are started per test via `mock_registry_start` /
`mock_osv_start` (see `lib/mock.sh`), which build real fixture artifacts with
`fglpkg pack` and point fglpkg at `http://127.0.0.1:<port>` via `FGLPKG_REGISTRY`
/ `FGLPKG_AUDIT_URL`. Covered:

| Area | Cases |
|---|---|
| `info` | latest metadata; `@version`; unknown package fails (404) |
| `search` | match; no-match returns no results |
| `install` | pinned download+verify+extract+lock; latest; **checksum-mismatch fails** |
| `remove` | uninstalls and prunes the tree |
| `outdated` | reports newer version, exits non-zero; exits zero when current |
| `publish --ci` | full POST/PUT/submit sequence; fails fast without a token |
| `deprecate` | package; `@version`; `--moved-to`; `--undo` |
| `audit` | clean; findings (exit 1); `--severity` floor; `--production`; `--json`; no-lockfile (exit 2) |

Still deferred: `audit signatures` (needs signed fixtures + a keys manifest),
Artifactory-backed publish (`FGLPKG_ARTIFACTORY_URL`), `bdl`/`run`, and the
interactive `login` browser flow.

## Continuous integration

`.github/workflows/functional-tests.yml` runs this suite on every push / PR:
it builds fglpkg from source (`go build -o $RUNNER_TEMP/fglpkg ./cmd/fglpkg`) and
runs `bash tests/functional/run.sh` with `FGLPKG_BIN` pointed at that build — so
CI exercises the current source, not the checked-in `bin/` binaries. **No Genero
runtime is needed** on the runner (the suite sets `FGLPKG_GENERO_VERSION` and
falls back to stub `.42m` fixtures when `fglcomp` is absent); it needs only Go,
`bash`, and `python3`, all present on `ubuntu-latest`.

## Adding a test

```bash
# cases/NN-thing.sh
suite "thing"
_does_x() { mkpkg; run thing --flag; assert_success; assert_contains "expected"; }
it "thing does x" _does_x
```

`run <args>` sets `$status` and combined `$output` (stdin is closed so a stray
prompt can't hang; use `run_in "<stdin>" <args>` for interactive commands like
`init`). The test body runs under `set -e`, so the first failing assertion fails
the test — enforced, and guarded by `cases/000-harness-contract.sh`. (That
guard exists because the invocation shape in `it()` silently suspended `set -e`
for a long while, letting a mid-body failure pass as long as the body's last
command succeeded.) `mkpkg [name] [ver]` drops a minimal valid, packable package
in cwd.

When asserting on a path, use `assert_contains_path` /
`assert_not_contains_path` and write the needle with forward slashes: they
normalise both sides, so one assertion covers POSIX and Windows. This matters
doubly for the negative form — a `/` needle can never occur in a `\` path, so a
plain `assert_not_contains` on a path holds vacuously on Windows and asserts
nothing.
