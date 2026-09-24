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
