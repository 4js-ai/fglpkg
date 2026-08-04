suite "classpath anchor lifecycle (mock maven)"

ANCHOR=".fglpkg/jars/.classpath.jar"

# _ca_manifest <artifactId> <version> — a Java-only project (no fgl deps, so no
# registry is contacted; JARs come from the mock maven mirror).
_ca_manifest() {
  cat > fglpkg.json <<EOF
{ "name":"jarapp", "version":"1.0.0", "genero":">=3.20",
  "dependencies": { "java": [
    { "groupId":"com.example", "artifactId":"$1", "version":"$2" } ] } }
EOF
}

# _ca_anchor_manifest — print the anchor jar's META-INF/MANIFEST.MF.
_ca_anchor_manifest() {
  python3 -c "import zipfile; print(zipfile.ZipFile('$ANCHOR').read('META-INF/MANIFEST.MF').decode())"
}

_ca_install_creates() {
  mock_maven_start
  export FGLPKG_MAVEN_URL="$MAVEN_URL"
  _ca_manifest lib 1.0.0
  run install --local
  assert_success
  assert_file ".fglpkg/jars/lib-1.0.0.jar"
  assert_file "$ANCHOR"
  assert_contains "Class-Path: lib-1.0.0.jar" "$(_ca_anchor_manifest)"
}
it "install creates the anchor listing the installed jar" _ca_install_creates

_ca_refresh_on_change() {
  mock_maven_start
  export FGLPKG_MAVEN_URL="$MAVEN_URL"
  _ca_manifest lib 1.0.0
  run install --local
  assert_success
  # Bump the dependency: the stale lock forces a re-resolve; the prune sweeps
  # the old jar and the anchor must follow the new jar set.
  _ca_manifest lib 2.0.0
  run install --local
  assert_success
  assert_file ".fglpkg/jars/lib-2.0.0.jar"
  assert_no_file ".fglpkg/jars/lib-1.0.0.jar"
  assert_contains "Class-Path: lib-2.0.0.jar" "$(_ca_anchor_manifest)"
  assert_not_contains "lib-1.0.0.jar" "$(_ca_anchor_manifest)"
}
it "a dependency bump refreshes the anchor to the new jar set" _ca_refresh_on_change

_ca_removed_when_no_jars() {
  mock_maven_start
  export FGLPKG_MAVEN_URL="$MAVEN_URL"
  _ca_manifest lib 1.0.0
  run install --local
  assert_success
  assert_file "$ANCHOR"
  # Drop the last Java dependency: the prune removes the jar and the anchor
  # must not linger as a stale CLASSPATH entry.
  cat > fglpkg.json <<'EOF'
{ "name":"jarapp", "version":"1.0.0", "genero":">=3.20", "dependencies": {} }
EOF
  run install --local
  assert_success
  assert_no_file ".fglpkg/jars/lib-1.0.0.jar"
  assert_no_file "$ANCHOR"
}
it "removing the last jar dependency deletes the anchor" _ca_removed_when_no_jars

_ca_env_references_never_writes() {
  mock_maven_start
  export FGLPKG_MAVEN_URL="$MAVEN_URL"
  _ca_manifest lib 1.0.0
  run install --local
  assert_success
  assert_file "$ANCHOR"

  # env emits the anchor (never the raw jar) ...
  run env --local
  assert_success
  assert_contains ".classpath.jar"
  assert_not_contains "lib-1.0.0.jar"

  # ... and is a pure read: the anchor's bytes and mtime must not change.
  # Backdate first so an unwanted rewrite is visible even with coarse
  # filesystem timestamps.
  python3 -c "import os; os.utime('$ANCHOR', (1000000000, 1000000000))"
  before="$(python3 -c "import os; print(os.stat('$ANCHOR').st_mtime_ns)")"
  run env --local
  assert_success
  after="$(python3 -c "import os; print(os.stat('$ANCHOR').st_mtime_ns)")"
  assert_eq "$before" "$after"
}
it "env references the anchor and never writes it" _ca_env_references_never_writes

_ca_env_warns_when_anchor_missing() {
  mock_maven_start
  export FGLPKG_MAVEN_URL="$MAVEN_URL"
  _ca_manifest lib 1.0.0
  run install --local
  assert_success
  rm -f "$ANCHOR"    # simulate a pre-anchor install / hand-deleted anchor
  run env --local
  assert_success
  assert_not_contains "CLASSPATH"                # no entry pointing at a missing file
  assert_contains "run 'fglpkg install' to regenerate the classpath anchor"
  assert_no_file "$ANCHOR"                       # and env did not recreate it
}
it "env warns (and does not write) when jars exist but the anchor is missing" _ca_env_warns_when_anchor_missing
