package installer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/classpath"
)

// TestPruneToKeepsClasspathAnchor verifies the jar sweep never treats the
// classpath anchor as an orphaned dependency jar: it is fglpkg-managed
// metadata, kept in step by classpath.Sync, not by the prune.
func TestPruneToKeepsClasspathAnchor(t *testing.T) {
	home := t.TempDir()
	inst := New(home, "", "", "")
	jarsDir := filepath.Join(home, "jars")
	if err := os.MkdirAll(jarsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"wanted.jar", "orphan.jar", classpath.AnchorName} {
		if err := os.WriteFile(filepath.Join(jarsDir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	pruned, err := inst.pruneTo(map[string]bool{}, map[string]bool{}, map[string]bool{"wanted.jar": true})
	if err != nil {
		t.Fatalf("pruneTo: %v", err)
	}

	if _, err := os.Stat(filepath.Join(jarsDir, "orphan.jar")); err == nil {
		t.Error("orphan.jar should have been pruned")
	}
	if _, err := os.Stat(filepath.Join(jarsDir, "wanted.jar")); err != nil {
		t.Error("wanted.jar should have been kept")
	}
	if _, err := os.Stat(filepath.Join(jarsDir, classpath.AnchorName)); err != nil {
		t.Errorf("the classpath anchor must never be pruned: %v", err)
	}
	for _, p := range pruned {
		if p == "jar "+classpath.AnchorName {
			t.Error("the anchor must not be reported as a pruned jar")
		}
	}
}
