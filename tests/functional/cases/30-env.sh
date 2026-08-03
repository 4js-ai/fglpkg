suite "env"

_env_global() {
  mkdir -p "$FGLPKG_HOME/packages/mypkg/com/fourjs/mypkg"
  run env --global
  assert_success
  assert_contains "FGLLDPATH"
  assert_contains_path "packages/mypkg"
}
it "env --global puts the installed package dir on FGLLDPATH" _env_global

_env_local() {
  mkdir -p ".fglpkg/packages/lpkg"
  run env --local
  assert_success
  assert_contains "FGLLDPATH"
  assert_contains "lpkg"
}
it "env --local emits FGLLDPATH for local packages" _env_local

_env_empty() {
  run env --global
  assert_success
  # Nothing installed in the global home → no package paths to set up, so the
  # output is empty. That is expected; the command must simply exit cleanly and
  # not emit a spurious FGLLDPATH pointing at an empty store.
  assert_not_contains "FGLLDPATH"
  # Same rule for every other managed variable: emit nothing rather than a
  # spurious empty export.
  assert_not_contains "FGLRESOURCEPATH"
  assert_not_contains "FGLDBPATH"
  assert_not_contains "FGLIMAGEPATH"
  assert_not_contains "FGLPROFILE"
}
it "env --global exits cleanly (no output) with an empty home" _env_empty
