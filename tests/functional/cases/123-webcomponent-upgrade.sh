suite "upgrading an installed web component (GIS-579)"

# `fglpkg install <widget>@<newver>` over an installed version fails whenever
# the package ships files OUTSIDE its own COMPONENTTYPE directory — a BDL
# wrapper, docs, examples. extractWebcomponentZip's pass 1 flags every existing
# non-owned file whose content differs, without ever consulting
# webcomponent-owners.json, so the package's OWN files from the previous
# version are indistinguishable from another package's.
#
# The failure then leaves the project inconsistent: fglpkg.json and the lock
# already record the new version, the disk still holds the old one, and the
# normal replay commands never notice.
#
# The fixture mirrors fjs-map: a webcomponent-only artifact (no BDL content, so
# `pack` tags it "webcomponent") whose zip carries both the declared
# COMPONENTTYPE dir and a non-owned top-level tree.
#
# Note the explicit `|| return 1` on each assertion: `it()` runs the body in a
# subshell inside an `if`, so bash suppresses errexit and a bare failing
# assertion mid-body would be ignored (see the note in case 113).

_wcup_fixtures() {  # _wcup_fixtures <dir>
  local dir="$1"; mkdir -p "$dir"
  mock_build_fixtures "$dir" || return 1

  local build; build="$(mktemp -d "$_SANDBOX_ROOT/wcup.XXXXXX")"
  (
    cd "$build"
    mkdir -p webcomponents/Map com/acme
    printf '<html>v1</html>\n' > webcomponents/Map/Map.html
    printf 'FUNCTION mapInit()\n  -- v1\nEND FUNCTION\n' > com/acme/Map.4gl
    cat > fglpkg.json <<'EOF'
{ "name":"fx-map","version":"1.0.0","description":"Widget with a BDL wrapper",
  "genero":">=3.20","license":"MIT","author":"fglpkg tests",
  "webcomponents":["Map"], "docs":["com/**/*.4gl"] }
EOF
    "$FGLPKG" pack -o "$dir/fx-map-1.0.0-webcomponent.zip" </dev/null >/dev/null 2>&1 || exit 1

    # 1.2.0 changes BOTH the component and the shared wrapper. The wrapper is
    # what pass 1 sees as a clash.
    printf '<html>v2</html>\n' > webcomponents/Map/Map.html
    printf 'FUNCTION mapInit()\n  -- v2, different\nEND FUNCTION\n' > com/acme/Map.4gl
    sed 's/"version":"1.0.0"/"version":"1.2.0"/' fglpkg.json > f2 && mv f2 fglpkg.json
    "$FGLPKG" pack -o "$dir/fx-map-1.2.0-webcomponent.zip" </dev/null >/dev/null 2>&1 || exit 1
  ) || { _diag "_wcup_fixtures: pack failed"; return 1; }

  python3 - "$dir/packages.json" <<'PY' || return 1
import json, sys
path = sys.argv[1]
with open(path) as fh:
    doc = json.load(fh)
doc["packages"].append({
    "slug": "fx-map", "name": "fx-map",
    "description": "Widget with a BDL wrapper",
    "genero": ">=3.20",
    "owner": {"partner_id": "mock", "name": "fglpkg tests"},
    "versions": [
        {"version": v, "genero": ">=3.20", "author": "fglpkg tests", "license": "MIT",
         "artifacts": [{"variant": "webcomponent", "zip": "fx-map-%s-webcomponent.zip" % v}]}
        for v in ("1.0.0", "1.2.0")
    ],
})
with open(path, "w") as fh:
    json.dump(doc, fh, indent=2)
PY
}

# The headline: moving an installed widget to another version must work.
_wcup_upgrade_in_place() {
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/wcupfx.XXXXXX")"
  _wcup_fixtures "$fx" || return 1
  mock_registry_start "$fx" || return 1

  run install fx-map@1.0.0
  assert_success || return 1
  assert_file_contains ".fglpkg/webcomponents/com/acme/Map.4gl" "v1" || return 1

  run install fx-map@1.2.0
  assert_success || return 1
  assert_not_contains "refusing to clobber" || return 1
  # The package's own files must actually move to the new version.
  assert_file_contains ".fglpkg/webcomponents/com/acme/Map.4gl" "different" || return 1
  assert_file_contains ".fglpkg/webcomponents/Map/Map.html" "v2" || return 1
}
it "a widget upgrade replaces the package's own shared files" \
  _wcup_upgrade_in_place

