suite "search grades from published builds (GIS-575)"

# `fglpkg search` graded compatibility from the publisher's declared `genero`
# constraint alone. That field is optional and 8 of the 9 registry packages
# leave it empty, so the GENERO column read "-" and the verdict "?" for nearly
# every result — including `odatalib`, which publishes genero5 and genero6 only
# and cannot run on Genero 4 at all. "Unknown" and "no" are very different
# things to tell someone about to install.
#
# The published build list is the signal that is always there, because
# uploading a build is what creates it. These cases drive the real binary
# against the mock registry's browse endpoint, which now serves `variants`
# exactly as the registry does.
#
# Note the explicit `|| return 1` on each assertion: `it()` runs the body in a
# subshell inside an `if`, so bash suppresses errexit and a bare failing
# assertion mid-body would be ignored (see the note in case 113).

_variants_fixtures() {  # _variants_fixtures <dir>
  local dir="$1"; mkdir -p "$dir"
  mock_build_fixtures "$dir" || return 1

  # Search never downloads, so every synthetic package points its artifacts at
  # the zip mock_build_fixtures already made — only the variant TAGS matter
  # here, and inventing four more packages' worth of real zips would say
  # nothing extra.
  python3 - "$dir/packages.json" <<'PY' || return 1
import json, sys

ZIP = "demo-pkg-1.0.0-genero6.zip"

def pkg(slug, description, variants, genero=""):
    return {
        "slug": slug, "name": slug, "description": description, "genero": genero,
        "owner": {"partner_id": "mock", "name": "fglpkg tests"},
        "versions": [{
            "version": "1.0.0", "genero": genero,
            "author": "fglpkg tests", "license": "MIT",
            "artifacts": [{"variant": v, "zip": ZIP} for v in variants],
        }],
    }

path = sys.argv[1]
with open(path) as fh:
    doc = json.load(fh)
doc["packages"] += [
    pkg("fx-poi", "builds for every major", ["genero4", "genero5", "genero6"]),
    pkg("fx-odata", "no genero4 build", ["genero5", "genero6"]),
    pkg("fx-widget", "a browser widget", ["webcomponent"]),
    pkg("fx-pinned", "declares a constraint too", ["genero4", "genero5", "genero6"],
        genero=">=4.1.3 <7.0.0"),
]
with open(path, "w") as fh:
    json.dump(doc, fh, indent=2)
PY
}

_variants_grade_on_genero4() {
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/fxv.XXXXXX")"
  _variants_fixtures "$fx" || return 1
  mock_registry_start "$fx" || return 1

  run search fx --genero 4.01.12
  assert_success || return 1

  # Majors with a build, and a verdict that is no longer "?".
  assert_match 'fx-poi +1\.0\.0 +4, 5, 6 +✓' || return 1
  # The case the ticket is named for: provably incompatible, and now says so.
  assert_match 'fx-odata +1\.0\.0 +5, 6 +✗' || return 1
  # Web components ship browser assets, not p-code: every major serves.
  assert_match 'fx-widget +1\.0\.0 +any +✓' || return 1
  # A declared constraint is more precise than a build list (it carries a
  # minimum patch level), so it is what the column shows when both exist.
  assert_match 'fx-pinned +1\.0\.0 +>=4\.1\.3 <7\.0\.0 +✓' || return 1

  # Nothing in this result set is unknown any more.
  assert_not_match 'fx-(poi|odata|widget|pinned).*\?' || return 1
}
it "search grades each result from the builds that exist" _variants_grade_on_genero4

_variants_follow_the_target() {
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/fxv.XXXXXX")"
  _variants_fixtures "$fx" || return 1
  mock_registry_start "$fx" || return 1

  # The same package, the same listing, a different runtime. fx-odata has a
  # genero6 build, so here it is compatible — proof the verdict is computed
  # against the target rather than baked into the row.
  run search fx-odata --genero 6.00.01
  assert_success || return 1
  assert_match 'fx-odata +1\.0\.0 +5, 6 +✓' || return 1

  run search fx-odata --genero 4.01.12
  assert_success || return 1
  assert_match 'fx-odata +1\.0\.0 +5, 6 +✗' || return 1
}
it "the same build list grades differently per Genero version" _variants_follow_the_target
