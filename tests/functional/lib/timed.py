#!/usr/bin/env python3
"""Run a command with a hard timeout, combined stdout+stderr, optional stdin.

Usage: timed.py <timeout_seconds> <stdin_path|-> <cmd> [args...]
Exits with the child's return code, or 124 on timeout, 127 if not found.
Keeps the functional harness hang-proof: a command that blocks on a prompt or
a network call is killed and its test fails, instead of wedging the whole run.

Set TIMED_STDERR=<path> to keep the child's stderr SEPARATE, written to that
file instead of merged into stdout. `fglpkg env` puts its diagnostics on stderr
precisely so stdout stays safe to `eval`, and that contract is untestable while
the two streams are combined.
"""
import os, sys, subprocess

timeout = float(sys.argv[1])
stdin_path = sys.argv[2]
cmd = sys.argv[3:]

data = b""
if stdin_path != "-":
    with open(stdin_path, "rb") as f:
        data = f.read()

errfile = os.environ.get("TIMED_STDERR")
stderr_to = subprocess.PIPE if errfile else subprocess.STDOUT


def emit(out, err):
    sys.stdout.buffer.write(out or b"")
    if errfile:
        with open(errfile, "wb") as f:
            f.write(err or b"")


try:
    r = subprocess.run(cmd, input=data, stdout=subprocess.PIPE,
                       stderr=stderr_to, timeout=timeout)
    emit(r.stdout, r.stderr)
    sys.exit(r.returncode)
except subprocess.TimeoutExpired as e:
    emit(e.output, e.stderr)
    sys.stderr.write("\n[harness] TIMEOUT after %ss: %s\n" % (timeout, " ".join(cmd)))
    sys.exit(124)
except FileNotFoundError:
    sys.stderr.write("[harness] command not found: %s\n" % (cmd[0] if cmd else "?"))
    sys.exit(127)
