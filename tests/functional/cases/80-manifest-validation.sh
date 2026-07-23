suite "manifest validation"

_malformed_json() {
  printf '{ this is not json ' > fglpkg.json
  run pack --list
  assert_failure
}
it "malformed fglpkg.json is rejected" _malformed_json

_unknown_field() {
  cat > fglpkg.json <<'EOF'
{ "name":"demo.pkg", "version":"1.0.0", "genero":">=3.20", "totallyBogusField": true }
EOF
  run pack --list
  assert_failure
}
it "unknown manifest field is rejected (strict decode)" _unknown_field

# NOTE: `pack` does NOT validate the version string (it will happily pack
# "@not-a-semver"); `publish` validation does. So the bad-version check targets
# the publish surface, which reports it before any auth/network step.
_bad_version() {
  cat > fglpkg.json <<'EOF'
{ "name":"demo.pkg", "version":"not-a-semver", "description":"d", "genero":">=3.20",
  "license":"MIT", "repository":"https://github.com/x/y", "author":"Mike", "files":["*.42m"] }
EOF
  printf 'stub' > mod.42m
  run publish --dry-run
  assert_failure
  assert_contains "not valid semver"
}
it "an invalid version string is rejected at publish validation" _bad_version
