package cli

import (
	"strings"
	"testing"
)

// `fglpkg env` with nothing installed must EXPLAIN itself on stderr rather than
// exiting 0 in silence: stdout is consumed by `eval "$(fglpkg env)"`, so an
// empty stdout looked like success and the compile failed much later with
// -8403 Module does not exist (GIS-588).
//
// An explicit --global is the one exception. The documented profile line is
// `eval "$(fglpkg env --global)"` in ~/.bashrc (docs/user-guide.md), so for
// anyone whose packages are all project-local — an expected, correct state —
// the explanation would print on every new shell. The ticket's failure is the
// default and --local modes, where the user IS inside a project and does
// expect that project's packages.
func TestEnvNothingFoundExplainsItselfExceptForGlobal(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantNote bool
	}{
		{"default mode", nil, true},
		{"explicit --local", []string{"--local"}, true},
		{"explicit --global", []string{"--global"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			empty := t.TempDir()
			t.Setenv("FGLPKG_HOME", empty)
			t.Setenv("FGLPKG_GLOBAL_DIR", empty)
			// FGLDIR would otherwise contribute its own merged root, so there
			// would be something to export and this path never runs.
			t.Setenv("FGLDIR", "")
			chdir(t, t.TempDir())

			out, err := captureStderr(t, func() error { return cmdEnv(tc.args) })
			if err != nil {
				t.Fatalf("cmdEnv(%v) = %v, want no error", tc.args, err)
			}
			got := strings.Contains(out, "no installed packages found")
			if got != tc.wantNote {
				if tc.wantNote {
					t.Errorf("stderr carried no explanation, so an empty `eval` is silent again (GIS-588):\n%s", out)
				} else {
					t.Errorf("an explicit --global with an empty global root is an expected state, "+
						"but this prints on every new shell through the documented profile line:\n%s", out)
				}
			}
		})
	}
}
