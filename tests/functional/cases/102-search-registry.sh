suite "search --registry (mock registry)"

# With only the built-in GI registry configured, buildRepositorySet returns a nil
# set and search takes the single-registry path. --registry gi names that one
# registry, so it is a no-op and the search runs normally.
_search_registry_gi_noop() {
  mock_registry_start
  run search demo --registry gi
  assert_success
  assert_contains "demo-pkg"
  assert_contains 'Results for "demo" (Genero 6.00.01):'
  # The equals-form parses to the same thing ($output is overwritten, so re-run).
  run search demo --registry=gi
  assert_success
  assert_contains "demo-pkg"
}
it "search --registry gi is a no-op with only GI configured" _search_registry_gi_noop

# Single-registry path: any name other than "gi" is unknown and errors — without
# listing configured names (contrast the multi-provider error below).
_search_registry_unknown_single() {
  mock_registry_start
  run search demo --registry bogus
  assert_failure
  assert_contains 'no repository named "bogus" is configured'
  assert_contains "(add it to fglpkg.json or ~/.fglpkg/config.json)"
}
it "search --registry <unknown> errors (single-registry)" _search_registry_unknown_single

# Adding a secondary registry flips search onto the multi-provider fan-out.
# --registry gi restricts the fan-out to gi: only the mock is queried, the
# unreachable secondary is skipped (no "search in ... failed" warning), and the
# result is tagged with its SOURCE (gi).
_search_registry_gi_multiprovider() {
  mock_registry_start
  run registry add myrepo https://example.invalid/repo --repo-key generic-local
  assert_success
  run search demo --registry gi
  assert_success
  assert_contains "demo-pkg"
  assert_contains "SOURCE"           # multi-provider output always tags the source
  assert_contains "gi"               # the row's source tag
  assert_not_contains "warning: search in"   # the skipped secondary is never dialed
}
it "search --registry gi filters the multi-provider fan-out" _search_registry_gi_multiprovider

# Multi-provider path: an unknown name errors AND lists the configured registries
# in priority order (gi, myrepo) — the distinct multi-provider error surface.
_search_registry_unknown_multiprovider() {
  mock_registry_start
  run registry add myrepo https://example.invalid/repo --repo-key generic-local
  assert_success
  run search demo --registry bogus
  assert_failure
  assert_contains 'no repository named "bogus" is configured.'
  assert_contains "Configured registries: gi, myrepo"
}
it "search --registry <unknown> lists configured registries (multi-provider)" _search_registry_unknown_multiprovider

# The flag needs a value; the guard fires in the arg parser before any config
# load or network access.
_search_registry_missing_value() {
  run search demo --registry
  assert_failure
  assert_contains "--registry requires a value"
}
it "search --registry with no value errors" _search_registry_missing_value
