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

# _im_clash_fixtures <dir> — two packages (alpha, beta) that BOTH declare the same
# PACKAGE namespace com.dup, each shipping its module under com/dup/. Installing
# the second must be a hard error (GIS-359): a namespace can have only one owner.
_im_clash_fixtures() {
  local dir="$1"; mkdir -p "$dir"
  local p build
  for p in alpha beta; do
    build="$(mktemp -d "$_SANDBOX_ROOT/imclash.XXXXXX")"
    (
      cd "$build"
      cat > fglpkg.json <<EOF
{ "name":"$p","version":"1.0.0","description":"clash demo","genero":">=3.20",
  "license":"MIT","repository":"https://github.com/example/$p","author":"fglpkg tests","files":["*.42m"] }
EOF
      mkdir -p com/dup
      printf 'PACKAGE com.dup\nFUNCTION f() END FUNCTION\n' > "com/dup/$p.4gl"
      printf 'stub-pcode' > "com/dup/$p.42m"
      "$FGLPKG" pack -o "$dir/$p-1.0.0-genero6.zip" </dev/null >/dev/null 2>&1 || exit 1
    ) || { _diag "_im_clash_fixtures: pack $p failed"; return 1; }
  done
  cat > "$dir/packages.json" <<'EOF'
{ "packages": [
  { "slug":"alpha","name":"alpha","description":"clash demo","genero":">=3.20",
    "owner":{"partner_id":"mock","name":"fglpkg tests"},
    "versions":[ { "version":"1.0.0","genero":">=3.20","author":"fglpkg tests","license":"MIT",
      "artifacts":[ { "variant":"genero6","zip":"alpha-1.0.0-genero6.zip" } ] } ] },
  { "slug":"beta","name":"beta","description":"clash demo","genero":">=3.20",
    "owner":{"partner_id":"mock","name":"fglpkg tests"},
    "versions":[ { "version":"1.0.0","genero":">=3.20","author":"fglpkg tests","license":"MIT",
      "artifacts":[ { "variant":"genero6","zip":"beta-1.0.0-genero6.zip" } ] } ] } ] }
EOF
}

_im_install_clash_aborts() {
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/imfx.XXXXXX")"
  _im_clash_fixtures "$fx" || return 1
  mock_registry_start "$fx"
  run install alpha@1.0.0; assert_success
  assert_file ".fglpkg/merged/com/dup/alpha.42m"
  # Adding beta (same namespace) makes the merged-root rebuild clash — a hard error.
  run install beta@1.0.0
  assert_failure
  assert_contains "both declare the Genero PACKAGE namespace"
  assert_contains "com.dup"
  assert_contains "Refusing to merge: a namespace can be owned by only one package."
  assert_contains "Remove or rename one of the two so the namespace is unique."
  # The clash is detected before the merged root is torn down, so alpha's
  # already-materialized module survives the aborted install.
  assert_file ".fglpkg/merged/com/dup/alpha.42m"
}
it "install aborts on a same-namespace clash and leaves the merged root intact" _im_install_clash_aborts

# _im_two_ns_fixtures <dir> — two packages in DISTINCT namespaces (no clash):
# dbconnection (com.fourjs.db) and strutils (org.util).
_im_two_ns_fixtures() {
  local dir="$1"; mkdir -p "$dir"
  local build
  build="$(mktemp -d "$_SANDBOX_ROOT/imdb.XXXXXX")"
  ( cd "$build"
    cat > fglpkg.json <<'EOF'
{ "name":"dbconnection","version":"1.0.0","description":"db","genero":">=3.20",
  "license":"MIT","repository":"https://github.com/example/db","author":"fglpkg tests","files":["*.42m"] }
EOF
    mkdir -p com/fourjs/db
    printf 'PACKAGE com.fourjs.db\nFUNCTION connect() END FUNCTION\n' > com/fourjs/db/DbConnection.4gl
    printf 'stub-pcode' > com/fourjs/db/DbConnection.42m
    "$FGLPKG" pack -o "$dir/dbconnection-1.0.0-genero6.zip" </dev/null >/dev/null 2>&1 || exit 1
  ) || { _diag "_im_two_ns_fixtures: pack dbconnection failed"; return 1; }
  build="$(mktemp -d "$_SANDBOX_ROOT/imutil.XXXXXX")"
  ( cd "$build"
    cat > fglpkg.json <<'EOF'
{ "name":"strutils","version":"1.0.0","description":"util","genero":">=3.20",
  "license":"MIT","repository":"https://github.com/example/util","author":"fglpkg tests","files":["*.42m"] }
EOF
    mkdir -p org/util
    printf 'PACKAGE org.util\nFUNCTION upper() END FUNCTION\n' > org/util/Strings.4gl
    printf 'stub-pcode' > org/util/Strings.42m
    "$FGLPKG" pack -o "$dir/strutils-1.0.0-genero6.zip" </dev/null >/dev/null 2>&1 || exit 1
  ) || { _diag "_im_two_ns_fixtures: pack strutils failed"; return 1; }
  cat > "$dir/packages.json" <<'EOF'
{ "packages": [
  { "slug":"dbconnection","name":"dbconnection","description":"db","genero":">=3.20",
    "owner":{"partner_id":"mock","name":"fglpkg tests"},
    "versions":[ { "version":"1.0.0","genero":">=3.20","author":"fglpkg tests","license":"MIT",
      "artifacts":[ { "variant":"genero6","zip":"dbconnection-1.0.0-genero6.zip" } ] } ] },
  { "slug":"strutils","name":"strutils","description":"util","genero":">=3.20",
    "owner":{"partner_id":"mock","name":"fglpkg tests"},
    "versions":[ { "version":"1.0.0","genero":">=3.20","author":"fglpkg tests","license":"MIT",
      "artifacts":[ { "variant":"genero6","zip":"strutils-1.0.0-genero6.zip" } ] } ] } ] }
EOF
}

_im_remove_keeps_survivors() {
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/imfx.XXXXXX")"
  _im_two_ns_fixtures "$fx" || return 1
  mock_registry_start "$fx"
  run install dbconnection@1.0.0; assert_success
  run install strutils@1.0.0;    assert_success
  assert_file ".fglpkg/merged/com/fourjs/db/DbConnection.42m"
  assert_file ".fglpkg/merged/org/util/Strings.42m"
  run remove dbconnection; assert_success
  # The removed package's module is gone; the surviving package's is re-linked,
  # not wiped (the merged root is cleared and rebuilt from every surviving store).
  assert_no_file ".fglpkg/merged/com/fourjs/db/DbConnection.42m"
  assert_file ".fglpkg/merged/org/util/Strings.42m"
}
it "remove rebuilds the merged root keeping a surviving package's modules" _im_remove_keeps_survivors
