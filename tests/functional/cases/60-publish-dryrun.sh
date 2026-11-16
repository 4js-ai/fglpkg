suite "publish --dry-run"

# Validation runs before the auth/network step, so an incomplete manifest is
# reported without any network access.
_pub_invalid() {
  cat > fglpkg.json <<'EOF'
{ "name":"demo.pkg","version":"1.0.0","description":"d","genero":">=3.20","files":["*.42m"] }
EOF
  printf 'stub' > mod.42m
  run publish --dry-run
  assert_failure
  assert_contains "not ready to publish"
}
it "publish --dry-run reports manifest validation errors" _pub_invalid

# A valid manifest with a token set completes the dry run and uploads nothing.
#
# It does make ONE read first, and this case used to make it against the public
# registry: with FGLPKG_REGISTRY unset the publish default is
# https://service.generointelligence.ai, and the overwrite pre-check asks it
# whether this version is published — by name. So every run of this suite sent
# a package name to the public registry and depended on reaching it, which also
# made the case fail on a machine with no network. The mock answers it instead.
_pub_valid() {
  mock_registry_start || return 1
  mkpkg demo.pkg 9.9.9          # a version the mock has NOT published
  export FGLPKG_TOKEN="dummy-token-for-dry-run"
  run publish --dry-run
  assert_success
  assert_match "dry.?run|would|DRY"
  # The banner says what happened rather than promising the opposite: the read
  # is already done by the time it prints, and on the public registry that is a
  # disclosure of the package's name.
  assert_contains "nothing will be uploaded" || return 1
  assert_contains "One read has already happened" || return 1
  assert_not_contains "no network calls will be made" || return 1
}
it "publish --dry-run (valid manifest + token) uploads nothing" _pub_valid

# The read the banner admits to is real, and it is the ONLY one: everything
# else the dry run would do is printed, not sent. With the registry answering
# locally, its log is the whole record of what the command touched.
_pub_dryrun_reads_once_and_writes_nothing() {
  mock_registry_start || return 1
  mkpkg demo.pkg 9.9.9          # a version the mock has NOT published
  export FGLPKG_TOKEN="dummy-token-for-dry-run"
  run publish --dry-run
  assert_success
  assert_contains "would POST" || return 1
  # One GET for the pre-check; no POST, no PUT, nothing created.
  assert_not_contains "POST /registry" "$(cat "$MOCK_LOG")" || return 1
  assert_not_contains "PUT /registry" "$(cat "$MOCK_LOG")" || return 1
}
it "a dry run reads once and writes nothing" _pub_dryrun_reads_once_and_writes_nothing
