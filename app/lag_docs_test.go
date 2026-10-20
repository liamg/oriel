//go:build !race

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/tools/perf"
)

// longMD is a markdown doc of n sections, each a heading, a paragraph and a list.
func longMD(n int) string {
	var b strings.Builder
	b.WriteString("# Notes\n\nIntro paragraph.\n\n")
	for i := range n {
		fmt.Fprintf(&b, "## Section %d\n\nSome text about section %d, with `code` and *emphasis*.\n\n- one\n- two\n\n", i, i)
	}
	return b.String()
}

// avgFrame is the average of 100 frames, after 10 to settle.
func avgFrame(s *errScene) time.Duration {
	s.frames(10)
	start := time.Now()
	s.frames(100)
	return time.Since(start) / 100
}

// markdownPreviewFrame is the average frame of the markdown preview of a doc of n sections.
func markdownPreviewFrame(t *testing.T, n int) time.Duration {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "notes.md"), []byte(longMD(n)), 0o644)
	s := threadsScene(t, dir, [4]string{"notes.md", "3", "Intro paragraph.", "Is this intro enough?"})
	s.run("editor.open", registry.Args{"path": "notes.md"})
	s.frames(3)
	s.run("MarkdownPreview", nil)
	s.frames(3)
	if !strings.Contains(s.frameText(), "Is this intro enough?") {
		t.Fatalf("the preview of %d sections doesn't show its thread:\n%s", n, s.frameText())
	}
	return avgFrame(s)
}

// The markdown preview's frame doesn't grow with the doc: what's built from its text is kept until it changes.
func TestMarkdownPreviewWithLongDocBudget(t *testing.T) {
	perf.Timed(t)
	few, many := markdownPreviewFrame(t, 10), markdownPreviewFrame(t, 2000)
	t.Logf("the preview's frame over 10 sections: %v; over 2000: %v", few, many)
	if many > 2*few+2*time.Millisecond {
		t.Errorf("2000 sections make the preview's frame %v, against %v over 10", many, few)
	}
}

// planFrame is the average frame of the plan view on a PLAN.md of n sections, with n edits of it in the transcript.
func planFrame(t *testing.T, n int) time.Duration {
	ws, proj := t.TempDir(), t.TempDir()
	plan := filepath.Join(ws, "PLAN.md")
	os.WriteFile(plan, []byte(longMD(n)), 0o644)
	s := newScene(t, "", proj, mockupSize)
	s.now = at(14, 30, 0)
	var transcript []core.Item
	for i := range n {
		transcript = append(transcript, core.Item{ID: fmt.Sprint("e", i), Kind: core.ItemTool, Tool: "Edit", Time: s.now.Add(-time.Minute),
			Diff: &core.FileDiff{Path: plan, Added: 1, Lines: []core.DiffLine{{Op: "+", New: 3, Text: "Intro paragraph."}}}})
	}
	se := core.SessionState{ID: "plan", Name: "plan", Kind: core.KindPlan, StartWork: true, Status: core.StatusNeedsYou, Dir: ws,
		Projects:   []core.Project{{Name: "proj", Dir: proj, Branch: "main", Primary: true}},
		Transcript: transcript,
		Threads: []core.Thread{{ID: "a1b2c3d4", Status: core.ThreadOpen, Created: s.now, Updated: s.now,
			Anchor:   &core.Anchor{Project: core.DocsProject, File: "PLAN.md", Line: 3, Text: "Intro paragraph."},
			Comments: []core.Comment{{ID: "c1", Author: core.AuthorYou, Name: "Liam", Body: "Is this intro enough?", Time: s.now, Delivered: true}}}},
	}
	for _, ev := range []core.Event{core.SessionSynced{Session: se}, core.SessionFocused{ID: se.ID}} {
		if err := s.store.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	s.run("plan.open", nil)
	s.frames(3)
	if text := s.frameText(); !strings.Contains(text, "Is this intro enough?") || !strings.Contains(text, fmt.Sprintf("rev %d", n)) {
		t.Fatalf("the plan of %d sections doesn't show its thread or its revision:\n%s", n, text)
	}
	return avgFrame(s)
}

// The plan view's frame doesn't grow with its doc or the agent's edits of it: the doc is read and built again only
// when it changes, and its revisions counted when the transcript does.
func TestPlanViewWithLongDocBudget(t *testing.T) {
	perf.Timed(t)
	few, many := planFrame(t, 10), planFrame(t, 2000)
	t.Logf("the plan's frame over 10 sections: %v; over 2000: %v", few, many)
	if many > 2*few+2*time.Millisecond {
		t.Errorf("2000 sections make the plan's frame %v, against %v over 10", many, few)
	}
}

// agentEditFrame is the average frame of an editor on a file of n lines the agent just edited.
func agentEditFrame(t *testing.T, n int) time.Duration {
	dir := t.TempDir()
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	path := filepath.Join(dir, "notes.txt")
	os.WriteFile(path, []byte(b.String()), 0o644)
	s := newScene(t, "", dir, mockupSize)
	s.now = at(14, 30, 0)
	var diff []core.DiffLine
	for i := range n {
		diff = append(diff, core.DiffLine{Op: "+", New: i + 1, Text: fmt.Sprintf("line %d", i)})
	}
	se := core.SessionState{ID: "notes", Name: "notes", Kind: core.KindWork, Status: core.StatusWorking,
		Projects:   []core.Project{{Name: "notes", Dir: dir, Branch: "main", Primary: true}},
		Transcript: []core.Item{{ID: "e", Kind: core.ItemTool, Tool: "Write", Time: s.now.Add(-4 * time.Second), Diff: &core.FileDiff{Path: path, Added: n, Lines: diff}}}}
	for _, ev := range []core.Event{core.SessionSynced{Session: se}, core.SessionFocused{ID: se.ID}} {
		if err := s.store.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	s.run("editor.open", registry.Args{"path": "notes.txt"})
	s.frames(3)
	if !strings.Contains(s.frameText(), "agent edited 4s ago") {
		t.Fatalf("the agent's edit of %d lines isn't shown:\n%s", n, s.frameText())
	}
	return avgFrame(s)
}

// An editor on a file the agent just wrote: its frame doesn't grow with the file, the edit's lines found once for
// each version of the text.
func TestAgentEditsWithLongFileBudget(t *testing.T) {
	perf.Timed(t)
	few, many := agentEditFrame(t, 100), agentEditFrame(t, 50000)
	t.Logf("an editor on 100 lines the agent wrote: %v; on 50000: %v", few, many)
	if many > 2*few+2*time.Millisecond {
		t.Errorf("50000 lines make the editor's frame %v, against %v over 100", many, few)
	}
}
