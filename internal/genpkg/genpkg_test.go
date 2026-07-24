package genpkg

import "testing"

func TestParsePackageDecl(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		wantNS string
		wantOK bool
	}{
		{
			name:   "bare declaration on line 1",
			src:    "PACKAGE com.fourjs.db\n",
			wantNS: "com.fourjs.db",
			wantOK: true,
		},
		{
			name:   "PackageB shape: PACKAGE then hash comments",
			src:    "PACKAGE com.fourjs.db\n\n# DbConnection — a small library\n# usage ...\nFUNCTION connect()\n",
			wantNS: "com.fourjs.db",
			wantOK: true,
		},
		{
			name:   "leading hash comments before PACKAGE",
			src:    "# license header\n# more\n\nPACKAGE org.example.util\n",
			wantNS: "org.example.util",
			wantOK: true,
		},
		{
			name:   "leading dash-dash comments",
			src:    "-- a comment\n-- another\nPACKAGE a.b\n",
			wantNS: "a.b",
			wantOK: true,
		},
		{
			name:   "brace block comment before PACKAGE",
			src:    "{ this is a\n  multi-line block comment }\nPACKAGE x.y.z\n",
			wantNS: "x.y.z",
			wantOK: true,
		},
		{
			name:   "block comment then PACKAGE on the same line",
			src:    "{ header } PACKAGE single.seg\n",
			wantNS: "single.seg",
			wantOK: true,
		},
		{
			name:   "single-segment namespace",
			src:    "PACKAGE hello\n",
			wantNS: "hello",
			wantOK: true,
		},
		{
			name:   "lowercase keyword (case-insensitive)",
			src:    "package Com.Fourjs.Db\n",
			wantNS: "Com.Fourjs.Db",
			wantOK: true,
		},
		{
			name:   "leading whitespace/tabs",
			src:    "   \t PACKAGE  spaced.ns  \n",
			wantNS: "spaced.ns",
			wantOK: true,
		},
		{
			name:   "underscores and digits in segments",
			src:    "PACKAGE a_1.b2c._d\n",
			wantNS: "a_1.b2c._d",
			wantOK: true,
		},
		{
			name:   "no PACKAGE: MAIN program",
			src:    "# a program\nMAIN\n  DISPLAY \"hi\"\nEND MAIN\n",
			wantOK: false,
		},
		{
			name:   "no PACKAGE: IMPORT first",
			src:    "IMPORT FGL com.fourjs.db.DbConnection\nFUNCTION foo() END FUNCTION\n",
			wantOK: false,
		},
		{
			name:   "empty source",
			src:    "",
			wantOK: false,
		},
		{
			name:   "only comments",
			src:    "# nothing but\n-- comments here\n{ and a block }\n",
			wantOK: false,
		},
		{
			name:   "identifier that merely starts with PACKAGE is not the keyword",
			src:    "PACKAGES com.x\n",
			wantOK: false,
		},
		{
			name:   "PACKAGE keyword but no namespace token",
			src:    "PACKAGE\n",
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns, ok := ParsePackageDecl([]byte(tc.src))
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (ns=%q)", ok, tc.wantOK, ns)
			}
			if ns != tc.wantNS {
				t.Fatalf("namespace = %q, want %q", ns, tc.wantNS)
			}
		})
	}
}

func TestNamespacePath(t *testing.T) {
	cases := map[string]string{
		"com.fourjs.db": "com/fourjs/db",
		"hello":         "hello",
		"a.b.c.d":       "a/b/c/d",
		"":              "",
	}
	for in, want := range cases {
		if got := NamespacePath(in); got != want {
			t.Errorf("NamespacePath(%q) = %q, want %q", in, got, want)
		}
	}
}
