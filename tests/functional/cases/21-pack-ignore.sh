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

# ── fglpkg's own store artifacts are never packed (GIS-586) ─────────────────
#
# `.fglpkg-installed` is the INSTALLER's record of what it put in a package
# directory, and the installer relies on being its only writer: a published zip
# carrying one overwrites the incomplete marker written just before extraction,
# so a failed install reads as a finished one on the next replay.
#
# The way such a zip gets built is mundane — packing a directory that was itself
# installed, e.g. vendoring a package into Artifactory. A broad `files` glob
# picks the file up, and because "." sorts first it lands as the archive's FIRST
# entry, ahead of everything the installer writes afterwards.

# The archive paths `pack --list` reports, without the size column. Asserting on
# the listing rather than the whole output matters here: pack WARNS about the
# file it skipped, so the name appears in the output either way.
_packed_files() { printf '%s\n' "$output" | sed -n 's/^ *[0-9][0-9]*  //p'; }

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
  assert_contains "keep.txt" "$(_packed_files)" || return 1
  assert_not_contains ".fglpkg-installed" "$(_packed_files)" || return 1
  # Skipping silently would leave the publisher wondering where the file went.
  assert_contains "install record" || return 1
}
it "pack never ships fglpkg's own install stamp, however broad the glob" \
  _ignore_never_packs_the_install_stamp

# The reservation is on the ARCHIVE path, not the project-relative one, and
# `importRoot` is what makes the two differ. A first attempt put this guard in
# the .fglpkgignore layer, which only ever sees project-relative paths — so with
# `importRoot: "dist"` the project's dist/.fglpkg-installed sailed through and
# shipped as the archive root's stamp, which is the one the installer reads.
_ignore_reservation_follows_importroot() {
  mkdir -p dist/com/acme
  printf 'stub' > dist/com/acme/m.42m
  printf '{"version":"9.9.9","complete":true}\n' > dist/.fglpkg-installed
  cat > fglpkg.json <<'EOF'
{ "name":"rebased","version":"1.0.0","genero":">=3.20","license":"MIT",
  "author":"fglpkg tests","importRoot":"dist","files":["dist/*","dist/**/*"] }
EOF

  run pack --list
  assert_success || return 1
  # The glob really does reach into dist/ and the rebase really happens —
  # without this the case would pass vacuously.
  assert_contains "com/acme/m.42m" "$(_packed_files)" || return 1
  assert_not_contains ".fglpkg-installed" "$(_packed_files)" || return 1
}
it "the install-stamp reservation follows importRoot to the archive root" \
  _ignore_reservation_follows_importroot

# The converse, so the reservation cannot quietly widen into "any file with this
# name anywhere": one that lands away from the archive root is ordinary package
# content and ships.
_ignore_reservation_is_archive_root_only() {
  mkpkg
  mkdir -p docs
  printf 'not the stamp\n' > docs/.fglpkg-installed
  python3 - fglpkg.json <<'PY' || return 1
import json, sys
with open(sys.argv[1]) as fh:
    m = json.load(fh)
m["files"] = ["*", "docs/*"]
with open(sys.argv[1], "w") as fh:
    json.dump(m, fh, indent=2)
PY

  run pack --list
  assert_success || return 1
  assert_contains "docs/.fglpkg-installed" "$(_packed_files)" || return 1
}
it "a stamp-named file away from the archive root still ships" \
  _ignore_reservation_is_archive_root_only
