package transcript

import (
	"encoding/json"
	"strings"

	"github.com/liamg/oriel/core"
)

// Diff is what an Edit, MultiEdit or Write did to its file: the patch Claude Code wrote beside the result when it
// has one, else what the call's input says. nil for any other tool.
func Diff(tool string, input, result json.RawMessage) *core.FileDiff {
	switch tool {
	case "Edit", "MultiEdit", "Write":
	default:
		return nil
	}
	var in struct {
		Path    string `json:"file_path"`
		Old     string `json:"old_string"`
		New     string `json:"new_string"`
		Content string `json:"content"`
		Edits   []struct {
			Old string `json:"old_string"`
			New string `json:"new_string"`
		} `json:"edits"`
	}
	_ = json.Unmarshal(input, &in)
	if d := patchDiff(result); d != nil {
		if d.Path == "" {
			d.Path = in.Path
		}
		return d
	}
	if in.Path == "" {
		return nil
	}
	d := &core.FileDiff{Path: in.Path}
	switch tool {
	case "Write":
		addLines(d, "", in.Content)
	case "Edit":
		addLines(d, in.Old, in.New)
	case "MultiEdit":
		for _, e := range in.Edits {
			addLines(d, e.Old, e.New)
		}
	}
	return d
}

// patchDiff reads the structuredPatch of a tool's result.
func patchDiff(result json.RawMessage) *core.FileDiff {
	var r struct {
		Path    string `json:"filePath"`
		Type    string `json:"type"`
		Content string `json:"content"`
		Patch   []struct {
			OldStart int      `json:"oldStart"`
			NewStart int      `json:"newStart"`
			Lines    []string `json:"lines"`
		} `json:"structuredPatch"`
	}
	if len(result) == 0 || json.Unmarshal(result, &r) != nil {
		return nil
	}
	if len(r.Patch) == 0 {
		if r.Type == "create" && r.Path != "" {
			d := &core.FileDiff{Path: r.Path}
			addLines(d, "", r.Content)
			return d
		}
		return nil
	}
	d := &core.FileDiff{Path: r.Path}
	for _, h := range r.Patch {
		old, nw := h.OldStart, h.NewStart
		for _, l := range h.Lines {
			if l == "" {
				continue
			}
			op, text := l[:1], l[1:]
			switch op {
			case "-":
				d.Lines = append(d.Lines, core.DiffLine{Op: op, Old: old, Text: text})
				d.Removed++
				old++
			case "+":
				d.Lines = append(d.Lines, core.DiffLine{Op: op, New: nw, Text: text})
				d.Added++
				nw++
			case " ":
				d.Lines = append(d.Lines, core.DiffLine{Op: op, Old: old, New: nw, Text: text})
				old++
				nw++
			}
		}
	}
	return d
}

// addLines adds old's lines as removed and new's as added; with no patch there are no line numbers.
func addLines(d *core.FileDiff, old, new string) {
	for _, l := range lines(old) {
		d.Lines = append(d.Lines, core.DiffLine{Op: "-", Text: l})
		d.Removed++
	}
	for _, l := range lines(new) {
		d.Lines = append(d.Lines, core.DiffLine{Op: "+", Text: l})
		d.Added++
	}
}

func lines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}
