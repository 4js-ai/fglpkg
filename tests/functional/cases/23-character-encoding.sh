suite "character encoding (GIS-285)"

# A package built under one charset yields mojibake — or silently wrong string
# lengths — when consumed under another, so `pack` records what the artifacts
# PROVE and `install` acts on it.
#
# The fixtures are REAL compiled output (tests/functional/fixtures/encoding),
# committed as bytes so the suite still needs no Genero toolchain. Source text
# written into a file named .42m would not do: it never contains the structure
# bytes >= 0x80 that real p-code carries, which is exactly the case an earlier
# design got wrong.
#
# FGLPKG_CHARMAP is pinned throughout so detection cannot depend on the
# machine's locale. pack must IGNORE it; install must use it.

_enc_fixture() {  # _enc_fixture <name>... — copy fixtures into the project
  local f
  for f in "$@"; do cp "$_FIXTURES/encoding/$f" . || return 1; done
}

# _mkpkg_enc [extra-manifest-json] — a packable project; add artifacts yourself.
_mkpkg_enc() {
  local extra="${1:-}"
  cat > fglpkg.json <<EOF
{
  "name": "enc.pkg",
  "version": "1.0.0",
  "description": "encoding fixture",
  "genero": ">=3.20",
  "license": "MIT",
  "repository": "https://github.com/example/enc",
  "author": "fglpkg functional tests",
  "files": ["*.42m", "*.42f", "*.42s"]${extra:+,
  $extra}
}
EOF
}

# ---------------------------------------------------------------- pack ------

# The case a byte scan of the artifact gets wrong. AsciiBig.42m is compiled
# from a pure-ASCII source and still holds a byte >= 0x80 (an opcode operand);
# 78 of 99 real modules look like that. The source staged beside it proves the
# text is ASCII.
_enc_pack_proves_ascii_from_source() {
  export FGLPKG_CHARMAP=ISO-8859-15 FGLPKG_LENGTH_SEMANTICS=BYTE
  _mkpkg_enc '"files": ["*.42m", "*.4gl"]'
  _enc_fixture AsciiBig.42m AsciiBig.4gl || return 1
  run pack -o p.zip
  assert_success || return 1
  unzip -p p.zip fglpkg.json > shipped.json || return 1
  # Not ISO-8859-15: the packer's shell is not evidence.
  assert_file_contains shipped.json '"characterEncoding": "US-ASCII"' || return 1
}
it "pack proves US-ASCII from a pure-ASCII source beside real p-code" _enc_pack_proves_ascii_from_source

# Without a source, a .42m's high bytes prove nothing. Recording a value here
# is what stamped packages with the packer's shell charset.
_enc_pack_records_nothing_without_evidence() {
  export FGLPKG_CHARMAP=ISO-8859-15 FGLPKG_LENGTH_SEMANTICS=BYTE
  _mkpkg_enc
  _enc_fixture AsciiBig.42m || return 1
  run pack -o p.zip
  assert_success || return 1
  unzip -p p.zip fglpkg.json > shipped.json || return 1
  assert_file_not_contains shipped.json 'characterEncoding' || return 1
}
it "pack records no encoding when the artifacts prove none" _enc_pack_records_nothing_without_evidence

# The same bytes must pack identically whatever shell they are packed from.
# Packing under LANG=C once installed everywhere and packing under UTF-8 was
# refused by ISO-8859-15 consumers, for byte-identical artifacts.
_enc_pack_ignores_the_build_environment() {
  local first="" cm got
  for cm in US-ASCII UTF-8 ISO-8859-15; do
    rm -rf sub; mkdir sub; cd sub || return 1
    export FGLPKG_CHARMAP="$cm" FGLPKG_LENGTH_SEMANTICS=BYTE
    _mkpkg_enc '"files": ["*.42m", "*.4gl"]'
    _enc_fixture AsciiBig.42m AsciiBig.4gl || { cd ..; return 1; }
    run pack -o p.zip
    assert_success || { cd ..; return 1; }
    unzip -p p.zip fglpkg.json > shipped.json || { cd ..; return 1; }
    got="$(grep -c 'characterEncoding.*US-ASCII' shipped.json || true)"
    cd .. || return 1
    [ -z "$first" ] && first="$got"
    if [ "$got" != "$first" ] || [ "$got" != "1" ]; then
      _diag "FGLPKG_CHARMAP=$cm changed what pack recorded"
      return 1
    fi
  done
}
it "pack records the same thing whatever charmap it runs under" _enc_pack_ignores_the_build_environment

