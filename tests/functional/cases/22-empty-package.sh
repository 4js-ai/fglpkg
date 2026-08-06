suite "empty-package guard (GIS-276)"

# An asset-less package — one whose staged archive holds nothing but fglpkg.json
# and files matched solely by `docs` — is flagged by pack and refused by publish
# unless --allow-empty. Any real content (BDL, bin, webcomponents, include)
# counts as an asset and passes.

# _empty_pkg writes a well-formed, publishable manifest whose `files` glob
# matches nothing, plus a README that only `docs` picks up. Result: empty.
_empty_pkg() {
  cat > fglpkg.json <<'EOF'
{
  "name": "empty.demo",
  "version": "1.0.0",
  "description": "demo package for functional tests",
  "genero": ">=3.20",
  "license": "MIT",
  "repository": "https://github.com/example/demo",
  "author": "fglpkg functional tests",
  "files": ["*.42m"],
  "docs": ["README.md"]
}
EOF
  printf '# readme\n' > README.md
}

_pack_flags_empty() {
  _empty_pkg
  run pack --list
  assert_success
  assert_contains "no assets"
}
it "pack --list flags an asset-less package" _pack_flags_empty

_publish_refuses_empty() {
  _empty_pkg
  export FGLPKG_TOKEN="dummy-token-for-dry-run"
  run publish --dry-run
  assert_failure
  assert_contains "no assets"
  assert_contains "--allow-empty"
}
it "publish refuses an asset-less package" _publish_refuses_empty

# --allow-empty suppresses the guard: publish proceeds past it and completes the
# dry-run. Uses the mock registry so the variant pre-check (which runs before the
# dry-run branch) stays hermetic rather than reaching the real GI. "refusing to
# publish" is unique to the guard's refusal (the --allow-empty warning also says
# "no assets"), so its absence proves the guard was bypassed.
_publish_allow_empty_overrides() {
  _empty_pkg
  mock_registry_start                  # sets FGLPKG_REGISTRY + FGLPKG_TOKEN
  run publish --dry-run --allow-empty
  assert_success
  assert_not_contains "refusing to publish"
  assert_match "dry.?run|would|DRY"
}
it "publish --allow-empty overrides the guard" _publish_allow_empty_overrides

# A pure-webcomponent package (html/js only, no BDL source) is NOT empty.
_wc_not_empty() {
  mkdir -p webcomponents/Chart
  printf '<html></html>\n' > webcomponents/Chart/Chart.html
  printf 'console.log(1)\n' > webcomponents/Chart/Chart.js
  cat > fglpkg.json <<'EOF'
{
  "name": "wc.demo",
  "version": "1.0.0",
  "description": "demo package for functional tests",
  "genero": ">=3.20",
  "license": "MIT",
  "repository": "https://github.com/example/demo",
  "author": "fglpkg functional tests",
  "webcomponents": ["Chart"]
}
EOF
  run pack --list
  assert_success
  assert_not_contains "no assets"
  assert_contains "Chart/Chart.html"
}
it "a pure-webcomponent package is not flagged empty" _wc_not_empty

# A package with BDL source in the tree that stages NONE of it (wrong root/files)
# is blocked, even though a bin script keeps it non-empty — the empty guard alone
# would miss this, so the dropped-BDL-source check must catch it (GIS-276 review).
_dropped_bdl_blocked() {
  mkdir -p src
  printf 'MAIN\nEND MAIN\n' > src/Main.4gl    # source, uncompiled, and root is never set
  printf '#!/bin/sh\necho hi\n' > deploy.sh
  cat > fglpkg.json <<'EOF'
{
  "name": "dropped.demo",
  "version": "1.0.0",
  "description": "demo package for functional tests",
  "genero": ">=3.20",
  "license": "MIT",
  "repository": "https://github.com/example/demo",
  "author": "fglpkg functional tests",
  "bin": { "deploy": "deploy.sh" }
}
EOF
  run pack --list
  assert_failure
  assert_contains "BDL source"
}
it "a package that drops all its BDL source is blocked" _dropped_bdl_blocked

# A bin-only package (shell script, not BDL source) is NOT empty — the case the
# old BDL-only "no modules" error got wrong.
_bin_not_empty() {
  printf '#!/bin/sh\necho hi\n' > deploy.sh
  cat > fglpkg.json <<'EOF'
{
  "name": "bin.demo",
  "version": "1.0.0",
  "description": "demo package for functional tests",
  "genero": ">=3.20",
  "license": "MIT",
  "repository": "https://github.com/example/demo",
  "author": "fglpkg functional tests",
  "bin": { "deploy": "deploy.sh" },
  "docs": ["README.md"]
}
EOF
  printf '# readme\n' > README.md
  run pack --list
  assert_success
  assert_not_contains "no assets"
}
it "a bin-only package is not flagged empty" _bin_not_empty
