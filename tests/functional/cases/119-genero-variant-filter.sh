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
# `audit signatures` to rebuild the signed variant, so it has to name the build
# that was actually delivered, not the runtime that asked for it.
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
it "the lock file records the variant that was delivered" \
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
