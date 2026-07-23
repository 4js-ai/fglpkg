suite "workspace"

# NOTE: the created file is fglpkg.workspace.json (the `workspace --help` text
# says "fglpkg-workspace.json" — a doc discrepancy in fglpkg 4.0.4).
_ws_init() {
  run workspace init
  assert_success
  assert_file fglpkg.workspace.json
  assert_json fglpkg.workspace.json
}
it "workspace init creates fglpkg.workspace.json" _ws_init

_ws_alias() {
  run ws init
  assert_success
  assert_file fglpkg.workspace.json
}
it "ws is an alias for workspace" _ws_alias

# create a member project dir $1 with a valid manifest named $2
_mk_member() {
  mkdir -p "$1"
  cat > "$1/fglpkg.json" <<EOF
{ "name":"$2","version":"1.0.0","description":"member","genero":">=3.20","license":"MIT" }
EOF
}

_ws_add_list() {
  run workspace init; assert_success
  _mk_member pkgs/a member.a
  run workspace add pkgs/a; assert_success
  run workspace list; assert_success; assert_contains "member.a"
}
it "workspace add + list shows the member" _ws_add_list
