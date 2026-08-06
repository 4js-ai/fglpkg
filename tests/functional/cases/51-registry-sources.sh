suite "registry list (SOURCE attribution) + committed project registries"

# The SOURCE column (GIS-366) attributes each registry to the cascade layer it
# came from. With nothing configured, only the always-present built-in gi shows,
# tagged builtin.
_rs_source_column_builtin() {
  run registry list
  assert_success
  assert_contains "SOURCE"
  assert_match "LOGIN +SOURCE +DEFAULT +URL"   # column order in the header (DEFAULT: GIS-364)
  assert_match "gi.*genero.*builtin"     # the built-in row is tagged builtin
}
it "registry list prints the SOURCE column and marks gi builtin" _rs_source_column_builtin

# A registry added without --local lands in the per-user ~/.fglpkg/config.json
# and is attributed to the global source.
_rs_global_source() {
  run registry add myrepo https://example.com/repo --repo-key generic-local
  assert_success
  assert_contains 'Added registry "myrepo" to'
  assert_file "$FGLPKG_HOME/config.json"
  run registry list
  assert_success
  assert_match "myrepo.*artifactory.*global"
  assert_match "gi.*genero.*builtin"
}
it "registry list attributes a globally-added registry to global" _rs_global_source

# A registry added with --local is written into the committed fglpkg.json (no
# machine config touched) and attributed to the project source — the "clone and
# it just works" story.
_rs_project_source() {
  mkpkg demo.pkg
  run registry add acme https://a.example --repo-key GeneroBDL --local
  assert_success
  assert_contains 'Added registry "acme" to fglpkg.json'
  assert_file_contains "fglpkg.json" '"registries"'
  assert_no_file "$FGLPKG_HOME/config.json"    # nothing written to the machine config
  run registry list
  assert_success
  assert_match "acme.*artifactory.*project"
  assert_match "gi.*genero.*builtin"
}
it "a project fglpkg.json registry is inherited and shown as project" _rs_project_source

# Credentials never land in the committed fglpkg.json nor the machine config.json;
# a login secret goes only to ~/.fglpkg/credentials.json.
_rs_credentials_stay_separate() {
  mkpkg demo.pkg
  run registry add acme https://a.example --repo-key K --local
  assert_success
  run login --registry acme --token super-secret-bearer-token-9f3a
  assert_success
  assert_contains 'Credentials saved for registry "acme" (https://a.example, bearer auth)'
  assert_file_contains "$FGLPKG_HOME/credentials.json" "super-secret-bearer-token-9f3a"
  assert_not_contains "super-secret-bearer-token-9f3a" "$(cat fglpkg.json)"
  assert_no_file "$FGLPKG_HOME/config.json"
}
it "registry credentials stay out of fglpkg.json and config.json" _rs_credentials_stay_separate
