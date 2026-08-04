suite "global package root (FGLPKG_GLOBAL_DIR) — GIS-367"

# The global package install root is separable from the config/credentials home:
# with FGLPKG_GLOBAL_DIR set, a --global install lands there, NOT under
# FGLPKG_HOME (which continues to hold config/credentials).
_gd_global_install_splits_from_home() {
  mock_registry_start
  local gdir="$TESTWD/global-pkgs"
  export FGLPKG_GLOBAL_DIR="$gdir"
  run install demo.pkg --global
  assert_success
  assert_dir "$gdir/packages/demo-pkg"                       # packages go to FGLPKG_GLOBAL_DIR
  assert_no_file "$FGLPKG_HOME/packages/demo-pkg/fglpkg.json" # not under the config/credentials home
}
it "a --global install goes to FGLPKG_GLOBAL_DIR, separate from FGLPKG_HOME" _gd_global_install_splits_from_home

# env --global then points FGLLDPATH at the relocated global root.
_gd_env_global_uses_global_dir() {
  mock_registry_start
  local gdir="$TESTWD/global-pkgs"
  export FGLPKG_GLOBAL_DIR="$gdir"
  run install demo.pkg --global; assert_success
  run env --global
  assert_success
  assert_contains_path "global-pkgs"          # the relocated root is on the emitted paths
  assert_not_contains_path "$FGLPKG_HOME/packages"
}
it "env --global emits the FGLPKG_GLOBAL_DIR root" _gd_env_global_uses_global_dir

# With FGLPKG_GLOBAL_DIR unset, the global root stays under FGLPKG_HOME — the
# historical behaviour, unchanged (the FGLDIR default only applies when neither
# FGLPKG_GLOBAL_DIR nor FGLPKG_HOME is set, and the harness always sets HOME).
_gd_defaults_to_home_when_unset() {
  mock_registry_start
  unset FGLPKG_GLOBAL_DIR
  run install demo.pkg --global
  assert_success
  assert_dir "$FGLPKG_HOME/packages/demo-pkg"
}
it "global install stays under FGLPKG_HOME when FGLPKG_GLOBAL_DIR is unset" _gd_defaults_to_home_when_unset
