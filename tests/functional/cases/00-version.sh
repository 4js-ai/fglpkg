suite "version"

_version_prints() { run version; assert_success; assert_contains "fglpkg version"; }
it "version prints the tool version" _version_prints

# Release builds inject a semver; a plain `go build` (dev build, and CI here)
# reports "dev". Accept either so the suite is green on an un-stamped binary.
_version_token() { run version; assert_match 'version ([0-9]+\.[0-9]+\.[0-9]+|dev)'; }
it "version output contains a version token (semver or dev)" _version_token

_version_flag() { run --version; assert_success; assert_contains "fglpkg version"; }
it "--version prints the tool version" _version_flag

_version_flag_short() { run -v; assert_success; assert_contains "fglpkg version"; }
it "-v prints the tool version" _version_flag_short

# -V is the Genero house convention: fglrun, fglcomp and the rest all answer it,
# so a Genero developer reaches for the uppercase form before --version. Until
# SUPASIA-3231 it fell through to the dispatch default and exited 1 with
# `unknown command: "-V"`.
_version_flag_upper() { run -V; assert_success; assert_contains "fglpkg version"; }
it "-V prints the tool version (the Genero convention)" _version_flag_upper
