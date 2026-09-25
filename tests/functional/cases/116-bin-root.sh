suite "bin scripts under a package root (GIS-569)"

# `bin` paths are relative to the manifest's `root`, so a package published with
# root set ships its script at <pkg>/<root>/<script>. Resolving without root
# broke BOTH ends: the install failed outright ("cannot set bin script
# permissions" — the chmod pass looked in the wrong place), and once past that
# `run --list` advertised the command while `run <cmd>` said it was not found in
# any installed package.

# Builds a fixtures dir serving one package that sets root AND ships a bin,
# dogfooding `fglpkg pack` so the archive layout is the real one.
_binroot_fixtures() {  # _binroot_fixtures <dir>
  local dir="$1"; mkdir -p "$dir"
  local build; build="$(mktemp -d "$_SANDBOX_ROOT/binrootfx.XXXXXX")"
  (
    cd "$build"
    cat > fglpkg.json <<'EOF'
{ "name":"demo.pkg","version":"1.0.0","description":"Package with root + bin",
  "genero":">=3.20","license":"MIT","author":"fglpkg tests",
  "root":"src","files":["*.42m"],"bin":{"greet":"scripts/greet.sh"} }
EOF
    mkdir -p src/scripts
    printf 'stub' > src/mod.42m
    cat > src/scripts/greet.sh <<'EOF'
#!/bin/sh
echo "greet ran from the installed package"
EOF
    chmod +x src/scripts/greet.sh
    "$FGLPKG" pack -o "$dir/demo-pkg-1.0.0-genero6.zip" </dev/null >/dev/null 2>&1 || exit 1
  ) || return 1
  cat > "$dir/packages.json" <<'EOF'
{
  "packages": [
    {
      "slug": "demo-pkg",
      "name": "demo.pkg",
      "description": "Package with root + bin",
      "genero": ">=3.20",
      "owner": { "partner_id": "mock", "name": "fglpkg tests" },
      "versions": [
        { "version":"1.0.0", "genero":">=3.20", "author":"fglpkg tests", "license":"MIT",
          "artifacts":[ { "variant":"genero6", "zip":"demo-pkg-1.0.0-genero6.zip" } ] }
      ]
    }
  ]
}
EOF
}

_binroot_install_succeeds() {
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/binroot.XXXXXX")"
  _binroot_fixtures "$fx" || { _diag "could not build fixtures"; return 1; }
  mock_registry_start "$fx"
  run install demo.pkg@1.0.0
  assert_success                                   # used to fail on the chmod pass
  assert_not_contains "bin script permissions"
  # The script really is shipped under the package root.
  assert_file ".fglpkg/packages/demo-pkg/src/scripts/greet.sh"
}
it "installing a package with root + bin succeeds" _binroot_install_succeeds

_binroot_run_executes() {
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/binroot.XXXXXX")"
  _binroot_fixtures "$fx" || { _diag "could not build fixtures"; return 1; }
  mock_registry_start "$fx"
  run install demo.pkg@1.0.0
  assert_success

  run run --list
  assert_success
  assert_contains "greet"

  # The payoff: what --list advertises is what `run` can actually execute.
  run run greet
  assert_success
  assert_contains "greet ran from the installed package"
}
it "run <cmd> executes a bin script under the package root" _binroot_run_executes

# PR #87 review (T5): `root` is manifest-supplied too, so a registry package can
# use it to reach outside its own install directory — the bin path itself looks
# innocent. Serve a hand-built archive whose manifest sets root:"../../.." and
# check that a file in the CONSUMER's project directory is neither made
# executable nor runnable.
_binroot_escaping_root_fixtures() {  # <dir>
  local dir="$1"; mkdir -p "$dir"
  local build; build="$(mktemp -d "$_SANDBOX_ROOT/escfx.XXXXXX")"
  (
    cd "$build"
    # Hand-built: `pack` would reject this manifest, which is the point — the
    # consumer must not trust what a registry serves.
    cat > fglpkg.json <<'MANIFEST'
{ "name":"demo.pkg","version":"1.0.0","description":"Escaping root","genero":">=3.20",
  "license":"MIT","author":"fglpkg tests","root":"../../..","bin":{"greet":"victim.sh"} }
MANIFEST
    printf 'stub' > mod.42m
    python3 -c "import zipfile,sys; z=zipfile.ZipFile(sys.argv[1],'w'); z.write('fglpkg.json'); z.write('mod.42m'); z.close()" \
      "$dir/demo-pkg-1.0.0-genero6.zip" || exit 1
  ) || return 1
  cat > "$dir/packages.json" <<'META'
{
  "packages": [
    { "slug": "demo-pkg", "name": "demo.pkg", "description": "Escaping root", "genero": ">=3.20",
      "owner": { "partner_id": "mock", "name": "fglpkg tests" },
      "versions": [
        { "version":"1.0.0", "genero":">=3.20", "author":"fglpkg tests", "license":"MIT",
          "artifacts":[ { "variant":"genero6", "zip":"demo-pkg-1.0.0-genero6.zip" } ] }
      ] }
  ]
}
META
}

