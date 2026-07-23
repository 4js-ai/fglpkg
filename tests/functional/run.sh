#!/usr/bin/env bash
# fglpkg functional test runner.
#
#   ./run.sh                 # run all cases
#   ./run.sh pack            # run only cases whose filename matches "pack"
#   FGLPKG_BIN=/path/to/fglpkg ./run.sh
#   KEEP=1 ./run.sh          # keep the sandbox dir for debugging
#
# Exit code: 0 all passed, 1 one or more failed, 2 setup error.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/lib/assert.sh"
source "$HERE/lib/harness.sh"
source "$HERE/lib/mock.sh"

if [[ -z "$FGLPKG" || ! -x "$FGLPKG" ]]; then
  echo "ERROR: fglpkg binary not found. Set FGLPKG_BIN=/path/to/fglpkg." >&2
  exit 2
fi
if ! "$FGLPKG" version >/dev/null 2>&1; then
  echo "ERROR: '$FGLPKG version' did not run on this machine." >&2
  exit 2
fi

cleanup() { [[ "${KEEP:-0}" == 1 ]] || rm -rf "$_SANDBOX_ROOT"; }
trap cleanup EXIT

printf '%sfglpkg functional tests%s\n' "$C_B" "$C_0"
printf '  binary : %s\n' "$FGLPKG"
printf '  version: %s\n' "$("$FGLPKG" version 2>/dev/null | head -1)"
printf '  sandbox: %s  (FGLPKG_HOME per-test; FGLPKG_NO_UPDATE_CHECK=1; FGLPKG_GENERO_VERSION=%s)\n' \
  "$_SANDBOX_ROOT" "${FGLPKG_FT_GENERO:-6.00.01}"
[[ -n "${1:-}" ]] && printf '  filter : *%s*\n' "$1"

filter="${1:-}"
shopt -s nullglob
for f in "$HERE"/cases/*.sh; do
  [[ -n "$filter" && "$f" != *"$filter"* ]] && continue
  # shellcheck disable=SC1090
  source "$f"
done

printf '\n%s────────────────────────────────────────────%s\n' "$C_D" "$C_0"
printf '%sTotal %d   %sPassed %d%s   %sFailed %d%s\n' \
  "$C_B" "$TESTS_RUN" "$C_G" "$TESTS_PASS" "$C_0" \
  "$([[ $TESTS_FAIL -gt 0 ]] && printf '%s' "$C_R")" "$TESTS_FAIL" "$C_0"

if (( TESTS_FAIL > 0 )); then
  printf '\nFailures:\n'
  for n in "${FAILED_NAMES[@]}"; do printf '  - %s\n' "$n"; done
  exit 1
fi
[[ $TESTS_RUN -eq 0 ]] && { echo "no tests matched"; exit 2; }
exit 0
