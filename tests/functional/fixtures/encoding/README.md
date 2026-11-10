# Character-encoding fixtures (GIS-285)

Real output of `fglcomp` / `fglform` / `fglmkstr` 6.00.01, committed as bytes so
the suite needs no Genero toolchain. Each artifact is kept beside the source it
was built from, because a pure-ASCII source is what proves a `.42m` carries no
non-ASCII text — the artifact's own bytes cannot.

| File | High bytes | What it is for |
|---|---|---|
| `AsciiBig.42m` | 1 | A realistic module from a **pure-ASCII** source. Its high byte is an opcode operand, not a literal. 78 of 99 real modules look like this, so a byte scan alone would wrongly call it non-ASCII. |
| `AsciiTiny.42m` | 0 | The minority (21 of 99) whose p-code happens to be clean, so bytes alone prove ASCII. |
| `Utf8Lit.42m` | 4 | A genuine UTF-8 string literal. Indistinguishable from `AsciiBig.42m` by bytes, which is the point. |
| `AsciiForm.42f` | 0 | A **pure-ASCII** form built under `en_US.UTF-8`, so its prolog says `UTF-8`. The prolog names the build locale, not the content. |
| `Utf8Form.42f` | 2 | A form that really carries non-ASCII text, so its prolog is authoritative. |
| `AsciiMsg.42s` / `Utf8Msg.42s` / `Latin9Msg.42s` | 0 / 4 / 2 | `.42s` is a clean container, so its byte scan is reliable: ASCII, valid UTF-8, and high bytes that are not UTF-8. |

Regenerate with `fglcomp <f>.4gl`, `fglform -M <f>.per` (under `en_US.UTF-8`)
and `fglmkstr <f>.str`.
