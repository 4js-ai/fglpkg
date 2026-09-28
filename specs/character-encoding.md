# Spec: character encoding and length semantics for packages

**Status:** 📋 Not started — GIS-285 (spec ready; ticket was *Require More Analysis*)
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
`fglpkg.json`. **Derive both from the staged artifacts rather than asking the author to declare
them**, cross-check any declaration the author did write, and gate consumption on a *verified*
mismatch only.

The central finding behind this design: the encoding is largely recoverable **from the artifacts
themselves**, so `pack` can establish it as a fact rather than trusting either the author or the
shell they happened to publish from.

## Background — what the artifacts actually contain

Measured against Genero BDL 6.00.01 (`fglrun 6.00.01 rev-53231f0`).

### Compiled forms declare their encoding outright

`fglform` writes an XML prolog carrying the build encoding, already spelled as an IANA name:

| Build locale | `.42f` prolog |
|---|---|
| `en_US.UTF-8` | `<?xml version='1.0' encoding='UTF-8'?>` |
| `en_US.ISO8859-15` | `<?xml version='1.0' encoding='ISO-8859-15'?>` |
| `C` / `POSIX` | `<?xml version='1.0' encoding='ASCII'?>` |

This is authoritative and needs no heuristics.

### P-code and compiled strings carry literal bytes verbatim

`fglcomp` copies string literal bytes through unchanged. Compiling `DISPLAY "café naïve €"`:

| Source encoding | High bytes (≥ 0x80) in `.42m` |
|---|---|
| ASCII-only source | 0 |
| UTF-8 | 7 (`é`=2, `ï`=2, `€`=3) |
| ISO-8859-15 | 3 (one per character) |

`.42s` (from `fglmkstr`) behaves the same way: binary container, literal bytes passed through, no
encoding declaration.

So `.42m`/`.42s` classify into three tiers by a byte scan:

| Bytes ≥ 0x80 | Decodes as UTF-8? | Conclusion |
|---|---|---|
| none | — | **ASCII** — portable to every Genero charset |
| present | yes | **UTF-8** |
| present | no | **some 8-bit charset** — `ISO-8859-1` vs `-15` is undecidable from bytes |

### The runtime is the only source for length semantics

`fglrun -i` reports `Charmap`, `Multibyte`, `Stateless` and `Length Semantics`. **`fglcomp -i`
reports the first three but not Length Semantics.** fglpkg today detects the Genero version with
`fglcomp --version` and only ever *locates* `fglrun` (`genero.FglrunPath`), never runs it — so this
is new plumbing.

`FGL_LENGTH_SEMANTICS=CHAR` is silently reported as `BYTE` under a single-byte charmap:

```
LANG=en_US.UTF-8 FGL_LENGTH_SEMANTICS=CHAR  → Charmap: UTF-8  Length Semantics: CHAR
LANG=C           FGL_LENGTH_SEMANTICS=CHAR  → Charmap: ASCII  Length Semantics: BYTE
```

This settles the Leo/Sebastien thread mechanically: the two properties are **not independent**.
Declaring `lengthSemantics: "char"` implicitly declares a multibyte charset.

### Charmap names Genero reports

| Locale | `Charmap` | IANA preferred |
|---|---|---|
| `C`, `POSIX`, `*.US-ASCII` | `ASCII` | **`US-ASCII`** (`ASCII` is a registered alias) |
| `*.UTF-8` | `UTF-8` | `UTF-8` |
| `*.ISO8859-1` / `-15` | `ISO-8859-1` / `ISO-8859-15` | same |
| `zh_TW.Big5` | `Big5` | `Big5` |
| `ja_JP.SJIS` | `SHIFT_JIS` | **`Shift_JIS`** (case differs) |
| `ja_JP.eucJP` | `EUC-JP` | `EUC-JP` |
| `ru_RU.KOI8-R` | `KOI8-R` | `KOI8-R` |

Near-identity, with two wrinkles. IANA charset names are **case-insensitive**, which absorbs
`SHIFT_JIS`; only `ASCII` ↔ `US-ASCII` needs an explicit alias.

## Decision (2026-09-28)

**1. Derive, don't demand.** `pack` derives `characterEncoding` from the staged artifacts and writes
it into the shipped manifest. An author-written value is *cross-checked*, not trusted. This is
deliberately the inverse of checking the declaration against `fglrun -i` at publish time — see
Rationale.

**2. Warn by default; deny only on a verified mismatch.** A conflict that is *provable from bytes*
is an error. Anything unknown or unprovable is a warning.

### Rationale for deriving rather than checking the environment

GIS-285 originally proposed denying publication when the declared encoding does not match the
publisher's `fglrun -i`. That couples correctness to the publisher's shell and fails in both
directions:

- **CI publishes are usually `LANG=C` → Charmap `ASCII`.** A genuinely UTF-8 package published from
  CI would be rejected outright.
- **An all-ASCII package built on a UTF-8 workstation** would be forced to declare `UTF-8`,
  needlessly locking out every ISO-8859 consumer for artifacts that are in fact portable.

Deriving from bytes has neither failure mode, and makes the documented best practice — ship pure
ASCII — *mechanically provable* rather than merely advised. Most packages land in the ASCII tier
and then need no consumer-side check at all.

## Manifest schema