# A pure-ASCII .per built under a UTF-8 shell writes a UTF-8 prolog. The prolog
# names the BUILD LOCALE, so reading it as the content's encoding labelled an
# ASCII-only package UTF-8 and refused it on single-byte consumers.
_enc_pack_form_prolog_is_not_the_content() {
  export FGLPKG_CHARMAP=UTF-8 FGLPKG_LENGTH_SEMANTICS=CHAR
  _mkpkg_enc
  _enc_fixture AsciiForm.42f || return 1
  run pack -o p.zip
  assert_success || return 1
  unzip -p p.zip fglpkg.json > shipped.json || return 1
  assert_file_contains shipped.json '"characterEncoding": "US-ASCII"' || return 1
}
it "a pure-ASCII form is US-ASCII even though its prolog says UTF-8" _enc_pack_form_prolog_is_not_the_content

# ...and a form that really carries non-ASCII text is classified by its own
# BYTES, the same way a .42s is.
_enc_pack_form_with_real_text_uses_its_bytes() {
  export FGLPKG_CHARMAP=ISO-8859-15 FGLPKG_LENGTH_SEMANTICS=BYTE
  _mkpkg_enc
  _enc_fixture Utf8Form.42f || return 1
  run pack -o p.zip
  assert_success || return 1
  unzip -p p.zip fglpkg.json > shipped.json || return 1
  assert_file_contains shipped.json '"characterEncoding": "UTF-8"' || return 1
}
it "a form carrying non-ASCII text takes its encoding from its bytes" _enc_pack_form_with_real_text_uses_its_bytes

# The prolog must never override those bytes. Utf8Mislabelled.42f is the SAME
# .per as Utf8Form.per compiled under LANG=C: identical UTF-8 content, a prolog
# that says ASCII. Reading the prolog recorded US-ASCII here — the one tier
# install never checks — for a form that is not portable at all.
_enc_pack_form_prolog_never_overrides_the_bytes() {
  export FGLPKG_CHARMAP=ISO-8859-15 FGLPKG_LENGTH_SEMANTICS=BYTE
  _mkpkg_enc
  _enc_fixture Utf8Mislabelled.42f || return 1
  run pack -o p.zip
  assert_success || return 1
  unzip -p p.zip fglpkg.json > shipped.json || return 1
  assert_file_contains shipped.json '"characterEncoding": "UTF-8"' || return 1
  # Nor is the disagreement reported. fglrun uses a form's bytes as stored
  # whatever the prolog says (measured over three build locales and two
  # runtime locales), and the front ends never read the file, so a note here
  # would fire on every UTF-8 form built from a non-UTF-8 shell and ask for a
  # rebuild that changes nothing.
  assert_not_contains "prolog" || return 1
}
it "a form's prolog never overrides what its bytes say" _enc_pack_form_prolog_never_overrides_the_bytes

# A form whose high bytes are not UTF-8 is some 8-bit charset, and bytes cannot
# tell ISO-8859-1 from ISO-8859-15. Its prolog is not evidence either, so
# nothing is recorded and the author declares it.
_enc_pack_eight_bit_form_proves_nothing() {
  export FGLPKG_CHARMAP=UTF-8 FGLPKG_LENGTH_SEMANTICS=CHAR
  _mkpkg_enc
  _enc_fixture Latin9Form.42f || return 1
  run pack -o p.zip
  assert_success || return 1
  unzip -p p.zip fglpkg.json > shipped.json || return 1
  assert_file_not_contains shipped.json 'characterEncoding' || return 1
}
it "a form in an 8-bit charset records nothing, prolog or no prolog" _enc_pack_eight_bit_form_proves_nothing

