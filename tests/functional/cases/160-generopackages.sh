suite "generoPackages (pack records PACKAGE namespaces)"

# _gp_extract <zip> <member> <dest> — extract one zip member to a file.
# python3 is always available in the harness (used by the assertions).
_gp_extract() {
  python3 - "$1" "$2" "$3" <<'PY'
import zipfile, sys
open(sys.argv[3], "wb").write(zipfile.ZipFile(sys.argv[1]).read(sys.argv[2]))
PY
}

# A namespaced library module (PACKAGE com.fourjs.db) plus an out-of-namespace
# program. Only the sibling .4gl is parsed for its PACKAGE — the .42m is a stub,
# which is fine: nothing here executes Genero code.
_gp_mk_namespaced() {
  cat > fglpkg.json <<'EOF'
{ "name":"dbconnection","version":"1.0.0","description":"namespaced demo",
  "genero":">=3.20","license":"MIT","repository":"https://github.com/example/db",
  "author":"fglpkg tests","files":["*.42m"],"programs":["test/TestConnection"] }
EOF
  mkdir -p com/fourjs/db test
  printf 'PACKAGE com.fourjs.db\nFUNCTION connect() END FUNCTION\n' > com/fourjs/db/DbConnection.4gl
  printf 'stub-pcode' > com/fourjs/db/DbConnection.42m
  printf 'IMPORT FGL com.fourjs.db.DbConnection\nMAIN\nEND MAIN\n' > test/TestConnection.4gl
  printf 'stub-pcode' > test/TestConnection.42m
}

_gp_records_namespace() {
  _gp_mk_namespaced
  run pack -o out.zip
  assert_success
  _gp_extract out.zip fglpkg.json mf.json
  local body; body="$(cat mf.json)"
  assert_contains "generoPackages" "$body"
  assert_contains "com.fourjs.db" "$body"
}
it "pack records generoPackages from a PACKAGE declaration" _gp_records_namespace

_gp_excludes_program() {
  _gp_mk_namespaced
  run pack -o out.zip; assert_success
  _gp_extract out.zip fglpkg.json mf.json
  # The library namespace is recorded, and it is the ONLY entry — the program
  # (test/TestConnection, no PACKAGE) contributes nothing.
  assert_json_field mf.json generoPackages.0 com.fourjs.db
  run_raw python3 -c 'import json,sys; sys.exit(0 if len(json.load(open("mf.json")).get("generoPackages",[]))==1 else 1)'
  assert_success
}
it "pack excludes out-of-namespace programs from generoPackages" _gp_excludes_program

_gp_flat_records_none() {
  mkpkg   # flat package: mod.42m at the root, no PACKAGE declaration
  run pack -o out.zip; assert_success
  _gp_extract out.zip fglpkg.json mf.json
  local body; body="$(cat mf.json)"
  assert_not_contains "generoPackages" "$body"
}
it "pack records no generoPackages for a flat package" _gp_flat_records_none

# The poiapi shape: sources live in lib/, compiled .42m ship under a namespace
# tree with NO adjacent .4gl. The namespace must still be inferred from the
# shipped layout (root scopes the pack to the compiled tree).
_gp_infers_without_adjacent_source() {
  cat > fglpkg.json <<'EOF'
{ "name":"poiapi","version":"1.0.0","description":"layout-inferred demo",
  "genero":">=3.20","license":"MIT","repository":"https://github.com/example/poi",
  "author":"fglpkg tests","root":"com/fourjs/poiapi" }
EOF
  mkdir -p com/fourjs/poiapi lib
  printf 'PACKAGE com.fourjs.poiapi\nFUNCTION e() END FUNCTION\n' > lib/fgl_excel.4gl   # source elsewhere
  printf 'stub-pcode' > com/fourjs/poiapi/fgl_excel.42m                                 # shipped, no adjacent .4gl
  run pack -o out.zip
  assert_success
  assert_not_contains "treated as flat"          # the removed spurious warning must not return
  _gp_extract out.zip fglpkg.json mf.json
  assert_json_field mf.json generoPackages.0 com.fourjs.poiapi
}
it "pack infers generoPackages from layout when source is not adjacent" _gp_infers_without_adjacent_source
