suite "resolution honours published Genero variants (GIS-574)"

# A version is installable on a Genero major only if it publishes a build for
# it. That is independent of the "genero" constraint the version declares, and
# most publishers declare none — Satisfies("") is unconditionally true — so the
# variant list is usually the only compatibility signal there is.
#
# Before this, every version of such a package looked compatible with every
# runtime: resolution took the newest and the registry client handed back
# whatever build it had. The reported case was `fglunit ^1.0.0` on Genero 4
# resolving to 1.0.1, whose only build is genero6, and the lock file then
# recording generoMajor "4" next to a .../artifacts/genero6 download URL.

# Serves one package shaped like fglunit: 1.0.0 built for 4/5/6, 1.0.1 built
# only for 6, neither declaring a "genero" constraint.
_variant_fixtures() {  # _variant_fixtures <dir>
  local dir="$1"; mkdir -p "$dir"
  local build; build="$(mktemp -d "$_SANDBOX_ROOT/variantfx.XXXXXX")"
  (
    cd "$build"
    cat > fglpkg.json <<'EOF'
{ "name":"unitfx","version":"1.0.0","description":"Variant coverage fixture",
  "license":"MIT","author":"fglpkg tests","files":["*.42m"] }
EOF
    printf 'stub' > mod.42m
    local v
    for v in 1.0.0 1.0.1; do
      "$FGLPKG" pack -o "$dir/unitfx-$v.zip" </dev/null >/dev/null 2>&1 || exit 1
    done
  ) || return 1
  # One zip per variant; the bytes are irrelevant here, the variant tag is not.
  local v
  for v in genero4 genero5 genero6; do
    cp "$dir/unitfx-1.0.0.zip" "$dir/unitfx-1.0.0-$v.zip"
  done
  cp "$dir/unitfx-1.0.1.zip" "$dir/unitfx-1.0.1-genero6.zip"
  cat > "$dir/packages.json" <<'EOF'
{
  "packages": [
    {
      "slug": "unitfx",
      "name": "unitfx",
      "description": "Variant coverage fixture",
      "owner": { "partner_id": "mock", "name": "fglpkg tests" },
      "versions": [
        { "version":"1.0.0", "author":"fglpkg tests", "license":"MIT",
          "artifacts":[ { "variant":"genero4", "zip":"unitfx-1.0.0-genero4.zip" },
                        { "variant":"genero5", "zip":"unitfx-1.0.0-genero5.zip" },
                        { "variant":"genero6", "zip":"unitfx-1.0.0-genero6.zip" } ] },
        { "version":"1.0.1", "author":"fglpkg tests", "license":"MIT",
          "artifacts":[ { "variant":"genero6", "zip":"unitfx-1.0.1-genero6.zip" } ] }
      ]
    }
  ]
}
EOF
}

# Starts the mock with the fixtures and pins the runtime to <major>.
_variant_env() {  # _variant_env <genero-version>
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/variant.XXXXXX")"
  _variant_fixtures "$fx" || { _diag "could not build fixtures"; return 1; }
  mock_registry_start "$fx"
  export FGLPKG_GENERO_VERSION="$1"
}

_variant_skips_version_without_a_build() {
  _variant_env 4.01.12 || return 1
  run install unitfx
  assert_success
  # 1.0.1 publishes only a genero6 build, so the newest installable is 1.0.0.
  assert_contains "unitfx@1.0.0"
  assert_not_contains "unitfx@1.0.1"
}
it "an older version is chosen when the newest has no build for this Genero" \
  _variant_skips_version_without_a_build

_variant_takes_newest_when_supported() {
  _variant_env 6.00.01 || return 1
  run install unitfx
  assert_success
  assert_contains "unitfx@1.0.1"
}
it "the newest version is still chosen where a build exists" \
  _variant_takes_newest_when_supported

