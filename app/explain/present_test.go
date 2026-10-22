package explain_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
	"github.com/liamg/oriel/ui/layout"
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

// windows are the editor windows on screen.
func windows(s *apptest.Scene) []registry.EditorWindow {
	var out []registry.EditorWindow
	for _, v := range s.Host().Layout().Shown() {
		if w, ok := s.Editor().Window(v.Instance); ok && v.ID == kit.EditorView {
			out = append(out, w)
		}
	}
	return out
}

// pane is the editor window on screen showing name, relative to dir.
func pane(t *testing.T, s *apptest.Scene, dir, name string) registry.EditorWindow {
	t.Helper()
	for _, w := range windows(s) {
		if w.Path == filepath.Join(dir, name) {
			return w
		}
	}
	t.Fatalf("no window on %s", name)
	return registry.EditorWindow{}
}

// On a .md file, Preview renders it with its threads in the margin; the button reads Edit; Edit returns at the same
// place. Two windows on one file can be in Edit and Preview at once, and an edit in one shows in the other.
func TestMarkdownPreview(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "notes.md"), []byte(notesMD()), 0o644)
	s := apptest.ThreadsScene(t, apptest.Options{WorkDir: dir}, [4]string{"notes.md", "3", "Intro paragraph.", "Is this intro enough?"})
	s.Run("editor.open", registry.Args{"path": "notes.md"})
	s.Frames(3)
	if !strings.Contains(s.Text(), "Preview") {
		t.Fatalf("no Preview action on a markdown file:\n%s", s.Text())
	}
	s.Click(s.TextPoint("Preview"), 0)
	s.Frames(4)
	text := s.Text()
	for _, want := range []string{"Notes", "Intro paragraph.", "item 1", "Edit", "Is this intro enough?"} {
		if !strings.Contains(text, want) {
			t.Errorf("the preview lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "# Notes") || strings.Contains(text, "Preview") {
		t.Errorf("the preview shows the source, or the button still reads Preview:\n%s", text)
	}
	s.Lint("the markdown preview")
	para, ok1 := s.TextStart("Intro paragraph.")
	card, ok2 := s.TextStart("Is this intro enough?")
	if !ok1 || !ok2 || card.Y-para.Y < 0 || card.Y-para.Y > 120 || card.X <= para.X {
		t.Errorf("the thread isn't in the margin level with its paragraph: paragraph %v, card %v", para, card)
	}

	// Hidden, the thread is a bubble beside its block; a click on it shows it again.
	s.Run("threads.toggle", nil)
	s.Frames(2)
	if strings.Contains(s.Text(), "Is this intro enough?") {
		t.Error("threads.toggle didn't hide the preview's thread")
	}
	s.Click(apptest.Centre(s.Node("1 thread").Bounds), 0)
	s.Frames(2)
	if text := s.Text(); !strings.Contains(text, "Is this intro enough?") || strings.Contains(text, "New thread") {
		t.Errorf("a click on the bubble didn't just show the thread:\n%s", text)
	}

	// Edit returns at the same place; Preview opens where Edit is.
	s.Click(s.TextPoint("Edit"), 0)
	s.Frames(2)
	s.EditorKeys("27Gzt")
	top := pane(t, s, dir, "notes.md").Top
	s.Run("MarkdownPreview", nil)
	s.Frames(4)
	intro, _ := s.TextStart("Intro paragraph.")
	item, _ := s.TextStart("item 21")
	if intro.Y > 90 || item.Y < 90 || item.Y > s.Size().Y {
		t.Errorf("the preview doesn't open where the window was (line %d): the intro at %v, item 21 at %v", top+1, intro, item)
	}
	s.Run("MarkdownPreview", nil)
	s.Frames(2)
	if got := pane(t, s, dir, "notes.md").Top; got != top {
		t.Errorf("Edit came back with line %d at the top, not %d", got+1, top+1)
	}
	// Scrolling the preview moves the window: Edit comes back where the preview was.
	s.Run("MarkdownPreview", nil)
	s.Frames(4)
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: s.TextPoint("item 21")})
	s.Frame()
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerScroll, Pos: s.TextPoint("item 21"), Scroll: render.Point{Y: -5000}})
	s.Frames(3)
	s.Run("MarkdownPreview", nil)
	s.Frames(2)
	if got := pane(t, s, dir, "notes.md").Top; got != 0 {
		t.Errorf("after scrolling the preview to its start, Edit came back with line %d at the top", got+1)
	}

	// j/k move between blocks; o goes to the selected one's source.
	s.Run("MarkdownPreview", nil)
	s.Frames(3)
	for range 3 {
		s.Press("j")
	}
	s.Press("o")
	s.Frames(2)
	if p := pane(t, s, dir, "notes.md"); p.Cursor.Line != 4 || !strings.Contains(s.Text(), "Preview") {
		t.Errorf("go to source left the cursor on line %d, presenting %v", p.Cursor.Line+1, !strings.Contains(s.Text(), "Preview"))
	}

	// Two windows: one previews while the other edits, and the edit shows in the preview.
	s.EditorKeys("gg")
	s.EditorKeys(":vsplit<CR>")
	s.Frames(2)
	other := s.EditorWindow().ID
	var preview string
	for _, w := range windows(s) {
		if w.ID != other {
			preview = w.ID
		}
	}
	s.Run("editor.present", registry.Args{"name": "markdown", "window": preview})
	s.Frames(3)
	s.Host().Layout().Show(layout.ViewRef{ID: kit.EditorView, Instance: other})
	s.Frame()
	s.EditorKeys("ggOFresh line typed<Esc>")
	s.Frames(3)
	n := 0
	for _, op := range s.List().Ops {
		if op.Kind == render.OpGlyphs && strings.Contains(op.Run.Text, "Fresh line typed") {
			n++
		}
	}
	if n < 2 {
		t.Errorf("the edit shows in %d windows, not both:\n%s", n, s.Text())
	}
	if text := s.Text(); !strings.Contains(text, "Edit") || !strings.Contains(text, "Preview") {
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

// A Lua presenter (a CSV table, say) maps rows to lines; a thread on line 4 shows beside its row, and a comment made
// on a row anchors to that row's line and shows inline in Edit.
func TestLuaPresenterMapsRowsToLines(t *testing.T) {
	r, a, _ := apptest.AgentWindow(t)
	path := filepath.Join(r.ProjectDir("w"), "prices.csv")
	os.WriteFile(path, []byte("sku,price\nA-1,10\nB-2,20\nC-3,30\nD-4,40\nE-5,50\n"), 0o644)
	r.Run("lua", registry.Args{"args": `oriel.editor.presenter{ name = "csv", label = "Table", comments = true, ` +
		`when = function(p) return p:match("%.csv$") ~= nil end, ` +
		`build = function(ctx) for i, l in ipairs(ctx.lines) do if l ~= "" then ` +
		`ctx.block(i, i, function() return oriel.ui.text("row " .. i .. ": " .. (l:gsub(",", " | "))) end) end end end }`})
	r.Run("request.comment", registry.Args{"file": path, "line": 4, "text": "C-3,30", "body": "Is C-3 right?"})
	a.WaitSent(t)
	threadWith(t, r, "Is C-3 right?")
	rig.Answered(t, r.Store, a)
	a.Emit(agent.TurnEnd{})
	r.Run("editor.open", registry.Args{"path": path})
	r.WaitText("C-3,30")
	r.Run("editor.present", registry.Args{"name": "csv"})
	r.Latest("the thread beside row 4", func(f headless.Frame) bool {
		row, ok1 := opStarting(f, "row 4: C-3 | 30")
		card, ok2 := opStarting(f, "Is C-3 right?")
		return ok1 && ok2 && card.Y-row.Y >= 0 && card.Y-row.Y < 120 && card.X > row.X
	})

	r.ClickText("row 5: D-4 | 40")
	r.WaitText("New thread")
	r.TypeText("Raise D-4")
	r.WaitText("Raise D-4")
	r.TypeKeys("<CR>")
	in := a.WaitSent(t)
	for _, want := range []string{`where="prices.csv:5"`, "D-4,40", "Raise D-4"} {
		if !strings.Contains(in.Text, want) {
			t.Errorf("the comment reached the agent without %q:\n%s", want, in.Text)
		}
	}
	threadWith(t, r, "Raise D-4")
	r.Run("editor.present", registry.Args{"name": "csv"})
	r.WaitFrame("the new thread between the lines in Edit", func(s string) bool {
		return apptest.HasAll(s, "D-4,40", "· L5 ·", "Raise D-4") && !strings.Contains(s, "row 5:")
	})
}

// A window's presentation comes back after a restart, as its file and cursor do.
func TestPresentationComesBackAfterARestart(t *testing.T) {
	state, dir := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(dir, "notes.md"), []byte(notesMD()), 0o644)
	r := apptest.NewWindow(t, apptest.WindowOptions{State: state, WorkDir: dir})
	r.Run("editor.open", registry.Args{"path": filepath.Join(dir, "notes.md")})
	r.Run("editor.present", registry.Args{"name": "markdown"})
	r.WaitFrame("the preview", func(s string) bool {
		return apptest.HasAll(s, "Intro paragraph.", "Edit") && !strings.Contains(s, "Preview")
	})
	r.Stop()

	r2 := apptest.NewWindow(t, apptest.WindowOptions{State: state, WorkDir: dir})
	r2.WaitFrame("the preview back", func(s string) bool {
		return apptest.HasAll(s, "Intro paragraph.", "Edit") && !strings.Contains(s, "Preview")
	})
}

// oriel.editor.action adds a breadcrumb button whose label follows on(window); here it shows a Lua presenter.
func TestLuaEditorAction(t *testing.T) {
	s := apptest.LuaScene(t, `oriel.editor.presenter{ name = "csv", label = "Table", comments = true,
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
	os.WriteFile(filepath.Join(s.Editor().Dir(), "prices.csv"), []byte("sku,price\nA-1,10\n"), 0o644)
	s.Run("editor.open", registry.Args{"path": "prices.csv"})
	s.Frames(2)
	label := "Table #" + s.EditorWindow().ID
	s.Click(s.TextPoint(label), 0)
	s.Frames(3)
	text := s.Text()
	if !strings.Contains(text, "A-1  │  10") || !strings.Contains(text, "Edit #") || strings.Contains(text, "A-1,10") {
		t.Errorf("the Lua action didn't present the file, or its label didn't follow:\n%s", text)
	}
}