# The same, downward: nothing about this should depend on the direction.
_wcup_downgrade_in_place() {
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/wcupfx.XXXXXX")"
  _wcup_fixtures "$fx" || return 1
  mock_registry_start "$fx" || return 1

  run install fx-map@1.2.0
  assert_success || return 1
  run install fx-map@1.0.0
  assert_success || return 1
  assert_file_contains ".fglpkg/webcomponents/com/acme/Map.4gl" "v1" || return 1
}
it "a widget downgrade replaces the package's own shared files" \
  _wcup_downgrade_in_place

# The guard must not be thrown away with the bug: a DIFFERENT package writing
# over this one's files is still a clash, and must still be refused.
_wcup_other_package_still_clashes() {
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/wcupfx.XXXXXX")"
  _wcup_fixtures "$fx" || return 1
  # A second package shipping the same shared path with different content.
  local build; build="$(mktemp -d "$_SANDBOX_ROOT/wcup2.XXXXXX")"
  (
    cd "$build"
    mkdir -p webcomponents/Grid com/acme
    printf '<html>grid</html>\n' > webcomponents/Grid/Grid.html
    printf 'FUNCTION mapInit()\n  -- impostor\nEND FUNCTION\n' > com/acme/Map.4gl
    cat > fglpkg.json <<'EOF'
{ "name":"fx-grid","version":"1.0.0","description":"Another widget",
  "genero":">=3.20","license":"MIT","author":"fglpkg tests",
  "webcomponents":["Grid"], "docs":["com/**/*.4gl"] }
EOF
    "$FGLPKG" pack -o "$fx/fx-grid-1.0.0-webcomponent.zip" </dev/null >/dev/null 2>&1 || exit 1
  ) || return 1
  python3 - "$fx/packages.json" <<'PY' || return 1
import json, sys
path = sys.argv[1]
with open(path) as fh:
    doc = json.load(fh)
doc["packages"].append({
    "slug": "fx-grid", "name": "fx-grid", "description": "Another widget",
    "genero": ">=3.20",
    "owner": {"partner_id": "mock", "name": "fglpkg tests"},
    "versions": [{"version": "1.0.0", "genero": ">=3.20",
                  "author": "fglpkg tests", "license": "MIT",
                  "artifacts": [{"variant": "webcomponent", "zip": "fx-grid-1.0.0-webcomponent.zip"}]}],
})
with open(path, "w") as fh:
    json.dump(doc, fh, indent=2)
PY
  mock_registry_start "$fx" || return 1

  run install fx-map@1.0.0
  assert_success || return 1
  run install fx-grid@1.0.0
  assert_failure || return 1
  assert_contains "refusing to clobber" || return 1
}
it "another package's clashing file is still refused" \
  _wcup_other_package_still_clashes

