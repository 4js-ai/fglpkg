package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
)

// ─── flag parsing ─────────────────────────────────────────────────────────────

func TestParseListFlags(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    listFlags
		wantErr string
	}{
		{name: "no args", args: nil, want: listFlags{}},
		{name: "flat", args: []string{"--flat"}, want: listFlags{flat: true}},
		{name: "local short", args: []string{"-l"}, want: listFlags{local: true}},
		{name: "global short", args: []string{"-g"}, want: listFlags{global: true}},
		{name: "depth space form", args: []string{"--depth", "2"}, want: listFlags{depth: 2}},
		{name: "depth equals form", args: []string{"--depth=3"}, want: listFlags{depth: 3}},
		{name: "depth zero is unlimited", args: []string{"--depth=0"}, want: listFlags{depth: 0}},
		{name: "combined", args: []string{"--global", "--flat", "--depth=1"},
			want: listFlags{global: true, flat: true, depth: 1}},

		{name: "scope conflict", args: []string{"--local", "--global"}, wantErr: "mutually exclusive"},
		{name: "unknown flag", args: []string{"--force"}, wantErr: `unknown argument "--force"`},
		// Previously accepted and silently ignored; now an error, as for env.
		{name: "stray positional", args: []string{"somejunk"}, wantErr: `unknown argument "somejunk"`},
		{name: "depth missing value", args: []string{"--depth"}, wantErr: "--depth requires a value"},
		{name: "depth not a number", args: []string{"--depth=abc"}, wantErr: `invalid --depth "abc"`},
		{name: "depth negative", args: []string{"--depth=-1"}, wantErr: `invalid --depth "-1"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseListFlags(tc.args)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (flags %+v)", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("flags = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// ─── tree building & rendering ────────────────────────────────────────────────

func lockedPkg(name, version string, requiredBy ...string) lockfile.LockedPackage {
	return lockfile.LockedPackage{Name: name, Version: version, RequiredBy: requiredBy}
}

func lockedJar(group, artifact, version string) lockfile.LockedJAR {
	return lockfile.LockedJAR{
		Key: group + ":" + artifact, GroupID: group, ArtifactID: artifact, Version: version,
	}
}

// render is the whole pipeline under test: lock + JAR attribution -> text.
func render(lf *lockfile.LockFile, jarParents map[string][]string, maxDepth int) string {
	var buf bytes.Buffer
	writeTree(&buf, buildListTree(lf, jarParents, maxDepth), treeRootLabel(lf))
	return buf.String()
}

// TestListTreeEmptyLockPrintsNoPackages covers listTree's empty branch: a lock
// that exists but records zero packages, webcomponents, and JARs prints
// "No packages installed." rather than a bare root line. The branch returns
// before consulting the installer, so a nil installer is fine here.
func TestListTreeEmptyLockPrintsNoPackages(t *testing.T) {
	var buf bytes.Buffer
	lf := &lockfile.LockFile{} // no packages, webcomponents, or JARs
	if err := listTree(&buf, nil, lf, t.TempDir(), 0); err != nil {
		t.Fatalf("listTree: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != emptyInstall {
		t.Errorf("listTree on an empty lock = %q, want %q", got, emptyInstall)
	}
}

func TestBuildListTreeNestsAndOrders(t *testing.T) {
	lf := &lockfile.LockFile{
		RootManifest: lockfile.RootEntry{Name: "myproject", Version: "1.0.0"},
		Packages: []lockfile.LockedPackage{
			lockedPkg("dbtools", "2.1.0", "<root>"),
			lockedPkg("myutils", "1.4.2", "dbtools"),
		},
		JARs: []lockfile.LockedJAR{
			lockedJar("com.google.code.gson", "gson", "2.10.1"),
			lockedJar("com.google.guava", "guava", "32.1.3-jre"),
			lockedJar("org.apache.poi", "poi", "5.2.5"),
		},
	}
	jarParents := map[string][]string{
		"com.google.code.gson:gson": {"<root>"},
		"com.google.guava:guava":    {"myutils"},
		"org.apache.poi:poi":        {"dbtools"},
	}

	got := render(lf, jarParents, 0)
	want := strings.Join([]string{
		"myproject@1.0.0",
		"",
		"├─ dbtools@2.1.0",
		"│  ├─ myutils@1.4.2",
		"│  │  └─ com.google.guava:guava  32.1.3-jre",
		"│  └─ org.apache.poi:poi  5.2.5",
		"└─ com.google.code.gson:gson  2.10.1",
		"",
		"2 packages, 3 JARs.",
		"",
	}, "\n")
	if got != want {
		t.Errorf("tree mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// A JAR that no installed manifest declares must still appear — at the top
// level, which is the only honest place for it.
func TestBuildListTreeUnattributedJarGoesToRoot(t *testing.T) {
	lf := &lockfile.LockFile{
		RootManifest: lockfile.RootEntry{Name: "app", Version: "0.1.0"},
		Packages:     []lockfile.LockedPackage{lockedPkg("dbtools", "2.1.0", "<root>")},
		JARs:         []lockfile.LockedJAR{lockedJar("org.orphan", "mystery", "1.0.0")},
	}
	got := render(lf, nil, 0)
	if !strings.Contains(got, "└─ org.orphan:mystery  1.0.0") {
		t.Errorf("unattributed JAR should hang off the root, got:\n%s", got)
	}
}

// A JAR declared by two packages appears under both: the tree answers "who
// asked for this", and both did.
func TestBuildListTreeJarWithTwoParents(t *testing.T) {
	lf := &lockfile.LockFile{
		RootManifest: lockfile.RootEntry{Name: "app", Version: "0.1.0"},
		Packages: []lockfile.LockedPackage{
			lockedPkg("alpha", "1.0.0", "<root>"),
			lockedPkg("beta", "1.0.0", "<root>"),
		},
		JARs: []lockfile.LockedJAR{lockedJar("commons-io", "commons-io", "2.15.1")},
	}
	got := render(lf, map[string][]string{"commons-io:commons-io": {"alpha", "beta"}}, 0)
	// The second occurrence collapses to a (*) leaf, and the count is not
	// double-charged.
	if strings.Count(got, "commons-io:commons-io") != 2 {
		t.Errorf("JAR should appear under both parents, got:\n%s", got)
	}
	if !strings.Contains(got, "commons-io:commons-io  2.15.1 (*)") {
		t.Errorf("repeat occurrence should carry the (*) marker, got:\n%s", got)
	}
	if !strings.Contains(got, "2 packages, 1 JAR.") {
		t.Errorf("repeat must not be double-counted, got:\n%s", got)
	}
	if !strings.Contains(got, repeatLegend) {
		t.Errorf("legend should be printed when something was collapsed, got:\n%s", got)
	}
}

// A shared transitive dependency expands once, then collapses.
func TestBuildListTreeSharedSubtreeCollapses(t *testing.T) {
	lf := &lockfile.LockFile{
		RootManifest: lockfile.RootEntry{Name: "app", Version: "0.1.0"},
		Packages: []lockfile.LockedPackage{
			lockedPkg("alpha", "1.0.0", "<root>"),
			lockedPkg("beta", "1.0.0", "<root>"),
			lockedPkg("shared", "3.0.0", "alpha", "beta"),
			lockedPkg("deep", "4.0.0", "shared"),
		},
	}
	got := render(lf, nil, 0)
	if strings.Count(got, "deep@4.0.0") != 1 {
		t.Errorf("shared subtree should expand exactly once, got:\n%s", got)
	}
	if !strings.Contains(got, "shared@3.0.0 (*)") {
		t.Errorf("second occurrence of shared should collapse, got:\n%s", got)
	}
	if !strings.Contains(got, "4 packages, 0 JARs.") {
		t.Errorf("counts should be of distinct entries, got:\n%s", got)
	}
}

// A dependency cycle in a hand-edited lock must terminate, not recurse forever.
func TestBuildListTreeCycleTerminates(t *testing.T) {
	lf := &lockfile.LockFile{
		RootManifest: lockfile.RootEntry{Name: "app", Version: "0.1.0"},
		Packages: []lockfile.LockedPackage{
			lockedPkg("a", "1.0.0", "<root>", "b"),
			lockedPkg("b", "1.0.0", "a"),
		},
	}
	// a is attached to both the root and b, so it appears twice: expanded under
	// the root, then collapsed where the cycle closes. b appears once.
	got := render(lf, nil, 0)
	if !strings.Contains(got, "a@1.0.0 (*)") {
		t.Errorf("cycle should be broken with a (*) leaf, got:\n%s", got)
	}
	if strings.Count(got, "a@1.0.0") != 2 || strings.Count(got, "b@1.0.0") != 1 {
		t.Errorf("cycle rendered the wrong number of nodes, got:\n%s", got)
	}
	if !strings.Contains(got, "2 packages, 0 JARs.") {
		t.Errorf("both nodes should be counted exactly once, got:\n%s", got)
	}
}

// Two versions of the same name are distinct nodes: dedup keys on identity, not
// on the name alone.
func TestBuildListTreeDistinctVersionsBothExpand(t *testing.T) {
	lf := &lockfile.LockFile{
		RootManifest: lockfile.RootEntry{Name: "app", Version: "0.1.0"},
		Packages: []lockfile.LockedPackage{
			lockedPkg("alpha", "1.0.0", "<root>"),
			lockedPkg("alpha", "2.0.0", "<root>"),
		},
	}
	got := render(lf, nil, 0)
	if strings.Contains(got, "(*)") {
		t.Errorf("different versions are not repeats, got:\n%s", got)
	}
	if !strings.Contains(got, "2 packages, 0 JARs.") {
		t.Errorf("both versions should be counted, got:\n%s", got)
	}
}

func TestBuildListTreeAnnotations(t *testing.T) {
	lf := &lockfile.LockFile{
		RootManifest: lockfile.RootEntry{Name: "app", Version: "0.1.0"},
		Packages: []lockfile.LockedPackage{
			lockedPkg("testkit", "1.0.0", "<root>"),
			lockedPkg("maybe", "1.0.0", "<root>"),
		},
		Webcomponents: []lockfile.LockedWebcomponent{
			{Name: "chart-3d", Version: "0.9.0", RequiredBy: []string{"<root>"}},
		},
	}
	lf.Packages[0].Scope = "dev"
	lf.Packages[1].Scope = "optional"

	got := render(lf, nil, 0)
	for _, want := range []string{
		"testkit@1.0.0 (dev)",
		"maybe@1.0.0 (optional)",
		"chart-3d@0.9.0 (webcomponent)",
		"2 packages, 1 webcomponent, 0 JARs.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// An entry whose requiredBy names a package that isn't in the lock, or is empty,
// collapses onto the root rather than vanishing (the sbom rules).
func TestBuildListTreeStrayParentsCollapseToRoot(t *testing.T) {
	lf := &lockfile.LockFile{
		RootManifest: lockfile.RootEntry{Name: "app", Version: "0.1.0"},
		Packages: []lockfile.LockedPackage{
			lockedPkg("ghost-child", "1.0.0", "not-in-this-lock"),
			lockedPkg("no-parent", "2.0.0"),
		},
	}
	got := render(lf, nil, 0)
	for _, want := range []string{"├─ ghost-child@1.0.0", "└─ no-parent@2.0.0"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestBuildListTreeDepthLimit(t *testing.T) {
	lf := &lockfile.LockFile{
		RootManifest: lockfile.RootEntry{Name: "app", Version: "0.1.0"},
		Packages: []lockfile.LockedPackage{
			lockedPkg("level1", "1.0.0", "<root>"),
			lockedPkg("level2", "1.0.0", "level1"),
			lockedPkg("level3", "1.0.0", "level2"),
		},
	}
	got := render(lf, nil, 2)
	if !strings.Contains(got, "level2@1.0.0") {
		t.Errorf("--depth=2 should reach level 2, got:\n%s", got)
	}
	if strings.Contains(got, "level3") {
		t.Errorf("--depth=2 should not reach level 3, got:\n%s", got)
	}
	// Truncated entries are still counted as installed — the tree is a view, not
	// an inventory of what fits on screen.
	if !strings.Contains(got, "2 packages") {
		t.Errorf("counts should reflect what was rendered, got:\n%s", got)
	}
}

// A lock naming no project still needs a heading for the tree to hang off.
func TestTreeRootLabelFallbacks(t *testing.T) {
	tests := []struct {
		name, version, want string
	}{
		{"myproject", "1.0.0", "myproject@1.0.0"},
		{"myproject", "", "myproject"},
		{"", "", rootLabel},
	}
	for _, tc := range tests {
		lf := &lockfile.LockFile{RootManifest: lockfile.RootEntry{Name: tc.name, Version: tc.version}}
		if got := treeRootLabel(lf); got != tc.want {
			t.Errorf("treeRootLabel(%q,%q) = %q, want %q", tc.name, tc.version, got, tc.want)
		}
	}
}
