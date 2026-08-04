suite "default consume registry (GIS-364)"

# The consume default scopes install/update/search/info/outdated to ONE repository
# so a team whose Artifactory proxies every public name is not blocked by the
# collision guard. The assertions turn on WHO gets dialed, and the mock registry's
# request log is the proof. Two shapes of secondary appear below:
#
#   * an UNREACHABLE URL (as in 102-search-registry.sh) where the test wants the
#     secondary's failure to be the observable thing;
#   * mock_secondary_url — reachable, 404s everything (see lib/mock.sh) — where
#     the fan-out has to complete so the test can assert what install resolved
#     and whether the secondary was consulted at all. An unreachable secondary
#     cannot serve that: the fan-out aborts on its hard error (spec §7.2) before
#     there is anything to observe.

# ── single-registry sandbox (only the built-in gi) ────────────────────────────

# Naming the only configured registry restricts nothing, so a default of "gi" is
# a no-op and install behaves exactly as with no default at all.
_cd_gi_noop() {
  mock_registry_start
  export FGLPKG_CONSUME_REGISTRY=gi
  run install demo.pkg@1.0.0
  assert_success
  assert_dir ".fglpkg/packages/demo-pkg"
  assert_file_contains "fglpkg.lock" "demo-pkg"
}
it "a default of gi is a no-op with only GI configured" _cd_gi_noop

# Any other name cannot be honoured. It must error — never be silently ignored,
# which would leave the user believing their packages were scoped when they were
# not. The message names the config field, not --registry: the user typed no flag.
_cd_unknown_single() {
  mock_registry_start
  export FGLPKG_CONSUME_REGISTRY=bogus
  run install demo.pkg@1.0.0
  assert_failure
  assert_contains "default consume registry"
  assert_contains '"bogus"'
  assert_not_contains "--registry \"bogus\""
}
it "an unconfigured default errors (single-registry)" _cd_unknown_single

# search takes the same path, so the same guard applies there.
_cd_unknown_single_search() {
  mock_registry_start
  export FGLPKG_CONSUME_REGISTRY=bogus
  run search demo
  assert_failure
  assert_contains "default consume registry"
}
it "an unconfigured default errors on search (single-registry)" _cd_unknown_single_search

# ── multi-provider (gi + an unreachable secondary) ───────────────────────────

# With the default pointing at gi, the unreachable secondary is never dialed:
# no "search in ... failed" warning appears, which is only possible if the
# fan-out was actually scoped.
_cd_search_scoped_to_gi() {
  mock_registry_start
  run registry add myrepo https://example.invalid/repo --repo-key generic-local
  assert_success
  export FGLPKG_CONSUME_REGISTRY=gi
  run search demo
  assert_success
  assert_contains "demo-pkg"
  assert_contains "scoped to default consume registry"
  assert_not_contains "warning: search in"
}
it "the default scopes the search fan-out" _cd_search_scoped_to_gi

# The exclusion proof: with the default pointing at the unreachable secondary,
# install fails AND the mock registry log shows gi was never asked — even though
# gi holds the package. A precedence-wins implementation would have fallen back
# to gi and succeeded here.
_cd_excludes_gi() {
  mock_registry_start
  run registry add myrepo https://example.invalid/repo --repo-key generic-local
  assert_success
  export FGLPKG_CONSUME_REGISTRY=myrepo
  run install demo.pkg@1.0.0
  assert_failure
  assert_no_file "fglpkg.lock"
  # gi was excluded: the mock never saw a request for the package.
  assert_not_contains "demo-pkg" "$(cat "$MOCK_LOG")"
  assert_not_contains "demo.pkg" "$(cat "$MOCK_LOG")"
}
it "the default excludes every other repository, including gi" _cd_excludes_gi

# An explicit --registry outranks the default, so a per-command override still
# reaches gi even while the default points elsewhere.
_cd_flag_overrides() {
  mock_registry_start
  run registry add myrepo https://example.invalid/repo --repo-key generic-local
  assert_success
  export FGLPKG_CONSUME_REGISTRY=myrepo
  run install demo.pkg@1.0.0 --registry gi
  assert_success
  assert_dir ".fglpkg/packages/demo-pkg"
  # An explicit --registry pins the choice in fglpkg.json (unchanged behaviour);
  # the sticky default deliberately does not.
  assert_file_contains "fglpkg.json" "gi"
}
it "an explicit --registry overrides the default" _cd_flag_overrides

