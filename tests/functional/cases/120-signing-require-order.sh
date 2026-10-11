suite "FGLPKG_SIGNING=require refuses before touching the project (GIS-580)"

# Verification used to run AFTER the download, the extraction and the lock
# write, at every install site. Under `require` that failed the first run but
# left the rejected artifact in .fglpkg/ and its entry in fglpkg-lock.json — so
# the NEXT run took the already-installed fast path, reported "Nothing to
# install" and exited 0. A pipeline with a retry step therefore accepted the
# very artifact `require` exists to reject.
#
# The mock registry serves every artifact with "signature": null, so under
# require each one is refused as unsigned. The reason for the refusal is
# immaterial here — the ordering is what is under test.
#
# Note the explicit `|| return 1` on each assertion: `it()` runs the body in a
# subshell inside an `if`, so bash suppresses errexit and a bare failing
# assertion mid-body would be ignored (see the note in case 113).

# The headline regression: a retry must not launder a refused artifact.
_req_retry_does_not_launder() {
  mock_registry_start
  export FGLPKG_SIGNING=require
  run install demo.pkg@1.0.0
  assert_failure || return 1
  assert_contains "artifact is not signed" || return 1

  # This second run is the bug: it used to print "Nothing to install", exit 0,
  # and leave the unverified package in place as the project's dependency.
  run install
  assert_failure || return 1
  assert_not_contains "Nothing to install" || return 1
}
it "a re-run after a refused install does not accept the package" \
  _req_retry_does_not_launder

# Refusing has to be refusing: no artifact in the store, no lock claiming it.
_req_leaves_nothing_behind() {
  mock_registry_start
  export FGLPKG_SIGNING=require
  run install demo.pkg@1.0.0
  assert_failure || return 1
  assert_no_file ".fglpkg/packages/demo-pkg/fglpkg.json" || return 1
  assert_no_file "fglpkg-lock.json" || return 1
  # The project's OWN manifest too. This directory had no fglpkg.json before
  # the run, and `install <pkg>` generates one as it adds the dependency — so
  # until the add path learned to roll back (GIS-579), a refused install still
  # left a project behind declaring the package it had just refused.
  assert_no_file "fglpkg.json" || return 1
}
it "a refused install leaves no package, no lock and no project behind" \
  _req_leaves_nothing_behind

# The graph shape resolves from the manifest instead of adding a package, so it
# reaches the gate by a different route and is pinned separately.
_req_graph_leaves_nothing_behind() {
  mock_registry_start
  cat > fglpkg.json <<'EOF'
{ "name":"app", "version":"1.0.0", "genero":">=3.20",
  "dependencies": { "fgl": { "demo.pkg": "1.0.0" } } }
EOF
  export FGLPKG_SIGNING=require
  run install --local
  assert_failure || return 1
  assert_no_file ".fglpkg/packages/demo-pkg/fglpkg.json" || return 1
  assert_no_file "fglpkg-lock.json" || return 1
  run install --local
  assert_failure || return 1
  assert_not_contains "Nothing to install" || return 1
}
it "a refused graph install leaves nothing behind and still fails on retry" \
  _req_graph_leaves_nothing_behind

# The realistic rollout: a project already has the package installed and a lock
# committed, and CI then turns `require` on. Presence on disk is not evidence of
# trust, so the locked entry must be re-verified rather than fast-pathed.
_req_replay_reverifies_what_is_on_disk() {
  mock_registry_start
  export FGLPKG_SIGNING=off
  run install demo.pkg@1.0.0
  assert_success || return 1
  assert_dir ".fglpkg/packages/demo-pkg" || return 1
  assert_file "fglpkg-lock.json" || return 1

  export FGLPKG_SIGNING=require
  run install
  assert_failure || return 1
  assert_contains "artifact is not signed" || return 1
  assert_not_contains "Nothing to install" || return 1
}
it "require re-verifies a package that is already installed" \
  _req_replay_reverifies_what_is_on_disk

# A refused replay must not change the store at all. The prune runs on the
# lock-replay path, so verification has to come first — otherwise a run that
# installs nothing still deletes orphans before refusing, which is the opposite
# of what the resolve path promises.
_req_refusal_does_not_prune() {
  mock_registry_start
  export FGLPKG_SIGNING=off
  run install demo.pkg@1.0.0
  assert_success || return 1

  # An orphan the lock does not name: what the prune would sweep.
  mkdir -p .fglpkg/packages/orphan
  # Drop the locked package so the replay takes the install path rather than
  # the "Nothing to install" one.
  rm -rf .fglpkg/packages/demo-pkg

  export FGLPKG_SIGNING=require
  run install --local
  assert_failure || return 1
  assert_not_contains "pruned" || return 1
  assert_dir ".fglpkg/packages/orphan" || return 1
}
it "a refused replay prunes nothing" _req_refusal_does_not_prune

# The gate and the install pass must select the same set. A dev-scoped package
# is in a normal replay but not a --production one, so it is the entry that
# would fall through a gate whose selection had drifted from the installer's —
# and under require it would then be installed unverified.
_req_covers_a_dev_scoped_package() {
  mock_registry_start
  export FGLPKG_SIGNING=off
  run install demo.pkg@1.0.0 --save-dev
  assert_success || return 1
  assert_file_contains "fglpkg-lock.json" '"scope": "dev"' || return 1

  # Drop it from disk so the replay has something to fetch.
  rm -rf .fglpkg/packages/demo-pkg

  export FGLPKG_SIGNING=require
  run install
  assert_failure || return 1
  assert_contains "artifact is not signed" || return 1
  assert_no_file ".fglpkg/packages/demo-pkg/fglpkg.json" || return 1
}
it "require covers a dev-scoped package on a normal replay" \
  _req_covers_a_dev_scoped_package

# Control: the default mode is warn, and it must still install. Verifying
# earlier must not turn a warning into a failure — while GIS-576 is open most of
# the real registry fails verification, so this is the common path.
_req_warn_still_installs() {
  mock_registry_start
  export FGLPKG_SIGNING=warn
  run install demo.pkg@1.0.0
  assert_success || return 1
  assert_dir ".fglpkg/packages/demo-pkg" || return 1
  assert_file_contains "fglpkg-lock.json" "demo-pkg" || return 1
}
it "warn mode still installs an unsigned package" _req_warn_still_installs

# ...and a warn-mode replay of a package already on disk stays quiet. The
# warning was emitted when it was installed; repeating it on every replay would
# be noise that changes no outcome.
_req_warn_replay_is_quiet() {
  mock_registry_start
  export FGLPKG_SIGNING=warn
  run install demo.pkg@1.0.0
  assert_success || return 1
  run install
  assert_success || return 1
  assert_not_contains "signature check failed" || return 1
}
it "warn mode does not re-warn for a package already installed" \
  _req_warn_replay_is_quiet
