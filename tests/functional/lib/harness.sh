# lib/harness.sh — binary resolution, hermetic sandbox, run helpers, test runner.
# Sourced by run.sh (which sources assert.sh first).

# ---------- colors (only if stdout is a TTY) ----------
if [[ -t 1 ]]; then C_G=$'\033[32m'; C_R=$'\033[31m'; C_B=$'\033[1m'; C_D=$'\033[2m'; C_0=$'\033[0m'
else C_G=''; C_R=''; C_B=''; C_D=''; C_0=''; fi

# ---------- locate the fglpkg binary under test ----------
# Precedence: $FGLPKG_BIN → repo-relative bin/ (once moved in-repo) → dev checkout → PATH.
_detect_bin() {
  if [[ -n "${FGLPKG_BIN:-}" ]]; then printf '%s' "$FGLPKG_BIN"; return; fi
  local os arch name here
  case "$(uname -s)" in Darwin) os=darwin;; Linux) os=linux;; *) os=windows;; esac
  case "$(uname -m)" in x86_64|amd64) arch=amd64;; arm64|aarch64) arch=arm64;; *) arch=amd64;; esac
  name="fglpkg-$os-$arch"; [[ "$os" == windows ]] && name="$name.exe"
  here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
  local c
  for c in "$here/../../bin/$name" "$HOME/4js-github/fglpkg/bin/$name"; do
    [[ -x "$c" ]] && { printf '%s' "$c"; return; }
  done
  command -v fglpkg 2>/dev/null || printf ''
}
FGLPKG="$(_detect_bin)"

# ---------- sha256 (portable) ----------
sha256() {
  if command -v shasum >/dev/null 2>&1; then shasum -a256 "$1" | cut -d' ' -f1
  else sha256sum "$1" | cut -d' ' -f1; fi
}

# ---------- counters / state ----------
TESTS_RUN=0; TESTS_PASS=0; TESTS_FAIL=0; declare -a FAILED_NAMES=()
_SUITE=""
_SANDBOX_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/fglpkg-ft.XXXXXX")"

# ---------- hermetic sandbox (fresh HOME + workdir, scrubbed env) ----------
# Called INSIDE each test's subshell so exports/cwd never leak between tests and
# the real ~/.fglpkg (credentials, config) is never touched.
sandbox() {
  unset FGLPKG_TOKEN FGLPKG_REGISTRY FGLPKG_PUBLISH_REGISTRY FGLPKG_SIGNING \
        FGLPKG_ARTIFACTORY_URL FGLPKG_ARTIFACTORY_REPO FGLPKG_ARTIFACTORY_TOKEN \
        FGLPKG_AUDIT_URL FGLPKG_INSTALL_CONCURRENCY 2>/dev/null || true
  TESTHOME="$(mktemp -d "$_SANDBOX_ROOT/home.XXXXXX")"
  TESTWD="$(mktemp -d "$_SANDBOX_ROOT/wd.XXXXXX")"
  export FGLPKG_HOME="$TESTHOME"
  export FGLPKG_NO_UPDATE_CHECK=1                       # no background GitHub calls
  export FGLPKG_GENERO_VERSION="${FGLPKG_FT_GENERO:-6.00.01}"  # deterministic; override with FGLPKG_FT_GENERO
  cd "$TESTWD"
}

# ---------- run helpers (set $status and combined $output; never trip set -e) ----------
# Every fglpkg invocation goes through timed.py with a hard timeout, so a command
# that blocks on a prompt or a network call is killed (status 124) and fails its
# test instead of hanging the whole suite.
_LIBDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
_TIMED="$_LIBDIR/timed.py"
_FT_TIMEOUT="${FGLPKG_FT_TIMEOUT:-15}"

