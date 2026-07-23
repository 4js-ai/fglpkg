suite "info (mock registry)"

_info_latest() {
  mock_registry_start
  run info demo.pkg
  assert_success
  assert_contains "demo-pkg"
  assert_contains "1.1.0"          # latest of the two fixture versions
}
it "info shows registry metadata for the latest version" _info_latest

_info_pinned() {
  mock_registry_start
  run info demo.pkg@1.0.0
  assert_success
  assert_contains "1.0.0"
}
it "info <pkg>@<version> shows that version" _info_pinned

_info_missing() {
  mock_registry_start
  run info no-such-pkg
  assert_failure                    # 404 → ErrNotFound
}
it "info on an unknown package fails" _info_missing
