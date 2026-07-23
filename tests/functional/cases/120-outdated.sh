suite "outdated (mock registry)"

# outdated is a CI gate: non-zero exit when an update is available.
_outdated_reports() {
  mock_registry_start
  run install demo.pkg@1.0.0; assert_success
  run outdated
  assert_failure                       # 1.1.0 available → non-zero
  assert_contains "1.1.0"
  assert_contains "out of date"
}
it "outdated reports a newer version and exits non-zero" _outdated_reports

_outdated_uptodate() {
  mock_registry_start
  run install demo.pkg; assert_success   # installs latest (1.1.0)
  run outdated
  assert_success                          # nothing newer → exit 0
}
it "outdated exits zero when everything is current" _outdated_uptodate
