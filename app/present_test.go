package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
	"github.com/liamg/oriel/vim"
)

// notesMD is a markdown file long enough to scroll: a title, an intro, forty items and an ending.
func notesMD() string {
	var b strings.Builder
	b.WriteString("# Notes\n\nIntro paragraph.\n\n## Items\n\n")
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&b, "- item %d\n", i)
	}
	b.WriteString("\n## Last\n\nThe end.\n")
	return b.String()
}

// threadsScene is a session on dir with open threads anchored in it: each {file, line, text, body}.
func threadsScene(t *testing.T, dir string, threads ...[4]string) *errScene {
	t.Helper()
	s := newScene(t, "", dir, mockupSize)
	s.now = at(14, 30, 0)
	se := core.SessionState{ID: "notes", Name: "notes", Kind: core.KindWork, Status: core.StatusWorking,
		Projects: []core.Project{{Name: "notes", Dir: dir, Branch: "main", Primary: true}}}
	for i, th := range threads {
		var line int
		fmt.Sscan(th[1], &line)
		se.Threads = append(se.Threads, core.Thread{ID: core.ThreadID(fmt.Sprintf("t%d00000", i)), Status: core.ThreadOpen, Created: s.now.Add(-time.Minute),
			Updated: s.now.Add(-time.Minute), Anchor: &core.Anchor{Project: "notes", File: th[0], Line: line, Text: th[2]},
			Comments: []core.Comment{{ID: fmt.Sprint("c", i), Author: core.AuthorYou, Name: "Liam", Body: th[3], Time: s.now.Add(-time.Minute), Delivered: true}}})
	}
	for _, ev := range []core.Event{core.SessionSynced{Session: se}, core.SessionFocused{ID: se.ID}} {
		if err := s.store.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func (s *errScene) frames(n int) {
	for range n {
		s.frame()
	}
}

// pane is the editor window showing name.
func (s *errScene) pane(t *testing.T, name string) vim.Pane {
	t.Helper()
	for _, id := range s.editor().e.WindowIDs() {
		if p, ok := s.editor().e.Pane(id); ok && p.Name() == name {
			return p
		}
	}
	t.Fatalf("no window on %s", name)
	return vim.Pane{}
}

// ED-15, done when: "On a .md file, Preview renders it with its threads in the margin; the button reads Edit; Edit
// returns at the same place. Two windows on one file can be in Edit and Preview at once, and an edit in one shows in
// the other."
func TestMarkdownPreview(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "notes.md"), []byte(notesMD()), 0o644)
	s := threadsScene(t, dir, [4]string{"notes.md", "3", "Intro paragraph.", "Is this intro enough?"})
	s.run("editor.open", registry.Args{"path": "notes.md"})
	s.frames(3)
	if !strings.Contains(s.frameText(), "Preview") {
		t.Fatalf("no Preview action on a markdown file:\n%s", s.frameText())
	}
	s.click(s.textPoint(t, "Preview"))
	s.frames(4)
	text := s.frameText()
	for _, want := range []string{"Notes", "Intro paragraph.", "item 1", "Edit", "Is this intro enough?"} {
		if !strings.Contains(text, want) {
			t.Errorf("the preview lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "# Notes") || strings.Contains(text, "Preview") {
		t.Errorf("the preview shows the source, or the button still reads Preview:\n%s", text)
	}
	s.lint("the markdown preview")
	para, ok1 := s.runStarting("Intro paragraph.")
	card, ok2 := s.runStarting("Is this intro enough?")
	if !ok1 || !ok2 || card.Y-para.Y < 0 || card.Y-para.Y > 120 || card.X <= para.X {
		t.Errorf("the thread isn't in the margin level with its paragraph: paragraph %v, card %v", para, card)
	}

	// Hidden, the thread is a bubble beside its block; a click on it shows it again.
	s.run("threads.toggle", nil)
	s.frames(2)
	if strings.Contains(s.frameText(), "Is this intro enough?") {
		t.Error("threads.toggle didn't hide the preview's thread")
	}
	s.click(centre(s.access("1 thread").Bounds))
	s.frames(2)
	if text := s.frameText(); !strings.Contains(text, "Is this intro enough?") || strings.Contains(text, "New thread") {
		t.Errorf("a click on the bubble didn't just show the thread:\n%s", text)
	}

	// Edit returns at the same place; Preview opens where Edit is.
	s.click(s.textPoint(t, "Edit"))
	s.frames(2)
	s.keys("27Gzt")
	top := s.pane(t, "notes.md").Top()
	s.run("MarkdownPreview", nil)
	s.frames(4)
	intro, _ := s.runStarting("Intro paragraph.")
	item, _ := s.runStarting("item 21")
	if intro.Y > 90 || item.Y < 90 || item.Y > s.size.Y {
		t.Errorf("the preview doesn't open where the window was (line %d): the intro at %v, item 21 at %v", top+1, intro, item)
	}
	s.run("MarkdownPreview", nil)
	s.frames(2)
	if got := s.pane(t, "notes.md").Top(); got != top {
		t.Errorf("Edit came back with line %d at the top, not %d", got+1, top+1)
	}
	// Scrolling the preview moves the window: Edit comes back where the preview was.
	s.run("MarkdownPreview", nil)
	s.frames(4)
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: s.textPoint(t, "item 21")})
	s.frame()
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerScroll, Pos: s.textPoint(t, "item 21"), Scroll: render.Point{Y: -5000}})
	s.frames(3)
	s.run("MarkdownPreview", nil)
	s.frames(2)
	if got := s.pane(t, "notes.md").Top(); got != 0 {
		t.Errorf("after scrolling the preview to its start, Edit came back with line %d at the top", got+1)
	}

	// j/k move between blocks; o goes to the selected one's source.
	s.run("MarkdownPreview", nil)
	s.frames(3)
	for range 3 {
		s.keyCommand(t, "j")
	}
	s.keyCommand(t, "o")
	s.frames(2)
	if p := s.pane(t, "notes.md"); p.Cursor().Line != 4 || !strings.Contains(s.frameText(), "Preview") {
		t.Errorf("go to source left the cursor on line %d, presenting %v", p.Cursor().Line+1, !strings.Contains(s.frameText(), "Preview"))
	}

	// Two windows: one previews while the other edits, and the edit shows in the preview.
	s.keys("gg")
	s.editor().e.Run("vsplit")
	s.frames(2)
	other := s.editor().e.WindowID()
	var preview vim.WindowID
	for _, id := range s.editor().e.WindowIDs() {
		if id != other {
			preview = id
		}
	}
	s.run("editor.present", registry.Args{"name": "markdown", "window": fmt.Sprint(int(preview))})
	s.frames(3)
	s.editor().e.EnterWindow(other)
	s.keys("ggOFresh line typed<Esc>")
	s.frames(3)
	n := 0
	for _, op := range s.list.Ops {
		if op.Kind == render.OpGlyphs && strings.Contains(op.Run.Text, "Fresh line typed") {
			n++
		}
	}
	if n < 2 {
		t.Errorf("the edit shows in %d windows, not both:\n%s", n, s.frameText())
	}
	if text := s.frameText(); !strings.Contains(text, "Edit") || !strings.Contains(text, "Preview") {
		t.Errorf("the two windows' buttons don't read Edit and Preview:\n%s", text)
	}
}