# A failed install must not leave the project claiming the version it failed to
# install. This is what turns a loud, recoverable failure into silent drift:
# the lock says 1.2.0, the disk holds 1.0.0, and every replay reports success.
_wcup_failed_install_does_not_move_the_project() {
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/wcupfx.XXXXXX")"
  _wcup_fixtures "$fx" || return 1
  local build; build="$(mktemp -d "$_SANDBOX_ROOT/wcup3.XXXXXX")"
  (
    cd "$build"
    mkdir -p webcomponents/Grid com/acme
    printf '<html>grid</html>\n' > webcomponents/Grid/Grid.html
    printf 'FUNCTION mapInit()\n  -- impostor\nEND FUNCTION\n' > com/acme/Map.4gl
    cat > fglpkg.json <<'EOF'
{ "name":"fx-grid","version":"1.0.0","description":"Another widget",
  "genero":">=3.20","license":"MIT","author":"fglpkg tests",
  "webcomponents":["Grid"], "docs":["com/**/*.4gl"] }
EOF
    "$FGLPKG" pack -o "$fx/fx-grid-1.0.0-webcomponent.zip" </dev/null >/dev/null 2>&1 || exit 1
  ) || return 1
  python3 - "$fx/packages.json" <<'PY' || return 1
import json, sys
path = sys.argv[1]
with open(path) as fh:
    doc = json.load(fh)
doc["packages"].append({
    "slug": "fx-grid", "name": "fx-grid", "description": "Another widget",
    "genero": ">=3.20",
    "owner": {"partner_id": "mock", "name": "fglpkg tests"},
    "versions": [{"version": "1.0.0", "genero": ">=3.20",
                  "author": "fglpkg tests", "license": "MIT",
                  "artifacts": [{"variant": "webcomponent", "zip": "fx-grid-1.0.0-webcomponent.zip"}]}],
})
with open(path, "w") as fh:
    json.dump(doc, fh, indent=2)
PY
  mock_registry_start "$fx" || return 1

  run install fx-map@1.0.0
  assert_success || return 1

  run install fx-grid@1.0.0
  assert_failure || return 1

  # The refused package must not appear anywhere as if it were installed.
  assert_not_contains "fx-grid" "$(cat fglpkg.json)" || return 1
  assert_not_contains "fx-grid" "$(cat fglpkg-lock.json)" || return 1

  # And the replay must agree: nothing missing, nothing silently wrong.
  run install
  assert_success || return 1
}
it "a refused widget install leaves the manifest and lock untouched" \
  _wcup_failed_install_does_not_move_the_project

# ── a pulled widget bump installs on replay ─────────────────────────────────
#
# A web component leaves no manifest on disk, so a lock replay had nothing to
# compare against: it checked only that .fglpkg/webcomponents/ was non-empty.
# A pulled commit that bumped a widget and nothing else therefore reported
# "Nothing to install", exit 0, and left the old bundle in place — the same
# drift as GIS-586, reached through the other lock array. The ownership sidecar
# now records the version each package was installed at.

_wcup_publish_pull() {  # _wcup_publish_pull <fixtures> <version>
  local here; here="$(pwd)"
  _WCUP_PULLED="$here/pulled-$2"
  mkdir -p "$_WCUP_PULLED" && cd "$_WCUP_PULLED" || return 1
  run install "fx-map@$2"
  cd "$here" || return 1
  assert_success || return 1
}

_wcup_pulled_bump_installs_on_replay() {
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/wcupfx.XXXXXX")"
  _wcup_fixtures "$fx" || return 1
  mock_registry_start "$fx" || return 1
  _wcup_publish_pull "$fx" 1.2.0 || return 1

  mkdir -p mine && cd mine || return 1
  run install fx-map@1.0.0
  assert_success || return 1
  assert_file_contains ".fglpkg/webcomponents/Map/Map.html" "v1" || return 1

  # `git pull`: both files move to 1.2.0, .fglpkg/ is untouched.
  cp "$_WCUP_PULLED/fglpkg.json" "$_WCUP_PULLED/fglpkg-lock.json" . || return 1

  run install
  assert_success || return 1
  assert_not_contains "Nothing to install" || return 1
  assert_file_contains ".fglpkg/webcomponents/Map/Map.html" "v2" || return 1
  assert_file_contains ".fglpkg/webcomponents/com/acme/Map.4gl" "different" || return 1
}
it "a pulled widget bump installs on a plain replay" \
  _wcup_pulled_bump_installs_on_replay

# The other half of the contract: a store already holding what the lock names
# must still take the no-op fast path, or every install would re-extract every
# widget.
_wcup_matching_version_is_a_no_op() {
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/wcupfx.XXXXXX")"
  _wcup_fixtures "$fx" || return 1
  mock_registry_start "$fx" || return 1

  run install fx-map@1.0.0
  assert_success || return 1
  run install
  assert_success || return 1
  assert_contains "Nothing to install" || return 1
}
it "a replay of the widget version already on disk installs nothing" \
  _wcup_matching_version_is_a_no_op

