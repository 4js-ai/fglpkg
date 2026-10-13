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

# The listing names ONE version — the latest — but `install` installs the newest
# version that RUNS, stepping down past the ones that cannot (GIS-574). A
# package whose latest version dropped a Genero major therefore graded ✗ while
# `install` succeeded with an older version. `fglunit` is that shape in the
# registry today: 1.0.1 publishes genero6 only, 1.0.0 publishes all three.
#
# The fixture ships both versions as real zips, so the install at the end is a
# genuine one — the whole point is that search and install must give the same
# answer, and only an actual install proves it.
_stepdown_fixtures() {  # _stepdown_fixtures <dir>
  local dir="$1"; mkdir -p "$dir"
  mock_build_fixtures "$dir" || return 1

  local build; build="$(mktemp -d "$_SANDBOX_ROOT/sd.XXXXXX")"
  (
    cd "$build"
    printf 'stub' > mod.42m
    cat > fglpkg.json <<'EOF'
{ "name":"stepdown","version":"1.0.0","description":"latest dropped genero4",
  "license":"MIT","author":"fglpkg tests","files":["*.42m"] }
EOF
    "$FGLPKG" pack -o "$dir/stepdown-1.0.0.zip" </dev/null >/dev/null 2>&1 || exit 1
    sed 's/"version":"1.0.0"/"version":"1.0.1"/' fglpkg.json > f2 && mv f2 fglpkg.json
    "$FGLPKG" pack -o "$dir/stepdown-1.0.1.zip" </dev/null >/dev/null 2>&1 || exit 1
  ) || { _diag "_stepdown_fixtures: pack failed"; return 1; }

  python3 - "$dir/packages.json" <<'PY2' || return 1
import json, sys
path = sys.argv[1]
with open(path) as fh:
    doc = json.load(fh)
doc["packages"].append({
    "slug": "stepdown", "name": "stepdown",
    "description": "latest dropped genero4", "genero": "",
    "owner": {"partner_id": "mock", "name": "fglpkg tests"},
    "versions": [
        {"version": "1.0.0", "genero": "", "author": "fglpkg tests", "license": "MIT",
         "artifacts": [{"variant": v, "zip": "stepdown-1.0.0.zip"}
                       for v in ("genero4", "genero5", "genero6")]},
        {"version": "1.0.1", "genero": "", "author": "fglpkg tests", "license": "MIT",
         "artifacts": [{"variant": "genero6", "zip": "stepdown-1.0.1.zip"}]},
    ],
})
with open(path, "w") as fh:
    json.dump(doc, fh, indent=2)
PY2
}

_variants_stepdown() {
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/fxsd.XXXXXX")"
  _stepdown_fixtures "$fx" || return 1
  mock_registry_start "$fx" || return 1

  run search stepdown --genero 4.01.12
  assert_success || return 1

  # Every cell describes the version install would hand over, marked * because
  # it is not the latest published.
  assert_match 'stepdown +1\.0\.0\* +4, 5, 6 +✓' || return 1
  assert_contains "* shows the newest version that runs on Genero 4.01.12" || return 1
  assert_contains "latest is 1.0.1" || return 1

  # The claim the row makes, carried out. A ✓ search must be an install that
  # works, and on the version the row named. `install` has no --genero flag, so
  # the runtime is set the way a real Genero 4 machine would report it; the
  # export is confined to this test's subshell.
  export FGLPKG_GENERO_VERSION=4.01.12
  mkdir -p proj && cd proj || return 1
  run init --yes
  assert_success || return 1
  run install stepdown
  assert_success || return 1
  assert_contains "stepdown@1.0.0" || return 1
  assert_file_contains fglpkg-lock.json '"version": "1.0.0"' || return 1
}
it "a ✗ on the latest version steps down to the one install gives you" _variants_stepdown

# The other direction: on Genero 6 the latest version runs, so nothing steps
# down, no row is marked, and the note stays off the output entirely.
_variants_no_stepdown_when_latest_runs() {
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/fxsd.XXXXXX")"
  _stepdown_fixtures "$fx" || return 1
  mock_registry_start "$fx" || return 1

  run search stepdown --genero 6.00.01
  assert_success || return 1
  assert_match 'stepdown +1\.0\.1 +6 +✓' || return 1
  assert_not_contains "shows the newest version that runs" || return 1
}
it "nothing is marked when the latest version already runs" _variants_no_stepdown_when_latest_runs

