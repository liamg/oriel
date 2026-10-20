// Package diagram parses mermaid diagrams (flowcharts and sequence diagrams), lays them out with the real shaper,
// and draws them into a render.Frame at any size (spec/mermaid).
package diagram

import (
	"errors"
	"fmt"
	"html"
	"regexp"
	"strings"
)

// Diagram is a parsed mermaid source: one of Flow or Seq is set.
type Diagram struct {
	Type string // the header's keyword: "flowchart", "graph", "sequenceDiagram"
	Flow *Flowchart
	Seq  *Sequence
}

// Kind is what the diagram is called in words: "flowchart" or "sequence".
func (d *Diagram) Kind() string {
	if d.Seq != nil {
		return "sequence"
	}
	return "flowchart"
}

// UnsupportedError is a diagram type this package doesn't draw (gantt, pie, classDiagram…).
type UnsupportedError struct{ Type string }

func (e *UnsupportedError) Error() string { return fmt.Sprintf("%s diagrams aren't drawn", e.Type) }

// SyntaxError is a line the parser couldn't read.
type SyntaxError struct {
	Line int // 1-based, in the source
	Msg  string
}

func (e *SyntaxError) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Msg) }

var errEmpty = errors.New("no diagram")

// Parse reads a mermaid source. A type it doesn't draw is an *UnsupportedError, a line it can't read a *SyntaxError.
func Parse(src string) (*Diagram, error) {
	lines := sourceLines(src)
	if len(lines) == 0 {
		return nil, errEmpty
	}
	head := strings.Fields(lines[0].text)
	switch head[0] {
	case "flowchart", "graph", "flowchart-elk":
		f, err := parseFlowchart(head, lines[1:])
		if err != nil {
			return nil, err
		}
		return &Diagram{Type: head[0], Flow: f}, nil
	case "sequenceDiagram":
		s, err := parseSequence(lines[1:])
		if err != nil {
			return nil, err
		}
		return &Diagram{Type: head[0], Seq: s}, nil
	}
	return nil, &UnsupportedError{Type: head[0]}
}

type line struct {
	n    int // 1-based
	text string
}

// sourceLines is the source's statements, trimmed, without front matter, directives, comments or blank lines.
func sourceLines(src string) []line {
	raw := strings.Split(strings.ReplaceAll(src, "\r", ""), "\n")
	var out []line
	front := false
	for i := 0; i < len(raw); i++ {
		t := strings.TrimSpace(raw[i])
		if len(out) == 0 && t == "---" {
			front = !front
			continue
		}
		if front || t == "" || strings.HasPrefix(t, "%%") {
			continue
		}
		// a quoted label may run over lines
		for strings.Count(t, `"`)%2 == 1 && i+1 < len(raw) {
			i++
			t += "\n" + strings.TrimSpace(raw[i])
		}
		out = append(out, line{i + 1, stripComment(t)})
	}
	return out
}

// stripComment cuts a trailing %% comment outside quotes.
func stripComment(s string) string {
	in := false
	for i := 0; i+1 < len(s); i++ {
		switch {
		case s[i] == '"':
			in = !in
		case !in && s[i] == '%' && s[i+1] == '%':
			return strings.TrimSpace(s[:i])
		}
	}
	return s
}

// splitStatements splits a line at semicolons outside quotes.
func splitStatements(s string) []string {
	var out []string
	in, from := false, 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '"':
			in = !in
		case !in && s[i] == ';':
			out = append(out, s[from:i])
			from = i + 1
		}
	}
	out = append(out, s[from:])
	return out
}

var (
	breakTag = regexp.MustCompile(`(?i)<br\s*/?>`)
	entity   = regexp.MustCompile(`#([a-zA-Z]+|\d+);`)
	emphasis = regexp.MustCompile(`\*\*|__`)
)

// labelText is a label as it reads: quotes and markdown-string backticks off, <br> as a line break, entity codes
// (#quot;, #9829;) decoded.
func labelText(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	if len(s) >= 2 && s[0] == '`' && s[len(s)-1] == '`' {
		s = emphasis.ReplaceAllString(s[1:len(s)-1], "")
	}
	s = breakTag.ReplaceAllString(s, "\n")
	s = entity.ReplaceAllStringFunc(s, func(m string) string {
		if m[1] >= '0' && m[1] <= '9' {
			return html.UnescapeString("&#" + m[1:])
		}
		return html.UnescapeString("&" + m[1:])
	})
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.Join(lines, "\n")
}
