suite "registry name scopes (GIS-610)"

# `registries[].packages` is an allow-list of package NAMES: a repository is
# consulted only for names matching one of its globs. It is the dependency-
# confusion hardening the Artifactory guide recommends, so a scope that routes
# one spelling of a package and not another does not merely annoy — it drops a
# repository out of the fan-out, and "not found in any configured repository"
# then lists everything EXCEPT the repository holding the package, because the
# predicate runs before the lookup.
#
# The observable is the routing decision itself: whether the secondary was
# dialed. `mock_secondary_url` makes that visible in both directions — it is
# reachable and 404s, so the fan-out completes and gi still answers either way.
#
# `fglpkg info <name>` is the instrument for the spelling matrix because it
# routes the name exactly as typed. install routes twice: its resolution phase
# has already canonicalized the name by the time it routes, so install alone
# cannot show what a scope does with a spelling — it dials on the second pass
# whatever the first decided. The two installs at the end cover the whole path.
#
# The fixture package is named "demo.pkg" and its canonical slug is "demo-pkg".
# Both spellings name one package, so every scope below must treat them alike.
#
# The suite had no coverage of scopes at all before this, which is how two
# separate spelling splits — one per side of the match — reached a customer.

_rs_project() {  # _rs_project [<json-array-of-globs>]
  local scope=""
  [[ -n "${1:-}" ]] && scope=",\"packages\":$1"
  cat > fglpkg.json <<EOF
{ "name":"app", "version":"1.0.0", "genero":">=3.20",
  "registries":[{"name":"myrepo","type":"artifactory","url":"$(mock_secondary_url)",
                 "repoKey":"generic-local","priority":2$scope}] }
EOF
}

# ── the name as typed ────────────────────────────────────────────────────────
# A scope written in canonical form, asked about by the name its author wrote.

_rs_name_as_written() {
  mock_registry_start
  _rs_project '["demo-*"]'
  run info demo.pkg
  assert_success
  assert_secondary_dialed
}
it "a canonical scope admits the name as its author writes it" _rs_name_as_written

_rs_name_upper_case() {
  mock_registry_start
  _rs_project '["demo-*"]'
  run info DEMO.PKG
  assert_success
  assert_secondary_dialed
}
it "a canonical scope admits a shouted spelling of the same name" _rs_name_upper_case

# ── the pattern as written ───────────────────────────────────────────────────
# A scope is normally written to match the manifest name, so a package called
# "Demo_Pkg" gets the glob "demo_*". That must reach the same package as
# "demo-*", or the scope admits only spellings that fail a step later.

_rs_pattern_underscored() {
  mock_registry_start
  _rs_project '["demo_*"]'
  run info demo.pkg
  assert_success
  assert_secondary_dialed
}
it "an underscored scope admits the package" _rs_pattern_underscored

_rs_pattern_mixed_case() {
  mock_registry_start
  _rs_project '["DEMO-*"]'
  run info demo.pkg
  assert_success
  assert_secondary_dialed
}
it "a mixed-case scope admits the package" _rs_pattern_mixed_case

# These two are the split itself, as a control and its variant: one scope, one
# package, two spellings of its name. The dotted glob matched `demo.pkg` as
# written and `demo-pkg` not at all, so the repository was in the fan-out for
# one of them and absent for the other. The control passed before the fix.
_rs_dotted_as_written() {
  mock_registry_start
  _rs_project '["demo.*"]'
  run info demo.pkg
  assert_success
  assert_secondary_dialed
}
it "a dotted scope admits the name as written" _rs_dotted_as_written

_rs_dotted_by_slug() {
  mock_registry_start
  _rs_project '["demo.*"]'
  run info demo-pkg
  assert_success
  assert_secondary_dialed
}
it "...and the same package asked for by its slug" _rs_dotted_by_slug

# ── and the scope still scopes ───────────────────────────────────────────────
# Normalizing both sides widens the SPELLINGS a glob covers, never the names.

_rs_other_family() {
  mock_registry_start
  _rs_project '["acme-*"]'
  run info demo.pkg
  assert_success
  assert_secondary_not_dialed
}
it "a name outside the scope never reaches the repository" _rs_other_family

# The same exclusion for a glob that is not in canonical form, so the widening
# cannot be mistaken for "a non-canonical glob matches anything".
_rs_other_family_underscored() {
  mock_registry_start
  _rs_project '["acme_*"]'
  run info demo.pkg
  assert_success
  assert_secondary_not_dialed
}
it "an underscored scope for another family still excludes" _rs_other_family_underscored

# A near miss: the scope names one package exactly, and a longer name that
# merely starts with it is not that package.
_rs_exact_scope() {
  mock_registry_start
  _rs_project '["demo"]'
  run info demo.pkg
  assert_success
  assert_secondary_not_dialed
}
it "an exact scope does not admit a longer name" _rs_exact_scope

# ── the whole path ───────────────────────────────────────────────────────────
# install routes twice — once for the name as typed, once for the resolved
# slug. Under a scope that previously admitted neither, both passes must now
# reach the repository, and the install must still complete.

_rs_install_under_a_scope() {
  mock_registry_start
  _rs_project '["demo_*"]'
  run install demo.pkg@1.0.0
  assert_success
  assert_dir ".fglpkg/packages/demo-pkg"
  assert_secondary_dialed
}
it "install consults a scoped repository for either spelling" _rs_install_under_a_scope

_rs_install_unscoped() {
  mock_registry_start
  _rs_project
  run install demo.pkg@1.0.0
  assert_success
  assert_dir ".fglpkg/packages/demo-pkg"
  assert_secondary_dialed
}
it "an unscoped repository is consulted for any name" _rs_install_unscoped