# The step-down must make the SAME choice `install` makes, and "latest" never
# resolves to a prerelease (semver.Constraint.Matches skips one unless the
# constraint names it). A step-down that merely compared versions would announce
# a beta install does not pick — and where the only runnable version IS a
# prerelease, it would say ✓ for a package install refuses outright.
_prerelease_fixtures() {  # _prerelease_fixtures <dir>
  local dir="$1"; mkdir -p "$dir"
  mock_build_fixtures "$dir" || return 1

  local build; build="$(mktemp -d "$_SANDBOX_ROOT/pre.XXXXXX")"
  (
    cd "$build"
    printf 'stub' > mod.42m
    cat > fglpkg.json <<'EOF'
{ "name":"prerel","version":"1.0.0","description":"a beta sits above the stable",
  "license":"MIT","author":"fglpkg tests","files":["*.42m"] }
EOF
    "$FGLPKG" pack -o "$dir/prerel-1.0.0.zip" </dev/null >/dev/null 2>&1 || exit 1
  ) || { _diag "_prerelease_fixtures: pack failed"; return 1; }

  python3 - "$dir/packages.json" <<'PY2' || return 1
import json, sys
path = sys.argv[1]
with open(path) as fh:
    doc = json.load(fh)

def version(v, variants):
    return {"version": v, "genero": "", "author": "fglpkg tests", "license": "MIT",
            "artifacts": [{"variant": t, "zip": "prerel-1.0.0.zip"} for t in variants]}

# 2.0.0-beta.1 runs on Genero 4 and outranks 1.0.0 by version order, but
# "latest" will not select it, so install lands on 1.0.0.
doc["packages"].append({
    "slug": "prerel", "name": "prerel", "description": "a beta sits above the stable",
    "genero": "", "owner": {"partner_id": "mock", "name": "fglpkg tests"},
    "versions": [version("1.0.0", ["genero4"]),
                 version("2.0.0-beta.1", ["genero4", "genero6"]),
                 version("2.0.0", ["genero6"])],
})
# Here the ONLY version that runs on Genero 4 is a prerelease, so install has
# nothing to give and the ✗ must stand.
doc["packages"].append({
    "slug": "onlypre", "name": "onlypre", "description": "only a beta runs here",
    "genero": "", "owner": {"partner_id": "mock", "name": "fglpkg tests"},
    "versions": [version("1.0.0-beta.1", ["genero4"]),
                 version("2.0.0", ["genero6"])],
})
with open(path, "w") as fh:
    json.dump(doc, fh, indent=2)
PY2
}

_variants_prerelease() {
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/fxpre.XXXXXX")"
  _prerelease_fixtures "$fx" || return 1
  mock_registry_start "$fx" || return 1

  run search prerel --genero 4.01.12
  assert_success || return 1
  # 1.0.0, not 2.0.0-beta.1, even though the beta runs here and is newer.
  assert_match 'prerel +1\.0\.0\* +4 +✓' || return 1
  assert_not_contains "2.0.0-beta.1" || return 1

  run search onlypre --genero 4.01.12
  assert_success || return 1
  assert_match 'onlypre +2\.0\.0 +6 +✗' || return 1

  # Both claims, carried out against the real resolver.
  export FGLPKG_GENERO_VERSION=4.01.12
  mkdir -p proj && cd proj || return 1
  run init --yes
  assert_success || return 1

  run install prerel
  assert_success || return 1
  assert_contains "prerel@1.0.0" || return 1
  assert_file_contains fglpkg-lock.json '"version": "1.0.0"' || return 1

  run install onlypre
  assert_failure || return 1
  assert_contains "can run on Genero 4.01.12" || return 1
}
it "the step-down skips prereleases, exactly as install does" _variants_prerelease

# `fglpkg install <pkg>` has two resolve paths: RepositorySet.Resolve whenever a
# secondary repository is configured, registry.Resolve otherwise. Every case
# above takes the second. This one configures a secondary so install routes
# through the first, and checks it makes the same choice — that path used to
# carry its own copy of "newest version that runs here", and nothing compared
# the two.
#
# The secondary points at the mock itself, which answers 404 for Artifactory's
# storage paths. That is deliberate: it only has to EXIST for the routing to
# switch, and 404 means "not here" so routing moves on. An unreachable host
# would not do — a dial failure is not evidence a package is absent, so install
# aborts on it rather than guessing, which is the right behaviour and the wrong
# test.
_variants_prerelease_multiprovider() {
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/fxmp.XXXXXX")"
  _prerelease_fixtures "$fx" || return 1
  mock_registry_start "$fx" || return 1

  export FGLPKG_GENERO_VERSION=4.01.12
  mkdir -p proj && cd proj || return 1
  run init --yes
  assert_success || return 1
  run registry add secondary "$FGLPKG_REGISTRY" --repo-key generic-local
  assert_success || return 1

  # Same answer as the single-registry path: the beta runs here and is newer,
  # and "latest" still does not select it.
  run install prerel
  assert_success || return 1
  assert_contains "prerel@1.0.0" || return 1
  assert_file_contains fglpkg-lock.json '"version": "1.0.0"' || return 1

  run install onlypre
  assert_failure || return 1
  assert_contains "can run on Genero 4.01.12" || return 1
}
it "the multi-provider install path picks the same version" _variants_prerelease_multiprovider

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
