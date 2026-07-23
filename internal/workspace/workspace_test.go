package workspace_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/workspace"
)

// ─── Helpers ─────────────────────────────────────────────────────────────────

// makeWorkspace builds a temporary directory tree and returns the root path.
//
//	root/
//	  fglpkg.workspace.json
//	  core/fglpkg.json         (no local deps)
//	  utils/fglpkg.json        (depends on core locally)
//	  app/fglpkg.json          (depends on utils locally, and on "extlib" externally)
func makeWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// core — no deps
	writeMember(t, root, "core", "1.0.0", nil, nil)

	// utils — local dep on core
	writeMember(t, root, "utils", "1.1.0",
		map[string]string{"core": "*"},
		nil,
	)

	// app — local dep on utils, external dep on extlib, one Java JAR
	writeMember(t, root, "app", "2.0.0",
		map[string]string{
			"utils":  "^1.0.0",
			"extlib": "^3.0.0", // external package
		},
		[]manifest.JavaDependency{
			{GroupID: "com.google.code.gson", ArtifactID: "gson", Version: "2.10.1"},
		},
	)

	writeWorkspaceFile(t, root, []string{"core", "utils", "app"})
	return root
}

func writeMember(t *testing.T, root, name, version string,
	fglDeps map[string]string, javaDeps []manifest.JavaDependency) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll %s: %v", dir, err)
	}
	m := manifest.New(name, version, "", "")
	for n, c := range fglDeps {
		m.AddFGLDependency(n, c)
	}
	for _, dep := range javaDeps {
		m.AddJavaDependency(dep)
	}
	if err := m.Save(dir); err != nil {
		t.Fatalf("Save manifest for %s: %v", name, err)
	}
}

func writeWorkspaceFile(t *testing.T, root string, members []string) {
	t.Helper()
	if err := workspace.Init(root, members); err != nil {
		t.Fatalf("workspace.Init: %v", err)
	}
}

// ─── Load ─────────────────────────────────────────────────────────────────────

func TestLoadSuccess(t *testing.T) {
	root := makeWorkspace(t)
	ws, err := workspace.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(ws.Members) != 3 {
		t.Errorf("expected 3 members, got %d", len(ws.Members))
	}
}

func TestLoadMissingWorkspaceFile(t *testing.T) {
	_, err := workspace.Load(t.TempDir())
	if err == nil {
		t.Error("expected error for missing workspace file, got nil")
	}
}

// TestLoadEmptyMembersIsValid covers GIS-375: a workspace with no members (what
// `workspace init` produces) is a valid state, so Load must succeed with zero
// members rather than erroring.
func TestLoadEmptyMembersIsValid(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, nil) // empty members list
	ws, err := workspace.Load(root)
	if err != nil {
		t.Fatalf("Load of an empty workspace should succeed, got: %v", err)
	}
	if len(ws.Members) != 0 {
		t.Errorf("expected 0 members, got %d", len(ws.Members))
	}
}

func TestLoadMissingMemberManifest(t *testing.T) {
	root := t.TempDir()
	// Workspace file references a member with no fglpkg.json.
	writeWorkspaceFile(t, root, []string{"nonexistent"})
	_, err := workspace.Load(root)
	if err == nil {
		t.Error("expected error for missing member manifest, got nil")
	}
}

func TestLoadDuplicateMemberName(t *testing.T) {
	root := t.TempDir()
	// Two directories, same package name.
	writeMember(t, root, "a", "1.0.0", nil, nil)
	dir2 := filepath.Join(root, "b")
	os.MkdirAll(dir2, 0755)
	m := manifest.New("a", "2.0.0", "", "") // same name "a"
	m.Save(dir2)
	writeWorkspaceFile(t, root, []string{"a", "b"})

	_, err := workspace.Load(root)
	if err == nil {
		t.Error("expected error for duplicate member name, got nil")
	}
}

// ─── Topological sort ─────────────────────────────────────────────────────────

