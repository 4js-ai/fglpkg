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
  run registry add bad https://example.com/repo   # no /artifactory segment to infer from
  assert_failure
}
it "registry add (artifactory) requires --repo-key" _reg_add_needs_repokey

_reg_add_repokey_in_url() {
  # The key may be pasted on the end of the URL instead of passed as a flag.
  run registry add pasted https://example.com/artifactory/generic-local
  assert_success
  assert_contains "generic-local"                 # the note names the inferred key
  run registry list; assert_success
  assert_contains "https://example.com/artifactory"
  assert_not_contains "artifactory/generic-local" # key split off, not left in the URL
}
it "registry add infers the repo key from the URL" _reg_add_repokey_in_url

_reg_add_repokey_conflict() {
  run registry add bad https://example.com/artifactory/one --repo-key two
  assert_failure
}
it "registry add rejects a --repo-key that disagrees with the URL" _reg_add_repokey_conflict

_reg_remove() {
  run registry add myrepo https://example.com/repo --repo-key generic-local; assert_success
  run registry remove myrepo; assert_success
  run registry list; assert_success; assert_not_contains "myrepo"
}
it "registry remove deletes a configured repo" _reg_remove