# &include pulls another file's text into the module, so reading the .4gl alone
# is not enough. IncUtf8.4gl has no byte >= 0x80 and includes a UTF-8
# IncUtf8.inc; IncUtf8.42m really does carry the euro sign. Before the include
# walk this shipped "US-ASCII" under every shell.
_enc_pack_include_defeats_an_ascii_source() {
  export FGLPKG_CHARMAP=UTF-8 FGLPKG_LENGTH_SEMANTICS=CHAR
  _mkpkg_enc
  _enc_fixture IncUtf8.42m IncUtf8.4gl IncUtf8.inc || return 1
  run pack -o p.zip
  assert_success || return 1
  unzip -p p.zip fglpkg.json > shipped.json || return 1
  assert_file_not_contains shipped.json 'characterEncoding' || return 1
  assert_contains "IncUtf8.inc" || return 1
}
it "a UTF-8 &include stops a pure-ASCII source proving anything" _enc_pack_include_defeats_an_ascii_source

# fglcomp accepts the directive without the space, and with a space, tab or
# form feed between the & and `include`. IncTight.4gl is IncUtf8.4gl written
# `&include"IncTight.inc"`, compiled for real. A matcher that wants
# `&include ` records US-ASCII here for a module carrying UTF-8 text, and
# install never checks a US-ASCII package — so the miss is silent.
_enc_pack_include_without_a_space_is_still_an_include() {
  export FGLPKG_CHARMAP=UTF-8 FGLPKG_LENGTH_SEMANTICS=CHAR
  _mkpkg_enc
  _enc_fixture IncTight.42m IncTight.4gl IncTight.inc || return 1
  run pack -o p.zip
  assert_success || return 1
  unzip -p p.zip fglpkg.json > shipped.json || return 1
  assert_file_not_contains shipped.json 'characterEncoding' || return 1
  assert_contains "IncTight.inc" || return 1
}
it "&include with no space is still an include" _enc_pack_include_without_a_space_is_still_an_include

# ...and an include that IS pure ASCII must not cost the package its value, or
# following includes would simply disable the rule for every module using one.
_enc_pack_ascii_include_still_proves_ascii() {
  export FGLPKG_CHARMAP=ISO-8859-15 FGLPKG_LENGTH_SEMANTICS=BYTE
  _mkpkg_enc
  _enc_fixture IncAscii.42m IncAscii.4gl IncAscii.inc || return 1
  run pack -o p.zip
  assert_success || return 1
  unzip -p p.zip fglpkg.json > shipped.json || return 1
  assert_file_contains shipped.json '"characterEncoding": "US-ASCII"' || return 1
}
it "a pure-ASCII &include still proves the module ASCII" _enc_pack_ascii_include_still_proves_ascii

# A source edited after the build does not describe the artifact that ships.
_enc_pack_stale_source_proves_nothing() {
  export FGLPKG_CHARMAP=UTF-8 FGLPKG_LENGTH_SEMANTICS=CHAR
  _mkpkg_enc
  _enc_fixture AsciiBig.42m AsciiBig.4gl || return 1
  # An hour newer: a human edit, not a copy's ordering.
  touch -t "$(date -u -v+1H '+%Y%m%d%H%M' 2>/dev/null || date -u -d '+1 hour' '+%Y%m%d%H%M')" AsciiBig.4gl || return 1
  run pack -o p.zip
  assert_success || return 1
  unzip -p p.zip fglpkg.json > shipped.json || return 1
  assert_file_not_contains shipped.json 'characterEncoding' || return 1
  assert_contains "NEWER" || return 1
}
it "a source newer than its module proves nothing" _enc_pack_stale_source_proves_nothing

