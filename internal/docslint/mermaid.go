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
//
// The character class is mermaid's own. Before parsing anything, mermaid
// rewrites every /#\w+;/ in the source to a placeholder that holds neither '#'
// nor ';', so its lexer never sees an escape at all. \w includes the
// underscore, which is why this class does too: #a_b; is an escape to mermaid,
// and reporting the ';' in it would be a finding against a diagram that
// renders.
var entityCode = regexp.MustCompile(`#[A-Za-z0-9_]+;`)

const semicolonWhy = "a raw ';' inside a sequenceDiagram separates statements in mermaid, so the " +
	"message is cut off at it and whatever follows has to parse as a statement of its own — " +
	"write #semi; for a literal semicolon, or start a new line"

// ScanMermaid reports hazards in the mermaid blocks of one Markdown document.
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
// Where it cannot be exact it over-reports, because the two mistakes cost
// different amounts: a finding against a diagram that renders costs its author
// one escape or one line break, while a diagram that does not render reaches a
// reader of the documentation. So a ';' is reported even where mermaid accepts
// it as a plain statement separator ("A->>B: hello;" and "A->>B: one; B->>A:
// two" both parse and are both flagged), and a block is scanned as a sequence
// diagram whenever any of its lines starts with the keyword.
//
// Semicolons outside a sequence diagram are left alone: in a flowchart they are
// real statement terminators, and docs/architecture.md §2 uses two correctly.
//
// Two known limits, both measured against mermaid 11.4.1 rather than guessed:
//
//   - A raw ';' also breaks classDiagram, journey and erDiagram, which are not
//     checked. Each has its own lexer, and a rule this file cannot read off one
//     grammar is a rule it should not claim.
//   - A "%%" that is NOT at the start of a line is reported, and mermaid
//     sometimes accepts it: "end %% c; d" and "autonumber %% c; d" parse, while
//     "A->>B: x %% note; y" and "participant B %% c; d" do not. Whether the
//     comment rule applies depends on the lexer state the line has reached, and
//     an approximation of that would turn some of these over-reports into
//     silence — the one direction this check must not be wrong in.
func ScanMermaid(src string) []Finding {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	// What a reader of the Markdown sees, which is not always the line as
	// written: a fence inside a blockquote carries a "> " that is markup, not
	// diagram text, and GitHub draws the diagram all the same (asked of its own
	// renderer). Scanning the line as written missed those blocks entirely.
	// Findings still quote the line as written, which is what the author has to
	// find in the file.
	content := make([]string, len(lines))
	for i, line := range lines {
		content[i] = unquote(line)
	}

	var findings []Finding
	for i := 0; i < len(lines); i++ {
		f, ok := openFenceHere(strings.TrimSpace(content[i]))
		if !ok {
			continue
		}
		start := i + 1
		end := start
		for end < len(lines) && !closesFence(strings.TrimSpace(content[end]), f) {
			end++
		}
		if f.mermaid {
			findings = append(findings, scanBlock(content[start:end], lines[start:end], start+1)...)
		}
		// Past the whole block either way. Skipping a block that is NOT a
		// diagram is what keeps a quoted mermaid fence inside it from being
		// read as one, so a page may show a broken diagram as an example.
		i = end
	}
	return findings
}

// scanBlock checks one fenced block. block holds the diagram's own text, raw
// the lines as written in the file, and firstLine is the file line number of
// block[0].
func scanBlock(block, raw []string, firstLine int) []Finding {
	if !isSequenceDiagram(block) {
		return nil
	}
	var findings []Finding
	for n, line := range block[frontMatterEnd(block):] {
		n += frontMatterEnd(block)
		body := entityCode.ReplaceAllString(stripComment(line), "")
		if strings.Contains(body, ";") && !isBareDeclaration(line) {
			findings = append(findings, Finding{
				Line: firstLine + n,
				Text: strings.TrimSpace(raw[n]),
				Why:  semicolonWhy,
			})
		}
	}
	return findings
}

// isBareDeclaration reports whether a line is the diagram's type keyword on its
// own, with or without a terminator.
//
// "sequenceDiagram;" is a complete statement, mermaid parses it (checked
// against 11.4.1), and there is nothing after the ';' on that line for it to
// cut off — so it is the one ';' in a sequence diagram that is never a hazard.
// A line that puts a statement after it, "sequenceDiagram; A->>B: x", is not
// exempt: mermaid accepts that too, but it is reported like every other ';'
// this check over-reports on, because the next one along the line would be a
// real hazard and telling them apart is the grammar's job.
func isBareDeclaration(line string) bool {
	t := strings.TrimSpace(line)
	return t != "" && strings.EqualFold(strings.TrimRight(t, ";"), "sequencediagram")
}

// frontMatterEnd returns the index of the first line of the diagram proper,
// skipping a leading YAML front-matter block.
//
// Mermaid accepts one between "---" lines at the top of a diagram, for a title
// and config. It is YAML, not statements, so a ';' in it ends nothing —
// verified against mermaid 11.4.1, where a diagram whose front matter holds
// "title: a; b" parses. Reporting it was the plainest kind of false positive:
// a finding about a line that is not in the language being checked.
//
// An unterminated "---" is not front matter: mermaid would not read it as one
// either, so the block is scanned from the top as before.
func frontMatterEnd(block []string) int {
	first := 0
	for first < len(block) && strings.TrimSpace(block[first]) == "" {
		first++
	}
	if first >= len(block) || strings.TrimSpace(block[first]) != "---" {
		return 0
	}
	for i := first + 1; i < len(block); i++ {
		if strings.TrimSpace(block[i]) == "---" {
			return i + 1
		}
	}
	return 0
}

