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

# An extraction interrupted part-way leaves a package directory with no
# manifest in it. There is then no version to compare, so the entry must be
# treated as missing and reinstalled — never as present.
_drift_unreadable_manifest_reinstalls() {
  mock_registry_start
  run install demo.pkg@1.0.0
  assert_success || return 1
  rm -f .fglpkg/packages/demo-pkg/fglpkg.json || return 1

  run install
  assert_success || return 1
  assert_not_contains "Nothing to install" || return 1
  assert_file_contains ".fglpkg/packages/demo-pkg/fglpkg.json" '"version": "1.0.0"' || return 1
}
it "a package directory with no manifest is reinstalled, not assumed present" \
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
