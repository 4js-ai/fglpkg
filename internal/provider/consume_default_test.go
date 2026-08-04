package provider

import (
	"errors"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
)

// The consume default (GIS-364) exists so a team whose Artifactory proxies every
// public name is not blocked by the collision guard on every install. These tests
// pin the property that makes that safe: it works by consulting ONE repository
// (exclusion), never by tie-breaking between two (precedence).

// collidingSet returns a set where "utils" exists in BOTH repos — the fixture a
// precedence-wins implementation would silently resolve and this one must not.
func collidingSet(t *testing.T) (*RepositorySet, *fakeProvider, *fakeProvider) {
	t.Helper()
	gi := &fakeProvider{name: "gi", versions: map[string][]string{"utils": {"1.3.0"}}}
	acme := &fakeProvider{name: "acme", versions: map[string][]string{"utils": {"0.9.0"}}}
	return NewRepositorySet([]Provider{gi, acme}, descriptors(), nil), gi, acme
}

// With a default set, an unpinned colliding name resolves from the default —
// because only the default was asked, not because it outranked the other repo.
// The version proves which provider answered: acme has 0.9.0, gi has 1.3.0, and
// gi is the HIGHER-priority repo (priority 1 vs 2), so a priority-wins
// implementation would have returned 1.3.0 here.
func TestRoute_ConsumeDefaultScopesUnpinnedName(t *testing.T) {
	rs, _, _ := collidingSet(t)
	if err := rs.SetConsumeDefault("acme"); err != nil {
		t.Fatalf("SetConsumeDefault: %v", err)
	}

	vs, err := rs.Versions("utils")
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if len(vs) != 1 || vs[0].Version.String() != "0.9.0" {
		t.Fatalf("want [0.9.0] from acme (the default), got %+v", vs)
	}
	info, err := rs.Info("utils", "0.9.0", "")
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Source != "acme" {
		t.Fatalf("source = %q, want acme", info.Source)
	}
}

// The regression guard for the acceptance criteria: the identical fixture with no
// default configured must still be a hard collision error. Nothing about adding
// the default feature may soften the unconfigured path.
func TestRoute_CollisionStillErrorsWithoutConsumeDefault(t *testing.T) {
	rs, _, _ := collidingSet(t)

	_, err := rs.Versions("utils")
	if err == nil {
		t.Fatal("expected a collision error with no consume default set")
	}
	if !errors.Is(err, resolver.ErrCollision) {
		t.Fatalf("want a collision error, got %v", err)
	}
	if !strings.Contains(err.Error(), "more than one repository") {
		t.Fatalf("collision message changed shape: %v", err)
	}
}

// Clearing the default (SetConsumeDefault("")) restores the fan-out and therefore
// the guard — the field is not a one-way door.
func TestRoute_ClearingConsumeDefaultRestoresCollisionGuard(t *testing.T) {
	rs, _, _ := collidingSet(t)
	if err := rs.SetConsumeDefault("acme"); err != nil {
		t.Fatalf("SetConsumeDefault: %v", err)
	}
	if err := rs.SetConsumeDefault(""); err != nil {
		t.Fatalf("SetConsumeDefault(\"\"): %v", err)
	}
	if _, err := rs.Versions("utils"); !errors.Is(err, resolver.ErrCollision) {
		t.Fatalf("want the collision guard back, got %v", err)
	}
}

// A per-dependency pin is the more specific declaration and outranks the sticky
// default. Overriding it would 404 every package a project deliberately pinned
// elsewhere, so this precedence is load-bearing, not cosmetic.
func TestRoute_PinBeatsConsumeDefault(t *testing.T) {
	gi := &fakeProvider{name: "gi", versions: map[string][]string{"utils": {"1.3.0"}}}
	acme := &fakeProvider{name: "acme", versions: map[string][]string{"other": {"0.9.0"}}}
	rs := NewRepositorySet([]Provider{gi, acme}, descriptors(), map[string]string{"utils": "gi"})
	if err := rs.SetConsumeDefault("acme"); err != nil {
		t.Fatalf("SetConsumeDefault: %v", err)
	}

	// "utils" lives only in gi. Had the default overridden the pin, this would be
	// a not-found from acme instead of a clean resolve from gi.
	vs, err := rs.Versions("utils")
	if err != nil {
		t.Fatalf("pinned package should resolve from its pin: %v", err)
	}
	if len(vs) != 1 || vs[0].Version.String() != "1.3.0" {
		t.Fatalf("want [1.3.0] from the pinned gi, got %+v", vs)
	}
}

// A pin declared by a depending package's manifest also outranks the default:
// pinFor covers both pin kinds, so the default sits below them both.
func TestRoute_DeclaredPinBeatsConsumeDefault(t *testing.T) {
	gi := &fakeProvider{name: "gi", versions: map[string][]string{"utils": {"1.3.0"}}}
	acme := &fakeProvider{name: "acme", versions: map[string][]string{"utils": {"0.9.0"}}}
	rs := NewRepositorySet([]Provider{gi, acme}, descriptors(), nil)
	if err := rs.SetConsumeDefault("acme"); err != nil {
		t.Fatalf("SetConsumeDefault: %v", err)
	}
	// DeclarePin warns on stderr by design; the warning itself is covered by the
	// existing DeclarePin tests, so it is captured rather than asserted here.
	var declareErr error
	captureStderr(t, func() { declareErr = rs.DeclarePin("utils", "gi") })
	if declareErr != nil {
		t.Fatalf("DeclarePin: %v", declareErr)
	}

	vs, err := rs.Versions("utils")
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if len(vs) != 1 || vs[0].Version.String() != "1.3.0" {
		t.Fatalf("want [1.3.0] from the declared pin (gi), got %+v", vs)
	}
}

