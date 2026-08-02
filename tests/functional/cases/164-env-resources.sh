suite "env (resource search paths)"

# A package whose forms live under their PACKAGE namespace — the layout the
# merged root produces for modules, and the one a store-root-only
# FGLRESOURCEPATH would fail to resolve.
_er_namespaced_pkg() {
  mkdir -p ".fglpkg/packages/poiapi/com/fourjs/poiapi"
  cat > ".fglpkg/packages/poiapi/fglpkg.json" <<'EOF'
{ "name":"poiapi","version":"1.0.0","dependencies":{"fgl":{}} }
EOF
  printf 'F' > ".fglpkg/packages/poiapi/com/fourjs/poiapi/Customer.42f"
}

_er_resource_leaf_dir() {
  _er_namespaced_pkg
  run env --local
  assert_success
  assert_contains "FGLRESOURCEPATH"
  # The namespace directory itself must be on the path: these variables are
  # searched non-recursively by basename.
  assert_contains_path "packages/poiapi/com/fourjs/poiapi"
}
it "env emits FGLRESOURCEPATH pointing at the directory that holds the form" _er_resource_leaf_dir

_er_dbpath() {
  mkdir -p ".fglpkg/packages/dbkit/schema"
  cat > ".fglpkg/packages/dbkit/fglpkg.json" <<'EOF'
{ "name":"dbkit","version":"1.0.0","dependencies":{"fgl":{}} }
EOF
  printf 'S' > ".fglpkg/packages/dbkit/schema/stores.sch"
  run env --local
  assert_success
  assert_contains "FGLDBPATH"
  assert_contains_path "packages/dbkit/schema"
  assert_not_contains "FGLRESOURCEPATH"   # a .sch is not a program resource
}
it "env emits FGLDBPATH for a shipped .sch" _er_dbpath

_er_images_no_gas_hint() {
  mkdir -p ".fglpkg/packages/icons/img"
  cat > ".fglpkg/packages/icons/fglpkg.json" <<'EOF'
{ "name":"icons","version":"1.0.0","dependencies":{"fgl":{}} }
EOF
  printf 'P' > ".fglpkg/packages/icons/img/logo.png"
  run env --local
  assert_success
  assert_contains "FGLIMAGEPATH"
  assert_contains_path "packages/icons/img"
  # No webcomponents are installed, so there is nothing to tell GAS about.
  assert_not_contains "WEB_COMPONENT_DIRECTORY"
}
it "env emits FGLIMAGEPATH for packaged images and omits the GAS hint" _er_images_no_gas_hint

_er_modules_only() {
  mkdir -p ".fglpkg/packages/dbkit/com/fourjs/db"
  cat > ".fglpkg/packages/dbkit/fglpkg.json" <<'EOF'
{ "name":"dbkit","version":"1.0.0","dependencies":{"fgl":{}} }
EOF
  printf 'P' > ".fglpkg/packages/dbkit/com/fourjs/db/DbConnection.42m"
  run env --local
  assert_success
  assert_contains "FGLLDPATH"
  # Nothing but modules installed → no empty exports for the resource vars.
  assert_not_contains "FGLRESOURCEPATH"
  assert_not_contains "FGLDBPATH"
  assert_not_contains "FGLIMAGEPATH"
  assert_not_contains "FGLPROFILE"
}
it "env emits no resource variables for a modules-only package" _er_modules_only

# The merged root holds only *.42m, so a fully-covered package leaves FGLLDPATH
# but must keep contributing its forms — they exist nowhere else.
_er_merged_store_still_has_resources() {
  mkdir -p ".fglpkg/packages/dbkit/com/fourjs/db" ".fglpkg/merged/com/fourjs/db"
  cat > ".fglpkg/packages/dbkit/fglpkg.json" <<'EOF'
{ "name":"dbkit","version":"1.0.0","generoPackages":["com.fourjs.db"],"dependencies":{"fgl":{}} }
EOF
  printf 'P' > ".fglpkg/packages/dbkit/com/fourjs/db/DbConnection.42m"
  printf 'P' > ".fglpkg/merged/com/fourjs/db/DbConnection.42m"
  printf 'F' > ".fglpkg/packages/dbkit/com/fourjs/db/Customer.42f"
  run env --local
  assert_success
  assert_contains_path ".fglpkg/merged"
  assert_contains "FGLRESOURCEPATH"
  assert_contains_path "packages/dbkit/com/fourjs/db"
}
it "env keeps a materialized package's store dir on FGLRESOURCEPATH" _er_merged_store_still_has_resources

