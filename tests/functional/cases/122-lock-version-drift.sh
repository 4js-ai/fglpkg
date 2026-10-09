suite "a lock replay installs the version the lock names (GIS-586)"

# `LockFile.Validate` decided a locked package was installed by stat'ing
# .fglpkg/packages/<name> and nothing else — the version the directory actually
# held was never compared with the version the lock named. So the most ordinary
# team workflow silently did nothing: pull a commit that bumps a dependency, run
# `fglpkg install`, get "Nothing to install" and exit 0, and keep running the old
# version. The manifest check does not catch it, because the teammate regenerated
# the lock and `diffDeclared` is therefore clean.
#
# These cases drive the real workflow: a "teammate" project resolves the new
# version, and its fglpkg.json + fglpkg-lock.json are copied over a project that
# still has the old version extracted (the `git pull` — .fglpkg/ is untouched).
#
# Note the explicit `|| return 1` on each assertion: `it()` runs the body in a
# subshell inside an `if`, so bash suppresses errexit and a bare failing
# assertion mid-body would be ignored (see the note in case 113).

# Resolve <version> in a throwaway project and leave its fglpkg.json and
# fglpkg-lock.json in $_DRIFT_PULLED for a later `cp` into the project under
# test. Returns with the cwd restored.
_drift_publish_pull() {  # _drift_publish_pull <version>
  local here; here="$(pwd)"
  _DRIFT_PULLED="$here/pulled-$1"
  mkdir -p "$_DRIFT_PULLED" && cd "$_DRIFT_PULLED" || return 1
  run install "demo.pkg@$1"
  cd "$here" || return 1
  assert_success || return 1
}

# The headline regression: the bump has to land on disk.
_drift_pulled_bump_installs() {
  mock_registry_start
  _drift_publish_pull 1.1.0 || return 1

  mkdir -p mine && cd mine || return 1
  run install demo.pkg@1.0.0
  assert_success || return 1
  assert_file_contains ".fglpkg/packages/demo-pkg/fglpkg.json" '"version": "1.0.0"' || return 1

  # `git pull`: both files move to 1.1.0, .fglpkg/ is untouched.
  cp "$_DRIFT_PULLED/fglpkg.json" "$_DRIFT_PULLED/fglpkg-lock.json" . || return 1

  run install
  assert_success || return 1
  # This is the bug: it used to print "Nothing to install", exit 0, and leave
  # 1.0.0 extracted while the lock claimed 1.1.0.
  assert_not_contains "Nothing to install" || return 1
  assert_file_contains ".fglpkg/packages/demo-pkg/fglpkg.json" '"version": "1.1.0"' || return 1
}
it "a pulled dependency bump replaces the installed version" \
  _drift_pulled_bump_installs

# A downgrade is the same drift reached from the other side, and the direction
# matters: a version-aware check must compare for EQUALITY with what the lock
# names, not ask whether the disk is new enough.
_drift_pulled_downgrade_installs() {
  mock_registry_start
  _drift_publish_pull 1.0.0 || return 1

  mkdir -p mine && cd mine || return 1
  run install demo.pkg@1.1.0
  assert_success || return 1

  cp "$_DRIFT_PULLED/fglpkg.json" "$_DRIFT_PULLED/fglpkg-lock.json" . || return 1

  run install
  assert_success || return 1
  assert_file_contains ".fglpkg/packages/demo-pkg/fglpkg.json" '"version": "1.0.0"' || return 1
}
it "a pulled dependency downgrade replaces the installed version" \
  _drift_pulled_downgrade_installs

# The other half of the contract: a store that already holds exactly what the
# lock names must still take the no-op fast path. A version check that reported
# every package stale would re-download the world on every install.
_drift_matching_version_is_a_no_op() {
  mock_registry_start
  run install demo.pkg@1.0.0
  assert_success || return 1
  run install
  assert_success || return 1
  assert_contains "Nothing to install" || return 1
}
it "a replay of the version already on disk installs nothing" \
  _drift_matching_version_is_a_no_op

# A store with neither an install stamp nor a manifest: a pre-stamp fglpkg
# whose extraction died before it wrote fglpkg.json. Nothing on disk can name a
# version, so the entry must be treated as missing and reinstalled — never as
# present. (A store with a stamp is covered by the incomplete-stamp case below;
# a stamped install does not consult the manifest at all.)
_drift_unreadable_manifest_reinstalls() {
  mock_registry_start
  run install demo.pkg@1.0.0
  assert_success || return 1
  rm -f .fglpkg/packages/demo-pkg/fglpkg.json || return 1
  rm -f .fglpkg/packages/demo-pkg/.fglpkg-installed || return 1

  run install
  assert_success || return 1
  assert_not_contains "Nothing to install" || return 1
  assert_file_contains ".fglpkg/packages/demo-pkg/fglpkg.json" '"version": "1.0.0"' || return 1
}
it "a package with neither a stamp nor a manifest is reinstalled" \
  _drift_unreadable_manifest_reinstalls

