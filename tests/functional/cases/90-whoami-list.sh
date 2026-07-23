suite "whoami / logout / list"

_whoami() {
  run whoami
  assert_success
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
