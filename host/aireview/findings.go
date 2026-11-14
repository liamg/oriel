package aireview

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/liamg/oriel/core"
)

// SetFindings records findings as the user changed them: dismissed, or back.
func (x *Reviews) SetFindings(r core.FindingsRecorded) (err error) {
	defer x.env.Recover("SetFindings", &err)
	se, err := x.st.Session(r.Session)
	if err != nil {
		return err
	}
	for i, f := range r.Findings {
		old, ok := se.Finding(f.ID)
		if !ok {
			return fmt.Errorf("no finding %s", f.ID)
		}
		old.Dismissed = f.Dismissed
		r.Findings[i] = old
	}
	return x.st.Emit(r)
}

// rawFinding is a finding as a reviewer gives it, through the tool or in a JSON block, read leniently.
type rawFinding struct {
	Severity    string          `json:"severity"`
	Category    string          `json:"category"`
	Title       string          `json:"title"`
	Body        string          `json:"body"`
	Description string          `json:"description"`
	File        string          `json:"file"`
	Path        string          `json:"path"`
	Line        json.RawMessage `json:"line"`
	Project     string          `json:"project"`
	Repo        string          `json:"repo"`
}

var severityWords = map[string]string{
	"critical": "critical", "blocker": "critical", "severe": "critical",
	"high": "high", "major": "high", "error": "high",
	"medium": "medium", "med": "medium", "moderate": "medium",
	"low": "low", "minor": "low", "warning": "low",
	"nit": "nit", "nitpick": "nit", "info": "nit", "trivial": "nit",
}

var fileLine = regexp.MustCompile(`^(.+?):(\d+)(?:[-:]\d+)?$`)

func (in rawFinding) finding() (core.Finding, bool) {
	f := core.Finding{Category: strings.ToLower(strings.TrimSpace(in.Category)), Title: strings.TrimSpace(in.Title),
		Body: strings.TrimSpace(in.Body), File: strings.TrimSpace(in.File), Project: strings.TrimSpace(in.Project), Repo: strings.TrimSpace(in.Repo)}
	if f.Body == "" {
		f.Body = strings.TrimSpace(in.Description)
	}
	if f.Title == "" {
		f.Title, _, _ = strings.Cut(f.Body, "\n")
	}
	if f.Title == "" {
		return f, false
	}
	f.Severity = severityWords[strings.ToLower(strings.TrimSpace(in.Severity))]
	if f.Severity == "" {
		f.Severity = "low"
	}
	if f.File == "" {
		f.File = strings.TrimSpace(in.Path)
	}
	var n json.Number
	var s string
	switch {
	case json.Unmarshal(in.Line, &n) == nil:
		v, _ := n.Int64()
		f.Line = int(v)
	case json.Unmarshal(in.Line, &s) == nil:
		s, _, _ = strings.Cut(s, "-")
		f.Line, _ = strconv.Atoi(strings.TrimSpace(s))
	}
	if m := fileLine.FindStringSubmatch(f.File); m != nil {
		f.File = m[1]
		if f.Line == 0 {
			f.Line, _ = strconv.Atoi(m[2])
		}
	}
	f.File = strings.TrimPrefix(filepath.ToSlash(f.File), "./")
	return f, true
}

var (
	fenced    = regexp.MustCompile("(?s)```[a-zA-Z]*\\s*\\n(.*?)```")
	jsonStart = regexp.MustCompile(`(?m)^\s*[\[{]`)
)

// ParseFindings reads the findings a reviewer wrote as JSON in its answer: a fenced block or a bare one, either
// {"findings": [...]} or the list itself.
func ParseFindings(text string) []core.Finding {
	var blocks []string
	for _, m := range fenced.FindAllStringSubmatch(text, -1) {
		blocks = append(blocks, m[1])
	}
	if loc := jsonStart.FindStringIndex(text); loc != nil {
		blocks = append(blocks, text[loc[0]:])
	}
	for _, b := range blocks {
		dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(b)))
		var v any
		if dec.Decode(&v) != nil {
			continue
		}
		raw, _ := json.Marshal(v)
		var list []rawFinding
		if obj, ok := v.(map[string]any); ok {
			inner, _ := json.Marshal(obj["findings"])
			if json.Unmarshal(inner, &list) != nil {
				continue
			}
		} else if json.Unmarshal(raw, &list) != nil {
			continue
		}
		var out []core.Finding
		for _, in := range list {
			if f, ok := in.finding(); ok {
				out = append(out, f)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}
