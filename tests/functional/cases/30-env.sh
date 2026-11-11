suite "env"

_env_global() {
  mkdir -p "$FGLPKG_HOME/packages/mypkg/com/fourjs/mypkg"
  run env --global
  assert_success
  assert_contains "FGLLDPATH"
  assert_contains_path "packages/mypkg"
}
it "env --global puts the installed package dir on FGLLDPATH" _env_global

_env_local() {
  mkdir -p ".fglpkg/packages/lpkg"
  run env --local
  assert_success
  assert_contains "FGLLDPATH"
  assert_contains "lpkg"
}
it "env --local emits FGLLDPATH for local packages" _env_local

_env_empty() {
  run env --global
  assert_success
  # Nothing installed in the global home → no package paths to set up, so the
  # output is empty. That is expected; the command must simply exit cleanly and
  # not emit a spurious FGLLDPATH pointing at an empty store.
  assert_not_contains "FGLLDPATH"
  # Same rule for every other managed variable: emit nothing rather than a
  # spurious empty export.
  assert_not_contains "FGLRESOURCEPATH"
  assert_not_contains "FGLDBPATH"
  assert_not_contains "FGLIMAGEPATH"
  assert_not_contains "FGLPROFILE"
}
it "env --global exits cleanly (no output) with an empty home" _env_empty

# With nothing installed, `fglpkg env` printed
#   export FGLLDPATH="${FGLLDPATH:+:$FGLLDPATH}"
# and exited 0 with nothing on stderr. That line adds no package path, turns an
# existing FGLLDPATH=/opt/app/lib into :/opt/app/lib (a leading ":" means the
# current directory to fglrun), and exports an empty value when it was unset.
#
# The corruption is the lesser half: `eval "$(fglpkg env)"` looked like it had
# worked, so the failure surfaced much later as -8403 Module does not exist
# (GIS-588). stdout must therefore be EMPTY and the explanation on stderr —
# which is why these cases split the streams instead of using `run`.
_env_empty_stdout_is_clean() {
  local out err
  out="$("$FGLPKG" env 2>/dev/null)" || true
  err="$("$FGLPKG" env 2>&1 >/dev/null)" || true

  [[ -z "$out" ]] || { _diag "stdout was not empty with nothing installed: $out"; return 1; }
  assert_not_contains "FGLLDPATH" "$out" || return 1
  assert_contains "no installed packages found" "$err" || return 1
}
it "env with nothing installed writes nothing to stdout" _env_empty_stdout_is_clean

# Evaluating the output must leave an existing FGLLDPATH untouched. This is the
# assertion that would have caught the corruption directly.
_env_empty_does_not_corrupt_fglldpath() {
  local before="/opt/app/lib" after
  after="$(FGLLDPATH="$before" bash -c 'eval "$('"$FGLPKG"' env 2>/dev/null)"; printf "%s" "$FGLLDPATH"')"
  assert_eq "$after" "$before" || return 1
}
it "env with nothing installed leaves an existing FGLLDPATH unchanged" _env_empty_does_not_corrupt_fglldpath

# An unset FGLLDPATH must stay unset rather than becoming set-but-empty.
_env_empty_leaves_unset_unset() {
  local state
  state="$(env -u FGLLDPATH bash -c 'eval "$('"$FGLPKG"' env 2>/dev/null)"; printf "%s" "${FGLLDPATH+set}"')"
  [[ -z "$state" ]] || { _diag "FGLLDPATH became set (empty) where it had been unset"; return 1; }
}
it "env with nothing installed leaves an unset FGLLDPATH unset" _env_empty_leaves_unset_unset

# Every shell spelling corrupted the variable in its own way, so none of them
# may emit the line.
_env_empty_quiet_for_every_shell() {
  local sh out
  for sh in sh cmd powershell; do
    out="$("$FGLPKG" env --shell "$sh" 2>/dev/null)" || true
    [[ -z "$out" ]] || { _diag "--shell $sh emitted: $out"; return 1; }
  done
}
it "env emits nothing on stdout for every shell when nothing is installed" _env_empty_quiet_for_every_shell

# The most likely way to hit this: isProjectDir() looks only at the current
# directory, so running from a project's subdirectory falls through to the
# merged default mode and finds nothing local. Naming the parent turns a silent
# no-op into a one-line fix.
_env_subdirectory_names_the_project_above() {
  printf '{ "name": "proj", "version": "1.0.0" }\n' > fglpkg.json
  mkdir -p src && cd src || return 1
  local err; err="$("$FGLPKG" env 2>&1 >/dev/null)" || true
  assert_contains "is not a project" "$err" || return 1
  assert_contains "cd " "$err" || return 1
}
it "env from a project subdirectory points at the project above" _env_subdirectory_names_the_project_above

# An explicit --global is the one mode that stays SILENT when nothing is
# installed. The documented profile line is `eval "$(fglpkg env --global)"` in
# ~/.bashrc (docs/user-guide.md), so for anyone whose packages are all
# project-local — an expected, correct state — an explanation here would print
# on every new shell. The GIS-588 failure is the default and --local modes,
# where the user is inside a project and does expect that project's packages.
_env_global_is_silent_with_an_empty_global_root() {
  local out err
  out="$("$FGLPKG" env --global 2>/dev/null)" || true
  err="$("$FGLPKG" env --global 2>&1 >/dev/null)" || true

  [[ -z "$out" ]] || { _diag "stdout was not empty: $out"; return 1; }
  [[ -z "$err" ]] || {
    _diag "stderr spoke on a plain --global with nothing installed, which the documented"
    _diag "shell-profile line runs on every new terminal: $err"
    return 1
  }
}
it "env --global says nothing when the global root is empty" _env_global_is_silent_with_an_empty_global_root

# ...while the default mode in the same empty state still explains itself, so
# the --global exemption cannot be widened into the silence GIS-588 reports.
_env_default_still_explains_itself() {
  local err; err="$("$FGLPKG" env 2>&1 >/dev/null)" || true
  assert_contains "no installed packages found" "$err" || return 1
}
it "env (default mode) still explains an empty result" _env_default_still_explains_itself
