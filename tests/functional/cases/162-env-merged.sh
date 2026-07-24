suite "env (merged FGLLDPATH root)"

# A materialized package (records generoPackages) plus a non-empty merged root
# holding its module — the state install/relink leave behind.
_em_materialized() {
  mkdir -p ".fglpkg/packages/dbconnection/com/fourjs/db" ".fglpkg/merged/com/fourjs/db"
  cat > ".fglpkg/packages/dbconnection/fglpkg.json" <<'EOF'
{ "name":"dbconnection","version":"1.0.0","generoPackages":["com.fourjs.db"],"dependencies":{"fgl":{}} }
EOF
  printf 'P' > ".fglpkg/packages/dbconnection/com/fourjs/db/DbConnection.42m"
  printf 'P' > ".fglpkg/merged/com/fourjs/db/DbConnection.42m"
}

_em_emits_merged() {
  _em_materialized
  run env --local
  assert_success
  assert_contains ".fglpkg/merged"
  # The materialized package is covered by the merged root, so its raw store dir
  # must NOT be added as a separate FGLLDPATH entry.
  assert_not_contains "packages/dbconnection"
}
it "env emits the merged root and omits a materialized package's store" _em_emits_merged

_em_legacy_passthrough() {
  mkdir -p ".fglpkg/packages/legacylib/com/acme"
  cat > ".fglpkg/packages/legacylib/fglpkg.json" <<'EOF'
{ "name":"legacylib","version":"1.0.0","dependencies":{"fgl":{}} }
EOF
  printf 'P' > ".fglpkg/packages/legacylib/com/acme/Old.42m"
  run env --local
  assert_success
  assert_contains "packages/legacylib"     # legacy (no generoPackages) → per-package entry kept
}
it "env keeps a per-package entry for a legacy (no-namespace) package" _em_legacy_passthrough