_binroot_escaping_root_is_refused() {
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/escroot.XXXXXX")"
  _binroot_escaping_root_fixtures "$fx" || { _diag "could not build fixtures"; return 1; }
  mock_registry_start "$fx"

  # A file of the consumer's own, outside .fglpkg/packages/demo-pkg/.
  cat > victim.sh <<'VICTIM'
#!/bin/sh
echo "OUTSIDE-FILE EXECUTED"
VICTIM
  chmod 644 victim.sh

  run install demo.pkg@1.0.0
  # Whether the install refuses or simply declines to touch the file, the
  # invariant is the same: nothing outside the package becomes executable.
  if [[ -x victim.sh ]]; then
    _diag "a file outside the package was made executable by install"
    return 1
  fi

  run run greet
  assert_not_contains "OUTSIDE-FILE EXECUTED"
}
it "an installed package's escaping root cannot reach outside the package" _binroot_escaping_root_is_refused

# GIS-570: `importRoot` may sit INSIDE `root` (root ".", importRoot "lib" — the
# shape `fglpkg init` scaffolds). PublishCopy cannot rebase `root` there
# (filepath.Rel("lib",".") escapes), so it leaves root alone while staging
# strips the lib/ prefix from the file. The shipped manifest then pointed at
# "lib/scripts/greet.sh" while the script sat at "scripts/greet.sh", and the
# install failed on the executable-bit pass — same symptom as the root bug
# above, different cause.
_binroot_importroot_inside_root_fixtures() {  # <dir>
  local dir="$1"; mkdir -p "$dir"
  local build; build="$(mktemp -d "$_SANDBOX_ROOT/irfx.XXXXXX")"
  (
    cd "$build"
    cat > fglpkg.json <<'EOF2'
{ "name":"demo.pkg","version":"1.0.0","description":"importRoot inside root",
  "genero":">=3.20","license":"MIT","author":"fglpkg tests",
  "root":".","importRoot":"lib","files":["*.42m"],
  "bin":{"greet":"lib/scripts/greet.sh"} }
EOF2
    mkdir -p lib/scripts
    printf 'stub' > lib/mod.42m
    cat > lib/scripts/greet.sh <<'EOF2'
#!/bin/sh
echo "greet ran from the stripped layout"
EOF2
    chmod +x lib/scripts/greet.sh
    "$FGLPKG" pack -o "$dir/demo-pkg-1.0.0-genero6.zip" </dev/null >/dev/null 2>&1 || exit 1
  ) || return 1
  cat > "$dir/packages.json" <<'EOF2'
{
  "packages": [
    { "slug": "demo-pkg", "name": "demo.pkg", "description": "importRoot inside root", "genero": ">=3.20",
      "owner": { "partner_id": "mock", "name": "fglpkg tests" },
      "versions": [
        { "version":"1.0.0", "genero":">=3.20", "author":"fglpkg tests", "license":"MIT",
          "artifacts":[ { "variant":"genero6", "zip":"demo-pkg-1.0.0-genero6.zip" } ] }
      ] }
  ]
}
EOF2
}

_binroot_importroot_inside_root() {
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/irroot.XXXXXX")"
  _binroot_importroot_inside_root_fixtures "$fx" || { _diag "could not build fixtures"; return 1; }
  mock_registry_start "$fx"

  run install demo.pkg@1.0.0
  assert_success
  assert_not_contains "bin script permissions"
  # The lib/ prefix is stripped in the archive, so the script lands at the root.
  assert_file ".fglpkg/packages/demo-pkg/scripts/greet.sh"

  run run greet
  assert_success
  assert_contains "greet ran from the stripped layout"
}
it "a bin resolves when importRoot sits inside root" _binroot_importroot_inside_root
