suite "\$HOME is not a project (GIS-571)"

# `.fglpkg/` names two unrelated things: a project's local install directory,
# and — as ~/.fglpkg — fglpkg's own home. isProjectDir() could not tell them
# apart, so $HOME counted as a project and every global command silently fell
# back to project behaviour there: `install --global` wrote fglpkg.json and
# fglpkg-lock.json into $HOME, and `remove --global` only edited that accidental
# manifest while never touching the store.

# _h571_home makes the cwd look exactly like a real $HOME: the fglpkg home is
# ./.fglpkg, which with no FGLPKG_GLOBAL_DIR is ALSO the global package root —
# the default arrangement on a normal machine.
_h571_home() {
  local home="$TESTWD/fakehome"
  mkdir -p "$home/.fglpkg"
  export FGLPKG_HOME="$home/.fglpkg"
  unset FGLPKG_GLOBAL_DIR
  cd "$home"
}

# The reported symptom, first half (GIS-565 was inert here).
_h571_install_global_writes_nothing() {
  _h571_home
  mock_registry_start
  run install demo.pkg@1.0.0 --global
  assert_success

  # Nothing turned $HOME into a project.
  assert_no_file "fglpkg.json"
  assert_no_file "fglpkg-lock.json"
  assert_not_contains "Added demo-pkg"          # the in-project wording

  # And the package really is in the store, which here IS ~/.fglpkg.
  assert_dir ".fglpkg/packages/demo-pkg"
}
it "install --global from \$HOME writes no project files" _h571_install_global_writes_nothing

# The reported symptom, second half (GIS-567 was inert here): the remove only
# edited the accidental manifest and reported the store as untouched.
_h571_remove_global_uninstalls() {
  _h571_home
  mock_registry_start
  run install demo.pkg@1.0.0 --global
  assert_success

  run remove demo.pkg --global
  assert_success
  assert_contains "Removed demo-pkg from the global store"
  assert_not_contains "from dependencies"       # the in-project branch
  assert_no_file ".fglpkg/packages/demo-pkg"
}
it "remove --global from \$HOME uninstalls from the store" _h571_remove_global_uninstalls

# A bare remove from $HOME must still explain the scope rather than silently
# falling into project behaviour (GIS-567's rule, now reachable from here).
_h571_bare_remove_explains_scope() {
  _h571_home
  mock_registry_start
  run install demo.pkg@1.0.0 --global
  assert_success

  run remove demo.pkg
  assert_failure
  assert_contains "--global"
  assert_dir ".fglpkg/packages/demo-pkg"        # survives
}
it "a bare remove from \$HOME explains the scope and deletes nothing" _h571_bare_remove_explains_scope

# The other half of the rule: a directory whose .fglpkg/ is nobody's home is a
# real project, and --global there keeps its in-project meaning.
_h571_real_project_still_detected() {
  mock_registry_start
  mkdir -p .fglpkg                              # a project's local install dir
  run install demo.pkg@1.0.0 --global
  assert_success
  assert_file "fglpkg.json"                     # recorded in THIS project
  assert_contains "Added demo-pkg"
}
it "a project's own .fglpkg/ still marks it as a project" _h571_real_project_still_detected

# FGLPKG_GLOBAL_DIR can point somewhere else entirely while ~/.fglpkg still
# exists holding config and credentials. Keying only on the ACTIVE store would
# leave $HOME broken for exactly those users (e.g. anyone with FGLDIR bound).
_h571_home_when_store_is_elsewhere() {
  local home="$TESTWD/fakehome"
  mkdir -p "$home/.fglpkg"
  export FGLPKG_HOME="$home/.fglpkg"
  export FGLPKG_GLOBAL_DIR="$TESTWD/elsewhere"
  cd "$home"

  mock_registry_start
  run install demo.pkg@1.0.0 --global
  assert_success
  assert_no_file "fglpkg.json"
  assert_no_file "fglpkg-lock.json"
  assert_dir "$TESTWD/elsewhere/packages/demo-pkg"
}
it "\$HOME is not a project even when the store lives elsewhere" _h571_home_when_store_is_elsewhere

# AC4: `run` from $HOME is unaffected — it already tolerates a missing manifest
# there (GIS-566), and must not start warning now that $HOME is not a project.
_h571_run_unaffected() {
  _h571_home
  run run --list
  assert_success
  assert_not_contains "failed to load"
  assert_not_contains "arning"
}
it "run --list from \$HOME still works and stays quiet" _h571_run_unaffected