Two new optional fields. Named in camelCase to match the existing schema (`importRoot`,
`defaultRegistry`, `mavenMirror`, `optionalDependencies`) rather than the hyphenated form sketched
on the ticket.

```jsonc
{
  "characterEncoding": "US-ASCII",   // IANA name; derived by pack
  "lengthSemantics": "char"          // "char" | "byte"; author-declared only
}
```

- **`characterEncoding`** — IANA charset name. Compared case-insensitively, with `ASCII` accepted as
  an alias of `US-ASCII`. Written canonically as the IANA preferred name.
- **`lengthSemantics`** — `"char"` or `"byte"`, case-insensitive on read. **Author-declared only:**
  it cannot be derived, because it is a property of the code's *behaviour* (Leo's `getCharAt` /
  sorting example), not of its bytes. Absent means "no constraint", per Sebastien's resolution.

`Validate()` rejects an unknown `lengthSemantics` value, and rejects
`lengthSemantics: "char"` alongside a single-byte `characterEncoding` — the runtime would silently
downgrade it to `BYTE`, so the declaration could never hold.

## Derivation algorithm (`pack`)

Run over the staged tree, after `stageBDLFiles`:

1. For each `.42f`, read the XML prolog encoding. Authoritative.
2. For each `.42m` / `.42s`, scan for bytes ≥ 0x80; if any, test whether the whole file decodes as
   UTF-8.
3. Combine:
   - every artifact ASCII (no high bytes, no non-ASCII prolog) → **`US-ASCII`**
   - a `.42f` prolog names a charset → that charset wins; if two prologs disagree, **error**
   - no prolog, but high bytes that decode as UTF-8 → **`UTF-8`**
   - no prolog, high bytes that do not decode as UTF-8 → **undecidable**

In the undecidable case: use the author's `characterEncoding` if present; otherwise fall back to
`fglrun -i` *with a warning* naming the file and advising an explicit declaration. This is the only
place the build environment is consulted, and it never overrides evidence.

**Cross-check.** If the author declared `characterEncoding` and derivation contradicts it *provably*
(declared `US-ASCII` but high bytes exist; declared `ISO-8859-15` but a `.42f` prolog says `UTF-8`),
fail the pack naming the file and both encodings. A declaration that merely *narrows* an undecidable
result is accepted silently.

## Consume-time behaviour (`install`)

Let `pkg` be the package's `characterEncoding` and `env` the consumer's `fglrun -i` Charmap.

| `pkg` | Action |
|---|---|
| absent | proceed; no message (the status quo for every package published so far) |
| `US-ASCII` | proceed; no check — compatible with every Genero charset |
| equals `env` (case-insensitive, `ASCII`≈`US-ASCII`) | proceed |
| differs from `env` | **error**; suggest `--force-character-encoding` |

`lengthSemantics`, when present, must equal the consumer's `fglrun -i` Length Semantics, else the
same error. Absent → no check.

`--force-character-encoding` bypasses both checks with a loud warning, per the ticket.

### Migration

Every package on the registry today (20 at the time of writing) declares no encoding, so the
`absent` row is the overwhelmingly common case and must stay silent, not warn. The field populates
itself as packages are republished, because `pack` derives it — authors need not learn about it.

## `init`

`fglpkg init` does **not** write `characterEncoding`. The ticket suggested seeding it from
`fglrun -i`, but at `init` there are no artifacts, and the value would be a guess from the author's
current shell that `pack` would then have to contradict. Leaving it absent lets derivation own it.

## Testing

Detection must not depend on the machine's locale or the suite becomes unrunnable in CI. Add
`FGLPKG_CHARMAP` and `FGLPKG_LENGTH_SEMANTICS` overrides, mirroring the existing
`FGLPKG_GENERO_VERSION` escape hatch the functional harness already pins.

Unit: the three-tier byte classifier (empty file, pure ASCII, valid UTF-8, invalid UTF-8, a UTF-8
sequence split across a read boundary); XML prolog parsing (single vs double quotes, absent prolog,
disagreeing prologs); IANA comparison (`ASCII`/`US-ASCII`/`us-ascii`, `SHIFT_JIS`/`Shift_JIS`);
`Validate()` on `lengthSemantics`.

Functional: pack an ASCII package → shipped manifest says `US-ASCII`; pack a UTF-8 package → says
`UTF-8`; pack with a provably wrong declaration → pack fails naming the file; install an
`ISO-8859-15` package under a `UTF-8` consumer → refused, and accepted under
`--force-character-encoding`; install a package with no `characterEncoding` → silent.

Fixtures must be committed as bytes (not generated by the local `fglcomp`), so the suite does not
require a Genero toolchain.

## Non-goals

- Guessing *which* 8-bit charset an artifact uses. `ISO-8859-1` and `-15` are indistinguishable from
  bytes; only a declaration can resolve it.
- Transcoding artifacts. fglpkg reports incompatibility; it never rewrites p-code.
- Deriving `lengthSemantics`. It is a property of behaviour, not bytes.
- Checking `.4gl`/`.per` sources. Only shipped compiled artifacts are examined.

## Open questions

1. Should a *source-only* package (GIS-295) be scanned for `.4gl` encoding too? Deferred until the
   source-only semantics land.
2. Should the registry index `characterEncoding` so `search` can filter to packages compatible with
   the caller's charmap? Server-side; out of scope here.
