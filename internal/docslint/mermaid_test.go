package docslint

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestScanMermaid(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		wantLines []int
		why       string
	}{
		{
			name: "the GIS-608 shape",
			src: "intro\n\n" +
				"```mermaid\n" +
				"sequenceDiagram\n" +
				"    R->>R: filter by genero constraint + semver; pick variant\n" +
				"```\n",
			wantLines: []int{5},
			why:       "the reported bug: a raw ';' in message text",
		},
		{
			name: "the escape clears it",
			src: "```mermaid\n" +
				"sequenceDiagram\n" +
				"    R->>R: filter by genero constraint + semver#semi; pick variant\n" +
				"```\n",
			wantLines: nil,
			why:       "#semi; is the documented escape; its ';' belongs to the entity code",
		},
		{
			name: "a flowchart keeps its terminators",
			src: "```mermaid\n" +
				"flowchart TD\n" +
				"    a[\"one\"] --> b[\"two\"]\n" +
				"    classDef ext fill:#eee,stroke:#999,stroke-dasharray:4 3;\n" +
				"    class a,b ext;\n" +
				"```\n",
			wantLines: nil,
			why:       "docs/architecture.md §2 uses these two correctly — flagging them would be wrong",
		},
		{
			name: "a comment is not a statement",
			src: "```mermaid\n" +
				"sequenceDiagram\n" +
				"    %% a note to the author; harmless\n" +
				"    A->>B: fine\n" +
				"```\n",
			wantLines: nil,
			why:       "mermaid consumes %% to end of line, so a ';' in there breaks nothing",
		},
		{
			name: "another language's fence is not a diagram",
			src: "```text\n" +
				"sequenceDiagram\n" +
				"    A->>B: an example of the bug; quoted in prose\n" +
				"```\n",
			wantLines: nil,
			why:       "only a ```mermaid fence is rendered as a diagram; quoting one elsewhere is fine",
		},
		{
			name:      "prose outside a fence is not scanned",
			src:       "A paragraph; it has semicolons; several of them.\n\n    A->>B: indented; but not fenced\n",
			wantLines: nil,
			why:       "the check must not leak out of ```mermaid blocks",
		},
		{
			name: "a directive before the declaration still counts",
			src: "```mermaid\n" +
				"%%{init: {'theme':'dark'}}%%\n" +
				"sequenceDiagram\n" +
				"    A->>B: x; y\n" +
				"```\n",
			wantLines: []int{4},
			why:       "the type keyword is not always the block's first line",
		},
		{
			name: "escapes do not mask a real semicolon beside them",
			src: "```mermaid\n" +
				"sequenceDiagram\n" +
				"    A->>B: #quot;quoted#quot;; and more\n" +
				"```\n",
			wantLines: []int{3},
			why:       "stripping entity codes must not swallow the bare ';' that follows",
		},
		{
			name: "line numbers are counted in the file, across blocks",
			src: "# Title\n\n" +
				"```mermaid\n" +
				"flowchart TD\n" +
				"    a --> b;\n" +
				"```\n\n" +
				"Some prose.\n\n" +
				"```mermaid\n" +
				"sequenceDiagram\n" +
				"    A->>B: one; two\n" +
				"    A->>B: clean\n" +
				"    A->>B: three; four\n" +
				"```\n",
			wantLines: []int{12, 14},
			why:       "a finding must point at the right line of the right file",
		},
		{
			name: "an unterminated fence is still scanned",
			src: "```mermaid\n" +
				"sequenceDiagram\n" +
				"    A->>B: one; two\n",
			wantLines: []int{3},
			why:       "end of file closes the block; a missing fence must not silence the check",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ScanMermaid(tc.src)
			var gotLines []int
			for _, f := range got {
				gotLines = append(gotLines, f.Line)
				if f.Text == "" || f.Why == "" {
					t.Errorf("finding at line %d has an empty Text or Why: %+v", f.Line, f)
				}
			}
			if !sameInts(gotLines, tc.wantLines) {
				t.Errorf("findings on lines %v, want %v (%s)\nfindings: %+v",
					gotLines, tc.wantLines, tc.why, got)
			}
		})
	}
}

// TestRepositoryMermaidBlocksAreClean is the guard itself: every mermaid block
// in every Markdown file of this repository must be free of the hazard above.
//
// It counts the blocks it scanned and fails if it found none, because a walk
// that silently reaches nothing would pass forever while checking nothing.
func TestRepositoryMermaidBlocksAreClean(t *testing.T) {
	root := repoRoot(t)

	var problems []string
	blocks, files := 0, 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "bin":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// Counted independently of the scanner's own fence detection, so a
		// broken detector cannot also report that there was nothing to find.
		n := strings.Count(strings.ToLower(string(src)), "```mermaid")
		blocks += n
		if n > 0 {
			files++
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		for _, f := range ScanMermaid(string(src)) {
			problems = append(problems, fmt.Sprintf("%s:%d: %s\n    %s",
				filepath.ToSlash(rel), f.Line, f.Why, f.Text))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}

	if blocks == 0 {
		t.Fatalf("no ```mermaid blocks found under %s — the walk is checking nothing", root)
	}
	t.Logf("scanned %d mermaid block(s) in %d file(s)", blocks, files)

	if len(problems) > 0 {
		t.Errorf("mermaid blocks that will not render:\n%s", strings.Join(problems, "\n"))
	}
}

// repoRoot walks up from this test file to the directory holding go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	dir := filepath.Dir(here)
	for i := 0; i < 5; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not locate go.mod above %s", here)
	return ""
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