# search's override likewise reaches gi and reports results, source-tagged.
_cd_flag_overrides_search() {
  mock_registry_start
  run registry add myrepo https://example.invalid/repo --repo-key generic-local
  assert_success
  export FGLPKG_CONSUME_REGISTRY=myrepo
  run search demo --registry gi
  assert_success
  assert_contains "demo-pkg"
  assert_not_contains "scoped to default consume registry"
}
it "an explicit --registry overrides the default on search" _cd_flag_overrides_search

# An unconfigured default errors here too, and lists what IS configured so the
# typo is fixable from the message alone.
_cd_unknown_multi() {
  mock_registry_start
  run registry add myrepo https://example.invalid/repo --repo-key generic-local
  assert_success
  export FGLPKG_CONSUME_REGISTRY=bogus
  run search demo
  assert_failure
  assert_contains "not configured"
  assert_contains "gi, myrepo"
}
it "an unconfigured default lists the configured registries" _cd_unknown_multi

# ── the sticky default does NOT write per-dependency pins ─────────────────────

# The point of declaring the source once is that it stays declared once. An
# install under the default must not scatter "registry" pins through the
# manifest — the lockfile records the actual source instead.
_cd_writes_no_pin() {
  mock_registry_start
  export FGLPKG_CONSUME_REGISTRY=gi
  run install demo.pkg@1.0.0
  assert_success
  assert_not_contains '"registry"' "$(cat fglpkg.json)"
  assert_file_contains "fglpkg.lock" "demo-pkg"
}
it "installing under the default writes no per-dependency pin" _cd_writes_no_pin

# ── config surfaces and their precedence ─────────────────────────────────────

# The committed project block is the GIS-366 "clone → install just works" surface:
# the registry set AND the default that scopes it both travel in fglpkg.json.
#
# A second registry is required for this test to mean anything. With only gi
# configured, buildRepositorySet returns no set and applyConsumeRegistry treats a
# default of "gi" as the no-op it is — scoping to the only configured repository
# restricts nothing, and noteConsumeDefault stays deliberately silent. Nothing
# would be exercised. The reachable secondary makes the exclusion observable:
# unscoped, the fan-out would probe myrepo; scoped, it must not.
_cd_project_block() {
  mock_registry_start
  cat > fglpkg.json <<EOF
{ "name":"app", "version":"1.0.0", "genero":">=3.20",
  "registries":[{"name":"myrepo","type":"artifactory","url":"$(mock_secondary_url)","repoKey":"generic-local","priority":2}],
  "defaultConsumeRegistry": "gi" }
EOF
  run install demo.pkg@1.0.0
  assert_success
  assert_contains "scoped to default consume registry"
  assert_dir ".fglpkg/packages/demo-pkg"
  # The scoping proof: myrepo answers, so an unscoped fan-out WOULD have probed
  # it. It was never dialed, so routing really was restricted to gi.
  assert_secondary_not_dialed
}
it "a committed defaultConsumeRegistry block scopes the install" _cd_project_block

# Precedence: env beats the project block. The block names the unreachable repo,
# so only the env override reaching gi can make this succeed.
_cd_env_beats_project() {
  mock_registry_start
  run registry add myrepo https://example.invalid/repo --repo-key generic-local
  assert_success
  cat > fglpkg.json <<'EOF'
{ "name":"app", "version":"1.0.0", "genero":">=3.20",
  "registries":[{"name":"myrepo","type":"artifactory","url":"https://example.invalid/repo","repoKey":"generic-local","priority":2}],
  "defaultConsumeRegistry": "myrepo" }
EOF
  export FGLPKG_CONSUME_REGISTRY=gi
  run install demo.pkg@1.0.0
  assert_success
  assert_dir ".fglpkg/packages/demo-pkg"
}
it "FGLPKG_CONSUME_REGISTRY overrides the project block" _cd_env_beats_project

