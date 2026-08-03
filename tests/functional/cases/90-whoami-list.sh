suite "whoami / logout / list"

_whoami() {
  run whoami
  # Not-logged-in is a non-zero exit by design (cli.go cmdWhoami returns an
  # error), so `if fglpkg whoami; then ...` is a usable auth check in a script.
  # This asserted exit 0 until the harness started enforcing mid-body failures.
  assert_exit 1
  assert_contains "not logged in"
}
it "whoami with no credentials reports not-logged-in" _whoami

_logout() {
  run logout
  assert_success           # graceful no-op when nothing is stored
}
it "logout with no credentials is graceful" _logout

_list_empty() {
  run list
  assert_success
  assert_contains "No packages installed"
}
it "list reports an empty install" _list_empty
