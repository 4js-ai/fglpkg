suite "artifactory legacy folder names (GIS-610)"

# fglpkg <= 4.2.9 published to an Artifactory repository under the manifest's
# name as its author typed it; 4.2.10 publishes under the canonical slug, and
# canonicalizes every lookup, which is what makes a package reachable however
# its name is typed. The cost is that a folder left behind by the old publisher
# — "DateTime/" rather than "datetime/" — is no longer addressable.
#
# `install` could never reach one: it resolved the typed name against the
# server's own spelling and 404'd as often as not. `info` could, and in the
# release candidate it stopped, which is the regression these cases pin. The
# fallback is deliberately narrow:
#
#   * only `info`, which describes a package without recording it anywhere. A
#     package installed out of DateTime/ would be written into fglpkg.json and
#     the lock under the slug "datetime", where the next install cannot find it.
#   * only after the canonical lookup has failed, so a repository that holds
#     both folders still answers from the canonical one.
#   * only when the two spellings differ: `info datetime` against a repository
#     holding only DateTime/ has nothing to guess from, and says so.
#
# The mock holds its tree in memory and matches it exactly, so DateTime/ and
# datetime/ stay two packages on a case-insensitive filesystem.

_legacy_only()    { mock_registry_start && mock_artifactory_start "DateTime:0.1.0"; }
_canonical_only() { mock_registry_start && mock_artifactory_start "datetime:0.2.0"; }
_both()           { mock_registry_start && mock_artifactory_start "DateTime:0.1.0" "datetime:0.2.0"; }

# ── the regression itself ────────────────────────────────────────────────────

_alf_legacy_typed_exactly() {
  _legacy_only
  mock_artifactory_project
  run info DateTime
  assert_success
  assert_contains "DateTime@0.1.0" "$output"
  # ...and the output says the folder is a leftover, because nothing else can
  # reach it and the reader's next move is to re-publish or rename.
  assert_contains "cannot reach it" "$output"
  assert_contains "4.2.9" "$output"
  # ...and it does not then offer, on stdout, the install command the warning
  # has just said cannot work. A reader who redirected stdout to a file would
  # have seen only that hint.
  assert_contains "Install: not possible" "$output"
  assert_not_contains "fglpkg install DateTime@0.1.0" "$output"
}

_alf_legacy_typed_canonically() {
  _legacy_only
  mock_artifactory_project
  run info datetime
  assert_failure
  assert_contains "not found" "$output"
  # No warning: there is nothing to warn ABOUT. The name asked for is already
  # the slug, so there is no second spelling to try.
  assert_not_contains "cannot reach it" "$output"
}

# ── the fallback must not displace the canonical lookup ──────────────────────

_alf_canonical_either_spelling() {
  _canonical_only
  mock_artifactory_project
  run info datetime
  assert_success
  assert_contains "datetime@0.2.0" "$output"
  assert_not_contains "cannot reach it" "$output"

  run info DateTime
  assert_success
  assert_contains "@0.2.0" "$output"
  # The canonicalizing lookup found it on the first try, so this is the normal
  # path and not a leftover folder.
  assert_not_contains "cannot reach it" "$output"
}

_alf_canonical_wins_when_both_exist() {
  _both
  mock_artifactory_project
  run info DateTime
  assert_success
  # 0.2.0 lives under datetime/, 0.1.0 under DateTime/. The canonical folder
  # answers, so the version proves which one was read.
  assert_contains "@0.2.0" "$output"
  assert_not_contains "0.1.0" "$output"
  assert_not_contains "cannot reach it" "$output"
}

# ── everything else is unchanged ─────────────────────────────────────────────

_alf_install_still_cannot_reach_it() {
  _legacy_only
  mock_artifactory_project
  run install DateTime
  assert_failure
  assert_contains "not found" "$output"
  # And nothing was written: a package install cannot reach must not appear in
  # the manifest under a slug that resolves nowhere.
  assert_not_contains "datetime" "$(cat fglpkg.json)"
}

_alf_search_describes_each_folder_itself() {
  _both
  mock_artifactory_project
  run search date
  assert_success
  # Search lists what the repository reported, folder by folder, and reads each
  # one's versions from its own name — so both spellings appear, with their own
  # contents. Canonicalizing there would describe one folder with the other's.
  assert_contains "DateTime" "$output"
  assert_contains "0.1.0" "$output"
  assert_contains "0.2.0" "$output"
}

