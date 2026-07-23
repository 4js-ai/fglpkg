suite "pack"

_pack_list() {
  mkpkg
  run pack --list
  assert_success
  assert_contains "demo.pkg@1.0.0"
  assert_contains "fglpkg.json"
  assert_contains "mod.42m"
  assert_match "SHA256:"
}
it "pack --list shows contents and metadata" _pack_list

_pack_writes_zip() {
  mkpkg
  run pack -o out.zip
  assert_success
  assert_file out.zip
}
it "pack -o writes a zip" _pack_writes_zip

_pack_reproducible() {
  mkpkg
  run pack -o a.zip; assert_success
  run pack -o b.zip; assert_success
  assert_eq "$(sha256 a.zip)" "$(sha256 b.zip)"
}
it "pack is byte-reproducible (identical SHA-256 twice)" _pack_reproducible
