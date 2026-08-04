suite "harness self-check"

# Guards the harness's own contract: assert.sh promises that a failed assertion
# "aborts (and fails) the current test" (lib/assert.sh:2-3). That only holds if
# the per-test subshell actually honours `set -e`.
#
# It did not. Written as `if ( set -eo pipefail; sandbox; "$fn" ); then`, bash
# suspends errexit for the entire dynamic extent of the condition — including
# inside the subshell and the functions it calls — so a failing assertion
# mid-body was ignored and the verdict came from the body's LAST command. Green
# meant "the last line of each body returned 0", not "every assertion passed".
#
# These tests run a child harness so the contract is checked directly rather
# than inferred, and a regression fails here instead of silently turning some
# other suite's real failure into a pass.

_child_lib="$(cd "$(dirname "${BASH_SOURCE[0]}")/../lib" && pwd)"

# Runs a one-test child harness and leaves the combined output in $output.
# body_src is the shell source of the test function's body.
_run_child_harness() {  # _run_child_harness <body_src>
  mkdir -p childtmp
  cat > child.sh <<EOF
set -uo pipefail
source "$_child_lib/assert.sh"
source "$_child_lib/harness.sh"
_subject() { $1 ; }
it "subject" _subject
printf 'TALLY run=%s pass=%s fail=%s\n' "\$TESTS_RUN" "\$TESTS_PASS" "\$TESTS_FAIL"
EOF
  # Invoked with "$BASH" directly rather than through run_raw: run_raw shells
  # out via timed.py, and on Windows a bare "bash" there resolves to WSL's bash
  # instead of the Git Bash actually running this suite. The child is a handful
  # of shell commands, so it needs no timeout of its own — the fglpkg call it
  # makes is still wrapped by the child harness's own run().
  output="$(TMPDIR="$PWD/childtmp" "$BASH" child.sh 2>&1)" && status=0 || status=$?
}

# The regression itself: a failing assertion followed by a succeeding command.
# Before the fix this reported ok, because `echo` was the body's last command.
_mid_body_failure_fails() {
  _run_child_harness 'assert_eq "a" "b"; echo "REACHED-AFTER-FAILURE"' || return 1
  assert_contains "TALLY run=1 pass=0 fail=1" || return 1
  # errexit must abort at the assertion, so the trailing command never runs.
  assert_not_contains "REACHED-AFTER-FAILURE" || return 1
}
it "a mid-body assertion failure fails the test" _mid_body_failure_fails

# The fix must not invert the verdict: a body whose assertions all pass, and
# whose last command is deliberately quiet, still counts as a pass.
_passing_body_passes() {
  _run_child_harness 'assert_eq "a" "a"; true' || return 1
  assert_contains "TALLY run=1 pass=1 fail=0" || return 1
}
it "a body with no failing assertion still passes" _passing_body_passes

# A non-assertion command failing mid-body must also abort, so an unexpected
# tool or setup failure cannot be masked by a later succeeding line.
_plain_command_failure_fails() {
  _run_child_harness 'false; echo "REACHED-AFTER-FAILURE"' || return 1
  assert_contains "TALLY run=1 pass=0 fail=1" || return 1
  assert_not_contains "REACHED-AFTER-FAILURE" || return 1
}
it "a plain command failing mid-body fails the test" _plain_command_failure_fails

# run() and friends must stay errexit-safe: a non-zero fglpkg exit sets $status
# for the test to assert on, and must not abort the body by itself.
_run_helper_does_not_abort() {
  # pass=1 is the whole proof: had run()'s non-zero exit aborted the body, the
  # child test would have failed before reaching assert_failure. (The body's own
  # output cannot be asserted on here — it is logged and discarded on success.)
  _run_child_harness 'run --no-such-flag-xyz; assert_failure' || return 1
  assert_contains "TALLY run=1 pass=1 fail=0" || return 1
}
it "run() keeps a non-zero exit assertable instead of aborting" _run_helper_does_not_abort