# A store installed before the sidecar recorded versions: the record exists but
# names no version, so the widget must re-extract once rather than be assumed
# current, and the run after that is a no-op again.
_wcup_legacy_sidecar_reextracts_once() {
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/wcupfx.XXXXXX")"
  _wcup_fixtures "$fx" || return 1
  mock_registry_start "$fx" || return 1

  run install fx-map@1.0.0
  assert_success || return 1

  # Strip the versions map, leaving the pre-GIS-579 shape.
  python3 - .fglpkg/webcomponent-owners.json <<'PY' || return 1
import json, sys
path = sys.argv[1]
with open(path) as fh:
    doc = json.load(fh)
doc.pop("versions", None)
with open(path, "w") as fh:
    json.dump(doc, fh, indent=2)
PY

  run install
  assert_success || return 1
  assert_not_contains "Nothing to install" || return 1

  run install
  assert_success || return 1
  assert_contains "Nothing to install" || return 1
}
it "a pre-version sidecar re-extracts once, then settles" \
  _wcup_legacy_sidecar_reextracts_once

# The ownership sidecar lives NEXT TO .fglpkg/webcomponents/, not inside it, so
# that Genero's FGLIMAGEPATH scan never mistakes it for a COMPONENTTYPE dir.
# The consequence is that `rm -rf .fglpkg/webcomponents/` leaves the sidecar
# behind, still naming versions for files that are gone — so the version check
# alone would report the store as current. The directory check is what catches
# it, and both are needed.
_wcup_deleted_store_is_reinstalled() {
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/wcupfx.XXXXXX")"
  _wcup_fixtures "$fx" || return 1
  mock_registry_start "$fx" || return 1

  run install fx-map@1.0.0
  assert_success || return 1
  rm -rf .fglpkg/webcomponents || return 1
  # The sidecar survives, and still claims 1.0.0 is installed.
  assert_file_contains ".fglpkg/webcomponent-owners.json" '"1.0.0"' || return 1

  run install
  assert_success || return 1
  assert_not_contains "Nothing to install" || return 1
  assert_file_contains ".fglpkg/webcomponents/Map/Map.html" "v1" || return 1
}
it "a deleted webcomponents store is reinstalled despite a surviving sidecar" \
  _wcup_deleted_store_is_reinstalled

# ── the rollback covers every way out, not just the install ────────────────
#
# Review round 1. The restore was wired to the install's own error branch, so
# two earlier `return err`s after the manifest was saved skipped it — the
# repository rebuild, and the PREINSTALL HOOK. A failing hook therefore left
# fglpkg.json declaring a package that was never fetched, which is the same
# false claim the rollback exists to prevent. It is now deferred with a success
# flag, so every exit path is covered, including any added later.
_wcup_failing_preinstall_hook_rolls_back() {
  mock_registry_start

  cat > fglpkg.json <<'EOF'
{ "name":"app","version":"1.0.0","genero":">=3.20","license":"MIT",
  "hooks": { "preinstall": [ { "op":"copy-files", "from":"does-not-exist.txt", "to":"out" } ] } }
EOF
  cp fglpkg.json before.json || return 1

  run install demo.pkg@1.0.0
  assert_failure || return 1
  assert_contains "preinstall" || return 1

  # The manifest must be byte-identical to what it was before the run.
  run_raw diff before.json fglpkg.json
  assert_success || return 1
  assert_no_file "fglpkg-lock.json" || return 1
}
it "a failing preinstall hook leaves the manifest untouched" \
  _wcup_failing_preinstall_hook_rolls_back

# The .fglpkg/ scaffolding goes too. ensureDirs lays it down before anything is
# fetched, and isProjectDir counts it as a project marker — so without this a
# refused install in an empty directory left something behind that still read
# as a project.
_wcup_refused_install_leaves_no_store() {
  mock_registry_start
  export FGLPKG_SIGNING=require     # the mock serves unsigned artifacts

  run install demo.pkg@1.0.0
  assert_failure || return 1
  assert_no_file "fglpkg.json" || return 1
  assert_no_file "fglpkg-lock.json" || return 1
  assert_no_file ".fglpkg" || return 1
}
it "a refused install in an empty directory leaves no store behind" \
  _wcup_refused_install_leaves_no_store