_er_gst() {
  _er_namespaced_pkg
  mkdir -p ".fglpkg/packages/poiapi/schema"
  printf 'S' > ".fglpkg/packages/poiapi/schema/stores.sch"
  run env --gst
  assert_success
  assert_contains 'FGLRESOURCEPATH=$(ProjectDir)/.fglpkg/packages/poiapi/com/fourjs/poiapi;$(FGLRESOURCEPATH)'
  assert_contains 'FGLDBPATH=$(ProjectDir)/.fglpkg/packages/poiapi/schema;$(FGLDBPATH)'
}
it "env --gst emits the resource variables in \$(ProjectDir) form" _er_gst

# Two packages shipping the same basename: first on the path wins, and that has
# to be said out loud (GIS-359) — but on stderr, never on stdout.
_er_collision_fixture() {
  for p in alpha beta; do
    mkdir -p ".fglpkg/packages/$p/forms"
    cat > ".fglpkg/packages/$p/fglpkg.json" <<EOF
{ "name":"$p","version":"1.0.0","dependencies":{"fgl":{}} }
EOF
    printf 'F' > ".fglpkg/packages/$p/forms/Customer.42f"
  done
}

_er_collision_warns_on_stderr() {
  _er_collision_fixture
  run_split env --local
  assert_success                                  # a warning must never fail the command
  assert_contains "warning:"     "$err"
  assert_contains "Customer.42f" "$err"
  assert_contains "alpha"        "$err"
  assert_contains "beta"         "$err"
  # stdout is fed straight to `eval`, so it must carry no diagnostics.
  assert_not_contains "warning" "$out"
}
it "env warns about a basename collision on stderr, never on stdout" _er_collision_warns_on_stderr

_er_gst_has_no_comments() {
  _er_collision_fixture
  mkdir -p ".fglpkg/webcomponents/MyWidget"
  run_split env --gst
  assert_success
  assert_contains "warning:" "$err"
  # Genero Studio parses stdout as a strict VAR=value list: no comments, and no
  # GAS hint even though a webcomponent is installed.
  assert_not_contains "WEB_COMPONENT_DIRECTORY" "$out"
  # Line-anchored: a bare assert_not_contains "#" would also fire on any sandbox
  # path containing a "#", and would not actually test "no comment LINES".
  assert_not_match '^(#|REM )' "$out"
}
it "env --gst keeps stdout comment-free while warning on stderr" _er_gst_has_no_comments

# The end-to-end contract: the output really is safe to eval, and really does
# set the variable.
#
# --shell sh is explicit because this suite runs under Git Bash even on Windows,
# where the DEFAULT output is cmd syntax a POSIX shell cannot eval. Asking for the
# shell we are actually in is what lets this assertion run on every platform
# instead of skipping on Windows.
_er_eval_sets_resourcepath() {
  _er_namespaced_pkg
  run env --local --shell sh
  assert_success
  eval "$("$FGLPKG" env --local --shell sh 2>/dev/null)"
  assert_contains_path "packages/poiapi/com/fourjs/poiapi" "${FGLRESOURCEPATH:-}"
}
it "eval \"\$(fglpkg env --local --shell sh)\" sets FGLRESOURCEPATH" _er_eval_sets_resourcepath

