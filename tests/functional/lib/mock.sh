# lib/mock.sh — start/stop local mock services for phase-2 cases.
#
# Two servers, both speaking fglpkg's own override env vars so everything stays
# hermetic and offline:
#   mock_registry_start  -> FGLPKG_REGISTRY   (registry_server.py; /registry/* protocol)
#   mock_osv_start       -> FGLPKG_AUDIT_URL   (osv_server.py; OSV.dev query API)
#
# Both track their PID and install a single EXIT trap on the test's subshell, so
# every server started in a test is killed when the test ends — even if an
# assertion aborts it. Servers pick an ephemeral port and publish it in a
# port-file once the socket is listening.

_MOCK_REGISTRY_SERVER="$_LIBDIR/../mock/registry_server.py"
_MOCK_OSV_SERVER="$_LIBDIR/../mock/osv_server.py"
_MOCK_MAVEN_SERVER="$_LIBDIR/../mock/maven_server.py"

_MOCK_PIDS=()
_mock_track() { _MOCK_PIDS+=("$1"); trap _mock_stop_all EXIT; }
_mock_stop_all() {
  local p
  for p in "${_MOCK_PIDS[@]:-}"; do
    [[ -n "$p" ]] || continue
    kill "$p" 2>/dev/null || true
    wait "$p" 2>/dev/null || true
  done
  _MOCK_PIDS=()
}

# _mock_launch <server.py> [extra args...]
# Launches a python server that writes its port to a port-file after binding;
# waits for readiness and reports results via _ML_LOG / _ML_PID / _ML_PORT.
_ML_LOG=""; _ML_PID=""; _ML_PORT=""
_mock_launch() {
  local server="$1"; shift
  [[ -f "$server" ]] || { _diag "mock server missing: $server"; return 1; }
  local portfile log pid port="" i
  portfile="$(mktemp "$_SANDBOX_ROOT/mock.port.XXXXXX")"; : > "$portfile"
  log="$(mktemp "$_SANDBOX_ROOT/mock.log.XXXXXX")"
  python3 "$server" --port-file "$portfile" "$@" >"$log" 2>&1 &
  pid=$!
  _mock_track "$pid"
  for i in $(seq 1 100); do            # up to ~5s
    if ! kill -0 "$pid" 2>/dev/null; then _diag "mock server exited early:"; cat "$log" >&2; return 1; fi
    port="$(cat "$portfile" 2>/dev/null)"; [[ -n "$port" ]] && break
    perl -e 'select(undef,undef,undef,0.05)' 2>/dev/null || sleep 0.05
  done
  [[ -n "$port" ]] || { _diag "mock server did not report a port:"; cat "$log" >&2; return 1; }
  _ML_LOG="$log"; _ML_PID="$pid"; _ML_PORT="$port"
}

# ---- registry ----
mock_registry_start() {  # mock_registry_start [fixtures_dir]   (builds standard fixtures if none given)
  local fixtures="${1:-}"
  if [[ -z "$fixtures" ]]; then
    fixtures="$(mktemp -d "$_SANDBOX_ROOT/fx.XXXXXX")"
    mock_build_fixtures "$fixtures" || return 1
  fi
  _mock_launch "$_MOCK_REGISTRY_SERVER" --fixtures "$fixtures" || return 1
  MOCK_LOG="$_ML_LOG"; MOCK_PID="$_ML_PID"; MOCK_PORT="$_ML_PORT"
  export FGLPKG_REGISTRY="http://127.0.0.1:$MOCK_PORT"
  export FGLPKG_TOKEN="mock-token"     # satisfies the auth gate; the mock doesn't verify it
  export FGLPKG_SIGNING=off            # serve unsigned artifacts; skip the cosmetic signature warning
  return 0
}

# ---- OSV (audit) ----
mock_osv_start() {  # mock_osv_start
  _mock_launch "$_MOCK_OSV_SERVER" || return 1
  OSV_LOG="$_ML_LOG"; OSV_PID="$_ML_PID"; OSV_PORT="$_ML_PORT"
  export FGLPKG_AUDIT_URL="http://127.0.0.1:$OSV_PORT/v1/query"
  return 0
}

# ---- Maven mirror (JAR downloads) ----
# Starts maven_server.py and reports its base URL in MAVEN_URL (host+port in
# MAVEN_HOST). It does NOT export FGLPKG_MAVEN_URL: JAR-mirror tests need to place
# the URL differently each time — the env var, a project `mavenMirror` block, or a
# per-dependency `url` — and the env var would otherwise always win. Pass
# --require-token <tok> to make the server answer 403 unless the request carries a
# matching bearer token (the fail-closed auth path).
mock_maven_start() {  # mock_maven_start [--require-token <tok>]
  _mock_launch "$_MOCK_MAVEN_SERVER" "$@" || return 1
  MAVEN_LOG="$_ML_LOG"; MAVEN_PID="$_ML_PID"; MAVEN_PORT="$_ML_PORT"
  MAVEN_URL="http://127.0.0.1:$MAVEN_PORT"
  MAVEN_HOST="127.0.0.1:$MAVEN_PORT"
  return 0
}

