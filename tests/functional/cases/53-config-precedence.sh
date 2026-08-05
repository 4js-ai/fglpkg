suite "config precedence: local (project) vs global (user) — GIS-368"

# ── registry add scope flag ───────────────────────────────────────────────────
# --local writes the checked-in project manifest; global is the default target;
# --local/--global conflict; --project is a still-working deprecated alias.

_cp_add_local_writes_manifest() {
  cat > fglpkg.json <<'EOF'
{ "name":"demo", "version":"1.0.0", "genero":">=6.00.00" }
EOF
  run registry add acme https://acme.jfrog.io/artifactory/GeneroBDL --local
  assert_success
  assert_contains 'Added registry "acme" to fglpkg.json'
  assert_file_contains "fglpkg.json" '"acme"'
  assert_no_file "$FGLPKG_HOME/config.json"     # the user config is untouched
}
it "registry add --local writes the project fglpkg.json" _cp_add_local_writes_manifest

_cp_add_defaults_to_global() {
  run registry add acme https://acme.jfrog.io/artifactory/GeneroBDL   # no scope flag
  assert_success
  assert_file "$FGLPKG_HOME/config.json"
  assert_file_contains "$FGLPKG_HOME/config.json" '"acme"'
}
it "registry add writes the global user config by default" _cp_add_defaults_to_global

_cp_add_scope_conflict() {
  cat > fglpkg.json <<'EOF'
{ "name":"demo", "version":"1.0.0", "genero":">=6.00.00" }
EOF
  run registry add acme https://acme.jfrog.io/artifactory/K --local --global
  assert_failure
  assert_contains "mutually exclusive"
}
it "registry add --local and --global are mutually exclusive" _cp_add_scope_conflict

_cp_add_project_alias() {
  cat > fglpkg.json <<'EOF'
{ "name":"demo", "version":"1.0.0", "genero":">=6.00.00" }
EOF
  run registry add acme https://acme.jfrog.io/artifactory/K --project   # deprecated alias
  assert_success
  assert_file_contains "fglpkg.json" '"acme"'
  assert_no_file "$FGLPKG_HOME/config.json"
}
it "registry add --project still works as an alias for --local" _cp_add_project_alias

# ── merge: a project registry overrides a same-named global one (local wins) ──

_cp_project_registry_overrides_global() {
  mkdir -p "$FGLPKG_HOME"
  cat > "$FGLPKG_HOME/config.json" <<'EOF'
{ "registries": [ {"name":"acme","type":"artifactory","url":"https://GLOBALHOST.example/artifactory","repoKey":"G","priority":2} ] }
EOF
  cat > fglpkg.json <<'EOF'
{ "name":"demo", "version":"1.0.0", "genero":">=6.00.00",
  "registries": [ {"name":"acme","type":"artifactory","url":"https://LOCALHOST.example/artifactory","repoKey":"L","priority":2} ] }
EOF
  run registry list
  assert_success
  assert_contains "LOCALHOST.example"        # the project layer wins for a shared name
  assert_not_contains "GLOBALHOST.example"
}
it "a project registry overrides a same-named global one" _cp_project_registry_overrides_global

# ── scalar precedence, observed via the consume-default resolver: an unconfigured
#    default errors naming the value it resolved, so we can see which layer won ──

_cp_consume_env_beats_project() {
  mock_registry_start                        # single-registry sandbox (only gi)
  cat > fglpkg.json <<'EOF'
{ "name":"demo", "version":"1.0.0", "genero":">=6.00.00", "defaultConsumeRegistry":"projloses" }
EOF
  export FGLPKG_CONSUME_REGISTRY=envwins
  run install demo.pkg@1.0.0
  assert_failure
  assert_contains '"envwins"'                # env outranks the project manifest
  assert_not_contains '"projloses"'
}
it "FGLPKG_CONSUME_REGISTRY beats the project default" _cp_consume_env_beats_project

_cp_consume_project_beats_global() {
  mock_registry_start
  unset FGLPKG_CONSUME_REGISTRY
  mkdir -p "$FGLPKG_HOME"
  cat > "$FGLPKG_HOME/config.json" <<'EOF'
{ "defaultConsumeRegistry": "globalloses" }
EOF
  cat > fglpkg.json <<'EOF'
{ "name":"demo", "version":"1.0.0", "genero":">=6.00.00", "defaultConsumeRegistry":"projwins" }
EOF
  run install demo.pkg@1.0.0
  assert_failure
  assert_contains '"projwins"'               # the project default outranks the global one
  assert_not_contains '"globalloses"'
}
it "a project consume default beats the global one" _cp_consume_project_beats_global

# ── lint validates the project's routing config ──

_cp_lint_dangling_default_warns() {
  mkdir -p src; : > src/foo.42m
  cat > fglpkg.json <<'EOF'
{ "name":"demo", "version":"1.0.0", "genero":">=6.00.00", "root":"src", "defaultConsumeRegistry":"ghost" }
EOF
  run lint
  assert_success                             # a dangling default is advisory (warning)
  assert_contains "defaultConsumeRegistry"
  assert_contains "not declared"
}
it "lint warns on a dangling default registry" _cp_lint_dangling_default_warns

_cp_lint_malformed_registry_errors() {
  mkdir -p src; : > src/foo.42m
  cat > fglpkg.json <<'EOF'
{ "name":"demo", "version":"1.0.0", "genero":">=6.00.00", "root":"src",
  "registries": [ {"name":"acme","type":"bogus","url":"https://x","priority":2} ] }
EOF
  run lint
  assert_failure
  assert_contains "unknown type"
}
it "lint errors on a malformed project registry" _cp_lint_malformed_registry_errors

# ── guardrail: a checked-in manifest cannot set user/global policy ──

_cp_policy_key_rejected() {
  cat > fglpkg.json <<'EOF'
{ "name":"demo", "version":"1.0.0", "genero":">=6.00.00", "signing":{"enforce":"off"} }
EOF
  run lint
  assert_failure
  assert_contains "user/global setting"      # signing.enforce is not a project field (GIS-368)
  assert_not_contains "check for a typo"
}
it "a project fglpkg.json cannot set signing.enforce" _cp_policy_key_rejected
