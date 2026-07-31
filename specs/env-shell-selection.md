# Spec: `fglpkg env` shell selection and value quoting

**Status:** ✅ Implemented — issue #60
**Date:** 2026-07-31
**Author:** Maximilian Harold
**Tracking:** Split out of [#58](https://github.com/4js-mikefolcher/fglpkg/issues/58) item 3; sibling of #59 (the other five items)
**Origin:** Reading `prependExportLine` for #58 surfaced that the PowerShell setup fglpkg documents cannot work, alongside the already-known unquoted-value limitation. Both are one decision about what `fglpkg env` emits and for which shell, so they are fixed together.

---

## Summary

`fglpkg env` chose its output shape from `runtime.GOOS` — `export VAR=…` on Unix,
`SET VAR=…;%VAR%` on Windows — with no way to ask for anything else. Two defects
followed:

1. The documented PowerShell setup was broken. `printUsage` told Windows users to
   run `fglpkg env --global | Invoke-Expression`, but the emitter produced cmd
   syntax, which PowerShell cannot execute.
2. Values were never quoted, so a path containing a space (or `&`, `|`, `^`)
   produced a statement the shell mis-parsed.

This change adds `--shell sh|powershell|cmd`, a real PowerShell emitter, and
per-shell quoting that engages only when a value needs it.

## Background — how it worked today

### The emitter

`prependExportLine` (`internal/env/env.go`) had two branches and no parameters
beyond the key and value:

```go
if runtime.GOOS == "windows" {
    return fmt.Sprintf("SET %s=%s;%%%s%%", key, value, key)
}
return fmt.Sprintf("export %s=%s\"${%s:+:%s}\"", key, value, key, "$"+key)
```

`gasHintComment` had the same shape on a smaller scale, prefixing its hint with
`REM ` on Windows and `# ` elsewhere.

### Defect 1 — PowerShell cannot run cmd syntax

Verified on Windows PowerShell 5.1.26100:

```
PS> 'SET FGLLDPATH=C:\demo\.fglpkg\merged;%FGLLDPATH%' | Invoke-Expression
The term '%FGLLDPATH%' is not recognized as the name of a cmdlet, ...

PS> "[$($env:FGLLDPATH)]"
[]                                     # never set

PS> Get-Variable -Name 'FGLLDPATH*'
FGLLDPATH=C:\demo\.fglpkg\merged        # junk PowerShell variable, as a side effect
```

`SET` is an alias for `Set-Variable`, not cmd's `set`, and `%VAR%` is not
expansion. The documented flow therefore threw, set nothing, and left a garbage
variable behind. It failed loudly on the first variable, which is some mercy, but
a PowerShell user had no working documented path and had to fall back to the
Command Prompt instructions and set variables by hand.

### Defect 2 — values were unquoted

The `KNOWN LIMITATION` comment on `prependExportLine` documented this, and #52
deferred it deliberately. Verified in cmd:

```
> set V=C:\Jane Doe\a&b;%V%
'b' is not recognized as an internal or external command
> echo %V%
C:\Jane Doe\a                           # truncated at the '&'
```

The resource variables added in #47/#52 widened the exposure: `FGLIMAGEPATH` and
`FGLRESOURCEPATH` point at asset directories, likelier than a module root to sit
under a spaced path.

## Goals

- A working, documented setup path for every shell fglpkg's users are actually in.
- Values that survive spaces and shell metacharacters.
- **No change for anyone who does not pass the new flag.** The output is wired into
  shell profiles, `.bat` files and Genero Studio settings.

## Non-goals

- Changing the default on Windows. It stays `cmd`.
- Autodetecting the parent shell. Fallible and hard to test; an explicit flag is
  better than a wrong guess.
- Quoting `--gwa` output. The `--webcomponent /path` lines have the same flaw and
  it should be fixed, but separately — this change is already a format decision.
- Touching `--gst`. Genero Studio output is a file format, not shell syntax.

## Proposed changes

### The shell/separator distinction

Two properties are easy to conflate, and separating them is what makes the
implementation correct:

- The **shell** is a property of whoever will EXECUTE the emitted line. Chosen by
  `--shell`, defaulting to `DefaultShell()`.
- The path **separator** is a property of the Genero runtime that will PARSE the
  value: `fglrun` splits `FGLLDPATH` on `;` on Windows and `:` elsewhere,
  whichever shell set it. It stays GOOS-derived in `pathSeparator()`.

The combination is real and load-bearing: the functional suite runs a
GOOS=windows `fglpkg.exe` under Git Bash, which is `sh` syntax with a `;`
separator. This also exposed a second latent bug — the POSIX emitter hardcoded `:`
in the inherited-value suffix `"${VAR:+:$VAR}"`, which is wrong under
`--shell sh` on Windows. `sep` is now threaded into the emitter, so the join
separator and the inherited-value separator cannot disagree.

### New file — `internal/env/shell.go`

`Shell` type, the three constants, alias table, `ShellValues`, `ParseShell`,
`DefaultShell`, `commentPrefix`, the quoting predicate and transforms,
`prependLine`, and `shellLimitWarnings`. No dependency on `Generator`, so it is
directly unit-testable — `prependExportLine` had no direct test at all, which is
how a broken PowerShell recommendation shipped.

Emitted forms:

| shell | form |
|---|---|
| `sh` | `export VAR=value"${VAR:+<sep>$VAR}"` |
| `powershell` | `$env:VAR = 'value' + $(if ($env:VAR) { '<sep>' + $env:VAR })` |
| `cmd` | `SET "VAR=value<sep>%VAR%"`, unquoted when the value is inert |

The PowerShell form needs no unset-vs-empty special case: `$env:X` is `$null` when
unset, PowerShell deletes an environment variable assigned `''`, and
`if ($env:X)` is false for both — exactly sh's `${X:+…}` semantics. Verified
through `| Invoke-Expression` on 5.1 for unset, `''` and a real value. Only
PowerShell 2.0+ syntax is used (no `??`, no ternary, no `-join`).

Rejected alternatives for that line: `-join` (non-obvious precedence against `+`,
fragile `$null` coercion) and `if/else` (duplicates the value, roughly doubling
line length against a variable that already warns near the 32 KB block limit).

### Quoting — only when needed, allowlist not denylist

Two rules:

1. **Quote only when needed.** A value of inert characters is emitted
   byte-for-byte as before. This is what keeps shipped profiles working.
2. **Allowlist.** A denylist is wrong by default for every character nobody
   thought of (`|`, `\n`, `*`, `?`, U+00A0). Over-cautious means correctly quoted.

`inertEverywhere` is alphanumerics plus `/._-:+,=@`. cmd additionally treats
`` \;~$#'[]{} `` as ordinary — notably `;`, which appears in every multi-entry
Windows value and is what preserves byte-identity for today's `SET` lines.
Printable non-ASCII is inert, so i18n paths keep their bytes; control and space
runes (NBSP, U+2028) are quoted because they are invisible in a diff.

Transforms: POSIX single-quote with `'\''` splicing; PowerShell single-quote with
`''` doubling; cmd `SET "VAR=…"`. PowerShell is always quoted — there is no
shipped unquoted form to stay compatible with, and one shape is one shape to
review.

### What cmd cannot represent

Contrary to the framing in #60, cmd *does* have usable quoting: `SET "VAR=value"`
handles spaces, `&`, `|`, `^`, `<`, `>`, `(`, `)` and a trailing space (verified).
So cmd stays first-class rather than being deprecated.

Two characters remain unrepresentable: a literal `%` (cmd expands it, and `%%` is
an escape only inside a `.bat` file, not at the prompt) and a literal `"`.
`shellLimitWarnings` reports these on stderr and the line is still emitted — the
text is still the user's best copy/paste source, and silently dropping a variable
is worse than emitting one the shell complains about.

### The cmd `FOR /F` recipe was already broken

Verified: `FOR /F "tokens=*" %i IN ('fglpkg env --global') DO %i` does not
re-expand a `%VAR%` that arrives on stdout — it lands as literal text, so the
inherited value is lost. A `.bat` file does expand it, because each line is parsed
as it executes. The documentation now says:

```cmd
fglpkg env --global --shell cmd > setup-env.bat
call setup-env.bat
```

This is independent of the emitter change; the recipe never worked as documented.

### CLI

env gets its own `parseEnvFlags` rather than extending the shared `parseFlags`,
which matches `extraAllowed` as exact tokens and so cannot express a
flag-with-value (`--shell=sh` would be rejected as an unknown flag). The shared
parser is used by ~10 commands and its reject-unknown-flag behaviour is a
deliberate safety property with its own regression test.

`--shell` combined with `--gst` or `--gwa` is an error: all three select an output
format, and this repo already errors on conflicting selectors of the same kind.
The check gates on an *explicit* `--shell`, not on `f.shell` — the GOOS default is
always present, so testing the value would make `--gst` fail on every platform.

## Test plan

### New tests

- `internal/env/shell_test.go` — `TestLegacyShellFormatsAreUnchanged` (the
  compatibility tripwire, pinning the exact bytes printed in the docs),
  `TestPrependLine` across all three shells (space, trailing space, `'`, `"`, `$`,
  backtick, `%`, cmd metacharacters, `!`, unicode, NBSP, empty value, Windows path
  under `sh`), `TestPrependLineEmbedsValueExactlyOnce`, `TestNeedsQuote`,
  `TestParseShell`, `TestDefaultShellIsGOOSDerived`,
  `TestShellValuesCoversEveryAlias`, `TestCommentPrefix`,
  `TestShellLimitWarnings`, `TestShellChangesWrapperNotValues`,
  `TestRawEnvIsNeverQuoted`.
- `internal/cli/env_flags_test.go` — both flag spellings, every alias, case
  folding, missing value, invalid value naming the accepted set, default when
  absent, the `--gst`/`--gwa` conflicts, and that `--gst` alone still works.
- `tests/functional/cases/164-env-resources.sh` — spaced-path eval round trip,
  `--shell` changes syntax not paths, invalid `--shell` rejected.
- `tests/functional/cases/165-env-profile.sh` — no stray separator when the
  variable is unset.

### Existing tests that MUST be updated

- `internal/env/resources_test.go` — `envValue` hardcoded both wrappers. It now
  derives them by rendering a sentinel through `prependLine` (`envLine`), so the
  helper cannot drift from the emitter and covers every shell. The quoted probe
  must be tried first: for sh, quoting only adds characters *inside* the value
  region, so a quoted line still matches the bare template and would yield a value
  with the wrapper's quotes attached. This also makes the tests survive a checkout
  under a spaced path.
- `TestFGLPROFILEEmitsDeclaredFilesInOrder` re-implemented the wrapper literally
  and branched on `runtime.GOOS`; it now asserts the ordering for all three shells
  with the marker derived from the emitter.
- The GST no-comment-lines assertion now loops over shells using
  `commentPrefix(sh)`.
- `internal/env/env_test.go` — a raw `"FGLIMAGEPATH="` substring probe would miss
  PowerShell's `$env:FGLIMAGEPATH = ` spelling; switched to `envValue`.
- `tests/functional/` — `164`/`165` passed `--shell sh` and **dropped their Windows
  skips**, turning two dead assertions into live coverage. `165`'s ordering check
  had to normalise separators first; it had only ever run on POSIX, so its
  `packages/poiapi` pattern never matched a backslash path.
- `assert_not_contains "#"` on GST stdout replaced with a line-anchored
  `assert_not_match '^(#|REM )'`; the old form also fired on any sandbox path
  containing a `#`.

## Acceptance criteria

- `fglpkg env --global --shell powershell | Invoke-Expression` sets the variables
  in Windows PowerShell 5.1, preserves an existing value after the package paths,
  and leaves no stray PowerShell variables. ✅ verified
- `fglpkg env --shell cmd > setup-env.bat && call setup-env.bat` sets the
  variables and preserves an existing value. ✅ verified
- `eval "$(fglpkg env --local --shell sh)"` works under Git Bash on Windows. ✅
- A project under a directory with a space in its name round-trips through every
  shell. ✅
- Output is byte-identical to the previous release for a path with no special
  characters, on stdout and stderr, for `env`, `--local`, `--global`, `--gst` and
  `--gwa`. ✅ verified by diffing against a binary built from `main`
- `fglpkg bdl` still resolves package resources — no quote character reaches
  `cmd.Env`. ✅ guarded by `TestRawEnvIsNeverQuoted`

## Risks & backward compatibility

- **Output format.** Mitigated by quote-only-when-needed plus an unchanged
  GOOS-derived default, and locked by `TestLegacyShellFormatsAreUnchanged` plus a
  diff against `main`. A path that *does* contain a space changes shape — but its
  old shape was broken, which is the point.
- **`fglpkg env somejunk` and `fglpkg env --force` now error** where the shared
  parser accepted and silently ignored them. Intended (it matches
  `audit`/`sbom`), but `--force` is in the universal completion list so it was
  tab-completable.
- **cmd leaves a stray separator when the variable is unset** (a literal `%VAR%`
  at the prompt, an empty expansion in a `.bat`, hence a trailing `;`).
  Pre-existing and unchanged by quoting; the new quoted form only makes it look
  deliberate. Fixing it needs two statements per variable (`if defined VAR …`),
  breaking the one-line-per-variable contract that the test helpers and users'
  greps rely on. Documented, not fixed.
- **PowerShell deletes a variable assigned `''`**, so the empty-`FGLLDPATH`
  `alwaysLD` line leaves an unset variable unset — the same leading-separator
  quirk sh has always had. The three emitters are kept uniform rather than
  special-casing one.
- **`env_output_is_windows_style` is no longer an "am I on Windows" probe**, since
  `--shell sh` produces `export` lines from a Windows binary. It only ever
  answered "did this output use the cmd shape"; its remaining uses are narrow.

## Cross-references

- #58 — the follow-up list this was split out of.
- #59 — the other five items; deliberately does not touch this.
- #47 / #52 — added the resource variables that widened the quoting exposure, and
  deferred the fix.
- `todo/windows-integration-plan.md` — its "Help text assumes bash" item proposed
  the `| Invoke-Expression` recipe that this change proves does not work without a
  PowerShell emitter, and its "Already Handled" entry for `fglpkg env` was stale.
