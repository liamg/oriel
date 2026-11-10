//go:build perf && !race

package app_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
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

// markdownPreviewScene is the markdown preview of a doc of n sections.
func markdownPreviewScene(t *testing.T, n int) *apptest.Scene {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "notes.md"), []byte(longMD(n)), 0o644)
	s := apptest.ThreadsScene(t, apptest.Options{WorkDir: dir}, [4]string{"notes.md", "3", "Intro paragraph.", "Is this intro enough?"})
	s.Run("editor.open", registry.Args{"path": "notes.md"})
	s.Frames(3)
	s.Run("MarkdownPreview", nil)
	s.Frames(3)
	if !strings.Contains(s.Text(), "Is this intro enough?") {
		t.Fatalf("the preview of %d sections doesn't show its thread:\n%s", n, s.Text())
	}
	return s
}

// The markdown preview's frame doesn't grow with the doc: what's built from its text is kept until it changes.
func TestMarkdownPreviewWithLongDocBudget(t *testing.T) {
	f := apptest.MeanFrames(markdownPreviewScene(t, 10), markdownPreviewScene(t, 2000))
	few, many := f[0], f[1]
	t.Logf("the preview's frame over 10 sections: %v; over 2000: %v", few, many)
	if many > 2*few+2*time.Millisecond {
		t.Errorf("2000 sections make the preview's frame %v, against %v over 10", many, few)
	}
}

// planScene is the plan view on a PLAN.md of n sections, with n edits of it in the transcript.
func planScene(t *testing.T, n int) *apptest.Scene {
	ws, proj := t.TempDir(), t.TempDir()
	plan := filepath.Join(ws, "PLAN.md")
	os.WriteFile(plan, []byte(longMD(n)), 0o644)
	s := apptest.New(t, apptest.Options{WorkDir: proj})
	s.Now = apptest.At(14, 30, 0)
	var transcript []core.Item
	for i := range n {
		transcript = append(transcript, core.Item{ID: fmt.Sprint("e", i), Kind: core.ItemTool, Tool: "Edit", Time: s.Now.Add(-time.Minute),
			Diff: &core.FileDiff{Path: plan, Added: 1, Lines: []core.DiffLine{{Op: "+", New: 3, Text: "Intro paragraph."}}}})
	}
	se := core.SessionState{ID: "plan", Name: "plan", Kind: core.KindPlan, StartWork: true, Status: core.StatusNeedsYou, Dir: ws,
		Projects:   []core.Project{{Name: "proj", Dir: proj, Branch: "main", Primary: true}},
		Transcript: transcript,
		Threads: []core.Thread{{ID: "a1b2c3d4", Status: core.ThreadOpen, Created: s.Now, Updated: s.Now,
			Anchor:   &core.Anchor{Project: core.DocsProject, File: "PLAN.md", Line: 3, Text: "Intro paragraph."},
			Comments: []core.Comment{{ID: "c1", Author: core.AuthorYou, Name: "Liam", Body: "Is this intro enough?", Time: s.Now, Delivered: true}}}},
	}
	for _, ev := range []core.Event{core.SessionSynced{Session: se}, core.SessionFocused{ID: se.ID}} {
		if err := s.Store().Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	s.Run("plan.open", nil)
	s.Frames(3)
	if text := s.Text(); !strings.Contains(text, "Is this intro enough?") || !strings.Contains(text, fmt.Sprintf("rev %d", n)) {
		t.Fatalf("the plan of %d sections doesn't show its thread or its revision:\n%s", n, text)
	}
	return s
}

// The plan view's frame doesn't grow with its doc or the agent's edits of it: the doc is read and built again only
// when it changes, and its revisions counted when the transcript does.
func TestPlanViewWithLongDocBudget(t *testing.T) {
	f := apptest.MeanFrames(planScene(t, 10), planScene(t, 2000))
	few, many := f[0], f[1]
	t.Logf("the plan's frame over 10 sections: %v; over 2000: %v", few, many)
	if many > 2*few+2*time.Millisecond {
		t.Errorf("2000 sections make the plan's frame %v, against %v over 10", many, few)
	}
}

// agentEditScene is an editor on a file of n lines the agent just edited.
func agentEditScene(t *testing.T, n int) *apptest.Scene {
	dir := t.TempDir()
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	path := filepath.Join(dir, "notes.txt")
	os.WriteFile(path, []byte(b.String()), 0o644)
	s := apptest.New(t, apptest.Options{WorkDir: dir})
	s.Now = apptest.At(14, 30, 0)
	var diff []core.DiffLine
	for i := range n {
		diff = append(diff, core.DiffLine{Op: "+", New: i + 1, Text: fmt.Sprintf("line %d", i)})
	}
	se := core.SessionState{ID: "notes", Name: "notes", Kind: core.KindWork, Status: core.StatusWorking,
		Projects:   []core.Project{{Name: "notes", Dir: dir, Branch: "main", Primary: true}},
		Transcript: []core.Item{{ID: "e", Kind: core.ItemTool, Tool: "Write", Time: s.Now.Add(-4 * time.Second), Diff: &core.FileDiff{Path: path, Added: n, Lines: diff}}}}
	for _, ev := range []core.Event{core.SessionSynced{Session: se}, core.SessionFocused{ID: se.ID}} {
		if err := s.Store().Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	s.Run("editor.open", registry.Args{"path": "notes.txt"})
	s.Frames(3)
	if !strings.Contains(s.Text(), "agent edited 4s ago") {
		t.Fatalf("the agent's edit of %d lines isn't shown:\n%s", n, s.Text())
	}
	return s
}

// An editor on a file the agent just wrote: its frame doesn't grow with the file, the edit's lines found once for
// each version of the text.
func TestAgentEditsWithLongFileBudget(t *testing.T) {
	f := apptest.MeanFrames(agentEditScene(t, 100), agentEditScene(t, 50000))
	few, many := f[0], f[1]
	t.Logf("an editor on 100 lines the agent wrote: %v; on 50000: %v", few, many)
	if many > 2*few+2*time.Millisecond {
		t.Errorf("50000 lines make the editor's frame %v, against %v over 100", many, few)
	}
}