// unquote strips one level of Markdown blockquote marker from a line, leaving
// everything else alone. A deeper quotation keeps its remaining levels, and a
// line that is not quoted is returned as it was.
func unquote(line string) string {
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, ">") {
		return line
	}
	return strings.TrimPrefix(trimmed[1:], " ")
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
		// A trailing ';' is a statement separator, so "sequenceDiagram;" is a
		// sequence diagram and mermaid parses it as one (checked against
		// 11.4.1). Not recognising it left every hazard in such a block
		// unreported, which is the direction that ships a broken diagram.
		if strings.EqualFold(strings.TrimRight(firstWord(stripComment(raw)), ";"), "sequencediagram") {
			return true
		}
	}
	return false
}

// fence describes an open Markdown code fence.
type fence struct {
	char    byte // '`' or '~'; a fence is closed only by its own character
	width   int  // how many of them opened it; a closer needs at least as many
	mermaid bool // whether GitHub renders this block as a diagram
}

// openFence reports whether a trimmed line opens a code fence, and what kind.
//
// The shapes accepted are the ones GitHub actually draws as a diagram, asked of
// its own Markdown renderer (`gh api markdown`) rather than assumed: three or
// more backticks OR tildes, with an info string whose first word is "mermaid"
// in any case. The info string may carry more after that word, and more than
// three fence characters are allowed — "~~~mermaid", "```mermaid title=x",
// "```Mermaid" and a four-backtick "mermaid" are all rendered as diagrams, and
// the first two used to slip past this check with their hazards intact.
//
// Indentation is the one place this is knowingly looser than CommonMark, which
// makes four or more leading spaces an indented code block rather than a fence
// unless it sits in a list item. Treating it as a fence anyway over-reports,
// which is the side to be wrong on.
func openFence(trimmed string) (fence, bool) {
	var f fence
	switch {
	case strings.HasPrefix(trimmed, "```"):
		f.char = '`'
	case strings.HasPrefix(trimmed, "~~~"):
		f.char = '~'
	default:
		return fence{}, false
	}
	info := strings.TrimLeft(trimmed, string(f.char))
	f.width = len(trimmed) - len(info)
	f.mermaid = strings.EqualFold(firstWord(info), "mermaid")
	return f, true
}

// openFenceHere is openFence, also accepting a fence that opens on the same
// line as a list marker ("- ```mermaid"), which GitHub draws like any other.
//
// Only the OPENING line can carry a marker: the rest of a list item's content
// is carried by indentation, which TrimSpace already absorbs. That is why this
// is here and not in the line-by-line rewriting a blockquote needs — stripping
// a leading "- " from every line would reach into diagram text, where it could
// turn a line into something it is not.
func openFenceHere(trimmed string) (fence, bool) {
	if f, ok := openFence(trimmed); ok {
		return f, true
	}
	return openFence(afterListMarker(trimmed))
}

// afterListMarker returns what follows a leading list marker — "-", "*", "+"
// or "1." and friends — or "" when the line does not start with one.
func afterListMarker(trimmed string) string {
	rest := strings.TrimLeft(trimmed, "0123456789")
	switch {
	case len(rest) < len(trimmed) && strings.HasPrefix(rest, ".") || strings.HasPrefix(rest, ")"):
		rest = rest[1:]
	case strings.HasPrefix(trimmed, "-"), strings.HasPrefix(trimmed, "*"), strings.HasPrefix(trimmed, "+"):
		rest = trimmed[1:]
	default:
		return ""
	}
	if !strings.HasPrefix(rest, " ") && !strings.HasPrefix(rest, "\t") {
		return "" // "-foo" is not a list item
	}
	return strings.TrimLeft(rest, " \t")
}

// closesFence reports whether a trimmed line closes the open fence f: the same
// character, at least as many of them, and nothing else on the line.
//
// A shorter run, or a run of the other character, is content. That is what
// makes a ```mermaid fence inside a four-backtick block part of that block,
// exactly as GitHub shows it, so quoting a broken diagram in prose no longer
// fails the build for the page that explains it.
func closesFence(trimmed string, f fence) bool {
	rest := strings.TrimLeft(trimmed, string(f.char))
	return len(trimmed)-len(rest) >= f.width && strings.TrimSpace(rest) == ""
}

// stripComment blanks a mermaid comment line, and leaves every other line
// alone.
//
// Only a line whose first non-blank characters are "%%" counts as one. Mermaid
// does skip a "%%" to end of line wherever its lexer meets one, but inside a
// message it never meets one: the rule that takes the text after ':' stops only
// at '#', ';' or the newline, so a "%%" there is part of the message and a ';'
// after it still ends the statement. Verified against mermaid 11.4.1 —
// "A->>B: 100%% done; more" and "A->>B: x %% note; y" are both parse errors,
// while a whole-line "%% a; b" parses. Cutting the line at the first "%%"
// anywhere therefore hid the exact breakage this check exists to catch.
//
// A "%%{init: ...}%%" directive line is blanked too. It is not a comment, but
// it carries no statement either, so nothing in one can truncate a message.
func stripComment(line string) string {
	if strings.HasPrefix(strings.TrimSpace(line), "%%") {
		return ""
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