// opStarting is where the first run starting with prefix is drawn in f.
func opStarting(f headless.Frame, prefix string) (render.Point, bool) {
	for _, op := range f.Ops {
		if op.Kind == render.OpGlyphs && strings.HasPrefix(op.Run.Text, prefix) {
			return op.At, true
		}
	}
	return render.Point{}, false
}

// ED-15, done when: "A Lua presenter (a CSV table, say) maps rows to lines; a thread on line 4 shows beside its row,
// and a comment made on a row anchors to that row's line and shows inline in Edit."
func TestLuaPresenterMapsRowsToLines(t *testing.T) {
	r, a, _ := agentSession(t)
	path := filepath.Join(projectDir(t, r), "prices.csv")
	os.WriteFile(path, []byte("sku,price\nA-1,10\nB-2,20\nC-3,30\nD-4,40\nE-5,50\n"), 0o644)
	run := func(id string, args registry.Args) {
		t.Helper()
		if err := r.do(func() error { return r.host.Run(context.Background(), id, args) }); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	run("lua", registry.Args{"args": `oriel.editor.presenter{ name = "csv", label = "Table", comments = true, ` +
		`when = function(p) return p:match("%.csv$") ~= nil end, ` +
		`build = function(ctx) for i, l in ipairs(ctx.lines) do if l ~= "" then ` +
		`ctx.block(i, i, function() return oriel.ui.text("row " .. i .. ": " .. (l:gsub(",", " | "))) end) end end end }`})
	run("request.comment", registry.Args{"file": path, "line": 4, "text": "C-3,30", "body": "Is C-3 right?"})
	a.WaitSent(t)
	threadWith(t, r, "Is C-3 right?")
	a.Emit(agent.TurnEnd{})
	run("editor.open", registry.Args{"path": path})
	r.waitText("C-3,30")
	run("editor.present", registry.Args{"name": "csv"})
	r.latest("the thread beside row 4", func(f headless.Frame) bool {
		row, ok1 := opStarting(f, "row 4: C-3 | 30")
		card, ok2 := opStarting(f, "Is C-3 right?")
		return ok1 && ok2 && card.Y-row.Y >= 0 && card.Y-row.Y < 120 && card.X > row.X
	})

	r.clickText("row 5: D-4 | 40")
	r.waitText("New thread")
	r.typeText("Raise D-4")
	r.waitText("Raise D-4")
	r.typeKeys("<CR>")
	in := a.WaitSent(t)
	for _, want := range []string{`where="prices.csv:5"`, "D-4,40", "Raise D-4"} {
		if !strings.Contains(in.Text, want) {
			t.Errorf("the comment reached the agent without %q:\n%s", want, in.Text)
		}
	}
	threadWith(t, r, "Raise D-4")
	run("editor.present", registry.Args{"name": "csv"})
	r.waitFrame("the new thread between the lines in Edit", func(s string) bool {
		return containsAll(s, "D-4,40", "· L5 ·", "Raise D-4") && !strings.Contains(s, "row 5:")
	})
}

// ED-15: a window's presentation comes back after a restart, as its file and cursor do.
func TestPresentationComesBackAfterARestart(t *testing.T) {
	state, dir := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(dir, "notes.md"), []byte(notesMD()), 0o644)
	r := startApp(t, state, dir, nil)
	if err := r.do(func() error {
		if err := r.host.Run(context.Background(), "editor.open", registry.Args{"path": filepath.Join(dir, "notes.md")}); err != nil {
			return err
		}
		return r.host.Run(context.Background(), "editor.present", registry.Args{"name": "markdown"})
	}); err != nil {
		t.Fatal(err)
	}
	r.waitFrame("the preview", func(s string) bool {
		return containsAll(s, "Intro paragraph.", "Edit") && !strings.Contains(s, "Preview")
	})
	r.stop()

	r2 := startApp(t, state, dir, nil)
	r2.waitFrame("the preview back", func(s string) bool {
		return containsAll(s, "Intro paragraph.", "Edit") && !strings.Contains(s, "Preview")
	})
}

