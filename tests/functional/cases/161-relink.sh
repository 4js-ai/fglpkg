suite "relink (rebuild merged FGLLDPATH root)"

# _rl_store <pkg> <namespace> <relpath.42m> — write an installed store under the
# local .fglpkg/packages with a manifest that records generoPackages plus one
# module at the given namespace path.
_rl_store() {
  local pkg="$1" ns="$2" rel="$3" base
  base=".fglpkg/packages/$pkg"
  mkdir -p "$base/$(dirname "$rel")"
  cat > "$base/fglpkg.json" <<EOF
{ "name":"$pkg","version":"1.0.0","generoPackages":["$ns"],"dependencies":{"fgl":{}} }
EOF
  printf 'PCODE' > "$base/$rel"
}

_rl_builds() {
  _rl_store dbconnection com.fourjs.db com/fourjs/db/DbConnection.42m
  run relink --local
  assert_success
  assert_file ".fglpkg/merged/com/fourjs/db/DbConnection.42m"
}
it "relink materializes the merged root from installed stores" _rl_builds

_rl_idempotent() {
  _rl_store strutils org.util org/util/Strings.42m
  run relink --local; assert_success
  run relink --local; assert_success
  assert_file ".fglpkg/merged/org/util/Strings.42m"
}
it "relink is idempotent" _rl_idempotent

_rl_clash() {
  _rl_store alpha com.dup com/dup/A.42m
  _rl_store beta com.dup com/dup/B.42m
  run relink --local
  assert_failure
  assert_contains "com.dup"     # the error names the clashing namespace
  assert_contains "alpha"
  assert_contains "beta"
}
it "relink fails when two packages claim the same namespace" _rl_clash

_rl_rejects_args() {
  run relink bogus
  assert_failure
  assert_contains 'relink takes no arguments (got "bogus")'
}
it "relink rejects positional arguments" _rl_rejects_args

_rl_mutually_exclusive() {
  run relink --local --global
  assert_failure
  assert_contains "--local and --global are mutually exclusive"
}
it "relink rejects --local and --global together" _rl_mutually_exclusive

# The one-line summary is user-facing: an empty scope and a populated one print
# distinct, accurate messages.
_rl_summary_empty() {
  run relink --local
  assert_success
  assert_contains "local: no namespaced packages to merge ("
}
it "relink reports an empty scope" _rl_summary_empty

_rl_summary_populated() {
  _rl_store dbconnection com.fourjs.db com/fourjs/db/DbConnection.42m
  run relink --local
  assert_success
  assert_contains "local: linked 1 module(s) from 1 package(s) into"
}
it "relink reports the linked module and package counts" _rl_summary_populated

# A legacy store (manifest without generoPackages, e.g. published before the
# feature) still materializes — namespaces inferred from layout — and relink is
# the one place that surfaces the inference (install/env stay quiet about it).
_rl_legacy_inferred_note() {
  local base=".fglpkg/packages/fgl-log4j/com/fourjs/log4j"
  mkdir -p "$base"
  cat > ".fglpkg/packages/fgl-log4j/fglpkg.json" <<'EOF'
{ "name":"fgl-log4j","version":"1.0.0","dependencies":{"fgl":{}} }
EOF
  printf 'PCODE' > "$base/Log4j.42m"
  run relink --local
  assert_success
  assert_file ".fglpkg/merged/com/fourjs/log4j/Log4j.42m"     # inferred + materialized
  assert_contains "fgl-log4j"
  assert_contains "com.fourjs.log4j"
  assert_contains "inferred"
  assert_contains "republish it with a current fglpkg to record them"   # actionable hint
}
it "relink surfaces inferred namespaces for a legacy package" _rl_legacy_inferred_note