# Precedence: the project block beats the machine-wide config.json. The global
# names the unreachable repo; the committed block redirects to gi.
_cd_project_beats_global() {
  mock_registry_start
  run registry add myrepo https://example.invalid/repo --repo-key generic-local --consume-default
  assert_success
  assert_file_contains "$FGLPKG_HOME/config.json" "defaultConsumeRegistry"
  cat > fglpkg.json <<'EOF'
{ "name":"app", "version":"1.0.0", "genero":">=3.20",
  "defaultConsumeRegistry": "gi" }
EOF
  run install demo.pkg@1.0.0
  assert_success
  assert_dir ".fglpkg/packages/demo-pkg"
}
it "the project block overrides the global config.json" _cd_project_beats_global

# The publish default is a separate knob: setting it must NOT scope consumption.
#
# myrepo has to be REACHABLE for this to test anything. Against an unreachable
# one the install fails on its DNS error whatever defaultRegistry says — the
# fan-out aborts on any non-not-found error — so the test would fail identically
# with the publish default removed, never touching the separation it names.
#
# Reachable, the discrimination is sharp and runs the other way: with no consume
# default the fan-out consults every admitting repository, so myrepo IS dialed,
# and that dial is the proof routing was never scoped. Had defaultRegistry leaked,
# route() would have taken the consumeDefault branch, consulted myrepo alone, and
# failed not-found.
_cd_publish_default_does_not_leak() {
  mock_registry_start
  cat > fglpkg.json <<EOF
{ "name":"app", "version":"1.0.0", "genero":">=3.20",
  "registries":[{"name":"myrepo","type":"artifactory","url":"$(mock_secondary_url)","repoKey":"generic-local","priority":2}],
  "defaultRegistry": "myrepo" }
EOF
  run install demo.pkg@1.0.0
  assert_success
  assert_dir ".fglpkg/packages/demo-pkg"
  assert_not_contains "scoped to default consume registry"
  # No leak: routing stayed unscoped, so the fan-out really did consult myrepo.
  assert_secondary_dialed
  # ...and gi won it. The lock collapses a "gi" source to absent (GIS-249 C2), so
  # a secondary having served the package would show up here.
  assert_file_contains "fglpkg.lock" "demo-pkg"
  assert_not_contains '"source"' "$(cat fglpkg.lock)"
}
it "the publish defaultRegistry does not scope consumption" _cd_publish_default_does_not_leak

# ── registry add --consume-default / registry list ────────────────────────────

# --consume-default writes the descriptor AND the field into the same file, so a
# committed fglpkg.json carries both.
_cd_add_project_writes_field() {
  mock_registry_start
  cat > fglpkg.json <<'EOF'
{ "name":"app", "version":"1.0.0", "genero":">=3.20" }
EOF
  run registry add myrepo https://example.invalid/repo --repo-key generic-local --project --consume-default
  assert_success
  assert_contains "Set as the default consume registry"
  assert_json_field "fglpkg.json" "defaultConsumeRegistry" "myrepo"
  assert_json_field "fglpkg.json" "registries.0.name" "myrepo"
}
it "registry add --project --consume-default writes both" _cd_add_project_writes_field

# The DEFAULT column makes the sticky routing visible rather than something you
# discover on your next install.
_cd_list_default_column() {
  mock_registry_start
  run registry add myrepo https://example.invalid/repo --repo-key generic-local --consume-default
  assert_success
  run registry list
  assert_success
  assert_contains "DEFAULT"
  assert_match "myrepo.*consume"
  # gi serves no declared default, so it is blank rather than implied.
  assert_not_match "gi .*consume"
}
it "registry list shows which registry serves the consume default" _cd_list_default_column

# Removing the registry a default points at clears the default too — otherwise
# every consuming command would fail against a repository that no longer exists.
_cd_remove_clears_default() {
  mock_registry_start
  run registry add myrepo https://example.invalid/repo --repo-key generic-local --consume-default
  assert_success
  run registry remove myrepo
  assert_success
  assert_not_contains "defaultConsumeRegistry" "$(cat "$FGLPKG_HOME/config.json")"
  # ...and consuming commands work again, unscoped.
  run search demo
  assert_success
  assert_contains "demo-pkg"
}
it "registry remove clears a dangling consume default" _cd_remove_clears_default