# `--production` narrows the install set but must not narrow the staleness
# check: a deploy replaying a lock onto a warm .fglpkg/ (a cached CI workspace,
# a reused build agent) is exactly where shipping the stale version hurts most.
_drift_production_replay_installs_the_bump() {
  mock_registry_start
  _drift_publish_pull 1.1.0 || return 1

  mkdir -p mine && cd mine || return 1
  run install demo.pkg@1.0.0
  assert_success || return 1

  cp "$_DRIFT_PULLED/fglpkg.json" "$_DRIFT_PULLED/fglpkg-lock.json" . || return 1

  run install --production
  assert_success || return 1
  assert_file_contains ".fglpkg/packages/demo-pkg/fglpkg.json" '"version": "1.1.0"' || return 1
}
it "a --production replay installs the bump onto a warm store" \
  _drift_production_replay_installs_the_bump

# ── Artifacts the publisher's manifest cannot describe ───────────────────────
#
# Review round 1 on PR #97. "Is the locked version installed?" was first
# answered from the package's own extracted fglpkg.json — the PUBLISHER's file,
# which says what they built and is not evidence that anything was installed.
# The installer accepts two artifacts that never satisfy such a check:
#
#   * a zip with no root fglpkg.json (readWebcomponentsFromZip returns no error
#     for one, so the install succeeds);
#   * a zip whose manifest names a version other than the one the registry
#     published it as — Artifactory takes the version from the folder path,
#     so a re-upload without bumping the zip leaves the two disagreeing.
#
# Each would have read as stale on EVERY replay and been re-downloaded forever,
# and a warm store holding one could no longer be replayed offline — which is
# worse than the bug this PR fixes, because `main` handles those stores today.
# The installer now writes its own stamp, and these cases pin that.

_drift_odd_fixtures() {  # _drift_odd_fixtures <dir>
  local dir="$1"; mkdir -p "$dir"
  mock_build_fixtures "$dir" || return 1

  # A package whose zip carries a manifest saying 1.0.0, published as 1.0.1.
  local build; build="$(mktemp -d "$_SANDBOX_ROOT/odd.XXXXXX")"
  (
    cd "$build"
    printf 'stub' > mod.42m
    cat > fglpkg.json <<'EOF'
{ "name":"skew","version":"1.0.0","description":"Manifest/registry version skew",
  "genero":">=3.20","license":"MIT","author":"fglpkg tests","files":["*.42m"] }
EOF
    "$FGLPKG" pack -o "$dir/skew-1.0.1-genero6.zip" </dev/null >/dev/null 2>&1 || exit 1
  ) || { _diag "_drift_odd_fixtures: pack failed"; return 1; }

  # A package with no root manifest at all. `fglpkg pack` always stages one, so
  # this zip is built directly.
  python3 - "$dir/nomani-1.0.0-genero6.zip" <<'PY' || return 1
import sys, zipfile
with zipfile.ZipFile(sys.argv[1], "w") as z:
    z.writestr("mod.42m", "stub")
PY

  python3 - "$dir/packages.json" <<'PY' || return 1
import json, sys

path = sys.argv[1]
with open(path) as fh:
    doc = json.load(fh)
for slug, name, version, zipname in (
    ("skew", "skew", "1.0.1", "skew-1.0.1-genero6.zip"),
    ("nomani", "nomani", "1.0.0", "nomani-1.0.0-genero6.zip"),
):
    doc["packages"].append({
        "slug": slug,
        "name": name,
        "description": "Odd-artifact fixture",
        "genero": ">=3.20",
        "owner": {"partner_id": "mock", "name": "fglpkg tests"},
        "versions": [{
            "version": version, "genero": ">=3.20",
            "author": "fglpkg tests", "license": "MIT",
            "artifacts": [{"variant": "genero6", "zip": zipname}],
        }],
    })
with open(path, "w") as fh:
    json.dump(doc, fh, indent=2)
PY
}

# Install one of the odd packages, then replay twice. Both replays must be
# no-ops: the first proves the stamp is consulted, the second that replaying
# does not undo it.
_drift_odd_replays_are_no_ops() {  # _drift_odd_replays_are_no_ops <pkg> <version>
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/oddfx.XXXXXX")"
  _drift_odd_fixtures "$fx" || return 1
  mock_registry_start "$fx" || return 1

  run install "$1@$2"
  assert_success || return 1

  run install
  assert_success || return 1
  assert_contains "Nothing to install" || return 1

  run install
  assert_success || return 1
  assert_contains "Nothing to install" || return 1
}

_drift_nomani_is_not_perpetually_stale() {
  _drift_odd_replays_are_no_ops nomani 1.0.0 || return 1
  assert_no_file ".fglpkg/packages/nomani/fglpkg.json" || return 1
}
it "a package whose zip has no manifest replays as a no-op" \
  _drift_nomani_is_not_perpetually_stale

