package env

import (
	"strings"
	"testing"
)

// The default mode used to emit FGLLDPATH even with nothing to add, producing
//
//	export FGLLDPATH="${FGLLDPATH:+:$FGLLDPATH}"
//
// which adds no package path, turns an existing FGLLDPATH=/opt/app/lib into
// :/opt/app/lib (a leading ":" means the current directory to fglrun), and
// exports an empty value when it was unset. All three generators must now
// agree: nothing installed, nothing emitted (GIS-588).
func TestGeneratorsEmitNoEmptyFGLLDPATH(t *testing.T) {
	home := t.TempDir()
	cases := []struct {
		name string
		gen  func(*Generator) ([]string, error)
	}{
		{"Generate (default mode)", (*Generator).Generate},
		{"GenerateLocal", (*Generator).GenerateLocal},
		{"GenerateGlobal", (*Generator).GenerateGlobal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines, err := tc.gen(New(home))
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			for _, l := range lines {
				if strings.Contains(l, "FGLLDPATH") {
					t.Errorf("emitted an FGLLDPATH line with nothing installed: %q", l)
				}
			}
		})
	}
}

// Each shell spelling of the empty line was its own corruption, so pin that
// none of them is produced: SET FGLLDPATH=:%FGLLDPATH% and the PowerShell
// equivalent are just as damaging as the sh one.
func TestGenerateEmitsNothingForEveryShell(t *testing.T) {
	home := t.TempDir()
	for _, sh := range []Shell{ShellSh, ShellCmd, ShellPowerShell} {
		lines, err := New(home).WithShell(sh).Generate()
		if err != nil {
			t.Fatalf("shell %v: %v", sh, err)
		}
		if len(lines) != 0 {
			t.Errorf("shell %v emitted %q with nothing installed, want no output", sh, lines)
		}
	}
}