# ---- a reachable secondary repository (served by the SAME mock process) ----
# registry_server.py answers 404 to any path it does not recognise, so an
# artifactory descriptor pointed under /mirror is a repository that is REACHABLE
# and says "not found" — rather than one that fails DNS. Two things that buys
# which an unreachable URL cannot:
#   * the unpinned fan-out COMPLETES. A non-not-found error aborts it ("never
#     silently drop a repo", spec §7.2), so with an unreachable secondary a test
#     can only assert that install failed — never what it resolved.
#   * MOCK_LOG records the probe, so "was the secondary dialed?" is directly
#     assertable in both directions, the way 52's _cd_excludes_gi asserts on gi.
# The /mirror path (rather than the bare mock base) keeps the descriptor's URL
# prefix disjoint from gi's download URLs, so the installer's per-repo auth
# matching (GIS-267) is unaffected and the log needle is unambiguous.
mock_secondary_url() { printf 'http://127.0.0.1:%s/mirror' "$MOCK_PORT"; }
assert_secondary_dialed()     { assert_contains     "/mirror/api/storage" "$(cat "$MOCK_LOG")"; }
assert_secondary_not_dialed() { assert_not_contains "/mirror/api/storage" "$(cat "$MOCK_LOG")"; }

# Dump a server's request log (debugging).
mock_dump_log() { [[ -n "${MOCK_LOG:-}" ]] && sed 's/^/    reg| /' "$MOCK_LOG" >&2; [[ -n "${OSV_LOG:-}" ]] && sed 's/^/    osv| /' "$OSV_LOG" >&2; [[ -n "${MAVEN_LOG:-}" ]] && sed 's/^/    mvn| /' "$MAVEN_LOG" >&2; return 0; }

# ---- fixtures builder (dogfoods `fglpkg pack` so served sha256s are real) ----
mock_build_fixtures() {  # mock_build_fixtures <dir>
  local dir="$1"; mkdir -p "$dir"
  local build; build="$(mktemp -d "$_SANDBOX_ROOT/fx.XXXXXX")"
  (
    cd "$build"
    cat > fglpkg.json <<'EOF'
{ "name":"demo.pkg","version":"1.0.0","description":"Demo package for functional tests",
  "genero":">=3.20","license":"MIT","repository":"https://github.com/example/demo",
  "author":"fglpkg tests","files":["*.42m"] }
EOF
    if command -v fglcomp >/dev/null 2>&1; then
      printf 'FUNCTION f()\nEND FUNCTION\n' > mod.4gl
      ( FGLLDPATH= fglcomp mod.4gl ) >/dev/null 2>&1 || printf 'stub' > mod.42m
    else
      printf 'stub' > mod.42m
    fi
    "$FGLPKG" pack -o "$dir/demo-pkg-1.0.0-genero6.zip" </dev/null >/dev/null 2>&1 || exit 1
    sed 's/"version":"1.0.0"/"version":"1.1.0"/' fglpkg.json > f2 && mv f2 fglpkg.json
    "$FGLPKG" pack -o "$dir/demo-pkg-1.1.0-genero6.zip" </dev/null >/dev/null 2>&1 || exit 1
  ) || { _diag "mock_build_fixtures: pack failed"; return 1; }
  cat > "$dir/packages.json" <<'EOF'
{
  "packages": [
    {
      "slug": "demo-pkg",
      "name": "demo.pkg",
      "description": "Demo package for functional tests",
      "genero": ">=3.20",
      "owner": { "partner_id": "mock", "name": "fglpkg tests" },
      "versions": [
        { "version":"1.0.0", "genero":">=3.20", "author":"fglpkg tests", "license":"MIT",
          "artifacts":[ { "variant":"genero6", "zip":"demo-pkg-1.0.0-genero6.zip" } ] },
        { "version":"1.1.0", "genero":">=3.20", "author":"fglpkg tests", "license":"MIT",
          "artifacts":[ { "variant":"genero6", "zip":"demo-pkg-1.1.0-genero6.zip" } ] }
      ]
    }
  ]
}
EOF
}

# Write a fglpkg.lock in cwd containing one JAR, for audit tests.
# mock_lock_with_jar <groupId> <artifactId> <version> [scope]
mock_lock_with_jar() {
  local g="$1" a="$2" v="$3" scope="${4:-}"
  local scopeline=""
  [[ -n "$scope" ]] && scopeline=", \"scope\": \"$scope\""
  cat > fglpkg.lock <<EOF
{
  "lockfileVersion": 1,
  "generatedAt": "2026-01-01T00:00:00Z",
  "generoVersion": "6.00.01",
  "root": { "name": "demo", "version": "1.0.0" },
  "packages": [],
  "jars": [
    { "key": "$g:$a", "groupId": "$g", "artifactId": "$a", "version": "$v"$scopeline }
  ]
}
EOF
}
