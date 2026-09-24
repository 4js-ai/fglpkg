suite "install <pkg> --global outside a project (GIS-565)"

# A global "tool" install materializes into the global store but writes NOTHING
# into the current (non-project) directory — the global store is tracked by
# scanning, not a project manifest/lock. This covers the cmdInstall wiring
# (skip the manifest Save + set SkipLock) that the Go unit tests do not reach.
_gt_leaves_cwd_clean() {
  mock_registry_start
  local gdir="$TESTWD/global-pkgs"
  export FGLPKG_GLOBAL_DIR="$gdir"
  run install demo.pkg --global
  assert_success
  assert_dir "$gdir/packages/demo-pkg"        # installed into the global store...
  assert_no_file "fglpkg.json"                # ...but the cwd is left untouched
  assert_no_file "fglpkg-lock.json"
  assert_no_file ".fglpkg"
}
it "install --global outside a project leaves the cwd untouched" _gt_leaves_cwd_clean

# --local wins over --global (resolveHome precedence), so the combination is a
# normal local install — never a store-only install that would orphan an empty
# .fglpkg/ with no manifest (the regression an earlier revision introduced).
_gt_local_wins_over_global() {
  mock_registry_start
  export FGLPKG_GLOBAL_DIR="$TESTWD/global-pkgs"
  run install demo.pkg --local --global
  assert_success
  assert_file "fglpkg.json"
  assert_file "fglpkg-lock.json"
  assert_dir ".fglpkg/packages/demo-pkg"
}
it "install --local --global is a normal local install" _gt_local_wins_over_global
