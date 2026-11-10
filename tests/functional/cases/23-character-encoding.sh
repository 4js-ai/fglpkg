suite "character encoding (GIS-285)"

# fglcomp/fglform write string literals into .42m/.42f/.42s in the encoding of
# the build locale, so a package built under UTF-8 yields mojibake — or
# silently wrong string lengths — when consumed under ISO-8859-15.
#
# `pack` DERIVES the encoding from the staged artifacts rather than asking, and
# `install` refuses a verified mismatch. Every fixture below is written as raw
# bytes rather than compiled, so these cases need no Genero toolchain: that is
# the whole point of pinning FGLPKG_CHARMAP too, without which detection would
# depend on the machine's locale and the suite would not be reproducible.

# The literal DISPLAY "café naïve €" as each locale compiles it. 7 high bytes
# under UTF-8 (all decoding), 3 under ISO-8859-15 (none decoding).
_ascii_bytes()  { printf 'DISPLAY "plain ascii"'; }
_utf8_bytes()   { printf 'DISPLAY "caf\303\251 na\303\257ve \342\202\254"'; }
_latin9_bytes() { printf 'DISPLAY "caf\351 na\357ve \244"'; }

# _mkpkg_enc <42m-writer-fn> [extra-manifest-json]
# A minimal packable project whose single .42m is written byte-for-byte.
_mkpkg_enc() {
  local writer="$1" extra="${2:-}"
  cat > fglpkg.json <<EOF
{
  "name": "enc.pkg",
  "version": "1.0.0",
  "description": "encoding fixture",
  "genero": ">=3.20",
  "license": "MIT",
  "repository": "https://github.com/example/enc",
  "author": "fglpkg functional tests",
  "files": ["*.42m", "*.42f"]${extra:+,
  $extra}
}
EOF
  "$writer" > mod.42m
}

# ---------------------------------------------------------------- pack ------

_enc_pack_derives_ascii() {
  export FGLPKG_CHARMAP=UTF-8 FGLPKG_LENGTH_SEMANTICS=CHAR
  _mkpkg_enc _ascii_bytes
  # Derived from the bytes, NOT from the build locale — the environment here is
  # UTF-8, yet an all-ASCII package must still be declared portable.
  run pack -o p.zip
  assert_success || return 1
  unzip -p p.zip fglpkg.json > shipped.json || return 1
  assert_file_contains shipped.json '"characterEncoding": "US-ASCII"' || return 1
}
it "pack derives US-ASCII from ASCII artifacts, not from the build locale" _enc_pack_derives_ascii

_enc_pack_derives_utf8() {
  export FGLPKG_CHARMAP=UTF-8 FGLPKG_LENGTH_SEMANTICS=CHAR
  _mkpkg_enc _utf8_bytes
  run pack -o p.zip
  assert_success || return 1
  unzip -p p.zip fglpkg.json > shipped.json || return 1
  assert_file_contains shipped.json '"characterEncoding": "UTF-8"' || return 1
}
it "pack derives UTF-8 from high bytes that decode as UTF-8" _enc_pack_derives_utf8

# A wrong US-ASCII claim is the most damaging one to let through: US-ASCII is
# the tier install skips its check for, so the package would ship mojibake to
# every consumer without a word.
_enc_pack_rejects_false_ascii() {
  export FGLPKG_CHARMAP=UTF-8 FGLPKG_LENGTH_SEMANTICS=CHAR
  _mkpkg_enc _utf8_bytes '"characterEncoding": "US-ASCII"'
  run pack -o p.zip
  assert_failure || return 1
  assert_contains "mod.42m" || return 1
  assert_contains "not pure ASCII" || return 1
}
it "pack refuses a declared US-ASCII contradicted by the bytes" _enc_pack_rejects_false_ascii

# A compiled form declares its encoding in the XML prolog, so it decides.
_enc_pack_prolog_is_authoritative() {
  export FGLPKG_CHARMAP=UTF-8 FGLPKG_LENGTH_SEMANTICS=CHAR
  _mkpkg_enc _ascii_bytes
  printf "<?xml version='1.0' encoding='ISO-8859-15'?><Form/>" > f.42f
  run pack -o p.zip
  assert_success || return 1
  unzip -p p.zip fglpkg.json > shipped.json || return 1
  assert_file_contains shipped.json '"characterEncoding": "ISO-8859-15"' || return 1
}
it "a form's XML prolog decides the package encoding" _enc_pack_prolog_is_authoritative

# ISO-8859-1 and -15 are indistinguishable from bytes, so a declaration that
# narrows an undecidable result is the one thing the author can add.
_enc_pack_declaration_narrows() {
  export FGLPKG_CHARMAP=UTF-8 FGLPKG_LENGTH_SEMANTICS=CHAR
  _mkpkg_enc _latin9_bytes '"characterEncoding": "ISO-8859-15"'
  run pack -o p.zip
  assert_success || return 1
  unzip -p p.zip fglpkg.json > shipped.json || return 1
  assert_file_contains shipped.json '"characterEncoding": "ISO-8859-15"' || return 1
}
it "a declaration narrowing an undecidable 8-bit result is accepted" _enc_pack_declaration_narrows

