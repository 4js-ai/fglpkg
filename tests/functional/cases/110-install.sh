suite "install (mock registry)"

_install_pinned() {
  mock_registry_start
  run install demo.pkg@1.0.0
  assert_success
  assert_dir ".fglpkg/packages/demo-pkg"          # installed under the canonical slug
  assert_file ".fglpkg/packages/demo-pkg/fglpkg.json"
  assert_file "fglpkg-lock.json"
  assert_file_contains "fglpkg-lock.json" "demo-pkg"
  assert_file_contains "fglpkg-lock.json" "1.0.0"
}
it "install <pkg>@<version> downloads, verifies, extracts, and locks" _install_pinned

_install_latest() {
  mock_registry_start
  run install demo.pkg
  assert_success
  assert_dir ".fglpkg/packages/demo-pkg"
  assert_file_contains "fglpkg-lock.json" "1.1.0"       # resolves the latest version
}
it "install <pkg> resolves and installs the latest version" _install_latest

# Checksum enforcement: corrupt the served artifact so the sha256 no longer
# matches the metadata; install must fail rather than extract bad bytes.
_install_bad_checksum() {
  local fx; fx="$(mktemp -d "$_SANDBOX_ROOT/badfx.XXXXXX")"
  mock_build_fixtures "$fx"
  mock_registry_start "$fx"          # server records the sha256 of the GOOD zip here
  # now corrupt the artifact on disk: the mock re-reads the file per request, so
  # the served bytes no longer match the sha256 it already advertised in metadata.
  printf 'corrupt-bytes-not-matching-sha256' >> "$fx/demo-pkg-1.0.0-genero6.zip"
  run install demo.pkg@1.0.0
  assert_failure
}
it "install fails on a checksum mismatch" _install_bad_checksum

# GIS-283: a lock that pins a package since deleted from the registry must fail
# with an actionable message on a clean checkout, not a raw HTTP/download error.
# Simulate it: install normally to generate the lock, repoint the locked
# downloadUrl at a path the registry now 404s, drop .fglpkg/, then replay.
_install_gone_locked_dep() {
  mock_registry_start
  run install demo.pkg@1.0.0
  assert_success
  assert_file "fglpkg-lock.json"

  python3 - "$FGLPKG_REGISTRY" <<'PY'
import json, sys
reg = sys.argv[1]
with open("fglpkg-lock.json") as f:
    lock = json.load(f)
lock["packages"][0]["downloadUrl"] = reg + "/artifacts/deleted/does-not-exist.zip"
with open("fglpkg-lock.json", "w") as f:
    json.dump(lock, f, indent=2)
PY
  rm -rf .fglpkg

  run install --frozen
  assert_failure
  assert_contains "no longer available"
  assert_contains "fglpkg update"
  assert_contains "fglpkg remove"
  assert_contains "fglpkg login"         # a 404 may just mean "no access", not "deleted"
  assert_not_contains "downloading"      # not the raw HTTP error
}
it "install from a lock whose package was deleted fails with an actionable message" _install_gone_locked_dep