# The quoting contract, end to end: a project under a path with a space in it is
# the case the unquoted format mis-parsed, and it is likelier for the asset
# variables than for a module root.
_er_eval_survives_spaced_path() {
  mkdir -p "space dir"
  cd "space dir" || return 1
  _er_namespaced_pkg
  run env --local --shell sh
  assert_success
  local before="${FGLRESOURCEPATH:-}"
  eval "$("$FGLPKG" env --local --shell sh 2>/dev/null)" \
    || { _diag "eval of quoted output failed"; return 1; }
  assert_contains_path "space dir" "${FGLRESOURCEPATH:-}"
  assert_contains_path "packages/poiapi/com/fourjs/poiapi" "${FGLRESOURCEPATH:-}"
  [[ "${FGLRESOURCEPATH:-}" != "$before" ]] \
    || { _diag "FGLRESOURCEPATH was not set"; return 1; }
  # The quote characters belong to the shell syntax, not to the value.
  assert_not_contains "'" "${FGLRESOURCEPATH:-}"
}
it "eval of env output sets a path containing a space" _er_eval_survives_spaced_path

# --shell selects syntax and nothing else: the paths must be identical whichever
# shell asked for them. Guards against quoting leaking into path collection.
_er_shell_changes_syntax_only() {
  _er_namespaced_pkg
  local sh_out ps_out cmd_out
  sh_out="$("$FGLPKG" env --local --shell sh 2>/dev/null)"
  ps_out="$("$FGLPKG" env --local --shell powershell 2>/dev/null)"
  cmd_out="$("$FGLPKG" env --local --shell cmd 2>/dev/null)"
  assert_match '^export FGLRESOURCEPATH=' "$sh_out"
  assert_match '^\$env:FGLRESOURCEPATH = ' "$ps_out"
  assert_match '^SET "?FGLRESOURCEPATH=' "$cmd_out"
  local p="packages/poiapi/com/fourjs/poiapi"
  assert_contains_path "$p" "$sh_out"
  assert_contains_path "$p" "$ps_out"
  assert_contains_path "$p" "$cmd_out"
}
it "env --shell changes syntax without changing the paths" _er_shell_changes_syntax_only

# --shell is an output-format selector, like --gst and --gwa; combining two of
# them has no meaning and must not be silently ignored.
_er_shell_rejects_bad_values() {
  _er_namespaced_pkg
  run env --local --shell tcsh
  assert_failure
  assert_contains "invalid --shell"
  assert_contains "powershell"   # the error names the accepted values
  run env --gst --shell sh
  assert_failure
  assert_contains "--shell does not apply to --gst"
  # --gst alone must keep working: the conflict check gates on an EXPLICIT
  # --shell, not on the always-present default.
  run env --gst
  assert_success
}
it "env rejects an unknown --shell and --shell with --gst" _er_shell_rejects_bad_values

# One package shipping the same basename from two directories: the first wins and
# the second is unreachable — said out loud on stderr, never on stdout.
_er_same_package_two_dirs() {
  mkdir -p ".fglpkg/packages/solo/formsA" ".fglpkg/packages/solo/formsB"
  cat > ".fglpkg/packages/solo/fglpkg.json" <<'EOF'
{ "name":"solo","version":"1.0.0","dependencies":{"fgl":{}} }
EOF
  printf 'F' > ".fglpkg/packages/solo/formsA/Customer.42f"
  printf 'F' > ".fglpkg/packages/solo/formsB/Customer.42f"
  run_split env --local
  assert_success
  assert_contains 'package "solo" ships "Customer.42f" in two directories' "$err"
  assert_contains "the second is unreachable" "$err"
  assert_not_contains "warning" "$out"
}
it "env warns when one package ships a basename in two directories" _er_same_package_two_dirs

# The cross-package collision message must name the winner, the shadowed copy, and
# the remediation (not merely that a collision happened) — strengthening the
# _er_collision_warns_on_stderr coverage above.
_er_collision_names_winner_and_remedy() {
  _er_collision_fixture
  run_split env --local
  assert_success
  assert_contains 'is shipped by both "alpha" (local) and "beta" (local)' "$err"
  assert_contains "first match wins" "$err"
  assert_contains "beta's is shadowed" "$err"
  assert_contains "set FGLRESOURCEPATH yourself to pick a winner" "$err"
  assert_not_contains "warning" "$out"
}
it "env collision warning names the winner, the shadowed copy, and the remedy" _er_collision_names_winner_and_remedy
