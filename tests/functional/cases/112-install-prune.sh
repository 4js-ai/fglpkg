suite "install/update prune a hand-edited manifest (mock registry)"

# Deleting a dependency from fglpkg.json by hand used to leave its files in
# .fglpkg/packages/ forever: the lock recorded only the project's name and
# version, so `install` saw no change, reported "Nothing to install", and the
# orphaned package kept resolving on FGLLDPATH and kept showing up in
# `fglpkg list`. install and update now converge the store on the manifest.

# _drop_all_deps empties dependencies.fgl in the project manifest, simulating a
# user deleting the entry in an editor (no fglpkg command involved).
_drop_all_deps() {
  python3 - <<'PY'
import json
with open("fglpkg.json") as f:
    m = json.load(f)
m.setdefault("dependencies", {})["fgl"] = {}
with open("fglpkg.json", "w") as f:
    json.dump(m, f, indent=2)
PY
}

_install_prunes_hand_removed_dep() {
  mock_registry_start
  run install demo.pkg@1.0.0
  assert_success
  assert_dir ".fglpkg/packages/demo-pkg"

  _drop_all_deps
  run install
  assert_success
  assert_contains "stale"                          # no longer "Nothing to install"
  assert_contains "pruned package demo-pkg"
  assert_no_file ".fglpkg/packages/demo-pkg"

  # The user-visible symptom: list must not report a package the manifest
  # no longer declares.
  run list
  assert_success
  assert_not_contains "demo-pkg"
}
it "install prunes a dependency deleted from fglpkg.json by hand" _install_prunes_hand_removed_dep

_update_prunes_hand_removed_dep() {
  mock_registry_start
  run install demo.pkg@1.0.0
  assert_success
  assert_dir ".fglpkg/packages/demo-pkg"

  _drop_all_deps
  run update
  assert_success
  assert_contains "pruned package demo-pkg"
  assert_no_file ".fglpkg/packages/demo-pkg"

  run list
  assert_success
  assert_not_contains "demo-pkg"
}
it "update prunes a dependency deleted from fglpkg.json by hand" _update_prunes_hand_removed_dep

_no_prune_keeps_orphan() {
  mock_registry_start
  run install demo.pkg@1.0.0
  assert_success

  _drop_all_deps
  run install --no-prune
  assert_success
  assert_dir ".fglpkg/packages/demo-pkg"           # escape hatch honoured
  assert_not_contains "pruned"
}
it "install --no-prune leaves the orphan on disk" _no_prune_keeps_orphan

# A dependency ADDED to fglpkg.json by hand must also re-resolve rather than be
# silently ignored — the same missing manifest/lock comparison caused both.
_install_picks_up_hand_added_dep() {
  mock_registry_start
  mkpkg consumer.app 0.1.0
  run install                                      # no deps yet
  assert_success

  python3 - <<'PY'
import json
with open("fglpkg.json") as f:
    m = json.load(f)
m.setdefault("dependencies", {})["fgl"] = {"demo.pkg": "1.0.0"}
with open("fglpkg.json", "w") as f:
    json.dump(m, f, indent=2)
PY
  run install
  assert_success
  assert_dir ".fglpkg/packages/demo-pkg"
}
it "install installs a dependency added to fglpkg.json by hand" _install_picks_up_hand_added_dep

# --frozen is the CI counterpart: it must refuse a lock that disagrees with the
# manifest instead of quietly re-resolving to different versions.
_frozen_rejects_stale_lock() {
  mock_registry_start
  run install demo.pkg@1.0.0
  assert_success

  _drop_all_deps
  run install --frozen
  assert_failure
  assert_contains "out of date"
  assert_dir ".fglpkg/packages/demo-pkg"           # refused before touching disk
}
it "install --frozen fails when the lock disagrees with the manifest" _frozen_rejects_stale_lock

_frozen_succeeds_on_matching_lock() {
  mock_registry_start
  run install demo.pkg@1.0.0
  assert_success

  run install --frozen
  assert_success
  assert_dir ".fglpkg/packages/demo-pkg"
}
it "install --frozen succeeds when the lock matches" _frozen_succeeds_on_matching_lock

_frozen_requires_a_lock() {
  mkpkg consumer.app 0.1.0
  run install --frozen
  assert_failure
  assert_contains "requires a committed fglpkg-lock.json"
}
it "install --frozen fails when no lock is committed" _frozen_requires_a_lock
