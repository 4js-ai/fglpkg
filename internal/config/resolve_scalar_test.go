package config

import "testing"

// ResolveScalar is the single source of truth for replace-semantics config
// precedence: environment > local (project) > global (user), with whitespace-only
// treated as unset and the winner returned trimmed (GIS-368).
func TestResolveScalar(t *testing.T) {
	cases := []struct {
		name               string
		env, local, global string
		want               string
	}{
		{"all empty", "", "", "", ""},
		{"only global", "", "", "g", "g"},
		{"only local", "", "l", "", "l"},
		{"only env", "e", "", "", "e"},
		{"local beats global", "", "l", "g", "l"},
		{"env beats local and global", "e", "l", "g", "e"},
		{"env beats global with no local", "e", "", "g", "e"},
		{"blank env does not shadow local", "   ", "l", "g", "l"},
		{"blank local does not shadow global", "", "  ", "g", "g"},
		{"winner is trimmed", "  e  ", "l", "g", "e"},
		{"whitespace-only everywhere is unset", " ", "\t", "\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveScalar(tc.env, tc.local, tc.global); got != tc.want {
				t.Errorf("ResolveScalar(%q,%q,%q) = %q, want %q", tc.env, tc.local, tc.global, got, tc.want)
			}
		})
	}
}
