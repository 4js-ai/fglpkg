suite "deprecate (mock registry)"

_deprecate_package() {
  mock_registry_start
  run deprecate demo.pkg "no longer maintained"
  assert_success
}
it "deprecate marks a whole package (with a message)" _deprecate_package

_deprecate_version() {
  mock_registry_start
  run deprecate demo.pkg@1.0.0 "superseded by 1.1.0"
  assert_success
}
it "deprecate <pkg>@<version> marks a single version" _deprecate_version

_deprecate_moved_to() {
  mock_registry_start
  run deprecate demo.pkg --moved-to other-pkg
  assert_success
}
it "deprecate --moved-to records a successor" _deprecate_moved_to

_deprecate_undo() {
  mock_registry_start
  run deprecate demo.pkg --undo
  assert_success
}
it "deprecate --undo clears deprecation" _deprecate_undo
