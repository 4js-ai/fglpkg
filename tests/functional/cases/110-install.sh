suite "install (mock registry)"

_install_pinned() {
  mock_registry_start
  run install demo.pkg@1.0.0
  assert_success
  assert_dir ".fglpkg/packages/demo-pkg"          # installed under the canonical slug
  assert_file ".fglpkg/packages/demo-pkg/fglpkg.json"
  assert_file "fglpkg.lock"
  assert_file_contains "fglpkg.lock" "demo-pkg"
  assert_file_contains "fglpkg.lock" "1.0.0"
}
it "install <pkg>@<version> downloads, verifies, extracts, and locks" _install_pinned

_install_latest() {
  mock_registry_start
  run install demo.pkg
  assert_success
  assert_dir ".fglpkg/packages/demo-pkg"
  assert_file_contains "fglpkg.lock" "1.1.0"       # resolves the latest version
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