# The lock file's generoMajor is read back by the installer and
# `audit signatures` to rebuild the signed variant, so it has to agree with the
# build the download URL names. Here the delivered build and the runtime are
# both genero4, so this guards the resolution fix end to end; recording the
# delivered variant when the two differ is covered by
# internal/lockfile/genero_major_test.go.
_variant_lock_records_delivered_build() {
  _variant_env 4.01.12 || return 1
  run install unitfx
  assert_success
  assert_file_contains "fglpkg-lock.json" '"generoMajor": "4"' || return 1
  assert_file_contains "fglpkg-lock.json" "artifacts/genero4" || return 1
  # The contradiction the bug produced: a genero6 URL beside generoMajor 4.
  if grep -q -- "artifacts/genero6" fglpkg-lock.json; then
    _diag "lock file still points at a genero6 artifact on a Genero 4 runtime"
    return 1
  fi
}
it "the lock file's generoMajor and download URL name the same build" \
  _variant_lock_records_delivered_build

# No version has a genero3 build, and there is nothing older to fall back to,
# so this must fail rather than install something unrunnable.
_variant_no_build_at_all_fails() {
  _variant_env 3.20.05 || return 1
  run install unitfx
  assert_failure
  assert_contains "unitfx"
  # The message must name what is published, or the user cannot tell whether to
  # pin, upgrade Genero, or ask the publisher for a build.
  assert_contains "Genero 4, 5, 6"
}
it "installing with no compatible build fails and names the builds that exist" \
  _variant_no_build_at_all_fails

# ── Review follow-ups ────────────────────────────────────────────────────────

# A second configured registry engages multi-provider routing for every
# consuming command. The configured secondary is reachable and 404s everything
# (see lib/mock.sh), so unitfx still resolves from gi — through RepositorySet.
_variant_with_secondary() {
  cat > fglpkg.json <<EOF
{ "name":"app", "version":"1.0.0",
  "registries":[{"name":"myrepo","type":"artifactory","url":"$(mock_secondary_url)","repoKey":"generic-local","priority":2}] }
EOF
}

# `info` reads metadata and installs nothing, so a latest version with no build
# for this runtime must still be described — its Variants line is the answer.
_variant_info_multi_registry() {
  _variant_env 4.01.12 || return 1
  _variant_with_secondary
  run info unitfx
  assert_success
  assert_contains "unitfx@1.0.1"
  assert_match "Variants: +6"
}
it "info describes a version with no build for this Genero (multi-registry)" \
  _variant_info_multi_registry

# The multi-provider resolve names what each version publishes, like the
# single-registry one.
_variant_multi_registry_error_names_builds() {
  _variant_env 3.20.05 || return 1
  _variant_with_secondary
  run install unitfx
  assert_failure
  assert_contains "has a build for Genero 3"
  assert_contains "1.0.0 has builds for Genero 4, 5, 6"
}
it "a multi-registry install with no compatible build names the builds that exist" \
  _variant_multi_registry_error_names_builds

# `outdated` must not offer a version `update` will never select: on Genero 4,
# 1.0.0 is the newest runnable unitfx, so the row is ok and the gate passes.
_variant_outdated_agrees_with_update() {
  _variant_env 4.01.12 || return 1
  cat > fglpkg.json <<'EOF'
{ "name":"app", "version":"1.0.0", "dependencies": { "fgl": { "unitfx": "^1.0.0" } } }
EOF
  run install
  assert_success
  run outdated
  assert_success
  assert_match "unitfx +1\.0\.0 +1\.0\.0 +1\.0\.0 +ok"
}
it "outdated does not offer a version with no build for this Genero" \
  _variant_outdated_agrees_with_update

# A manifest pin to a version that has no build here is not a constraint clash;
# the conflict must say why the existing version cannot be used.
_variant_pin_explains_genero() {
  _variant_env 4.01.12 || return 1
  cat > fglpkg.json <<'EOF'
{ "name":"app", "version":"1.0.0", "dependencies": { "fgl": { "unitfx": "1.0.1" } } }
EOF
  run install
  assert_failure
  assert_contains 'version conflict for "unitfx"'
  assert_contains "no matching version can be used on Genero 4.01.12"
  assert_contains "1.0.1 has builds for Genero 6"
}
it "pinning a version with no build for this Genero explains the conflict" \
  _variant_pin_explains_genero
