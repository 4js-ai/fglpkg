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

# `.fglpkg-installed` is the INSTALLER's record of what it put in a package
# directory, and the installer relies on being its only writer: a published zip
# carrying one overwrites the incomplete marker written just before extraction,
# so a failed install reads as a finished one on the next replay (GIS-586).
#
# The way such a zip gets built is mundane — packing a directory that was itself
# installed, e.g. vendoring a package into Artifactory. A broad `files` glob
# picks the file up, and because "." sorts first it lands as the archive's FIRST
# entry, ahead of everything the installer writes afterwards.
_ignore_never_packs_the_install_stamp() {
  mkpkg
  printf '{"version":"9.9.9","complete":true}\n' > .fglpkg-installed
  printf 'stub' > keep.txt
  # A glob broad enough to sweep up everything in the directory, which is what
  # makes this reachable at all.
  python3 - fglpkg.json <<'PY' || return 1
import json, sys
with open(sys.argv[1]) as fh:
    m = json.load(fh)
m["files"] = ["*"]
with open(sys.argv[1], "w") as fh:
    json.dump(m, fh, indent=2)
PY

  run pack --list
  assert_success || return 1
  # The glob really is broad — without this the case would pass vacuously.
  assert_contains "keep.txt" || return 1
  assert_not_contains ".fglpkg-installed" || return 1
}
it "pack never ships fglpkg's own install stamp, however broad the glob" \
  _ignore_never_packs_the_install_stamp
