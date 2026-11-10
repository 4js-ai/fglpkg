# Spec: character encoding and length semantics for packages

**Status:** ✅ Implemented 2026-11-10 — GIS-285 (branch `feat/gis-285-character-encoding`)
**Date:** 2026-09-28
**Author:** Mike Folcher
**Motivation:** `fglcomp`/`fglform` write string literals into `.42m`/`.42f`/`.42s` in the encoding
of the build locale, so a package built under UTF-8 produces mojibake — or worse, silently wrong
string lengths — when consumed under ISO-8859-15, and vice versa. Nothing in `fglpkg.json` records
what a package was built with, so neither the publisher nor the consumer can detect the mismatch.
**Related:** [fglpkg-prepublish-validation.md](fglpkg-prepublish-validation.md) (where the publish
gate lives), [import-root.md](import-root.md) (the staging walk this hooks into).
**Reported by:** Sebastien Flaesch, with discussion from Leo Schubert on GIS-285.

---

## Summary

Record the character encoding a package was built with, and the length semantics it requires, in
`fglpkg.json`. **Record only what the staged artifacts prove**, cross-check any declaration the
author did write, and gate consumption on a *verified* mismatch only.

> **Revised 2026-11-10 (PR #107 review).** The first design derived the value from a byte scan of
> every artifact and fell back to the packer's `fglrun -i` where that was undecidable. Measuring it
> against real compiled output showed the premise was wrong for `.42m` and `.42f`, and that the
> fallback was the common path rather than the rare one. The measurements are in Background below;
> the corrected design follows. The parts that were sound — the install gate,
> `--force-character-encoding`, `lengthSemantics`, the manifest validation and the `FGLPKG_CHARMAP`
> overrides — are unchanged.

## Background — what the artifacts actually contain

Measured against Genero BDL 6.00.01 (`fglrun 6.00.01 rev-53231f0`).

### A `.42m`'s own structure contains high bytes

`fglcomp` copies string literal bytes through unchanged, so a non-ASCII literal does reach the
p-code. But the container is not otherwise ASCII: opcode operands and the embedded source-path
length also set the high bit.

| Measurement | Result |
|---|---|
| Real `.4gl`/`.42m` pairs across four projects, sources pure ASCII | 99 |
| ...whose `.42m` also has no byte >= 0x80 | **21 (21%)** |
| A realistic pure-ASCII module (record, array, loop, `SFMT`) | 1 high byte, `0xdf`, an opcode operand at offset 685 |
| The same source compiled under `LANG=C`, `en_US.UTF-8`, `en_US.ISO8859-15` | **byte-identical** (same SHA-256) |

So for a `.42m`: **no high byte proves the text is ASCII; a high byte proves nothing**, and the
file records no charset of its own.

### A `.42f` prolog names the build locale, not the content

`fglform` writes an XML prolog carrying an IANA name — but it is the *build locale*, not a
description of the form:

| Build locale of a **pure-ASCII** `.per` | `.42f` prolog | High bytes in the `.42f` |
|---|---|---|
| `C` / `POSIX` | `ASCII` | 0 |
| `en_US.UTF-8` | `UTF-8` | 0 |
| `en_US.ISO8859-15` | `ISO-8859-15` | 0 |

So the prolog is authoritative only about bytes that are actually present. Read unconditionally, it
labelled an ASCII-only package `UTF-8` and failed a correct `"characterEncoding": "US-ASCII"`
declaration.

### A `.42s` is a clean container

`fglmkstr` output behaves as the first design assumed, and is the one artifact whose bytes are
reliable evidence:

| `.str` source | `.42s` high bytes | Decodes as UTF-8 |
|---|---|---|
| ASCII | 0 | — |
| UTF-8 | 4 | yes |
| ISO-8859-15 | 2 | no |

Byte-identical across build locales, so the scan is reproducible.

The runtime uses a `.42s` as its bytes stand, with **no transcoding**: a UTF-8 resource under an
ISO-8859-15 runtime shows raw bytes and reports `LENGTH` 12 for a 10-character string, and a
Latin-9 resource under a UTF-8 runtime is truncated at the first invalid byte. So a resource's
charset is a real constraint on the consumer that *selects* it.

### The runtime is the only source for length semantics

`fglrun -i` reports `Charmap`, `Multibyte`, `Stateless` and `Length Semantics`. **`fglcomp -i`
reports the first three but not Length Semantics.**

`FGL_LENGTH_SEMANTICS=CHAR` is silently reported as `BYTE` under a single-byte charmap:

```
LANG=en_US.UTF-8 FGL_LENGTH_SEMANTICS=CHAR  → Charmap: UTF-8  Length Semantics: CHAR
LANG=C           FGL_LENGTH_SEMANTICS=CHAR  → Charmap: ASCII  Length Semantics: BYTE
```

So the two properties are **not independent**: declaring `lengthSemantics: "char"` implicitly
declares a multibyte charset.

### Charmap names Genero reports

`$FGLDIR/etc/charmap.alias` enumerates the 70 canonical names Genero knows, which splits cleanly
into 52 single-byte and 18 multibyte. IANA charset names are case-insensitive, which absorbs
`SHIFT_JIS`; only `ASCII` ↔ `US-ASCII` needs an explicit alias.

Verified: `fglrun -i` under `zh_CN.GBK` reports `Charmap: GBK`, `Multibyte: yes`. So a charset
fglpkg does not recognise must **not** be assumed single-byte — doing so rejected the satisfiable
declaration `"characterEncoding": "GBK", "lengthSemantics": "char"`.

## Decision

**1. Record only what is proven.** `pack` writes a value only where the staged artifacts establish
it, and leaves the field absent otherwise. Absent means no consumer-side check, which is what every
package published before this feature already does.

**2. Never consult the build environment at pack time.** The `fglrun -i` fallback made the label a
property of the packer's shell: byte-identical artifacts installed everywhere when packed under
`LANG=C` and were refused by ISO-8859-15 consumers when packed under `en_US.UTF-8`. That is the
environment dependence the feature existed to remove, relocated to the consumer side.

**3. Separate code from string resources.** `characterEncoding` describes the code (`.42m`/`.42f`).
String resources get their own per-file field. Merging them meant a package following the
documented practice — pure-ASCII code, localized text in `.str` — was recorded as non-portable and
refused on exactly the single-byte consumers the practice exists to serve.

**4. Warn on resources; deny only on a verified code mismatch.**

### Rationale for deriving rather than checking the environment

GIS-285 originally proposed denying publication when the declared encoding does not match the
publisher's `fglrun -i`. That couples correctness to the publisher's shell and fails in both
directions: a CI publish typically runs `LANG=C` → Charmap `ASCII`, so a genuinely UTF-8 package
would be rejected; and an all-ASCII package built on a UTF-8 workstation would be forced to declare
`UTF-8`, locking out every ISO-8859 consumer for artifacts that are in fact portable.

That reasoning still stands, and is why the fallback had to go as well: it reintroduced the same
coupling by the back door.

## Manifest schema

```jsonc
{
  "characterEncoding": "US-ASCII",              // IANA name; the CODE's text
  "lengthSemantics": "char",                    // "char" | "byte"; author-declared only
  "stringResourceEncodings": {                  // per .42s carrying non-ASCII text
    "res/Messages.utf8.42s": "UTF-8",
    "res/Messages.latin9.42s": "ISO-8859-15"
  }
}
```

- **`characterEncoding`** — IANA charset name, compared case-insensitively with `ASCII` accepted as
  an alias of `US-ASCII`. Describes `.42m`/`.42f` only.
- **`lengthSemantics`** — `"char"` or `"byte"`. Author-declared only: it is a property of the code's
  *behaviour* (Leo's `getCharAt` / sorting example), not of its bytes. Absent means "no constraint".
- **`stringResourceEncodings`** — per-file, derived, advisory. An ASCII resource is omitted, since it
  constrains nothing. An author may declare an entry for a resource whose 8-bit charset bytes
  cannot name.

`Validate()` rejects an unknown `lengthSemantics` value, and rejects `lengthSemantics: "char"`
alongside a charset **known** to be single-byte. Unknown charsets are accepted: not recognising a
name is not evidence.

## Derivation algorithm (`pack`)

Run over the staged tree, after `stageBDLFiles`.

**Code artifacts (`.42m`, `.42f`).** Each one is classified as *ASCII*, *a named encoding*, or
*unproven*:

1. No byte >= 0x80 → **ASCII**. For a `.42f` this settles it regardless of the prolog.
2. A `.42f` with high bytes → its **prolog** names that text's encoding. Two forms naming different
   charsets is an error. A form with high bytes and no prolog that decodes as UTF-8 → `UTF-8`.
3. Otherwise, look for the **source beside the artifact** (`.4gl` for `.42m`, `.per` for `.42f`). A
   pure-ASCII source proves the artifact carries no non-ASCII text, because literals are copied
   through. This is what makes the documented practice provable; the p-code cannot do it.
4. Otherwise → **unproven**.

Then: every code artifact ASCII → `US-ASCII`; a form named an encoding → that encoding; any
unproven artifact → **no value**, with a note suggesting a declaration.

**String resources (`.42s`).** Byte scan per file: no high bytes → omitted; high bytes decoding as
UTF-8 → `UTF-8`; otherwise → undecidable, recorded only if the author declared it, with a note.

**Cross-check.** A declared `characterEncoding` fails only against a contradiction that is actually
provable: a form carrying non-ASCII text whose prolog names a different charset. The two checks that
were removed each rejected correct packages — "a high byte disproves US-ASCII" (78 of 99 real
pure-ASCII modules have one) and "any prolog is authoritative" (a pure-ASCII `.per` under a UTF-8
shell). A declared resource encoding fails when the file's bytes disprove it.

## Consume-time behaviour (`install`)

Let `pkg` be the package's `characterEncoding` and `env` the consumer's `fglrun -i` Charmap.

| `pkg` | Action |
|---|---|
| absent | proceed; no message |
| `US-ASCII` | proceed; no check — compatible with every Genero charset |
| equals `env` (case-insensitive, `ASCII`≈`US-ASCII`) | proceed |
| differs from `env` | **error**; suggest `fglpkg install --force-character-encoding` |

`lengthSemantics`, when present, must equal the consumer's Length Semantics, else the same error.

An unreadable environment — `fglrun` absent, **or present but reporting no charmap** — warns rather
than refusing. The second case matters: `DetectRuntimeInfo` returns no error when only
`FGLPKG_LENGTH_SEMANTICS` is set, and an empty charmap then "mismatched" every declaration, refusing
with "this environment uses " and nothing after it.

`stringResourceEncodings` **warns and never refuses**, naming each resource this environment cannot
read. A package may ship one resource per language and charset for the consumer to select at
runtime, so refusing the package would block a consumer about to select a different one.

The escape hatch names the command, `fglpkg install --force-character-encoding`, because the same
refusal fires during `fglpkg update`, which rejects the bare flag.

### Migration

Packages published before this field declare nothing, so the `absent` row stays silent. Because
`pack` now records a value only where it is proven, a package packed from a dist directory with no
sources also records nothing — which is correct, and no worse than the status quo.

## `init`

`fglpkg init` does **not** write `characterEncoding`. At `init` there are no artifacts, and the
value would be a guess from the author's current shell.

## Testing

Detection must not depend on the machine's locale or the suite becomes unrunnable in CI, so
`FGLPKG_CHARMAP` and `FGLPKG_LENGTH_SEMANTICS` override it, mirroring `FGLPKG_GENERO_VERSION`.

**Fixtures must be real compiled output, committed as bytes.** Source text written into a file named
`.42m` never contains the structure high bytes that real p-code carries, which is precisely the case
the first design got wrong — a suite built on fake fixtures agreed with a byte scan that disagrees
with 78 of 99 real modules. The committed set (`internal/cli/testdata/encoding`,
`tests/functional/fixtures/encoding`) covers: a realistic pure-ASCII module whose p-code has a
structure high byte, a tiny one whose p-code is clean, a module with a genuine UTF-8 literal, a
pure-ASCII form built under UTF-8 (prolog `UTF-8`, no high bytes), a form really carrying non-ASCII
text, and ASCII / UTF-8 / Latin-9 resource files. Each artifact is kept beside its source.

Unit: the three-tier byte classifier (empty, pure ASCII, valid UTF-8, invalid UTF-8, a UTF-8 sequence
split across a read boundary); prolog parsing; IANA comparison; the single-byte/multibyte/unknown
split; `Validate()`.

Functional: pack proves `US-ASCII` from a source beside real p-code; records nothing without
evidence; records the same thing under every `FGLPKG_CHARMAP`; a pure-ASCII form with a UTF-8 prolog
is `US-ASCII`; a form with real non-ASCII text takes its prolog; ASCII code plus a UTF-8 `.42s` stays
portable with the resource named; a correct `US-ASCII` declaration survives all three; a contradicted
declaration fails; install refuses a code mismatch, accepts `--force-character-encoding`, warns on a
resource mismatch, and is silent for a package that declares nothing — asserted on the shipped
manifest, not assumed.

## Non-goals

- Guessing *which* 8-bit charset an artifact uses. `ISO-8859-1` and `-15` are indistinguishable from
  bytes; only a declaration can resolve it.
- Transcoding artifacts. fglpkg reports incompatibility; it never rewrites p-code.
- Deriving `lengthSemantics`. It is a property of behaviour, not bytes.
- Parsing sources. A source is read only as a whole-file byte scan, to prove the absence of
  non-ASCII text. A non-ASCII source proves nothing, because the high bytes may be in comments, and
  telling a comment from a literal would mean parsing BDL.

## Open questions

1. Should a *source-only* package (GIS-295) be scanned for `.4gl` encoding too? Deferred until the
   source-only semantics land.
2. Should the registry index `characterEncoding` so `search` can filter to packages compatible with
   the caller's charmap? Server-side; out of scope here.
