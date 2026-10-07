suite "outdated sees installed web components (GIS-582)"

# `outdated` built its "what is installed" map from the lock file's `packages`
# array alone. A web component is locked in the separate `webcomponents` array,
# so every installed widget was reported as missing / not installed and the
# command exited 1 — in a project where `install` had just succeeded.
#
# The help documents `outdated` as a CI gate, so in any project depending on a
# widget the gate always failed, and for a wrong reason. These cases install a
# real web component rather than fabricating a lock, so they also pin that what
# `install` writes is what `outdated` reads.

_wcout_fixtures() {  # _wcout_fixtures <dir>
  local dir="$1"; mkdir -p "$dir"
  local build; build="$(mktemp -d "$_SANDBOX_ROOT/wcoutb.XXXXXX")"
  (
    cd "$build"
    mkdir -p webcomponents/Chart
    printf '<html></html>\n' > webcomponents/Chart/Chart.html
    printf 'console.log(1)\n' > webcomponents/Chart/Chart.js
    cat > fglpkg.json <<'EOF'
{ "name":"wcfx","version":"1.0.0","description":"Web component fixture",
  "license":"MIT","author":"fglpkg tests","webcomponents":["Chart"] }
EOF
    "$FGLPKG" pack -o "$dir/wcfx-1.0.0-webcomponent.zip" </dev/null >/dev/null 2>&1 || exit 1
    sed 's/"version":"1.0.0"/"version":"1.2.0"/' fglpkg.json > f2 && mv f2 fglpkg.json
    "$FGLPKG" pack -o "$dir/wcfx-1.2.0-webcomponent.zip" </dev/null >/dev/null 2>&1 || exit 1
  ) || return 1
  cat > "$dir/packages.json" <<'EOF'
{
  "packages": [
    {
      "slug": "wcfx",
      "name": "wcfx",
      "description": "Web component fixture",
      "owner": { "partner_id": "mock", "name": "fglpkg tests" },
      "versions": [
        { "version":"1.0.0", "author":"fglpkg tests", "license":"MIT",
          "artifacts":[ { "variant":"webcomponent", "zip":"wcfx-1.0.0-webcomponent.zip" } ] },
        { "version":"1.2.0", "author":"fglpkg tests", "license":"MIT",
          "artifacts":[ { "variant":"webcomponent", "zip":"wcfx-1.2.0-webcomponent.zip" } ] }
      ]
    }
  ]
}
EOF
}

_wcout_env() {
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/wcout.XXXXXX")"
  _wcout_fixtures "$fx" || { _diag "could not build fixtures"; return 1; }
  mock_registry_start "$fx"
}

# The gate: a widget already at the newest version is "ok" and exits 0. This is
# the reported failure — it used to exit 1 with "not installed".
_wcout_gate_passes_when_current() {
  _wcout_env || return 1
  run install wcfx@1.2.0
  assert_success || return 1
  # Confirm the premise: install really did lock it as a web component.
  assert_file_contains "fglpkg-lock.json" '"webcomponents"' || return 1

  run outdated
  assert_success || return 1
  assert_not_contains "not installed" || return 1
  assert_not_contains "missing" || return 1
  assert_contains "1.2.0" || return 1
}
it "outdated exits 0 for an up-to-date web component" \
  _wcout_gate_passes_when_current

# A widget that really is behind reports its real current version, so the
# non-zero exit carries the right reason.
_wcout_reports_real_current_version() {
  _wcout_env || return 1
  run install wcfx@1.0.0
  assert_success || return 1

  run outdated
  assert_failure || return 1
  assert_not_contains "not installed" || return 1
  assert_not_contains "missing" || return 1
  # Current 1.0.0, latest 1.2.0 — the exact pin install wrote puts the upgrade
  # outside the constraint, so this is "major available" rather than "update".
  assert_match "wcfx +1\.0\.0 .*1\.2\.0" || return 1
}
it "outdated reports a web component's real installed version" \
  _wcout_reports_real_current_version

# --json carries the same corrected Current, so a machine consumer of the gate
# sees the version rather than "missing".
_wcout_json_has_current() {
  _wcout_env || return 1
  run install wcfx@1.0.0
  assert_success || return 1
  run outdated --json
  assert_failure || return 1
  assert_contains '"current": "1.0.0"' || return 1
}
it "outdated --json reports a web component's installed version" \
  _wcout_json_has_current

# A project mixing a BDL library and a widget must report both. The two live in
# different lock arrays, so reading one and not the other is exactly the bug.
_wcout_mixed_project() {
  _wcout_env || return 1
  run install wcfx@1.2.0
  assert_success || return 1
  run outdated
  assert_success || return 1
  assert_contains "wcfx" || return 1
}
it "outdated covers a web component alongside the rest of the project" \
  _wcout_mixed_project
