# Character-encoding fixtures (GIS-285)

Real output of `fglcomp` / `fglform` / `fglmkstr` 6.00.01, committed as bytes so
the suite needs no Genero toolchain. Each artifact is kept beside the source it
was built from, and beside that source's `&include` files, because a `.42m` is
proven ASCII by its source rather than by its own bytes.

| File | High bytes | What it is for |
|---|---|---|
| `AsciiBig.42m` | 1 | A realistic module from a **pure-ASCII** source. Its high byte is an opcode operand, not a literal. 78 of 99 real modules look like this, so a byte scan alone would wrongly call it non-ASCII. |
| `AsciiTiny.42m` | 0 | The minority (21 of 99) whose p-code happens to be clean, so bytes alone prove ASCII. |
| `Utf8Lit.42m` | 4 | A genuine UTF-8 string literal. Indistinguishable from `AsciiBig.42m` by bytes, which is the point. |
| `IncAscii.42m` + `.4gl` + `.inc` | 1 | A pure-ASCII module whose source `&include`s a pure-ASCII file. Following includes must not cost this package its `US-ASCII`. |
| `IncUtf8.42m` + `.4gl` + `.inc` | 4 | The same shape with a **UTF-8** include. `IncUtf8.4gl` has no high byte, yet the module carries `€` — so the source alone is not proof. |
| `IncTight.42m` + `.4gl` + `.inc` | 5 | The same UTF-8 include written `&include"IncTight.inc"`, with no space. fglcomp accepts that and six other separator spellings; a matcher that wants `&include ` misses them and records `US-ASCII` for a module carrying `€`. |
| `IncDeep.42m` + `.4gl` + `IncDeepA.inc` + `IncDeepB.inc` | 5 | Two levels: the `.4gl` and the first include are pure ASCII, and only the **second** include carries `€`. A walk that stops at depth 1 records `US-ASCII` here. |
| `AsciiForm.42f` | 0 | A **pure-ASCII** form built under `en_US.UTF-8`, so its prolog says `UTF-8`. The prolog names the build locale, not the content. |
| `Utf8Form.42f` | 2 | A form that really carries UTF-8 text, built under `en_US.UTF-8` (prolog `UTF-8`). |
| `Utf8Mislabelled.42f` | 2 | **The same `.per`** compiled under `LANG=C`: identical UTF-8 content, prolog `ASCII`. Proves the prolog cannot classify a form even when the form does carry non-ASCII text. |
| `Latin9Form.42f` | 1 | A form carrying 8-bit, non-UTF-8 text (built under `en_US.ISO8859-15`). Bytes cannot name its charset, so it proves nothing. |
| `AsciiMsg.42s` / `Utf8Msg.42s` / `Latin9Msg.42s` | 0 / 4 / 2 | `.42s` is a clean container, so its byte scan is reliable: ASCII, valid UTF-8, and high bytes that are not UTF-8. |

Regenerate with `fglcomp -M <f>.4gl`, `fglform -M <f>.per` and
`fglmkstr <f>.str`, under the locale named in the table (`en_US.UTF-8` unless
stated).
