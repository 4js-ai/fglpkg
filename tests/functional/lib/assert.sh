# lib/assert.sh — tiny assertion library for the fglpkg functional suite.
# Every assertion prints a diagnostic to stderr and `return 1` on failure; under
# the per-test `set -e` subshell that aborts (and fails) the current test.

_diag() { printf 'assert: %s\n' "$*" >&2; }

# --- exit status (uses $status set by run()) ---
assert_success() { [[ "${status:-1}" -eq 0 ]] || { _diag "expected exit 0, got ${status:-?}"; _diag "output: ${output:-}"; return 1; }; }
assert_failure() { [[ "${status:-0}" -ne 0 ]] || { _diag "expected non-zero exit, got 0"; _diag "output: ${output:-}"; return 1; }; }
assert_exit()    { [[ "${status:-}" -eq "$1" ]] || { _diag "expected exit $1, got ${status:-?}"; _diag "output: ${output:-}"; return 1; }; }

# --- output / string content (default haystack = $output) ---
assert_contains() {
  local needle="$1" hay="${2-$output}"
  [[ "$hay" == *"$needle"* ]] || { _diag "expected to contain: $needle"; _diag "in:"; printf '%s\n' "$hay" >&2; return 1; }
}
assert_not_contains() {
  local needle="$1" hay="${2-$output}"
  [[ "$hay" != *"$needle"* ]] || { _diag "expected NOT to contain: $needle"; _diag "in:"; printf '%s\n' "$hay" >&2; return 1; }
}
assert_match() {  # extended-regex match against $output (or $2)
  local re="$1" hay="${2-$output}"
  grep -Eq -- "$re" <<<"$hay" || { _diag "expected to match /$re/"; _diag "in:"; printf '%s\n' "$hay" >&2; return 1; }
}
assert_eq() { [[ "$1" == "$2" ]] || { _diag "expected equal: [$1] == [$2]"; return 1; }; }

# --- path content (separator-agnostic) ---
# Write the needle with forward slashes; both it and the haystack are normalised
# before comparing, so one assertion covers the POSIX (a/b) and Windows (a\b)
# rendering of the same path.
assert_contains_path() {
  local needle="${1//\\//}" hay="${2-$output}"
  assert_contains "$needle" "${hay//\\//}"
}
assert_not_contains_path() {
  local needle="${1//\\//}" hay="${2-$output}"
  assert_not_contains "$needle" "${hay//\\//}"
}

# --- filesystem ---
assert_file()     { [[ -f "$1" ]] || { _diag "file not found: $1"; return 1; }; }
assert_dir()      { [[ -d "$1" ]] || { _diag "dir not found: $1"; return 1; }; }
assert_no_file()  { [[ ! -e "$1" ]] || { _diag "expected absent: $1"; return 1; }; }
assert_file_contains() { assert_file "$1" || return 1; grep -q -- "$2" "$1" || { _diag "file $1 missing: $2"; return 1; }; }

# --- JSON (python3) ---
assert_json() {  # valid JSON file
  python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "$1" 2>/dev/null \
    || { _diag "not valid JSON: $1"; return 1; }
}
assert_json_field() {  # assert_json_field <file> <dotted.path> <expected>
  local got
  got="$(python3 - "$1" "$2" <<'PY' 2>/dev/null
import json,sys
d=json.load(open(sys.argv[1]))
for k in sys.argv[2].split('.'):
    d = d[int(k)] if isinstance(d,list) else d[k]
print(d)
PY
)" || { _diag "cannot read $2 from $1"; return 1; }
  [[ "$got" == "$3" ]] || { _diag "$1: $2 = [$got], expected [$3]"; return 1; }
}
