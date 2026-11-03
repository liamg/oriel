package editor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

const tableMD = "# Rounding modes\n\nHow each mode rounds a tie, by currency precision.\n\n" +
	"| Mode | Rounds `2.5` to | Used by | Notes |\n" +
	"| :--- | :---: | ---: | --- |\n" +
	"| `HalfEven` | 2 | Stripe, finance | The default: ties go to the even neighbour, so totals don't drift. |\n" +
	"| `HalfUp` | 3 | the CSV export | Biased upwards: **avoid** for totals. |\n" +
	"| `Down` | 2 | — | Truncates; ~~never~~ rarely wanted. |\n\n" +
	"After the table.\n"

// A table in markdown draws as one: in the preview, a block on its lines a thread can sit beside, and in the
// thread's own markdown.
func TestMarkdownTablePreview(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "modes.md"), []byte(tableMD), 0o644)
	s := threadsScene(t, dir, [4]string{"modes.md", "7", "| `HalfEven` | 2 |", "Which do we keep?\n\n| Mode | Keep |\n|---|:-:|\n| `HalfEven` | yes |\n| `HalfUp` | no |"})
	s.Run("editor.open", registry.Args{"path": "modes.md"})
	s.Frames(3)
	s.Click(s.TextPoint("Preview"), 0)
	s.Frames(4)
	text := s.Text()
	for _, want := range []string{"Mode", "Rounds", "Used by", "Notes", "HalfEven", "Stripe, finance", "Biased upwards:", "avoid", "After the table.", "Keep", "yes"} {
		if !strings.Contains(text, want) {
			t.Errorf("the preview lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "| Mode") || strings.Contains(text, "---") {
		t.Errorf("the preview shows the table's source:\n%s", text)
	}
	s.Lint("a markdown table")
	s.Golden("markdown-table")
	head, _ := s.TextStart("Mode")
	row, ok1 := s.TextStart("HalfEven")
	card, ok2 := s.TextStart("· L7 ·")
	if !ok1 || !ok2 || card.X <= row.X || abs32(card.Y-row.Y) > 12 || abs32(card.Y-head.Y) < 20 {
		t.Errorf("the thread on L7 isn't level with its row: header %v, row %v, card %v", head, row, card)
	}
	if n, ok := runAt(s, "7", row.Y); !ok || n.X >= row.X {
		t.Errorf("no line number beside the row the thread is on: %v %v", n, ok)
	}
}

func abs32(v float32) float32 { return max(v, -v) }

// runAt is where a run reading text is drawn level (within a few pixels) with y.
func runAt(s *apptest.Scene, text string, y float32) (render.Point, bool) {
	for _, op := range s.List().Ops {
		if op.Kind == render.OpGlyphs && op.Run.Text == text && abs32(op.At.Y-y) < 8 {
			return op.At, true
		}
	}
	return render.Point{}, false
}

// A table's rows are parts of its block: a click on one, or j/k to it and c, opens a thread on its own line, and the
// delimiter row is no row.
func TestMarkdownTableRowsTakeComments(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "modes.md"), []byte(tableMD), 0o644)
	s := threadsScene(t, dir)
	s.Run("editor.open", registry.Args{"path": "modes.md"})
	s.Frames(3)
	s.Click(s.TextPoint("Preview"), 0)
	s.Frames(4)
	s.Click(s.TextPoint("the CSV export"), 0)
	s.Frames(3)
	if text := s.Text(); !strings.Contains(text, "New thread") || !strings.Contains(text, "· L8\n") {
		t.Fatalf("a click on the row on line 8 didn't open a thread on it:\n%s", text)
	}
	row, _ := s.TextStart("HalfUp")
	if card, ok := s.TextStart("· L8"); !ok || abs32(card.Y-row.Y) > 12 {
		t.Errorf("the new thread isn't level with its row: row %v, card %v", row, card)
	}
	s.Golden("markdown-table-comment")
	s.Press("<Esc>")
	s.Frames(2)
	s.Click(s.TextPoint("Mode"), 0)
	s.Frames(3)
	if text := s.Text(); !strings.Contains(text, "· L5\n") {
		t.Fatalf("a click on the header didn't open a thread on its line:\n%s", text)
	}
	s.Press("<Esc>")
	s.Frames(2)

	// j/k step through the rows: the title, the intro, the header, then each row; the delimiter's line is skipped.
	s.Click(s.TextPoint("After the table."), 0)
	s.Frames(2)
	s.Press("<Esc>")
	s.Frames(2)
	for _, k := range []string{"k", "k"} {
		s.Press(k)
	}
	s.Press("c")
	s.Frames(2)
	if text := s.Text(); !strings.Contains(text, "· L8\n") {
		t.Errorf("k twice from the paragraph after the table and c didn't open a thread on line 8:\n%s", text)
	}
	s.Press("<Esc>")
	s.Frames(2)
	for range 2 {
		s.Press("k")
	}
	s.Press("c")
	s.Frames(2)
	if text := s.Text(); !strings.Contains(text, "· L5\n") {
		t.Errorf("k twice more and c didn't open a thread on the header's line 5:\n%s", text)
	}
}

// A Lua presenter can draw a file as one block with a part per row (a node's lines): a thread on a row's line sits
// beside that row, and a click on a row comments on its line.
func TestLuaPresenterParts(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "prices.csv"), []byte("sku,price\nA-1,10\nB-2,20\nC-3,30\nD-4,40\n"), 0o644)
	s := threadsScene(t, dir, [4]string{"prices.csv", "4", "C-3,30", "Is C-3 right?"})
	s.Run("lua", registry.Args{"args": `oriel.editor.presenter{ name = "csv", label = "Table", comments = true, ` +
		`when = function(p) return p:match("%.csv$") ~= nil end, ` +
		`build = function(ctx) ctx.block(1, #ctx.lines, function() local rows = {} ` +
		`for i, l in ipairs(ctx.lines) do local r = oriel.ui.row({ oriel.ui.text("row " .. i .. ": " .. l) }); r.lines = { i, i }; rows[#rows + 1] = r end ` +
		`return oriel.ui.column(rows) end) end }`})
	s.Run("editor.open", registry.Args{"path": "prices.csv"})
	s.Frames(2)
	s.Run("editor.present", registry.Args{"name": "csv"})
	s.Frames(4)
	row, ok1 := s.TextStart("row 4: C-3,30")
	card, ok2 := s.TextStart("· L4 ·")
	first, _ := s.TextStart("row 1:")
	if !ok1 || !ok2 || abs32(card.Y-row.Y) > 12 || abs32(card.Y-first.Y) < 20 {
		t.Errorf("the thread on L4 isn't level with its row: row 1 %v, row 4 %v, card %v", first, row, card)
	}
	s.Click(s.TextPoint("row 5: D-4,40"), 0)
	s.Frames(3)
	if text := s.Text(); !strings.Contains(text, "New thread") || !strings.Contains(text, "· L5\n") {
		t.Errorf("a click on row 5 didn't open a thread on line 5:\n%s", text)
	}
}
