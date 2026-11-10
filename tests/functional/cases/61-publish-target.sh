suite "publish target guard (GIS-600)"

# With no --registry, no FGLPKG_PUBLISH_REGISTRY and no defaultRegistry, publish
# used to submit to the PUBLIC GI registry silently. That is the state right
# after installing fglpkg, so the documented bare `fglpkg publish` uploaded
# whatever was in the directory to a registry shared by every partner — and a
# submitted version cannot be withdrawn from the command line.
#
# run() gives the command no stdin, which is exactly the non-interactive case.
_pub_no_target_refused() {
  mkpkg
  export FGLPKG_TOKEN="dummy-token"
  run publish
  assert_failure
  assert_contains "no publish target is configured"
  assert_contains "service.generointelligence.ai"
}
it "bare publish with no target configured refuses instead of submitting" _pub_no_target_refused

# A refusal that does not say how to proceed just moves the problem, so the
# message must carry both ways out and where to look.
_pub_no_target_actionable() {
  mkpkg
  export FGLPKG_TOKEN="dummy-token"
  run publish
  assert_contains "--registry gi"
  assert_contains "registry list"
  assert_contains "--publish-default"
}
it "the refusal names the explicit opt-in and how to set a default" _pub_no_target_actionable

# The guard runs before the package is built: nothing is packed, compiled or
# staged for a publish that was never going to happen.
#
# The assertion has to be something the BUILD actually prints, or it cannot
# fail. An earlier version asserted that the output did not match
# "Building|Packing|Uploading|Submitting" — words no build step ever emits — so
# it passed just as happily with the guard moved to AFTER enforceLint, and
# pinned nothing. The unit tests cannot see call order either: they invoke
# confirmPublishTarget directly.
#
# A `files` pattern matching nothing makes the build emit a warning of its own,
# which is the observable that distinguishes the two orderings. Verified both
# ways: on this branch the output is only the refusal; with the guard moved
# after the build the first line is
#   warning: files: pattern "nomatch-*.42m" matched no files under root "."
_pub_no_target_before_build() {
  mkpkg
  export FGLPKG_TOKEN="dummy-token"
  python3 - <<'PYEOF' || return 1
import json
m = json.load(open("fglpkg.json"))
m["files"] = ["nomatch-*.42m"]
json.dump(m, open("fglpkg.json", "w"))
PYEOF
  rm -f mod.42m
  run publish
  assert_failure || return 1
  assert_contains "no publish target is configured" || return 1
  # The build's own diagnostic must be absent: reaching it means the archive
  # was staged for a publish the guard was about to refuse.
  assert_not_contains "matched no files" || return 1
}
it "the refusal happens before anything is built" _pub_no_target_before_build

# --dry-run uploads nothing by definition, so it is NOT blocked — failing it
# would break pipelines that use it to validate a manifest. It states the target
# instead, which is the other half of the complaint: the fallback was invisible.
_pub_dryrun_warns_not_blocked() {
  mkpkg
  export FGLPKG_TOKEN="dummy-token"
  run publish --dry-run
  assert_success
  assert_contains "no publish target is configured"
}
it "publish --dry-run warns about the fallback but still runs" _pub_dryrun_warns_not_blocked

# Naming the target is the user making the choice, so there is nothing to warn
# about — and no prompt, which would otherwise appear on every publish.
_pub_explicit_gi_no_warning() {
  mkpkg
  export FGLPKG_TOKEN="dummy-token"
  run publish --registry gi --dry-run
  assert_success
  assert_not_contains "no publish target is configured"
}
it "publish --registry gi is an explicit choice and warns nothing" _pub_explicit_gi_no_warning

# `registry list` used to print "-" on the gi row, documented as "no default
# points here" — the opposite of the truth for the one row publish targets.
_registry_list_marks_fallback() {
  run registry list
  assert_success
  assert_match "gi .*publish\*"
}
it "registry list marks the built-in gi fallback as the publish target" _registry_list_marks_fallback

# --publish-default is the way out of being asked. Once set, the gi row stops
# being the fallback and the named repository carries the publish default.
_registry_publish_default() {
  run registry add acme https://acme.example/artifactory/GeneroBDL --publish-default
  assert_success
  assert_contains "default publish target"
  run registry list
  assert_match "acme .*publish"
  assert_match "gi .*-"
}
it "registry add --publish-default moves the publish target off gi" _registry_publish_default

# The two defaults are separate knobs: adding a consume default must not
# silently rewire where packages are published to.
_consume_default_leaves_publish_alone() {
  run registry add corp https://corp.example/artifactory/GeneroBDL --consume-default
  assert_success
  run registry list
  assert_match "corp .*consume"
  assert_match "gi .*publish\*"
}
it "--consume-default does not change the publish target" _consume_default_leaves_publish_alone

# The repository field is the package's public source-code URL and is shown on
# the registry listing; a local checkout path there reaches nobody. `init`
# accepts it without comment, so publish is where it gets flagged.
_pub_warns_non_url_repository() {
  mkpkg
  export FGLPKG_TOKEN="dummy-token"
  python3 - <<'PY'
import json
m = json.load(open("fglpkg.json"))
m["repository"] = r"C:\work\git\repository\test.git"
json.dump(m, open("fglpkg.json", "w"), indent=2)
PY
  run publish --registry gi --dry-run
  assert_contains "not an http(s) URL"
}
it "publish warns when \"repository\" is not an http(s) URL" _pub_warns_non_url_repository
