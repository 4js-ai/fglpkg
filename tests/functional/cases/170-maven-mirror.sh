suite "Maven mirror for JAR downloads (mock maven)"

# The default `dependencies.java` fetch goes to Maven Central; FGLPKG_MAVEN_URL
# (GIS-365) reroutes it to a mirror. Install a Java-only project (no fgl deps, so
# no registry is contacted) and confirm the JAR came from the mirror and the
# resolved mirror URL is pinned in the lock. No checksum → integrity check skipped.
_mm_env_var_reroutes() {
  mock_maven_start
  cat > fglpkg.json <<'EOF'
{ "name":"jarapp", "version":"1.0.0", "genero":">=3.20",
  "dependencies": { "java": [
    { "groupId":"com.google.code.gson", "artifactId":"gson", "version":"2.10.1" } ] } }
EOF
  export FGLPKG_MAVEN_URL="$MAVEN_URL"
  run install --local
  assert_success
  assert_contains "Resolved 0 package(s), 1 JAR(s)"
  assert_contains "✓ gson-2.10.1.jar"
  assert_file ".fglpkg/jars/gson-2.10.1.jar"
  assert_json_field "fglpkg-lock.json" "jars.0.key" "com.google.code.gson:gson"
  assert_json_field "fglpkg-lock.json" "jars.0.groupId" "com.google.code.gson"
  assert_json_field "fglpkg-lock.json" "jars.0.downloadUrl" \
    "$MAVEN_URL/com/google/code/gson/gson/2.10.1/gson-2.10.1.jar"
  assert_not_contains "repo1.maven.org" "$(cat fglpkg-lock.json)"
  assert_file_contains "$MAVEN_LOG" "gson-2.10.1.jar"
}
it "install reroutes a JAR through FGLPKG_MAVEN_URL and pins the mirror URL" _mm_env_var_reroutes

# A committed project mavenMirror block routes the same way (the team-shared,
# checked-in config surface). Clear the env var so only the block can route.
_mm_manifest_block_reroutes() {
  unset FGLPKG_MAVEN_URL
  mock_maven_start
  cat > fglpkg.json <<EOF
{ "name":"jarapp", "version":"1.0.0", "genero":">=3.20",
  "mavenMirror": { "url": "$MAVEN_URL" },
  "dependencies": { "java": [
    { "groupId":"com.google.code.gson", "artifactId":"gson", "version":"2.10.1" } ] } }
EOF
  run install --local
  assert_success
  assert_file ".fglpkg/jars/gson-2.10.1.jar"
  assert_json_field "fglpkg-lock.json" "jars.0.downloadUrl" \
    "$MAVEN_URL/com/google/code/gson/gson/2.10.1/gson-2.10.1.jar"
  assert_file_contains "$MAVEN_LOG" "gson-2.10.1.jar"
}
it "a project mavenMirror block reroutes JAR downloads" _mm_manifest_block_reroutes

# A per-dependency `url` wins over the mirror outright — different path on the
# same mock, so the request log distinguishes the two candidates.
_mm_per_dep_url_wins() {
  unset FGLPKG_MAVEN_URL
  mock_maven_start
  cat > fglpkg.json <<EOF
{ "name":"jarapp", "version":"1.0.0", "genero":">=3.20",
  "mavenMirror": { "url": "$MAVEN_URL" },
  "dependencies": { "java": [
    { "groupId":"com.example", "artifactId":"lib", "version":"1.0.0",
      "url":"$MAVEN_URL/override/custom-lib.jar" } ] } }
EOF
  run install --local
  assert_success
  assert_json_field "fglpkg-lock.json" "jars.0.downloadUrl" "$MAVEN_URL/override/custom-lib.jar"
  assert_file ".fglpkg/jars/lib-1.0.0.jar"                    # on-disk name is coordinate-derived
  assert_file_contains "$MAVEN_LOG" "GET /override/custom-lib.jar"
  # the standard Maven2 path was never requested — the override short-circuits it
  assert_not_contains "/com/example/lib/1.0.0/lib-1.0.0.jar" "$(cat "$MAVEN_LOG")"
}
it "a per-dependency url override wins over the mirror" _mm_per_dep_url_wins

# Validation fails fast so a typo cannot silently downgrade to Maven Central.
_mm_rejects_empty_url() {
  cat > fglpkg.json <<'EOF'
{ "name":"jarapp", "version":"1.0.0", "genero":">=3.20",
  "mavenMirror": { "auth": "bearer" } }
EOF
  run pack --list
  assert_failure
  assert_contains "mavenMirror requires a non-empty 'url'"
}
it "pack rejects a mavenMirror with an empty url" _mm_rejects_empty_url

_mm_rejects_unknown_auth() {
  cat > fglpkg.json <<'EOF'
{ "name":"jarapp", "version":"1.0.0", "genero":">=3.20",
  "mavenMirror": { "url": "https://artifactory.example/m2", "auth": "token" } }
EOF
  run pack --list
  assert_failure
  assert_contains 'mavenMirror has unknown auth "token" (expected bearer|basic|apikey|anonymous)'
}
it "pack rejects a mavenMirror with an unknown auth scheme" _mm_rejects_unknown_auth

# Fail-closed: an authenticated mirror (403 without a token) aborts the install
# non-zero with the credential hint, and removes the partial file.
_mm_auth_403_hard_fail() {
  mock_maven_start --require-token secret-abc
  cat > fglpkg.json <<'EOF'
{ "name":"jarapp", "version":"1.0.0", "genero":">=3.20",
  "dependencies": { "java": [
    { "groupId":"com.google.code.gson", "artifactId":"gson", "version":"2.10.1" } ] } }
EOF
  export FGLPKG_MAVEN_URL="$MAVEN_URL"
  run install --local
  assert_failure
  assert_contains "failed to install JAR com.google.code.gson:gson"
  assert_contains "HTTP 403 downloading gson-2.10.1.jar"
  assert_contains "Forbidden — check your credentials for this repository (run 'fglpkg login')"
  assert_no_file ".fglpkg/jars/gson-2.10.1.jar"
}
it "an authenticated mirror 403s without a token and install fails hard" _mm_auth_403_hard_fail

# The positive auth path: a stored bearer credential for the mirror URL is sent,
# so the token-guarded mirror serves the JAR (success THROUGH the guard proves the
# Authorization header reached it).
_mm_auth_bearer_credential_installs() {
  unset FGLPKG_MAVEN_URL
  mock_maven_start --require-token secret-abc
  cat > "$FGLPKG_HOME/credentials.json" <<EOF
{ "registries": { "$MAVEN_URL": { "pat": "secret-abc", "savedAt": "2026-01-01T00:00:00Z" } } }
EOF
  cat > fglpkg.json <<EOF
{ "name":"jarapp", "version":"1.0.0", "genero":">=3.20",
  "mavenMirror": { "url": "$MAVEN_URL", "auth": "bearer" },
  "dependencies": { "java": [
    { "groupId":"com.google.code.gson", "artifactId":"gson", "version":"2.10.1" } ] } }
EOF
  run install --local
  assert_success
  assert_file ".fglpkg/jars/gson-2.10.1.jar"
  assert_json_field "fglpkg-lock.json" "jars.0.downloadUrl" \
    "$MAVEN_URL/com/google/code/gson/gson/2.10.1/gson-2.10.1.jar"
}
it "an authenticated mirror with a stored bearer credential installs the JAR" _mm_auth_bearer_credential_installs
