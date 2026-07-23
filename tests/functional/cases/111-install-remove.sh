suite "install + remove (mock registry)"

_install_then_remove() {
  mock_registry_start
  run install demo.pkg@1.0.0
  assert_success
  assert_dir ".fglpkg/packages/demo-pkg"

  run remove demo.pkg
  assert_success
  assert_no_file ".fglpkg/packages/demo-pkg"       # package tree pruned
}
it "remove uninstalls a package and prunes its tree" _install_then_remove
