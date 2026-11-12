suite "publish name canonicalization (GIS-610)"

# A package published to an Artifactory repository under the manifest's verbatim
# name is unreachable. Install always asks for the CANONICAL slug — lowercase,
# with runs of '-'/'_'/'.' collapsed to '-' (PEP 503, GIS-271) — because the
# resolver canonicalizes every lookup, and Artifactory paths are case-sensitive.
# The customer published "DateTime", install reported the package could not be
# found, and republishing under an all-lowercase name was the only way through.
#
# --dry-run prints the exact PUT URLs and makes no network call, so the stored
# path is observable here without a live repository. The URL is unreachable on
# purpose: reaching it would mean the dry run is not dry.
_artifactory_registry_block() {
  python3 - <<'PYEOF' || return 1
import json
m = json.load(open("fglpkg.json"))
m["registries"] = [{
    "name": "myrepo",
    "type": "artifactory",
    "url": "https://artifactory.invalid/artifactory",
    "repoKey": "generic-local",
    "auth": "anonymous",
    "priority": 2,
}]
json.dump(m, open("fglpkg.json", "w"), indent=2)
PYEOF
}

_pub_artifactory_canonicalizes_the_name() {
  mkpkg DateTime 0.1.0
  _artifactory_registry_block || return 1
  run publish --registry myrepo --dry-run
  assert_success || return 1
  # Both the folder and the zip file name carry the slug, because variantTag
  # parses the zip name back using the folder name as its prefix.
  assert_contains "/generic-local/datetime/0.1.0/datetime-0.1.0-" || return 1
  assert_contains "/generic-local/datetime/0.1.0/fglpkg.json" || return 1
  # The whole point: nothing is stored under the verbatim name.
  assert_not_contains "/DateTime/"
}
it "a mixed-case name is published under its lowercase slug" _pub_artifactory_canonicalizes_the_name

# Telling the user what happened is half the fix: they chose the name, and the
# path they will see in Artifactory is not the one they typed. The GI path has
# printed this line since GIS-271; the Artifactory path now matches it.
_pub_artifactory_reports_the_normalization() {
  mkpkg DateTime 0.1.0
  _artifactory_registry_block || return 1
  run publish --registry myrepo --dry-run
  assert_success || return 1
  assert_contains "Normalized name" || return 1
  assert_contains "DateTime" || return 1
  assert_contains "datetime"
}
it "publish reports the name it normalized, as the GI path does" _pub_artifactory_reports_the_normalization

# Separators collapse as well as case: "My_Date.Lib" and "my-date-lib" are the
# same package. Pinning this stops a fix that only lowercases.
_pub_artifactory_collapses_separators() {
  mkpkg My_Date.Lib 0.1.0
  _artifactory_registry_block || return 1
  run publish --registry myrepo --dry-run
  assert_success || return 1
  assert_contains "/generic-local/my-date-lib/0.1.0/my-date-lib-0.1.0-" || return 1
  assert_not_contains "/My_Date.Lib/"
}
it "separator runs collapse too, not just case" _pub_artifactory_collapses_separators

# A name that is already canonical must print no normalization line and must
# reach the same path it always did — this fix changes nothing for the packages
# people have already published correctly.
_pub_artifactory_leaves_a_canonical_name_alone() {
  mkpkg demo-pkg 0.1.0
  _artifactory_registry_block || return 1
  run publish --registry myrepo --dry-run
  assert_success || return 1
  assert_contains "/generic-local/demo-pkg/0.1.0/demo-pkg-0.1.0-" || return 1
  assert_not_contains "Normalized name"
}
it "an already-canonical name is untouched and unannounced" _pub_artifactory_leaves_a_canonical_name_alone

# The slug rules are enforced for BOTH publish paths by ValidateForPublish,
# which runs before either provider is chosen, so a name that cannot normalize
# to a valid slug is refused before any upload — including in --dry-run.
_pub_artifactory_rejects_an_unusable_name() {
  mkpkg "!!" 0.1.0
  _artifactory_registry_block || return 1
  run publish --registry myrepo --dry-run
  assert_failure || return 1
  assert_contains "not ready to publish" || return 1
  assert_contains "not a valid package name"
}
it "a name with no valid slug is refused before any upload" _pub_artifactory_rejects_an_unusable_name

# The GI path has canonicalized since GIS-271, and both paths now call the same
# publishSlug() — which is the whole argument for extracting it, since the bug
# was that one path had the logic and the other did not. Pin the GI call site
# too: without this, replacing its publishSlug() call with the raw manifest name
# passes every unit test and all eighteen other functional publish cases.
#
# --dry-run prints the request bodies and the artifact URL, so the slug and the
# display name are both observable with no network access. FGLPKG_TOKEN is a
# dummy: a dry run never sends it.
_pub_gi_canonicalizes_the_name() {
  mkpkg DateTime 0.1.0
  export FGLPKG_TOKEN="dummy-token-for-dry-run"
  run publish --dry-run
  assert_success || return 1
  assert_contains "Normalized name" || return 1
  # The slug is the identity in every /registry/... path...
  assert_contains 'slug:"datetime"' || return 1
  assert_contains "/registry/packages/datetime/versions" || return 1
  assert_contains "filename=datetime-0.1.0-" || return 1
  # ...while the name the author wrote travels as the display name.
  assert_contains 'name:"DateTime"' || return 1
  assert_not_contains "/registry/packages/DateTime"
}
it "the GI path publishes under the slug and keeps the display name" _pub_gi_canonicalizes_the_name
