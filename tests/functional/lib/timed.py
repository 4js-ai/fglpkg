#!/usr/bin/env python3
"""Run a command with a hard timeout, combined stdout+stderr, optional stdin.

Usage: timed.py <timeout_seconds> <stdin_path|-> <cmd> [args...]
Exits with the child's return code, or 124 on timeout, 127 if not found.
Keeps the functional harness hang-proof: a command that blocks on a prompt or
a network call is killed and its test fails, instead of wedging the whole run.
"""
import sys, subprocess

timeout = float(sys.argv[1])
stdin_path = sys.argv[2]
cmd = sys.argv[3:]

data = b""
if stdin_path != "-":
    with open(stdin_path, "rb") as f:
        data = f.read()

try:
    r = subprocess.run(cmd, input=data, stdout=subprocess.PIPE,
                       stderr=subprocess.STDOUT, timeout=timeout)
    sys.stdout.buffer.write(r.stdout or b"")
    sys.exit(r.returncode)
except subprocess.TimeoutExpired as e:
    if e.output:
        sys.stdout.buffer.write(e.output)
    sys.stderr.write("\n[harness] TIMEOUT after %ss: %s\n" % (timeout, " ".join(cmd)))
    sys.exit(124)
except FileNotFoundError:
    sys.stderr.write("[harness] command not found: %s\n" % (cmd[0] if cmd else "?"))
    sys.exit(127)