run() {  # run <fglpkg args...>   — no stdin
  output="$(python3 "$_TIMED" "$_FT_TIMEOUT" - "$FGLPKG" "$@" 2>&1)" && status=0 || status=$?
  return 0
}
run_in() {  # run_in "<stdin>" <fglpkg args...>
  local input="$1"; shift
  local f; f="$(mktemp "$_SANDBOX_ROOT/stdin.XXXXXX")"
  printf '%s' "$input" > "$f"
  output="$(python3 "$_TIMED" "$_FT_TIMEOUT" "$f" "$FGLPKG" "$@" 2>&1)" && status=0 || status=$?
  rm -f "$f"
  return 0
}
run_raw() {  # run any command (e.g. the toolchain), same capture contract + timeout
  output="$(python3 "$_TIMED" "$_FT_TIMEOUT" - "$@" 2>&1)" && status=0 || status=$?
  return 0
}
# run_split is run() with the streams kept apart, setting $out (stdout only) and
# $err (stderr only) alongside the usual $status and combined $output. Needed to
# assert the eval-safety contract of `fglpkg env`: its diagnostics must land on
# stderr, because stdout is fed straight to `eval` and, under --gst, must stay a
# strict VAR=value list.
run_split() {  # run_split <fglpkg args...>
  local ef; ef="$(mktemp "$_SANDBOX_ROOT/err.XXXXXX")"
  out="$(TIMED_STDERR="$ef" python3 "$_TIMED" "$_FT_TIMEOUT" - "$FGLPKG" "$@")" && status=0 || status=$?
  err="$(cat "$ef")"; rm -f "$ef"
  output="$out
$err"
  return 0
}

# ---------- platform helpers ----------
# Reports whether captured `fglpkg env` output is in cmd.exe `SET VAR=...;%VAR%`
# syntax, which a POSIX shell cannot eval. That is the DEFAULT on Windows.
#
# Prefer `env --local --shell sh` in new tests: asking for the shell the suite is
# actually running in (Git Bash, even on Windows) makes the eval round-trip work
# everywhere, which is why the two cases that used to skip on Windows no longer
# do. Note this predicate answers "did this output use the cmd shape", NOT "am I
# on Windows" — `--shell sh` produces `export` lines from a Windows binary.
env_output_is_windows_style() {  # env_output_is_windows_style "<captured output>"
  case "$1" in
    "SET "*|*$'\n'"SET "*) return 0 ;;
  esac
  return 1
}

# ---------- fixture helper: a minimal, valid, packable package in cwd ----------
mkpkg() {  # mkpkg [name] [version]
  local name="${1:-demo.pkg}" ver="${2:-1.0.0}"
  cat > fglpkg.json <<EOF
{
  "name": "$name",
  "version": "$ver",
  "description": "demo package for functional tests",
  "genero": ">=3.20",
  "license": "MIT",
  "repository": "https://github.com/example/demo",
  "author": "fglpkg functional tests",
  "files": ["*.42m"]
}
EOF
  if command -v fglcomp >/dev/null 2>&1; then
    printf 'FUNCTION f()\nEND FUNCTION\n' > mod.4gl
    ( FGLLDPATH= fglcomp mod.4gl ) >/dev/null 2>&1 || printf 'stub-pcode' > mod.42m
  else
    printf 'stub-pcode' > mod.42m
  fi
}

# ---------- test declaration ----------
suite() { _SUITE="$1"; printf '\n%s%s%s\n' "$C_B" "$1" "$C_0"; }

it() {  # it "description" <function-name>
  local desc="$1" fn="$2"
  TESTS_RUN=$((TESTS_RUN + 1))
  local log; log="$(mktemp "$_SANDBOX_ROOT/log.XXXXXX")"
  # The test subshell MUST be a plain command with $? read on the next line.
  # Bash suspends errexit for the whole dynamic extent of a command used as an
  # `if`/`while` condition, in a `&&`/`||` list, or under `!` — and that
  # suspension reaches inside the subshell and into the functions it calls, so
  # the body's own `set -e` becomes inert. Written as `if ( ... ); then`, a
  # failing assertion mid-body was silently ignored and the verdict came from
  # whatever the body's LAST command returned. run.sh deliberately does not set
  # -e, so a failing subshell here cannot abort the runner.
  ( set -eo pipefail; sandbox; "$fn" ) >"$log" 2>&1
  local rc=$?
  if [[ $rc -eq 0 ]]; then
    TESTS_PASS=$((TESTS_PASS + 1))
    printf '  %sok%s   %s\n' "$C_G" "$C_0" "$desc"
  else
    TESTS_FAIL=$((TESTS_FAIL + 1))
    FAILED_NAMES+=("${_SUITE} › ${desc}")
    printf '  %sFAIL%s %s\n' "$C_R" "$C_0" "$desc"
    sed "s/^/       ${C_D}│${C_0} /" "$log" >&2
  fi
  rm -f "$log"
}

# skip a test (documented, counts as neither pass nor fail)
skip() { printf '  %sskip%s %s  %s(%s)%s\n' "$C_D" "$C_0" "$1" "$C_D" "${2:-not yet implemented}" "$C_0"; }
