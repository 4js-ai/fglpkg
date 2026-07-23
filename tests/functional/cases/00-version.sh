suite "version"

_version_prints() { run version; assert_success; assert_contains "fglpkg version"; }
it "version prints the tool version" _version_prints

# Release builds inject a semver; a plain `go build` (dev build, and CI here)
# reports "dev". Accept either so the suite is green on an un-stamped binary.
_version_token() { run version; assert_match 'version ([0-9]+\.[0-9]+\.[0-9]+|dev)'; }
it "version output contains a version token (semver or dev)" _version_token

_version_flag() { run --version; assert_success; assert_contains "fglpkg version"; }
it "--version prints the tool version" _version_flag