it "info reaches a legacy folder by its exact name, and says so" _alf_legacy_typed_exactly
it "info does not guess a legacy spelling from the slug"        _alf_legacy_typed_canonically
it "a canonical folder answers either spelling, with no note"   _alf_canonical_either_spelling
it "the canonical folder wins when both exist"                  _alf_canonical_wins_when_both_exist
it "install still cannot reach a legacy folder"                 _alf_install_still_cannot_reach_it
it "search describes each folder by its own contents"           _alf_search_describes_each_folder_itself

# ── a refusal and a collision are answers, not absences ──────────────────────
#
# The fallback runs only after the canonical lookup has returned "not found", so
# whatever IT reports has to survive the second lookup. Two ways it can be an
# answer rather than an absence, and fglpkg <= 4.2.9 reported both:
#
#   * the folder is in two repositories, and no scoping says which — the
#     collision guard refuses to guess (spec §7.2);
#   * a repository answers 401 — credentials that do not cover this package say
#     nothing about whether it is there.
#
# Reporting the canonical "not found in any configured repository" for either
# tells the reader the opposite of what was measured: that the package is
# nowhere. The 401 rows are the narrow kind, where only one folder is refused,
# because that is what separates the two spellings of one name inside a single
# repository.

_alf_legacy_in_two_repositories() {
  mock_registry_start
  mock_artifactory_start "$ARTI_REPO/DateTime:0.1.0" "$ARTI_REPO2/DateTime:0.1.0"
  mock_artifactory_project_repos "$ARTI_REPO" "$ARTI_REPO2"
  run info DateTime
  assert_failure
  assert_contains "more than one repository" "$output"
  assert_contains "arti1" "$output"
  assert_contains "arti2" "$output"
  assert_not_contains "not found in any configured repository" "$output"
}

_alf_legacy_folder_refused_by_one_repository() {
  mock_registry_start
  # arti1 holds DateTime/; arti2 refuses exactly that folder. Neither holds the
  # canonical datetime/, so the canonical lookup reports "not found" and the
  # fallback is what meets the 401.
  mock_artifactory_start "$ARTI_REPO/DateTime:0.1.0" "401:$ARTI_REPO2/DateTime"
  mock_artifactory_project_repos "$ARTI_REPO" "$ARTI_REPO2"
  run info DateTime
  assert_failure
  assert_contains "authentication failed (401)" "$output"
  assert_not_contains "not found in any configured repository" "$output"
  # And nothing was described from arti1 on the strength of a half-read set.
  assert_not_contains "DateTime@0.1.0" "$output"
}

_alf_canonical_refusal_is_not_an_absence() {
  mock_registry_start
  # arti1 refuses the CANONICAL folder, arti2 holds only the legacy one. The
  # canonical lookup fails with a 401, not with "not found", so the fallback
  # must not run: answering DateTime@0.1.0 here would describe the package arti2
  # happens to hold while hiding that arti1 could not be read at all.
  mock_artifactory_start "401:$ARTI_REPO/datetime" "$ARTI_REPO2/DateTime:0.1.0"
  mock_artifactory_project_repos "$ARTI_REPO" "$ARTI_REPO2"
  run info DateTime
  assert_failure
  assert_contains "authentication failed (401)" "$output"
  assert_not_contains "DateTime@0.1.0" "$output"
}

_alf_absent_everywhere_names_what_was_searched() {
  _legacy_only
  mock_artifactory_project
  # A name that is in no repository under either spelling. The fallback runs
  # (the name is not its own slug) and finds nothing, and what must come back is
  # the CANONICAL lookup's error, which names the repositories searched — not
  # the bare "not found" of the second lookup, which names nothing.
  run info No.Such
  assert_failure
  assert_contains "not found in any configured repository" "$output"
  assert_contains "arti" "$output"
  assert_not_contains "cannot reach it" "$output"
}

it "a legacy folder in two repositories is refused, not called absent" _alf_legacy_in_two_repositories
it "a 401 on the legacy folder is reported, not called absent"        _alf_legacy_folder_refused_by_one_repository
it "a 401 on the canonical folder stops the legacy fallback"          _alf_canonical_refusal_is_not_an_absence
it "a name absent under both spellings names what was searched"       _alf_absent_everywhere_names_what_was_searched
