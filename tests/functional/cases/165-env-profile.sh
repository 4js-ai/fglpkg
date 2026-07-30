suite "env (FGLPROFILE)"

# The default `files` globs are *.42m/*.42f/*.sch, so a profile can only reach
# the archive because a declared `profile` is force-staged.
_ep_pack_ships_profile() {
  mkpkg "profile.demo" "1.0.0"
  mkdir -p profiles
  printf 'gwc.server.name = "demo"\n' > profiles/app.4gp
  python3 - <<'PY'
import json
m = json.load(open("fglpkg.json"))
m["files"] = ["*.42m"]
m["profile"] = ["profiles/app.4gp"]
json.dump(m, open("fglpkg.json", "w"), indent=2)
PY
  printf 'P' > Module.42m
  run pack
  assert_success
  run_raw python3 -c "import zipfile,glob,sys; z=zipfile.ZipFile(glob.glob('*.zip')[0]); print('\n'.join(z.namelist()))"
  assert_success
  assert_contains_path "profiles/app.4gp"
}
it "pack ships a declared profile the files globs do not match" _ep_pack_ships_profile

# An installed package: the store dir plus a manifest whose `profile` is already
# archive-relative (what pack writes).
_ep_installed_profile() {
  mkdir -p ".fglpkg/packages/poiapi/profiles"
  cat > ".fglpkg/packages/poiapi/fglpkg.json" <<'EOF'
{ "name":"poiapi","version":"1.0.0","profile":["profiles/app.4gp"],"dependencies":{"fgl":{}} }
EOF
  printf 'gwc.server.name = "demo"\n' > ".fglpkg/packages/poiapi/profiles/app.4gp"
}

_ep_emits_file_path() {
  _ep_installed_profile
  run env --local
  assert_success
  assert_contains "FGLPROFILE"
  # FGLPROFILE is a list of FILES, not directories.
  assert_contains_path "packages/poiapi/profiles/app.4gp"
}
it "env emits FGLPROFILE as a full file path" _ep_emits_file_path

_ep_omits_missing_profile() {
  mkdir -p ".fglpkg/packages/poiapi"
  cat > ".fglpkg/packages/poiapi/fglpkg.json" <<'EOF'
{ "name":"poiapi","version":"1.0.0","profile":["profiles/gone.4gp"],"dependencies":{"fgl":{}} }
EOF
  run_split env --local
  assert_success
  assert_not_contains "FGLPROFILE" "$out"   # never a dangling path
  assert_contains "gone.4gp" "$err"         # but say why
}
it "env omits FGLPROFILE when the declared file is not installed" _ep_omits_missing_profile

# FGLPROFILE entries are applied left to right with the LAST winning, so the
# package's files must come first for the user's own profile to still override.
_ep_user_value_wins() {
  _ep_installed_profile
  run env --local
  assert_success
  if env_output_is_windows_style "$output"; then
    skip "FGLPROFILE lists package files before the user's existing value" "output is cmd.exe SET syntax"
    return 0
  fi
  export FGLPROFILE="/tmp/mine.4gp"
  eval "$("$FGLPKG" env --local 2>/dev/null)"
  assert_contains_path "packages/poiapi/profiles/app.4gp" "$FGLPROFILE"
  assert_contains "/tmp/mine.4gp" "$FGLPROFILE"
  local pkg_at user_at
  pkg_at="${FGLPROFILE%%packages/poiapi*}"
  user_at="${FGLPROFILE%%/tmp/mine.4gp*}"
  [[ ${#pkg_at} -lt ${#user_at} ]] \
    || { _diag "package profile must precede the user's: $FGLPROFILE"; return 1; }
}
it "FGLPROFILE lists package files before the user's existing value" _ep_user_value_wins