_drift_skew_is_not_perpetually_stale() {
  _drift_odd_replays_are_no_ops skew 1.0.1 || return 1
  # The zip really does disagree with the registry — without that, the case
  # would pass for the wrong reason.
  assert_file_contains ".fglpkg/packages/skew/fglpkg.json" '"version": "1.0.0"' || return 1
}
it "a package whose zip names another version replays as a no-op" \
  _drift_skew_is_not_perpetually_stale

# The consequence that matters operationally: a warm store must replay with the
# registry gone. Killing the mock makes every download URL in the lock dead, so
# any re-fetch at all fails the run.
_drift_warm_store_replays_offline() {
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/oddfx.XXXXXX")"
  _drift_odd_fixtures "$fx" || return 1
  mock_registry_start "$fx" || return 1

  run install nomani@1.0.0
  assert_success || return 1
  run install skew@1.0.1
  assert_success || return 1
  run install demo.pkg@1.0.0
  assert_success || return 1

  # `it()` runs the body under `set -e`, and `wait` on a killed child reports
  # its signal, so both of these need their status discarded explicitly.
  kill "$MOCK_PID" 2>/dev/null || true
  wait "$MOCK_PID" 2>/dev/null || true

  run install
  assert_success || return 1
  assert_contains "Nothing to install" || return 1
}
it "a warm store replays with the registry unreachable" \
  _drift_warm_store_replays_offline

# An extraction killed part-way can leave a directory holding a manifest that
# names exactly the version the lock wants, because `fglpkg pack` writes entries
# alphabetically and fglpkg.json lands mid-zip. The install stamp is written
# before extraction and completed after, so the incomplete state is visible;
# here it is reproduced by hand, since a real interruption cannot be timed.
_drift_incomplete_install_is_reinstalled() {
  mock_registry_start
  run install demo.pkg@1.0.0
  assert_success || return 1

  printf '{\n  "version": "1.0.0",\n  "complete": false\n}\n' \
    > .fglpkg/packages/demo-pkg/.fglpkg-installed || return 1
  assert_file_contains ".fglpkg/packages/demo-pkg/fglpkg.json" '"version": "1.0.0"' || return 1

  run install
  assert_success || return 1
  assert_not_contains "Nothing to install" || return 1
  assert_file_contains ".fglpkg/packages/demo-pkg/.fglpkg-installed" '"complete": true' || return 1
}
it "an install that never completed is reinstalled, manifest notwithstanding" \
  _drift_incomplete_install_is_reinstalled

# A zip that already ships `.fglpkg-installed` — built before `pack` learned to
# exclude it, or by other tooling. Extraction must refuse the entry, so the
# marker the installer wrote just beforehand survives and still governs.
_drift_zip_stamp_fixtures() {  # _drift_zip_stamp_fixtures <dir>
  local dir="$1"; mkdir -p "$dir"
  mock_build_fixtures "$dir" || return 1

  # "." sorts first, so the forged stamp is the archive's first entry — exactly
  # where `fglpkg pack` over an installed directory would have put it.
  python3 - "$dir/stampy-1.0.0-genero6.zip" <<'PY' || return 1
import sys, zipfile
with zipfile.ZipFile(sys.argv[1], "w") as z:
    z.writestr(".fglpkg-installed", '{"version":"9.9.9","complete":true}\n')
    z.writestr("fglpkg.json", '{"name":"stampy","version":"1.0.0","genero":">=3.20","license":"MIT"}')
    z.writestr("mod.42m", "stub")
PY

  python3 - "$dir/packages.json" <<'PY' || return 1
import json, sys
path = sys.argv[1]
with open(path) as fh:
    doc = json.load(fh)
doc["packages"].append({
    "slug": "stampy", "name": "stampy",
    "description": "Ships fglpkg's own install stamp",
    "genero": ">=3.20",
    "owner": {"partner_id": "mock", "name": "fglpkg tests"},
    "versions": [{
        "version": "1.0.0", "genero": ">=3.20",
        "author": "fglpkg tests", "license": "MIT",
        "artifacts": [{"variant": "genero6", "zip": "stampy-1.0.0-genero6.zip"}],
    }],
})
with open(path, "w") as fh:
    json.dump(doc, fh, indent=2)
PY
}

_drift_zip_cannot_supply_the_stamp() {
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/stampfx.XXXXXX")"
  _drift_zip_stamp_fixtures "$fx" || return 1
  mock_registry_start "$fx" || return 1

  run install stampy@1.0.0
  assert_success || return 1
  assert_contains "install record" || return 1

  # The installer's stamp, not the zip's: the published version, and complete
  # because this install really did finish.
  assert_file_contains ".fglpkg/packages/stampy/.fglpkg-installed" '"version": "1.0.0"' || return 1
  assert_not_contains "9.9.9" "$(cat .fglpkg/packages/stampy/.fglpkg-installed)" || return 1

  run install
  assert_success || return 1
  assert_contains "Nothing to install" || return 1
}
it "a zip shipping an install stamp cannot supply it" \
  _drift_zip_cannot_supply_the_stamp
