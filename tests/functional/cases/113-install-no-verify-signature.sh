suite "install --no-verify-signature (mock registry)"

# `--no-verify-signature` is documented as overriding signing.enforce for one
# install. Under FGLPKG_SIGNING=require an unsigned package aborts, and the flag
# must be the escape hatch that lets it through — for BOTH install shapes.
#
# The two shapes take different code paths: `install` (no package) installs the
# graph straight from the manifest, while `install <pkg>` resolves the addition,
# saves the manifest, then REBUILDS the installer before installing the graph.
# The rebuild is where a per-invocation flag can go missing, and it did: the flag
# was inert for `install <pkg>` because every download runs through the rebuilt
# instance.
#
# Note the explicit `|| return 1` on each assertion. `it()` runs the test body in
# a subshell inside an `if` condition, and bash suppresses errexit there — so a
# bare failing assertion mid-body is ignored and the verdict comes from the last
# command alone. That is exactly how this bug hid: verification happens AFTER
# extraction and after fglpkg.lock is written, so the trailing "package is on
# disk" assertions all passed while the install had actually failed.

# Baseline: require mode must reject the unsigned fixture, or the tests below
# could pass for the wrong reason (nothing to override).
_nvs_require_rejects_unsigned() {
  mock_registry_start
  export FGLPKG_SIGNING=require
  run install demo.pkg@1.0.0
  assert_failure || return 1
  assert_contains "artifact is not signed" || return 1
}
it "require mode rejects an unsigned package (baseline)" _nvs_require_rejects_unsigned

# The regression: the add-a-package shape must honour the flag.
_nvs_flag_overrides_on_add() {
  mock_registry_start
  export FGLPKG_SIGNING=require
  run install demo.pkg@1.0.0 --no-verify-signature
  assert_success || return 1
  assert_not_contains "artifact is not signed" || return 1
  assert_dir ".fglpkg/packages/demo-pkg" || return 1
  assert_file ".fglpkg/packages/demo-pkg/fglpkg.json" || return 1
  assert_file_contains "fglpkg.lock" "demo-pkg" || return 1
}
it "install <pkg> --no-verify-signature overrides require mode" _nvs_flag_overrides_on_add

# The graph shape already worked; pinned so a refactor of the option plumbing
# cannot fix one path by breaking the other.
_nvs_flag_overrides_on_graph() {
  mock_registry_start
  cat > fglpkg.json <<'EOF'
{ "name":"app", "version":"1.0.0", "genero":">=3.20",
  "dependencies": { "fgl": { "demo.pkg": "1.0.0" } } }
EOF
  export FGLPKG_SIGNING=require
  run install --local --no-verify-signature
  assert_success || return 1
  assert_dir ".fglpkg/packages/demo-pkg" || return 1
}
it "install --no-verify-signature overrides require mode (graph install)" _nvs_flag_overrides_on_graph

# The flag is scoped to the invocation that passes it: nothing is persisted to
# config, so the next install enforces again. An escape hatch that quietly became
# permanent would be worse than one that did not work.
_nvs_flag_is_not_sticky() {
  mock_registry_start
  export FGLPKG_SIGNING=require
  run install demo.pkg@1.0.0 --no-verify-signature
  assert_success || return 1
  rm -rf .fglpkg fglpkg.lock
  run install
  assert_failure || return 1
  assert_contains "artifact is not signed" || return 1
}
it "--no-verify-signature does not persist to the next install" _nvs_flag_is_not_sticky

# Without the flag, require mode still aborts even though the flag now flows
# through the rebuild — i.e. the fix did not turn verification off wholesale.
_nvs_enforcement_still_applies_on_add() {
  mock_registry_start
  export FGLPKG_SIGNING=require
  run install demo.pkg@1.0.0
  assert_failure || return 1
  assert_contains "artifact is not signed" || return 1
  assert_no_file "fglpkg.lock.tmp" || return 1
}
it "install <pkg> without the flag still enforces require mode" _nvs_enforcement_still_applies_on_add
