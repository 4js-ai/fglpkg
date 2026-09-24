suite "remove --global: uninstall from the shared store (GIS-567)"

# `install <pkg> --global` works from anywhere (GIS-565); until now there was no
# way back out — `remove --global` outside a project died on the project
# manifest it never needed. These cases close the round trip.

_rg_install_global() {
  mock_registry_start
  run install demo.pkg@1.0.0 --global
  assert_success
}

_rg_round_trip() {
  _rg_install_global
  run list --global
  assert_contains "demo-pkg"

  run remove demo.pkg --global
  assert_success
  assert_contains "Removed demo-pkg"
  assert_not_contains "failed to load"        # the GIS-567 symptom

  # Really gone from the store, and the cwd is still not a project.
  run list --global
  assert_not_contains "demo-pkg"
  assert_no_file "fglpkg.json"
  assert_no_file "fglpkg-lock.json"
  assert_no_file ".fglpkg"
}
it "install --global then remove --global round-trips outside a project" _rg_round_trip

# The reported symptom: a misleading manifest error for a command that has no
# business reading a project manifest.
_rg_no_manifest_error() {
  _rg_install_global
  run remove demo.pkg --global
  assert_success
  assert_not_contains "fglpkg.json"
}
it "remove --global outside a project never reports a manifest error" _rg_no_manifest_error

# A name that is not in the store must not read as success (GIS-564's rule).
_rg_not_installed() {
  mock_registry_start
  run remove nosuchpkg --global
  assert_failure
  assert_contains "not installed in the global store"
  assert_not_contains "✓"
}
it "remove --global reports a package that is not in the store" _rg_not_installed

# Deleting from a store shared by every project is not inferred from an empty
# directory — the user may simply be in the wrong one. The message names the
# flag instead of dumping a manifest error.
_rg_requires_explicit_scope() {
  mock_registry_start
  run remove demo.pkg
  assert_failure
  assert_contains "--global"
  assert_not_contains "failed to load"
}
it "remove outside a project explains the scope instead of failing on the manifest" _rg_requires_explicit_scope

# Inside a project, --global keeps its existing meaning: drop the dependency
# from THIS project's manifest and leave the shared store alone (the install
# side of that asymmetry is GIS-565).
_rg_in_project_unchanged() {
  _rg_install_global
  # Become a project that declares the same package.
  cat > fglpkg.json <<'EOF'
{ "name": "myproj", "version": "1.0.0", "genero": ">=3.20",
  "dependencies": { "fgl": { "demo-pkg": "^1.0.0" } } }
EOF
  run remove demo-pkg --global
  assert_success
  assert_contains "Removed demo-pkg from dependencies"
  assert_contains "shared across projects"        # store deliberately untouched

  # The project manifest shrank; the global store still has the package.
  assert_file_contains "fglpkg.json" '"dependencies"'
  run list --global
  assert_contains "demo-pkg"
}
it "remove --global inside a project still edits the manifest, not the store" _rg_in_project_unchanged