# lengthSemantics "char" under a single-byte charset is unsatisfiable: the
# runtime reports CHAR as BYTE there, so no consumer could ever match it.
_enc_rejects_impossible_length_semantics() {
  export FGLPKG_CHARMAP=UTF-8 FGLPKG_LENGTH_SEMANTICS=CHAR
  _mkpkg_enc _ascii_bytes '"characterEncoding": "ISO-8859-15", "lengthSemantics": "char"'
  run pack --list
  assert_failure || return 1
  assert_contains "single-byte" || return 1
}
it "lengthSemantics char with a single-byte charset is rejected" _enc_rejects_impossible_length_semantics

_enc_rejects_unknown_length_semantics() {
  export FGLPKG_CHARMAP=UTF-8 FGLPKG_LENGTH_SEMANTICS=CHAR
  _mkpkg_enc _ascii_bytes '"lengthSemantics": "chars"'
  run pack --list
  assert_failure || return 1
  assert_contains "invalid lengthSemantics" || return 1
}
it "an unknown lengthSemantics value is rejected, not ignored" _enc_rejects_unknown_length_semantics

# ------------------------------------------------------------- install ------

# Build a fixtures dir serving one package that declares ISO-8859-15.
_enc_build_latin9_fixtures() {  # _enc_build_latin9_fixtures <dir>
  local dir="$1"; mkdir -p "$dir"
  local build; build="$(mktemp -d "$_SANDBOX_ROOT/enc.XXXXXX")"
  (
    cd "$build"
    _mkpkg_enc _latin9_bytes '"characterEncoding": "ISO-8859-15"'
    sed -i.bak 's/"name": "enc.pkg"/"name": "demo.pkg"/' fglpkg.json && rm -f fglpkg.json.bak
    "$FGLPKG" pack -o "$dir/demo-pkg-1.0.0-genero6.zip" </dev/null >/dev/null 2>&1 || exit 1
  ) || { _diag "could not build the ISO-8859-15 fixture"; return 1; }
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
  export FGLPKG_CHARMAP=ISO-8859-15 FGLPKG_LENGTH_SEMANTICS=BYTE
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/fx.XXXXXX")"
  _enc_build_latin9_fixtures "$fx" || return 1
  mock_registry_start "$fx" || return 1

  # Now consume it from a UTF-8 environment.
  export FGLPKG_CHARMAP=UTF-8 FGLPKG_LENGTH_SEMANTICS=CHAR
  run install demo.pkg@1.0.0
  assert_failure || return 1
  assert_contains "ISO-8859-15" || return 1
  assert_contains "--force-character-encoding" || return 1
  # Nothing may be left on disk: the gate runs before extraction, so a re-run
  # must not read the package as installed (the shape of GIS-580).
  assert_no_file ".fglpkg/packages/demo-pkg/fglpkg.json" || return 1
}
it "install refuses a package built with a different charset" _enc_install_refuses_mismatch

_enc_install_force_overrides() {
  export FGLPKG_CHARMAP=ISO-8859-15 FGLPKG_LENGTH_SEMANTICS=BYTE
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/fx.XXXXXX")"
  _enc_build_latin9_fixtures "$fx" || return 1
  mock_registry_start "$fx" || return 1

  export FGLPKG_CHARMAP=UTF-8 FGLPKG_LENGTH_SEMANTICS=CHAR
  run install demo.pkg@1.0.0 --force-character-encoding
  assert_success || return 1
  assert_file ".fglpkg/packages/demo-pkg/fglpkg.json" || return 1
}
it "--force-character-encoding installs the mismatch anyway" _enc_install_force_overrides

_enc_install_matching_charset_proceeds() {
  export FGLPKG_CHARMAP=ISO-8859-15 FGLPKG_LENGTH_SEMANTICS=BYTE
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/fx.XXXXXX")"
  _enc_build_latin9_fixtures "$fx" || return 1
  mock_registry_start "$fx" || return 1

  run install demo.pkg@1.0.0
  assert_success || return 1
  assert_file ".fglpkg/packages/demo-pkg/fglpkg.json" || return 1
}
it "install proceeds when the charsets match" _enc_install_matching_charset_proceeds

# Every package on the registry predates this field. Warning about them would
# make install noisy for everyone while saying nothing actionable.
_enc_install_absent_is_silent() {
  export FGLPKG_CHARMAP=UTF-8 FGLPKG_LENGTH_SEMANTICS=CHAR
  mock_registry_start || return 1
  run install demo.pkg@1.0.0
  assert_success || return 1
  assert_not_match "character encoding|characterEncoding" || return 1
}
it "a package declaring no encoding installs silently" _enc_install_absent_is_silent
