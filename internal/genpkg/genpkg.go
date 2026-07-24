// Package genpkg parses the Genero BDL PACKAGE declaration from a source
// module. It is a small, dependency-free lexical helper used by pack/publish
// to record each shipped library module's namespace (its `generoPackages`)
// so the consumer can materialize a PACKAGE-correct merged FGLLDPATH root
// without re-reading source or guessing from directory shape.
//
// See specs/package-layout-materialized-root.md (Decisions §1).
package genpkg

import (
	"regexp"
	"strings"
)

// nsPattern matches a Genero package namespace: dot-separated identifiers,
// each an identifier in the usual sense (leading letter/underscore, then
// letters, digits, underscores). Mirrors the grammar in the design spec.
var nsPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*`)

// ParsePackageDecl returns the namespace declared by a Genero source module,
// e.g. "com.fourjs.db" for `PACKAGE com.fourjs.db`, and ok=false when the
// module declares no PACKAGE (a MAIN program / flat module).
//
// The PACKAGE instruction, when present, is the first lexical token of a
// module — it may be preceded only by blank lines and comments (`#`, `--`, or
// `{ … }` block comments). If the first meaningful token is anything other
// than PACKAGE (IMPORT, SCHEMA, MAIN, FUNCTION, …), the module is not a
// namespaced library and ok is false. Matching is case-insensitive because
// BDL keywords are.
func ParsePackageDecl(src []byte) (namespace string, ok bool) {
	s := string(src)
	i := skipTrivia(s, 0)
	if i >= len(s) {
		return "", false
	}
	// Read the first identifier token — the leading keyword.
	start := i
	for i < len(s) && isIdentChar(s[i]) {
		i++
	}
	if !strings.EqualFold(s[start:i], "PACKAGE") {
		return "", false
	}
	// Skip whitespace/comments between PACKAGE and the namespace, then match
	// the dotted identifier.
	i = skipTrivia(s, i)
	ns := nsPattern.FindString(s[i:])
	if ns == "" {
		return "", false
	}
	return ns, true
}

// NamespacePath converts a dotted namespace to its FGLLDPATH-relative
// directory path, e.g. "com.fourjs.db" -> "com/fourjs/db".
func NamespacePath(ns string) string {
	return strings.ReplaceAll(ns, ".", "/")
}

// skipTrivia advances past whitespace and comments starting at i, returning
// the index of the next meaningful (code) byte. It handles the three BDL
// comment forms: `#` and `--` line comments, and `{ … }` block comments.
func skipTrivia(s string, i int) int {
	for i < len(s) {
		switch c := s[i]; {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' || c == '\v':
			i++
		case c == '#':
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case c == '{':
			i++
			for i < len(s) && s[i] != '}' {
				i++
			}
			if i < len(s) {
				i++ // consume the closing '}'
			}
		default:
			return i
		}
	}
	return i
}

// isIdentChar reports whether c may appear within a BDL identifier.
func isIdentChar(c byte) bool {
	return c == '_' ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9')
}