// An explicit --registry is a hard restriction that outranks everything,
// including the sticky default.
func TestRoute_RestrictBeatsConsumeDefault(t *testing.T) {
	rs, _, _ := collidingSet(t)
	if err := rs.SetConsumeDefault("acme"); err != nil {
		t.Fatalf("SetConsumeDefault: %v", err)
	}
	rs.Restrict("gi")

	vs, err := rs.Versions("utils")
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if len(vs) != 1 || vs[0].Version.String() != "1.3.0" {
		t.Fatalf("want [1.3.0] from the restricted gi, got %+v", vs)
	}
}

// A package missing from the default repository is a not-found that still
// unwraps to registry.ErrNotFound — the resolver's optional-dependency handling
// and install's private-repo hint both test for it with errors.Is. The message
// must say only the default was consulted, and name both escape hatches.
func TestRoute_ConsumeDefaultNotFoundWrapsErrNotFound(t *testing.T) {
	gi := &fakeProvider{name: "gi", versions: map[string][]string{"utils": {"1.3.0"}}}
	acme := &fakeProvider{name: "acme", versions: map[string][]string{}}
	rs := NewRepositorySet([]Provider{gi, acme}, descriptors(), nil)
	if err := rs.SetConsumeDefault("acme"); err != nil {
		t.Fatalf("SetConsumeDefault: %v", err)
	}

	_, err := rs.Versions("utils")
	if !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	for _, want := range []string{`"acme"`, "default consume registry", "--registry"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("not-found message missing %q:\n%s", want, err.Error())
		}
	}
	// It must NOT name gi — gi holds the package but was deliberately skipped, so
	// pointing at it would suggest it had been consulted. (Checked as the quoted
	// name: the bare letters also occur inside "registry".)
	if strings.Contains(err.Error(), `"gi"`) {
		t.Errorf("message names gi, implying it was consulted:\n%s", err.Error())
	}
}

// A hard (non-not-found) error from the default repository aborts rather than
// degrading to "not found" — the same fail-closed rule the fan-out follows, so a
// 401 can never be mistaken for an absent package.
func TestRoute_ConsumeDefaultAuthErrorIsHardError(t *testing.T) {
	gi := &fakeProvider{name: "gi", versions: map[string][]string{"utils": {"1.3.0"}}}
	acme := &fakeProvider{name: "acme", authErr: true}
	rs := NewRepositorySet([]Provider{gi, acme}, descriptors(), nil)
	if err := rs.SetConsumeDefault("acme"); err != nil {
		t.Fatalf("SetConsumeDefault: %v", err)
	}

	_, err := rs.Versions("utils")
	if err == nil {
		t.Fatal("expected the auth failure to abort")
	}
	if errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("auth failure must not degrade to not-found: %v", err)
	}
}

// An unconfigured name fails at the point of configuration, with the configured
// names listed — a typo in fglpkg.json should not surface later as a confusing
// not-found during resolution.
func TestSetConsumeDefault_UnknownRegistryErrors(t *testing.T) {
	rs, _, _ := collidingSet(t)

	err := rs.SetConsumeDefault("bogus")
	if err == nil {
		t.Fatal("expected an error for an unconfigured registry")
	}
	for _, want := range []string{`"bogus"`, "not configured", "gi, acme"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message missing %q:\n%s", want, err.Error())
		}
	}
	// A rejected name must leave the set unscoped, so the guard still applies.
	if _, err := rs.Versions("utils"); !errors.Is(err, resolver.ErrCollision) {
		t.Fatalf("a rejected default must not scope the set, got %v", err)
	}
}

// The `packages` allow-list does not filter the default out of its own routing —
// matching how an explicit --registry behaves. Without this, declaring a
// name-scope on the default repo would make every out-of-scope name unresolvable
// in a way the fan-out path never does.
func TestRoute_ConsumeDefaultIgnoresPackagesAllowList(t *testing.T) {
	gi := &fakeProvider{name: "gi", versions: map[string][]string{"utils": {"1.3.0"}}}
	acme := &fakeProvider{name: "acme", versions: map[string][]string{"utils": {"0.9.0"}}}
	descs := descriptors()
	descs[1].Packages = []string{"acme-*"} // does not admit "utils"
	rs := NewRepositorySet([]Provider{gi, acme}, descs, nil)
	if err := rs.SetConsumeDefault("acme"); err != nil {
		t.Fatalf("SetConsumeDefault: %v", err)
	}

	vs, err := rs.Versions("utils")
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if len(vs) != 1 || vs[0].Version.String() != "0.9.0" {
		t.Fatalf("want [0.9.0] from the named default, got %+v", vs)
	}
}