// threadsFile is a Go file with threads on lines 4 (two) and 7.
func threadsFile(t *testing.T) (*errScene, string) {
	dir := t.TempDir()
	src := "package pricing\n\nfunc Round() {\n\tfloor := scaled.Floor()\n\t_ = floor\n\n\treturn\n}\n"
	os.WriteFile(filepath.Join(dir, "round.go"), []byte(src), 0o644)
	os.WriteFile(filepath.Join(dir, "other.go"), []byte(src), 0o644)
	s := threadsScene(t, dir,
		[4]string{"round.go", "4", "\tfloor := scaled.Floor()", "Floor goes the wrong way for negatives"},
		[4]string{"round.go", "4", "\tfloor := scaled.Floor()", "And for zero?"},
		[4]string{"round.go", "7", "\treturn", "Return the rounded value"},
		[4]string{"other.go", "7", "\treturn", "Other file's thread"})
	return s, dir
}

// ED-15, done when: "threads.toggle hides a file's threads behind gutter bubbles; hovering one shows its popover, which
// goes when the pointer leaves; clicking shows the threads at that one."
func TestThreadsHideBehindBubbles(t *testing.T) {
	s, _ := threadsFile(t)
	s.run("editor.open", registry.Args{"path": "round.go"})
	s.frames(3)
	for _, want := range []string{"Floor goes the wrong way for negatives", "Return the rounded value"} {
		if !strings.Contains(s.frameText(), want) {
			t.Fatalf("the threads don't show at first: no %q", want)
		}
	}
	if s.access("threads 3") == nil {
		t.Errorf("no threads action with the file's count")
	}
	s.run("threads.toggle", nil)
	s.frames(3)
	if text := s.frameText(); strings.Contains(text, "Floor goes the wrong way") || strings.Contains(text, "Return the rounded value") {
		t.Fatalf("threads.toggle didn't hide the threads:\n%s", text)
	}
	bubble := s.access("2 threads")
	if bubble == nil || s.access("1 thread") == nil {
		t.Fatal("no speech bubbles in the gutter")
	}
	focus := s.ui.Focused()

	// Hovering the bubble shows the popover; the pointer can move into it; it goes when the pointer leaves.
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: centre(bubble.Bounds)})
	s.frames(2)
	if text := s.frameText(); !containsAll(text, "Floor goes the wrong way for negatives", "And for zero?", "OPEN") {
		t.Fatalf("no popover over the bubble:\n%s", text)
	}
	pop := s.access("Threads on line 4")
	if pop == nil {
		t.Fatal("the popover has no name")
	}
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: pop.Bounds.Max.X - 12, Y: pop.Bounds.Max.Y - 8}})
	s.frames(2)
	if !strings.Contains(s.frameText(), "And for zero?") {
		t.Error("the popover went when the pointer moved into it")
	}
	if s.ui.Focused() != focus {
		t.Error("the popover took focus")
	}
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: s.size.X - 20, Y: s.size.Y - 60}})
	s.frames(2)
	if strings.Contains(s.frameText(), "And for zero?") {
		t.Error("the popover stays with the pointer gone")
	}

	// A click on the bubble shows the threads again, at that one, its reply field ready.
	s.click(centre(s.access("2 threads").Bounds))
	s.frames(3)
	if text := s.frameText(); !containsAll(text, "Floor goes the wrong way for negatives", "Return the rounded value") {
		t.Fatalf("a click on the bubble didn't show the threads:\n%s", text)
	}
	if n := s.focusedNamed("Reply to the thread"); n == nil {
		t.Error("the thread's reply field isn't ready")
	}

	// Hidden again, ⏎ on a line with threads shows them.
	s.run("threads.toggle", nil)
	s.frames(2)
	s.click(s.textPoint(t, "round.go"))
	s.keys("7G")
	s.frames(2)
	s.keyCommand(t, "<CR>")
	s.frames(2)
	if !strings.Contains(s.frameText(), "Return the rounded value") {
		t.Errorf("⏎ on line 7 didn't show its thread:\n%s", s.frameText())
	}
}