# The practice the guide recommends: pure-ASCII code, localized text in .str.
# The package must stay portable, with the resource named separately — not
# stamped non-portable and refused on the consumers the practice serves.
_enc_pack_ascii_code_plus_localized_resources() {
  export FGLPKG_CHARMAP=UTF-8 FGLPKG_LENGTH_SEMANTICS=CHAR
  _mkpkg_enc '"files": ["*.42m", "*.4gl", "*.42s"]'
  _enc_fixture AsciiBig.42m AsciiBig.4gl Utf8Msg.42s || return 1
  run pack -o p.zip
  assert_success || return 1
  unzip -p p.zip fglpkg.json > shipped.json || return 1
  assert_file_contains shipped.json '"characterEncoding": "US-ASCII"' || return 1
  assert_file_contains shipped.json '"Utf8Msg.42s": "UTF-8"' || return 1
}
it "ASCII code plus a UTF-8 .str stays portable, with the resource named" _enc_pack_ascii_code_plus_localized_resources

# An ASCII resource constrains nothing, so it is not recorded.
_enc_pack_ascii_resource_is_not_recorded() {
  export FGLPKG_CHARMAP=UTF-8 FGLPKG_LENGTH_SEMANTICS=CHAR
  _mkpkg_enc
  _enc_fixture AsciiMsg.42s || return 1
  run pack -o p.zip
  assert_success || return 1
  unzip -p p.zip fglpkg.json > shipped.json || return 1
  assert_file_not_contains shipped.json 'stringResourceEncodings' || return 1
}
it "an ASCII string resource constrains nothing and is not recorded" _enc_pack_ascii_resource_is_not_recorded

# These declarations are CORRECT and used to fail the pack.
_enc_pack_accepts_correct_ascii_declaration() {
  export FGLPKG_CHARMAP=UTF-8 FGLPKG_LENGTH_SEMANTICS=CHAR
  _mkpkg_enc '"characterEncoding": "US-ASCII"'
  # Real p-code with a structure high byte, a pure-ASCII form whose prolog says
  # UTF-8, and a localized resource. None of them disproves US-ASCII code.
  _enc_fixture AsciiBig.42m AsciiForm.42f Utf8Msg.42s || return 1
  run pack -o p.zip
  assert_success || return 1
  unzip -p p.zip fglpkg.json > shipped.json || return 1
  assert_file_contains shipped.json '"characterEncoding": "US-ASCII"' || return 1
}
it "a correct US-ASCII declaration survives real p-code, a UTF-8 prolog and a .42s" _enc_pack_accepts_correct_ascii_declaration

# The one contradiction that is still provable.
_enc_pack_rejects_contradicted_declaration() {
  export FGLPKG_CHARMAP=UTF-8 FGLPKG_LENGTH_SEMANTICS=CHAR
  _mkpkg_enc '"characterEncoding": "ISO-8859-15"'
  _enc_fixture Utf8Form.42f || return 1
  run pack -o p.zip
  assert_failure || return 1
  assert_contains "Utf8Form.42f" || return 1
}
it "pack refuses a declaration a non-ASCII form's bytes disprove" _enc_pack_rejects_contradicted_declaration

_enc_rejects_impossible_length_semantics() {
  export FGLPKG_CHARMAP=UTF-8 FGLPKG_LENGTH_SEMANTICS=CHAR
  _mkpkg_enc '"characterEncoding": "ISO-8859-15", "lengthSemantics": "char"'
  _enc_fixture AsciiTiny.42m || return 1
  run pack -o p.zip
  assert_failure || return 1
  assert_contains "single-byte" || return 1
}
it "lengthSemantics char with a single-byte charset is rejected" _enc_rejects_impossible_length_semantics

# ...but a multibyte charset makes it satisfiable. Treating every unknown
# charset as single-byte rejected this: fglrun -i under zh_CN.GBK reports
# Multibyte: yes.
_enc_accepts_char_with_multibyte_charset() {
  export FGLPKG_CHARMAP=GBK FGLPKG_LENGTH_SEMANTICS=CHAR
  _mkpkg_enc '"characterEncoding": "GBK", "lengthSemantics": "char"'
  _enc_fixture AsciiTiny.42m || return 1
  run pack -o p.zip
  assert_success || return 1
}
it "lengthSemantics char with a multibyte charset is accepted" _enc_accepts_char_with_multibyte_charset

