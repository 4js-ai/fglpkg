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
#
# The package is installed FIRST on purpose: against an empty store the message
# assertions alone also hold when the --global requirement is removed (the
# "no packages matched … list --global" error contains "--global" and not
# "failed to load"), so the property has to be checked on the store itself.
_rg_requires_explicit_scope() {
  _rg_install_global
  run remove demo.pkg
  assert_failure
  assert_contains "--global"
  assert_not_contains "failed to load"

  # The real property: the store still has it.
  run list --global
  assert_contains "demo-pkg"
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

# ── what the removal must NOT take with it (PR #88 review) ──────────────────
#
# A global store is SHARED, and the things sharing it are invisible from inside
# it. These cases pin the artifacts a removal has no business deleting.

# W1: a web component installs only into webcomponents/ (no packages/<name>
# directory), so a keep-set built from packages/ contains none of them — every
# globally installed web component was pruned on ANY removal.
_rg_keeps_unrelated_webcomponent() {
  local gdir="$TESTWD/gstore"
  export FGLPKG_GLOBAL_DIR="$gdir"
  mock_registry_start
  run install demo.pkg@1.0.0 --global
  assert_success

  # Hand-place a globally installed web component alongside it: bundle files
  # plus the ownership sidecar the installer writes.
  mkdir -p "$gdir/webcomponents/ChartWidget"
  printf '<html>' > "$gdir/webcomponents/ChartWidget/ChartWidget.html"
  cat > "$gdir/webcomponent-owners.json" <<'OWNERS'
{ "packages": { "chart-widget": ["ChartWidget/ChartWidget.html"] } }
OWNERS

  run remove demo.pkg --global
  assert_success
  assert_not_contains "pruned webcomponent"
  assert_file "$gdir/webcomponents/ChartWidget/ChartWidget.html"
}
it "removing a package leaves unrelated global web components alone" _rg_keeps_unrelated_webcomponent

# W2: that same web component IS installed — its files and owners entry are
# there — so it must be removable by name.
_rg_removes_webcomponent_by_name() {
  local gdir="$TESTWD/gstore"
  export FGLPKG_GLOBAL_DIR="$gdir"
  mock_registry_start
  run install demo.pkg@1.0.0 --global
  assert_success

  mkdir -p "$gdir/webcomponents/ChartWidget"
  printf '<html>' > "$gdir/webcomponents/ChartWidget/ChartWidget.html"
  cat > "$gdir/webcomponent-owners.json" <<'OWNERS'
{ "packages": { "chart-widget": ["ChartWidget/ChartWidget.html"] } }
OWNERS

  run remove chart-widget --global
  assert_success
  assert_not_contains "not installed in the global store"
  assert_no_file "$gdir/webcomponents/ChartWidget/ChartWidget.html"
  assert_dir "$gdir/packages/demo-pkg"        # the BDL package is untouched
}
it "a globally installed web component can be removed by name" _rg_removes_webcomponent_by_name

# J1: the global store also holds JARs installed FOR A PROJECT (`install
# --global` from inside one records them in that project's manifest, which the
# store never sees). Sweeping everything "unreferenced" deleted those and broke
# the project's classpath — GIS-567's "does not touch any project".
_rg_keeps_jar_no_package_declares() {
  local gdir="$TESTWD/gstore"
  export FGLPKG_GLOBAL_DIR="$gdir"
  mock_registry_start
  run install demo.pkg@1.0.0 --global
  assert_success

  # Stand in for a JAR another project put here via `install --global`.
  mkdir -p "$gdir/jars"
  printf 'jar' > "$gdir/jars/gson-2.10.1.jar"

  run remove demo.pkg --global
  assert_success
  assert_not_contains "pruned jar gson"
  assert_file "$gdir/jars/gson-2.10.1.jar"
}
it "removing a package leaves JARs no installed package declares" _rg_keeps_jar_no_package_declares