// TestTopoOrder verifies that core comes before utils, and utils before app.
func TestTopoOrder(t *testing.T) {
	root := makeWorkspace(t)
	ws, err := workspace.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	pos := make(map[string]int, len(ws.Members))
	for i, m := range ws.Members {
		pos[m.Manifest.Name] = i
	}

	if pos["core"] >= pos["utils"] {
		t.Errorf("core (%d) should come before utils (%d)", pos["core"], pos["utils"])
	}
	if pos["utils"] >= pos["app"] {
		t.Errorf("utils (%d) should come before app (%d)", pos["utils"], pos["app"])
	}
}

func TestTopoSortCycleDetected(t *testing.T) {
	root := t.TempDir()
	// a depends on b, b depends on a — cycle.
	writeMember(t, root, "a", "1.0.0", map[string]string{"b": "*"}, nil)
	writeMember(t, root, "b", "1.0.0", map[string]string{"a": "*"}, nil)
	writeWorkspaceFile(t, root, []string{"a", "b"})

	_, err := workspace.Load(root)
	if err == nil {
		t.Error("expected cycle error, got nil")
	}
}

// ─── IsLocal / Member ─────────────────────────────────────────────────────────

func TestIsLocal(t *testing.T) {
	root := makeWorkspace(t)
	ws, _ := workspace.Load(root)

	if !ws.IsLocal("core") {
		t.Error("core should be local")
	}
	if ws.IsLocal("extlib") {
		t.Error("extlib should not be local")
	}
}

func TestMemberLookup(t *testing.T) {
	root := makeWorkspace(t)
	ws, _ := workspace.Load(root)

	m := ws.Member("utils")
	if m == nil {
		t.Fatal("Member(utils) returned nil")
	}
	if m.Manifest.Name != "utils" {
		t.Errorf("Member name = %q, want %q", m.Manifest.Name, "utils")
	}
}

func TestMemberLookupMissing(t *testing.T) {
	root := makeWorkspace(t)
	ws, _ := workspace.Load(root)
	if ws.Member("doesnotexist") != nil {
		t.Error("expected nil for unknown member")
	}
}

// ─── LocalDeps ────────────────────────────────────────────────────────────────

func TestLocalDeps(t *testing.T) {
	root := makeWorkspace(t)
	ws, _ := workspace.Load(root)

	appMember := ws.Member("app")
	deps := ws.LocalDeps(appMember.Manifest)

	names := make(map[string]bool)
	for _, d := range deps {
		names[d.Manifest.Name] = true
	}

	if !names["utils"] {
		t.Error("app local deps should include utils")
	}
	if names["extlib"] {
		t.Error("extlib is not local and should not appear in local deps")
	}
}

func TestLocalDepsNone(t *testing.T) {
	root := makeWorkspace(t)
	ws, _ := workspace.Load(root)

	coreMember := ws.Member("core")
	deps := ws.LocalDeps(coreMember.Manifest)
	if len(deps) != 0 {
		t.Errorf("core should have no local deps, got %d", len(deps))
	}
}

// ─── ExternalDeps ─────────────────────────────────────────────────────────────

func TestExternalDepsExcludesLocals(t *testing.T) {
	root := makeWorkspace(t)
	ws, _ := workspace.Load(root)

	ext := ws.ExternalDeps()

	// Local members must not appear as external deps.
	for _, local := range []string{"core", "utils", "app"} {
		if _, ok := ext.Dependencies.FGL[local]; ok {
			t.Errorf("local member %q should not appear in external deps", local)
		}
	}

	// extlib (from app) should appear.
	if _, ok := ext.Dependencies.FGL["extlib"]; !ok {
		t.Error("extlib should appear in external deps")
	}
}

func TestExternalDepsCollectsJARs(t *testing.T) {
	root := makeWorkspace(t)
	ws, _ := workspace.Load(root)

	ext := ws.ExternalDeps()
	if len(ext.Dependencies.Java) == 0 {
		t.Error("expected at least one Java dependency from app member")
	}

	found := false
	for _, dep := range ext.Dependencies.Java {
		if dep.ArtifactID == "gson" {
			found = true
		}
	}
	if !found {
		t.Error("expected gson JAR in external deps")
	}
}