# characterEncoding describes the CODE, and since the code/resource split the
# localized text lives in .42s. US-ASCII code that walks a UTF-8 resource
# string with getCharAt legitimately needs "char", and a multibyte consumer
# satisfies it. pack used to write that pair and then its own Validate()
# rejected the manifest it had just shipped.
_enc_accepts_char_with_ascii_code_and_utf8_resources() {
  export FGLPKG_CHARMAP=UTF-8 FGLPKG_LENGTH_SEMANTICS=CHAR
  _mkpkg_enc '"lengthSemantics": "char"'
  _enc_fixture AsciiBig.42m AsciiBig.4gl Utf8Msg.42s || return 1
  run pack -o p.zip
  assert_success || return 1
  unzip -p p.zip fglpkg.json > shipped.json || return 1
  assert_file_contains shipped.json '"characterEncoding": "US-ASCII"' || return 1
  assert_file_contains shipped.json '"lengthSemantics": "char"' || return 1
  # And the shipped manifest must be readable back, which is where this failed.
  rm -f fglpkg.json && cp shipped.json fglpkg.json || return 1
  run pack --list
  assert_success || return 1
}
it "US-ASCII code with lengthSemantics char packs and reads back" _enc_accepts_char_with_ascii_code_and_utf8_resources

_enc_rejects_unknown_length_semantics() {
  export FGLPKG_CHARMAP=UTF-8 FGLPKG_LENGTH_SEMANTICS=CHAR
  _mkpkg_enc '"lengthSemantics": "characters"'
  _enc_fixture AsciiTiny.42m || return 1
  run pack -o p.zip
  assert_failure || return 1
  assert_contains "lengthSemantics" || return 1
}
it "an unknown lengthSemantics value is rejected, not ignored" _enc_rejects_unknown_length_semantics

# ------------------------------------------------------------- install ------

# _enc_build_fixtures <dir> <extra-manifest-json> <fixture-file>...
_enc_build_fixtures() {
  local dir="$1" extra="$2"; shift 2
  mkdir -p "$dir"
  local build; build="$(mktemp -d "$_SANDBOX_ROOT/enc.XXXXXX")"
  (
    cd "$build" || exit 1
    _mkpkg_enc "$extra"
    sed -i.bak 's/"name": "enc.pkg"/"name": "demo.pkg"/' fglpkg.json && rm -f fglpkg.json.bak
    local f; for f in "$@"; do cp "$_FIXTURES/encoding/$f" . || exit 1; done
    "$FGLPKG" pack -o "$dir/demo-pkg-1.0.0-genero6.zip" </dev/null >/dev/null 2>&1 || exit 1
  ) || { _diag "could not build the encoding fixture"; return 1; }
  cat > "$dir/packages.json" <<'EOF'
{
  "packages": [
    {
      "slug": "demo-pkg",
      "name": "demo.pkg",
      "description": "encoding fixture",
      "genero": ">=3.20",
      "owner": { "partner_id": "mock", "name": "fglpkg tests" },
      "versions": [
        { "version":"1.0.0", "genero":">=3.20", "author":"fglpkg tests", "license":"MIT",
          "artifacts":[ { "variant":"genero6", "zip":"demo-pkg-1.0.0-genero6.zip" } ] }
      ]
    }
  ]
}
EOF
}

