package app

import (
	"strings"

	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/text"
)

// lineReplace is the edit that puts lines, as a file holds them (each with its line ending), in place of n of snap's
// lines from line (0-based; n 0 inserts before it). A buffer holds its file's text with LF line endings and no newline
// after the last line, so the edit's text does too.
func lineReplace(snap text.Snapshot, line, n int, lines []string) text.Replace {
	start, end := snap.Len(), snap.Len()
	if line < snap.Lines() {
		start = snap.LineStart(line)
	}
	if line+n < snap.Lines() {
		end = snap.LineStart(line + n)
	}
	t := strings.ReplaceAll(strings.Join(lines, ""), "\r\n", "\n")
	if end == snap.Len() {
		switch {
		case n == 0 && start == snap.Len() && t != "":
			t = "\n" + strings.TrimSuffix(t, "\n") // after the last line
		case t == "" && start > 0:
			start-- // the last lines go, with the newline before them
		default:
			t = strings.TrimSuffix(t, "\n")
		}
	}
	return text.Replace{Range: text.Range{Start: start, End: end}, Text: []byte(t)}
}

// hunkEdits are the edits that apply hunks, a diff from the buffer's text as written, to the buffer: in order, as
// one change.
func hunkEdits(snap text.Snapshot, hs []gitx.Hunk) []text.Replace {
	var out []text.Replace
	for _, h := range hs {
		line := h.OldStart - 1
		if h.OldLines == 0 {
			line = h.OldStart
		}
		out = append(out, lineReplace(snap, line, h.OldLines, h.New))
	}
	return out
}
