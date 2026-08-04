suite "list renders the dependency tree with JARs"

# `fglpkg list` used to print only BDL packages, flat, with no indication of why
# any of them was installed and no mention of the JARs in .fglpkg/jars/. It now
# prints a tree: Genero packages and webcomponents first at every level, then the
# JARs they pull in.
#
# Package parentage comes from the lock's requiredBy. JAR parentage is NOT in the
# lock (LockedJAR has no requiredBy), so it is reconstructed from each installed
# package's bundled fglpkg.json — which is what these cases pin down.

# _fixture_lock writes a project whose lock has a two-level package chain and two
# JARs with different declarers: poi is declared by the installed poiapi's
# bundled manifest, gson by the root project. No install is involved — list is a
# reporting command over the lock plus what is on disk.
_fixture_lock() {
  cat > fglpkg.json <<'EOF'
{ "name":"app", "version":"1.0.0", "genero":">=3.20",
  "dependencies": {
    "fgl": { "poiapi": "^1.4.0" },
    "java": [ { "groupId":"com.google.code.gson", "artifactId":"gson", "version":"2.10.1" } ]
  } }
EOF
  cat > fglpkg.lock <<'EOF'
{
  "lockfileVersion": 1,
  "generatedAt": "2026-01-01T00:00:00Z",
  "generoVersion": "6.00.01",
  "root": { "name": "app", "version": "1.0.0" },
  "packages": [
    { "name":"poiapi", "version":"1.4.0", "downloadUrl":"", "requiredBy":["<root>"] },
    { "name":"logger", "version":"2.0.0", "downloadUrl":"", "requiredBy":["poiapi"] }
  ],
  "jars": [
    { "key":"org.apache.poi:poi", "groupId":"org.apache.poi", "artifactId":"poi", "version":"5.3.0" },
    { "key":"com.google.code.gson:gson", "groupId":"com.google.code.gson",
      "artifactId":"gson", "version":"2.10.1" }
  ]
}
EOF
  mkdir -p .fglpkg/packages/poiapi
  cat > .fglpkg/packages/poiapi/fglpkg.json <<'EOF'
{ "name":"poiapi", "version":"1.4.0", "genero":">=3.20",
  "dependencies": {
    "fgl": { "logger": "^2.0.0" },
    "java": [ { "groupId":"org.apache.poi", "artifactId":"poi", "version":"5.3.0" } ]
  } }
EOF
}

_list_tree_nests_jars_under_declarer() {
  _fixture_lock
  run list
  assert_success
  assert_contains "app@1.0.0"
  # Genero children before JARs at the top level...
  assert_contains "├─ poiapi@1.4.0"
  # ...and again one level down, where poi hangs off the package that declared
  # it rather than off the root.
  assert_contains "│  ├─ logger@2.0.0"
  assert_contains "│  └─ org.apache.poi:poi  5.3.0"
  # The root's own JAR declaration stays at the top level, after the packages.
  assert_contains "└─ com.google.code.gson:gson  2.10.1"
  assert_contains "2 packages, 2 JARs."
}
it "list nests each JAR under the package whose manifest declares it" _list_tree_nests_jars_under_declarer

# --depth truncates the tree without lying about what is installed.
_list_tree_depth() {
  _fixture_lock
  run list --depth=1
  assert_success
  assert_contains "poiapi@1.4.0"
  assert_not_contains "logger@2.0.0"

  run list --depth 2
  assert_success
  assert_contains "logger@2.0.0"
}
it "list --depth limits how deep the tree recurses" _list_tree_depth

# --flat is the pre-tree output, kept for scripts: packages, then JARs, no glyphs.
_list_flat_has_no_tree() {
  _fixture_lock
  mkdir -p .fglpkg/jars && printf 'stub' > .fglpkg/jars/poi-5.3.0.jar
  run list --flat
  assert_success
  assert_contains "Installed packages:"
  assert_contains "poiapi"
  assert_contains "Installed JARs:"
  assert_contains "poi-5.3.0.jar"
  assert_not_contains "└─"
  assert_not_contains "├─"
}
it "list --flat prints no tree structure" _list_flat_has_no_tree

# A JAR that no installed manifest claims must still be listed — at the top
# level, the only honest place for it.
_list_unattributed_jar() {
  mkpkg consumer.app 0.1.0
  mock_lock_with_jar org.orphan mystery 1.0.0
  run list
  assert_success
  assert_contains "org.orphan:mystery  1.0.0"
  assert_contains "0 packages, 1 JAR."
}
it "list shows a JAR no manifest declares at the top level" _list_unattributed_jar

# The real install path: the JAR is downloaded, locked, and then reported by
# list — no hand-written fixture involved.
_list_after_real_install() {
  mock_maven_start
  cat > fglpkg.json <<'EOF'
{ "name":"jarapp", "version":"1.0.0", "genero":">=3.20",
  "dependencies": { "java": [
    { "groupId":"com.google.code.gson", "artifactId":"gson", "version":"2.10.1" } ] } }
EOF
  export FGLPKG_MAVEN_URL="$MAVEN_URL"
  run install --local
  assert_success
  assert_file ".fglpkg/jars/gson-2.10.1.jar"

  run list
  assert_success
  assert_contains "jarapp@1.0.0"
  assert_contains "└─ com.google.code.gson:gson  2.10.1"
  assert_contains "0 packages, 1 JAR."

  # Without a lock there is no parentage to draw, so list falls back to flat and
  # says why rather than silently dropping the tree.
  rm fglpkg.lock
  run list
  assert_success
  assert_contains "Installed JARs:"
  assert_contains "gson-2.10.1.jar"
  assert_contains "no fglpkg.lock"
  assert_not_contains "└─"
}
it "list reports a really-installed JAR, and falls back to flat with no lock" _list_after_real_install

# The global store has no lock file at all, so it is always flat — and must not
# claim a missing lock is a problem there.
_list_global_is_flat() {
  _fixture_lock
  run list --global
  assert_success
  assert_contains "No packages installed."
  assert_not_contains "no fglpkg.lock"
}
it "list --global stays flat and reports the empty global store" _list_global_is_flat

# Argument handling: the scope flags conflict, and a stray argument is a typo
# worth reporting rather than silently ignoring.
_list_rejects_bad_args() {
  _fixture_lock
  run list --local --global
  assert_failure
  assert_contains "mutually exclusive"

  run list somejunk
  assert_failure
  assert_contains "unknown argument"

  run list --depth=abc
  assert_failure
  assert_contains "invalid --depth"
}
it "list rejects conflicting scopes, stray arguments, and a bad --depth" _list_rejects_bad_args
