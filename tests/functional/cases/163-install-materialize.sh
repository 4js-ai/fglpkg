suite "install materializes the merged root (mock registry)"

# _im_fixtures <dir> — build a fixtures dir serving one namespaced package whose
# name (dbconnection) differs from its PACKAGE (com.fourjs.db), so a correct
# install must place the module by its namespace path, not the store-dir name.
_im_fixtures() {
  local dir="$1"; mkdir -p "$dir"
  local build; build="$(mktemp -d "$_SANDBOX_ROOT/imbuild.XXXXXX")"
  (
    cd "$build"
    cat > fglpkg.json <<'EOF'
{ "name":"dbconnection","version":"1.0.0","description":"namespaced demo",
  "genero":">=3.20","license":"MIT","repository":"https://github.com/example/db",
  "author":"fglpkg tests","files":["*.42m"] }
EOF
    mkdir -p com/fourjs/db
    printf 'PACKAGE com.fourjs.db\nFUNCTION connect() END FUNCTION\n' > com/fourjs/db/DbConnection.4gl
    printf 'stub-pcode' > com/fourjs/db/DbConnection.42m
    "$FGLPKG" pack -o "$dir/dbconnection-1.0.0-genero6.zip" </dev/null >/dev/null 2>&1 || exit 1
  ) || { _diag "_im_fixtures: pack failed"; return 1; }
  cat > "$dir/packages.json" <<'EOF'
{ "packages": [ {
  "slug":"dbconnection","name":"dbconnection","description":"namespaced demo","genero":">=3.20",
  "owner":{"partner_id":"mock","name":"fglpkg tests"},
  "versions":[ { "version":"1.0.0","genero":">=3.20","author":"fglpkg tests","license":"MIT",
    "artifacts":[ { "variant":"genero6","zip":"dbconnection-1.0.0-genero6.zip" } ] } ] } ] }
EOF
}

_im_install() {
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/imfx.XXXXXX")"
  _im_fixtures "$fx" || return 1
  mock_registry_start "$fx"
  run install dbconnection@1.0.0
  assert_success
  assert_dir ".fglpkg/packages/dbconnection"
  # Materialized by namespace path — this is the GIS-358 fix (name != PACKAGE).
  assert_file ".fglpkg/merged/com/fourjs/db/DbConnection.42m"
  # Ownership recorded in the lock (Phase 2/4).
  assert_file_contains "fglpkg.lock" "generoPackages"
  assert_file_contains "fglpkg.lock" "com.fourjs.db"
  assert_file_contains "fglpkg.lock" "materialized"
}
it "install materializes namespaced modules and records ownership in the lock" _im_install

_im_remove_clears() {
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/imfx.XXXXXX")"
  _im_fixtures "$fx" || return 1
  mock_registry_start "$fx"
  run install dbconnection@1.0.0; assert_success
  assert_file ".fglpkg/merged/com/fourjs/db/DbConnection.42m"
  run remove dbconnection; assert_success
  assert_no_file ".fglpkg/merged/com/fourjs/db/DbConnection.42m"
}
it "remove rebuilds the merged root without the removed package" _im_remove_clears
