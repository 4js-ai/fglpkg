suite "search (mock registry)"

_search_hit() {
  mock_registry_start
  run search demo
  assert_success
  assert_contains "demo-pkg"
}
it "search finds a matching package" _search_hit

_search_miss() {
  mock_registry_start
  run search zzzz-no-such-term
  assert_success                    # empty result set is still a success
  assert_not_contains "demo-pkg"
}
it "search with no matches returns no results" _search_miss
