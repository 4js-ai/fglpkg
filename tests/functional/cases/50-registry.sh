suite "registry config"

_reg_default() {
  run registry list
  assert_success
  assert_contains "gi"          # built-in Genero Intelligence registry
  assert_contains "genero"
}
it "registry list shows the built-in gi registry" _reg_default

_reg_add() {
  run registry add myrepo https://example.com/repo --repo-key generic-local
  assert_success
  run registry list; assert_success; assert_contains "myrepo"
  assert_file "$FGLPKG_HOME/config.json"
}
it "registry add writes an Artifactory repo to config.json" _reg_add

_reg_add_needs_repokey() {
  run registry add bad https://example.com/repo   # artifactory default needs --repo-key
  assert_failure
}
it "registry add (artifactory) requires --repo-key" _reg_add_needs_repokey

_reg_remove() {
  run registry add myrepo https://example.com/repo --repo-key generic-local; assert_success
  run registry remove myrepo; assert_success
  run registry list; assert_success; assert_not_contains "myrepo"
}
it "registry remove deletes a configured repo" _reg_remove
