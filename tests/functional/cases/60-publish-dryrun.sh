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

# A valid manifest with a token set should complete the dry-run "without calling
# out" (per the command help). FGLPKG_TOKEN is a dummy — no real request is made.
_pub_valid() {
  mkpkg
  export FGLPKG_TOKEN="dummy-token-for-dry-run"
  run publish --dry-run
  assert_success
  assert_match "dry.?run|would|DRY"
}
it "publish --dry-run (valid manifest + token) succeeds offline" _pub_valid
