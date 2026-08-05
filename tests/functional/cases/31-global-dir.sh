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

# The split has to reach the bin-command and relink paths too, not just install
# and env. `run --list`, `run <cmd>` and `relink --global` all resolve the global
# PACKAGE root; if any still used FGLPKG_HOME, a globally installed command would
# be unrunnable and `relink --global` would rebuild the wrong merged/ dir. These
# are reporting/resolving commands over what is on disk, so hand-place a package
# with a bin command under the relocated root — no install needed.
_gd_write_bin_pkg() {  # _gd_write_bin_pkg <global-root>
  mkdir -p "$1/packages/democli"
  cat > "$1/packages/democli/fglpkg.json" <<'EOF'
{ "name":"democli", "version":"1.0.0", "genero":">=3.20",
  "bin": { "demo-cmd": "run.sh" } }
EOF
  cat > "$1/packages/democli/run.sh" <<'EOF'
#!/bin/sh
echo "demo-cmd ran"
EOF
  chmod +x "$1/packages/democli/run.sh"
}

_gd_run_list_uses_global_dir() {
  local gdir="$TESTWD/global-pkgs"
  export FGLPKG_GLOBAL_DIR="$gdir"
  _gd_write_bin_pkg "$gdir"
  run run --list
  assert_success
  assert_contains "demo-cmd"                 # discovered from the relocated root...
  assert_contains "democli"                  # ...not from the (empty) FGLPKG_HOME
  assert_not_contains "No commands available"
}
it "run --list scans FGLPKG_GLOBAL_DIR, not FGLPKG_HOME" _gd_run_list_uses_global_dir

_gd_run_cmd_resolves_from_global_dir() {
  local gdir="$TESTWD/global-pkgs"
  export FGLPKG_GLOBAL_DIR="$gdir"
  _gd_write_bin_pkg "$gdir"
  run run demo-cmd
  assert_success
  assert_contains "demo-cmd ran"             # the script was actually found and run
}
it "run <cmd> resolves a bin command from FGLPKG_GLOBAL_DIR" _gd_run_cmd_resolves_from_global_dir

_gd_relink_global_targets_global_dir() {
  local gdir="$TESTWD/global-pkgs"
  export FGLPKG_GLOBAL_DIR="$gdir"
  _gd_write_bin_pkg "$gdir"
  run relink --global
  assert_success
  # The merged root is rebuilt under the relocated global dir (…/global-pkgs/merged),
  # not beside config/credentials under FGLPKG_HOME — "global-pkgs" never appears in
  # the home path, so this substring alone discriminates the fix from the old bug.
  # (A relative substring, not the absolute path: TESTWD can carry a // that the
  # binary's filepath.Join collapses.)
  assert_contains "global-pkgs/merged"
}
it "relink --global rebuilds the merged root under FGLPKG_GLOBAL_DIR" _gd_relink_global_targets_global_dir
