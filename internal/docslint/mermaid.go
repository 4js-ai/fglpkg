// Package docslint holds checks for this repository's own Markdown
// documentation. They run as ordinary Go tests, so they need nothing beyond the
// toolchain the project already builds with — in particular no Node, which the
// project deliberately does not depend on.
package docslint

import (
	"regexp"
	"strings"
)

// Finding is one hazard located in a Markdown file's mermaid blocks.
type Finding struct {
	Line int    // 1-based, counted in the file, not in the fenced block
	Text string // the offending source line, trimmed
	Why  string // what will go wrong, and what to write instead
}

// entityCode matches a mermaid entity code such as #semi;, #colon; or #35;.
// Its trailing ';' belongs to the escape and is not a raw semicolon.
var entityCode = regexp.MustCompile(`#[A-Za-z0-9]+;`)

const semicolonWhy = "a raw ';' inside a sequenceDiagram ends the statement, so the message is " +
	"truncated and the rest of the line fails to parse — write #semi; instead"

// ScanMermaid reports hazards in the ```mermaid blocks of one Markdown document.
//
// It checks ONE thing, deliberately: a raw semicolon inside a sequenceDiagram.
// That is a lexer-level rule — mermaid's sequence lexer returns NEWLINE for ';'
// — so it holds for every statement in the block and can be checked by reading,
// with no grammar involved. GIS-608 is what it is here to stop happening again:
// two of the four diagrams in docs/architecture.md rendered as a parse-error box
// on GitHub for want of three escapes.
//
// It is NOT a mermaid parser. A misspelled keyword, an unclosed loop/end, or a
// hazard in a diagram type other than sequenceDiagram still gets past it.
// Validating the grammar means running mermaid itself, which is JavaScript.
//
// Semicolons outside a sequence diagram are left alone: in a flowchart they are
// real statement terminators, and docs/architecture.md §2 uses two correctly.
func ScanMermaid(src string) []Finding {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")

	var findings []Finding
	for i := 0; i < len(lines); i++ {
		if !isMermaidFenceOpen(strings.TrimSpace(lines[i])) {
			continue
		}
		start := i + 1
		end := start
		for end < len(lines) && !isFenceClose(strings.TrimSpace(lines[end])) {
			end++
		}
		findings = append(findings, scanBlock(lines[start:end], start+1)...)
		i = end
	}
	return findings
}

// scanBlock checks one fenced block. firstLine is the file line number of block[0].
func scanBlock(block []string, firstLine int) []Finding {
	if !isSequenceDiagram(block) {
		return nil
	}
	var findings []Finding
	for n, raw := range block {
		body := entityCode.ReplaceAllString(stripComment(raw), "")
		if strings.Contains(body, ";") {
			findings = append(findings, Finding{
				Line: firstLine + n,
				Text: strings.TrimSpace(raw),
				Why:  semicolonWhy,
			})
		}
	}
	return findings
}

// isSequenceDiagram reports whether the block declares a sequence diagram.
//
// The declaration is not always the block's first line — a %%{init}%% directive
// or a frontmatter block may precede it — so any line that starts with the
// keyword counts. That also means a flowchart holding a line that happens to
// begin with "sequenceDiagram" would be checked as one, which is the direction
// to err in: a false positive fails CI loudly on correct text and is fixed by
// whoever wrote it, while a false negative ships the broken diagram, which is
// the whole failure this check exists to catch.
func isSequenceDiagram(block []string) bool {
	for _, raw := range block {
		if strings.EqualFold(firstWord(stripComment(raw)), "sequencediagram") {
			return true
		}
	}
	return false
}

// isMermaidFenceOpen reports whether a trimmed line opens a mermaid code fence.
func isMermaidFenceOpen(trimmed string) bool {
	if !strings.HasPrefix(trimmed, "```") {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(strings.TrimLeft(trimmed, "`")), "mermaid")
}

// isFenceClose reports whether a trimmed line is a bare closing fence.
func isFenceClose(trimmed string) bool {
	return len(trimmed) >= 3 && strings.Trim(trimmed, "`") == ""
}

// stripComment drops a mermaid line comment, which runs from %% to end of line.
// A semicolon inside one is consumed with the comment and breaks nothing.
func stripComment(line string) string {
	if i := strings.Index(line, "%%"); i >= 0 {
		return line[:i]
	}
	return line
}

// firstWord returns the first whitespace-delimited word of a line, or "".
func firstWord(line string) string {
	if fields := strings.Fields(line); len(fields) > 0 {
		return fields[0]
	}
	return ""
}
