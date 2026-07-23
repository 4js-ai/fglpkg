suite "publish --ci (mock registry)"

# A fresh slug (not in the fixtures) → the step-0 precheck GET returns 404, so the
# full publish sequence (POST package/version, PUT artifact, POST submit) runs.
_publish_ci() {
  mock_registry_start                  # sets FGLPKG_TOKEN (required by --ci)
  cat > fglpkg.json <<'EOF'
{ "name":"fresh.pkg","version":"2.0.0","description":"Fresh package","genero":">=3.20",
  "license":"MIT","repository":"https://github.com/example/fresh","author":"tests","files":["*.42m"] }
EOF
  if command -v fglcomp >/dev/null 2>&1; then
    printf 'FUNCTION g()\nEND FUNCTION\n' > g.4gl; ( FGLLDPATH= fglcomp g.4gl ) >/dev/null 2>&1 || printf 'stub' > g.42m
  else
    printf 'stub' > g.42m
  fi
  run publish --ci
  assert_success
  assert_contains "fglpkg-published"
  assert_contains "status=pending"
}
it "publish --ci runs the full submit sequence against the registry" _publish_ci

# Missing FGLPKG_TOKEN → --ci aborts before any network call.
_publish_ci_no_token() {
  mock_registry_start
  unset FGLPKG_TOKEN
  mkpkg fresh.pkg 2.0.0
  run publish --ci
  assert_failure
}
it "publish --ci without a token fails fast" _publish_ci_no_token