// focusedNamed is the focused accessible node named name, or nil.
func (s *errScene) focusedNamed(name string) *render.AccessNode {
	var find func(n *render.AccessNode) *render.AccessNode
	find = func(n *render.AccessNode) *render.AccessNode {
		if n.Name == name && n.Focused {
			return n
		}
		for _, k := range n.Children {
			if f := find(k); f != nil {
				return f
			}
		}
		return nil
	}
	return find(s.ui.AccessTree())
}

// ED-15: editor.threads = "hidden" opens files with their threads behind bubbles.
func TestThreadsHiddenOption(t *testing.T) {
	s, _ := threadsFile(t)
	if err := s.reg.Options().Set("editor.threads", registry.LayerUser, registry.At{}, "hidden"); err != nil {
		t.Fatal(err)
	}
	s.run("editor.open", registry.Args{"path": "other.go"})
	s.frames(3)
	if strings.Contains(s.frameText(), "Other file's thread") || s.access("1 thread") == nil {
		t.Errorf("other.go opened with its threads showing:\n%s", s.frameText())
	}
}

// ED-15: oriel.editor.action adds a breadcrumb button whose label follows on(window); here it shows a Lua presenter.
func TestLuaEditorAction(t *testing.T) {
	s := luaScene(t, `oriel.editor.presenter{ name = "csv", label = "Table", comments = true,
  when = function(path) return path:match("%.csv$") ~= nil end,
  build = function(ctx)
    for i, line in ipairs(ctx.lines) do
      ctx.block(i, i, function() return oriel.ui.text((line:gsub(",", "  │  "))) end)
    end
  end }
oriel.editor.action{ name = "csv.table", label = "Table", label_on = "Edit", command = "editor.present", args = { name = "csv" },
  when = function(path) return path:match("%.csv$") ~= nil end,
  on = function(window) return window.presenter == "csv" end,
  note = function(window) return "#" .. window.id end }`)
	os.WriteFile(filepath.Join(s.sh.cfg.WorkDir, "prices.csv"), []byte("sku,price\nA-1,10\n"), 0o644)
	s.run("editor.open", registry.Args{"path": "prices.csv"})
	s.frames(2)
	label := "Table #" + fmt.Sprint(int(s.editor().e.WindowID()))
	s.click(s.textPoint(t, label))
	s.frames(3)
	text := s.frameText()
	if !strings.Contains(text, "A-1  │  10") || !strings.Contains(text, "Edit #") || strings.Contains(text, "A-1,10") {
		t.Errorf("the Lua action didn't present the file, or its label didn't follow:\n%s", text)
	}
}
