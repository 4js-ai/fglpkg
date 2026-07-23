suite "pack .fglpkgignore"

_ignore_excludes() {
  mkpkg                          # creates mod.42m, files=["*.42m"]
  cp mod.42m secret.42m
  # sanity: without ignore, both modules are packed
  run pack --list; assert_success
  assert_contains "secret.42m"
  # with ignore, secret.42m is excluded but mod.42m remains
  printf 'secret.42m\n' > .fglpkgignore
  run pack --list; assert_success
  assert_not_contains "secret.42m"
  assert_contains "mod.42m"
}
it ".fglpkgignore excludes matching files from the pack" _ignore_excludes
