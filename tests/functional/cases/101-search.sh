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

# The registry paginates and defaults to 20 results per page. `search` issued a
# single unparameterised request and returned whatever that first page held, so
# it silently stopped at 20 — reporting no truncation and ignoring the `total`
# the response carries. A package past the cut-off simply looked as though it
# did not exist (GIS-584).
#
# The mock caps pageSize at 50, so 60 packages cannot arrive in one page no
# matter what the client asks for: reaching them all requires following pages.
_search_fixtures_many() {  # _search_fixtures_many <dir> <count>
  local dir="$1" count="$2" i
  mkdir -p "$dir"
  {
    printf '{\n  "packages": [\n'
    for i in $(seq 1 "$count"); do
      [[ $i -gt 1 ]] && printf ',\n'
      printf '    { "slug": "bulk-pkg-%03d", "name": "bulk.pkg.%03d", "description": "bulk search fixture",\n' "$i" "$i"
      printf '      "genero": ">=3.20", "owner": { "partner_id": "mock", "name": "fglpkg tests" },\n'
      # No artifacts: the mock reads every listed zip at startup, and search
      # only ever reads the listing, so referencing files would be dead weight.
      printf '      "versions": [ { "version": "1.0.0", "artifacts": [] } ] }'
    done
    printf '\n  ]\n}\n'
  } > "$dir/packages.json"
}

_search_pages_past_twenty() {
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/fx.XXXXXX")"
  _search_fixtures_many "$fx" 60 || return 1
  mock_registry_start "$fx" || return 1

  run search bulk-pkg
  assert_success || return 1
  local n; n="$(printf '%s\n' "$output" | grep -c 'bulk-pkg-' || true)"
  [[ "$n" -gt 20 ]] || { _diag "only $n results; search stopped at the registry's default page (GIS-584)"; return 1; }
  [[ "$n" -eq 60 ]] || { _diag "got $n results, want all 60"; return 1; }
  # The last package can only be reached by following pages to the end.
  assert_contains "bulk-pkg-060" || return 1
}
it "search follows pagination past the registry's default page size" _search_pages_past_twenty

_search_all_pages_too() {
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/fx.XXXXXX")"
  _search_fixtures_many "$fx" 60 || return 1
  mock_registry_start "$fx" || return 1

  run search --all
  assert_success || return 1
  local n; n="$(printf '%s\n' "$output" | grep -c 'bulk-pkg-' || true)"
  [[ "$n" -eq 60 ]] || { _diag "search --all returned $n of 60"; return 1; }
}
it "search --all lists every package, not just the first page" _search_all_pages_too