_enc_install_refuses_mismatch() {
  export FGLPKG_CHARMAP=UTF-8 FGLPKG_LENGTH_SEMANTICS=CHAR
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/fx.XXXXXX")"
  _enc_build_fixtures "$fx" '"characterEncoding": "ISO-8859-15"' AsciiTiny.42m || return 1
  mock_registry_start "$fx" || return 1

  run install demo.pkg@1.0.0
  assert_failure || return 1
  assert_contains "ISO-8859-15" || return 1
  # Name the COMMAND: `update` rejects a bare --force-character-encoding, so
  # the flag alone sent the user to a second error.
  assert_contains "fglpkg install --force-character-encoding" || return 1
  # Nothing may be left on disk: the gate runs before extraction, so a re-run
  # must not read the package as installed (the shape of GIS-580).
  assert_no_file ".fglpkg/packages/demo-pkg/fglpkg.json" || return 1
}
it "install refuses a package built with a different charset" _enc_install_refuses_mismatch

_enc_install_force_overrides() {
  export FGLPKG_CHARMAP=UTF-8 FGLPKG_LENGTH_SEMANTICS=CHAR
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/fx.XXXXXX")"
  _enc_build_fixtures "$fx" '"characterEncoding": "ISO-8859-15"' AsciiTiny.42m || return 1
  mock_registry_start "$fx" || return 1

  run install demo.pkg@1.0.0 --force-character-encoding
  assert_success || return 1
  assert_file ".fglpkg/packages/demo-pkg/fglpkg.json" || return 1
}
it "--force-character-encoding installs the mismatch anyway" _enc_install_force_overrides

_enc_install_matching_charset_proceeds() {
  export FGLPKG_CHARMAP=ISO-8859-15 FGLPKG_LENGTH_SEMANTICS=BYTE
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/fx.XXXXXX")"
  _enc_build_fixtures "$fx" '"characterEncoding": "ISO-8859-15"' AsciiTiny.42m || return 1
  mock_registry_start "$fx" || return 1

  run install demo.pkg@1.0.0
  assert_success || return 1
  assert_file ".fglpkg/packages/demo-pkg/fglpkg.json" || return 1
}
it "install proceeds when the charsets match" _enc_install_matching_charset_proceeds

# A string resource this environment cannot read is a WARNING. The package may
# ship one resource per language and charset for the consumer to select, so
# refusing would block a consumer who was going to use a different one.
_enc_install_resource_mismatch_warns_only() {
  export FGLPKG_CHARMAP=UTF-8 FGLPKG_LENGTH_SEMANTICS=CHAR
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/fx.XXXXXX")"
  _enc_build_fixtures "$fx" \
    '"characterEncoding": "US-ASCII", "stringResourceEncodings": {"Latin9Msg.42s": "ISO-8859-15"}' \
    AsciiTiny.42m Latin9Msg.42s || return 1
  mock_registry_start "$fx" || return 1

  run install demo.pkg@1.0.0
  assert_success || return 1
  assert_contains "Latin9Msg.42s" || return 1
  assert_file ".fglpkg/packages/demo-pkg/fglpkg.json" || return 1
}
it "a string resource this charmap cannot read warns but installs" _enc_install_resource_mismatch_warns_only

# Every package on the registry predates these fields. Warning about them would
# make install noisy for everyone while saying nothing actionable.
#
# The fixture must really declare nothing, which the standard mock fixture does
# NOT guarantee — pack derives a value for it. Real p-code with no source beside
# it proves nothing, so that is what is packed here, and the assertion below
# checks the shipped manifest before the install is believed.
_enc_install_absent_is_silent() {
  export FGLPKG_CHARMAP=UTF-8 FGLPKG_LENGTH_SEMANTICS=CHAR
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/fx.XXXXXX")"
  _enc_build_fixtures "$fx" '' AsciiBig.42m || return 1
  unzip -p "$fx/demo-pkg-1.0.0-genero6.zip" fglpkg.json > shipped.json || return 1
  assert_file_not_contains shipped.json 'characterEncoding' || return 1
  assert_file_not_contains shipped.json 'stringResourceEncodings' || return 1
  mock_registry_start "$fx" || return 1

  run install demo.pkg@1.0.0
  assert_success || return 1
  assert_not_match "character encoding|characterEncoding" || return 1
}
it "a package declaring no encoding installs silently" _enc_install_absent_is_silent