func TestExternalDepsDeduplicatesJARs(t *testing.T) {
	root := t.TempDir()
	gson := manifest.JavaDependency{
		GroupID: "com.google.code.gson", ArtifactID: "gson", Version: "2.10.1",
	}
	// Both members declare the same JAR.
	writeMember(t, root, "a", "1.0.0", nil, []manifest.JavaDependency{gson})
	writeMember(t, root, "b", "1.0.0", nil, []manifest.JavaDependency{gson})
	writeWorkspaceFile(t, root, []string{"a", "b"})

	ws, err := workspace.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	ext := ws.ExternalDeps()
	if len(ext.Dependencies.Java) != 1 {
		t.Errorf("expected 1 deduplicated JAR, got %d", len(ext.Dependencies.Java))
	}
}

// ─── FGLLDPATHEntries ─────────────────────────────────────────────────────────

func TestFGLLDPATHEntries(t *testing.T) {
	root := makeWorkspace(t)
	ws, _ := workspace.Load(root)

	entries := ws.FGLLDPATHEntries()
	if len(entries) != 3 {
		t.Errorf("expected 3 FGLLDPATH entries, got %d", len(entries))
	}

	entrySet := make(map[string]bool)
	for _, e := range entries {
		entrySet[e] = true
	}

	for _, m := range ws.Members {
		if !entrySet[m.Path] {
			t.Errorf("member path %q not in FGLLDPATH entries", m.Path)
		}
	}
}

// ─── FindRoot ─────────────────────────────────────────────────────────────────

func TestFindRoot(t *testing.T) {
	root := makeWorkspace(t)
	// Searching from a nested member subdirectory should find the root.
	nested := filepath.Join(root, "app")
	found := workspace.FindRoot(nested)
	if found != root {
		t.Errorf("FindRoot(%q) = %q, want %q", nested, found, root)
	}
}

func TestFindRootNotFound(t *testing.T) {
	dir := t.TempDir()
	found := workspace.FindRoot(dir)
	if found != "" {
		t.Errorf("FindRoot should return empty string outside a workspace, got %q", found)
	}
}

// ─── Init / AddMember ─────────────────────────────────────────────────────────

func TestInit(t *testing.T) {
	dir := t.TempDir()
	if err := workspace.Init(dir, []string{"a", "b"}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !workspace.Exists(dir) {
		t.Error("workspace file should exist after Init")
	}
}

func TestInitAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	workspace.Init(dir, []string{"a"}) //nolint:errcheck
	if err := workspace.Init(dir, []string{"b"}); err == nil {
		t.Error("expected error when Init called twice, got nil")
	}
}

func TestAddMember(t *testing.T) {
	dir := t.TempDir()
	workspace.Init(dir, []string{"a"}) //nolint:errcheck

	if err := workspace.AddMember(dir, "b"); err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	// Write member manifests and verify Load sees both.
	writeMember(t, dir, "a", "1.0.0", nil, nil)
	writeMember(t, dir, "b", "1.0.0", nil, nil)
	ws, err := workspace.Load(dir)
	if err != nil {
		t.Fatalf("Load after AddMember: %v", err)
	}
	if len(ws.Members) != 2 {
		t.Errorf("expected 2 members, got %d", len(ws.Members))
	}
}

func TestAddMemberDuplicate(t *testing.T) {
	dir := t.TempDir()
	workspace.Init(dir, []string{"a"}) //nolint:errcheck
	if err := workspace.AddMember(dir, "a"); err == nil {
		t.Error("expected error adding duplicate member, got nil")
	}
}

// ─── Summary ──────────────────────────────────────────────────────────────────

func TestSummary(t *testing.T) {
	root := makeWorkspace(t)
	ws, _ := workspace.Load(root)
	s := ws.Summary()

	for _, name := range []string{"core", "utils", "app"} {
		if !contains(s, name) {
			t.Errorf("Summary missing member %q", name)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
