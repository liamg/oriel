package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// tableCase is markdown and the blocks it parses to, a table's as its rows and aligns.
type tableCase struct {
	name, src string
	want      []mdBlock
}

func checkBlocks(t *testing.T, c tableCase) {
	t.Helper()
	got := mdParse(c.src)
	if len(got) != len(c.want) {
		t.Fatalf("%s: %d blocks: %+v", c.name, len(got), got)
	}
	for i, w := range c.want {
		g := got[i]
		if g.Kind != w.Kind || g.Text != w.Text || g.Line != w.Line || g.End != w.End || fmt.Sprint(g.Rows) != fmt.Sprint(w.Rows) ||
			fmt.Sprint(g.Aligns) != fmt.Sprint(w.Aligns) {
			t.Errorf("%s: block %d = %+v, want %+v", c.name, i, g, w)
		}
	}
}

func TestMarkdownTables(t *testing.T) {
	for _, c := range []tableCase{
		{
			// ref:   gfm/spec.md#Tables (extension)
			// quote: "A [table](@) is an arrangement of data with rows and columns, consisting of a single header row, a [delimiter row] separating the header from the data, and zero or more data rows."
			name: "header, delimiter and a data row",
			src:  "| foo | bar |\n| --- | --- |\n| baz | bim |",
			want: []mdBlock{{Kind: mdTable, Rows: [][]string{{"foo", "bar"}, {"baz", "bim"}}, Aligns: []string{"", ""}, Line: 1, End: 3}},
		},
		{
			// ref:   gfm/spec.md#Tables (extension)
			// quote: "and optionally, a leading or trailing colon (`:`), or both, to indicate left, right, or center alignment respectively."
			name: "alignment, with the pipes at the ends left out",
			src:  "| abc | defghi |\n:-: | -----------:\nbar | baz",
			want: []mdBlock{{Kind: mdTable, Rows: [][]string{{"abc", "defghi"}, {"bar", "baz"}}, Aligns: []string{"center", "right"}, Line: 1, End: 3}},
		},
		{
			name: "left alignment",
			src:  "a | b\n:-- | ---",
			want: []mdBlock{{Kind: mdTable, Rows: [][]string{{"a", "b"}}, Aligns: []string{"left", ""}, Line: 1, End: 2}},
		},
		{
			// ref:   gfm/spec.md#Tables (extension)
			// quote: "Include a pipe in a cell's content by escaping it, including inside other inline spans"
			name: "escaped pipes",
			src:  "| f\\|oo  |\n| ------ |\n| b `\\|` az |\n| b **\\|** im |",
			want: []mdBlock{{Kind: mdTable, Rows: [][]string{{"f|oo"}, {"b `|` az"}, {"b **|** im"}}, Aligns: []string{""}, Line: 1, End: 4}},
		},
		{
			// ref:   gfm/spec.md#Tables (extension)
			// quote: "The table is broken at the first empty line, or beginning of another block-level structure"
			name: "broken by another block",
			src:  "| abc | def |\n| --- | --- |\n| bar | baz |\n# bar",
			want: []mdBlock{{Kind: mdTable, Rows: [][]string{{"abc", "def"}, {"bar", "baz"}}, Aligns: []string{"", ""}, Line: 1, End: 3},
				{Kind: mdHeading, Text: "bar", Line: 4, End: 4}},
		},
		{
			name: "broken by a list item",
			src:  "| abc | def |\n| --- | --- |\n- bar",
			want: []mdBlock{{Kind: mdTable, Rows: [][]string{{"abc", "def"}}, Aligns: []string{"", ""}, Line: 1, End: 2},
				{Kind: mdItem, Text: "bar", Line: 3, End: 3}},
		},
		{
			// ref:   gfm/spec.md#Tables (extension)
			// quote: "The table is broken at the first empty line, or beginning of another block-level structure"
			name: "a line of text is a row until the empty line",
			src:  "| abc | def |\n| --- | --- |\n| bar | baz |\nbar\n\nbar",
			want: []mdBlock{{Kind: mdTable, Rows: [][]string{{"abc", "def"}, {"bar", "baz"}, {"bar", ""}}, Aligns: []string{"", ""}, Line: 1, End: 4},
				{Kind: mdPara, Text: "bar", Line: 6, End: 6}},
		},
		{
			// ref:   gfm/spec.md#Tables (extension)
			// quote: "The header row must match the [delimiter row] in the number of cells.  If not, a table will not be recognized"
			name: "a header that doesn't match its delimiter row",
			src:  "| abc | def |\n| --- |\n| bar |",
			want: []mdBlock{{Kind: mdPara, Text: "| abc | def | | --- | | bar |", Line: 1, End: 3}},
		},
		{
			name: "pipes with no delimiter row",
			src:  "a | b\nc | d",
			want: []mdBlock{{Kind: mdPara, Text: "a | b c | d", Line: 1, End: 2}},
		},
		{
			// ref:   gfm/spec.md#Tables (extension)
			// quote: "If there are a number of cells fewer than the number of cells in the header row, empty cells are inserted.  If there are greater, the excess is ignored"
			name: "short and long rows",
			src:  "| abc | def |\n| --- | --- |\n| bar |\n| bar | baz | boo |",
			want: []mdBlock{{Kind: mdTable, Rows: [][]string{{"abc", "def"}, {"bar", ""}, {"bar", "baz"}}, Aligns: []string{"", ""}, Line: 1, End: 4}},
		},
		{
			// ref:   gfm/spec.md#Tables (extension)
			// quote: "If there are no rows in the body, no `<tbody>` is generated in HTML output"
			name: "no body",
			src:  "| abc | def |\n| --- | --- |",
			want: []mdBlock{{Kind: mdTable, Rows: [][]string{{"abc", "def"}}, Aligns: []string{"", ""}, Line: 1, End: 2}},
		},
		{
			// ref:   gfm/spec.md#Paragraphs
			// quote: "A sequence of non-blank lines that cannot be interpreted as other kinds of blocks forms a [paragraph](@)."
			name: "a table straight after a paragraph's line",
			src:  "Costs:\n| plan | price |\n| --- | ---: |\n| team | $120 |\n\nAfter.",
			want: []mdBlock{{Kind: mdPara, Text: "Costs:", Line: 1, End: 1},
				{Kind: mdTable, Rows: [][]string{{"plan", "price"}, {"team", "$120"}}, Aligns: []string{"", "right"}, Line: 2, End: 4},
				{Kind: mdPara, Text: "After.", Line: 6, End: 6}},
		},
		{
			name: "a rule isn't a delimiter row",
			src:  "Some text\n---\nmore",
			want: []mdBlock{{Kind: mdPara, Text: "Some text", Line: 1, End: 1}, {Kind: mdRule, Line: 2, End: 2}, {Kind: mdPara, Text: "more", Line: 3, End: 3}},
		},
		{
			name: "inline HTML in a cell",
			src:  "| a | b |\n|---|---|\n| <b>x</b> &amp; y | one<br>two |",
			want: []mdBlock{{Kind: mdTable, Rows: [][]string{{"a", "b"}, {"**x** & y", "one two"}}, Aligns: []string{"", ""}, Line: 1, End: 3}},
		},
	} {
		checkBlocks(t, c)
	}
}

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
	s.run("editor.open", registry.Args{"path": "modes.md"})
	s.frames(3)
	s.click(s.textPoint(t, "Preview"))
	s.frames(4)
	text := s.frameText()
	for _, want := range []string{"Mode", "Rounds", "Used by", "Notes", "HalfEven", "Stripe, finance", "Biased upwards:", "avoid", "After the table.", "Keep", "yes"} {
		if !strings.Contains(text, want) {
			t.Errorf("the preview lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "| Mode") || strings.Contains(text, "---") {
		t.Errorf("the preview shows the table's source:\n%s", text)
	}
	s.lint("a markdown table")
	s.golden("markdown-table")
	head, _ := s.runStarting("Mode")
	row, ok1 := s.runStarting("HalfEven")
	card, ok2 := s.runStarting("· L7 ·")
	if !ok1 || !ok2 || card.X <= row.X || abs32(card.Y-row.Y) > 12 || abs32(card.Y-head.Y) < 20 {
		t.Errorf("the thread on L7 isn't level with its row: header %v, row %v, card %v", head, row, card)
	}
	if n, ok := s.runAt("7", row.Y); !ok || n.X >= row.X {
		t.Errorf("no line number beside the row the thread is on: %v %v", n, ok)
	}
}

func abs32(v float32) float32 { return max(v, -v) }

// runAt is where a run reading text is drawn level (within a few pixels) with y.
func (s *errScene) runAt(text string, y float32) (render.Point, bool) {
	for _, op := range s.list.Ops {
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
	s.run("editor.open", registry.Args{"path": "modes.md"})
	s.frames(3)
	s.click(s.textPoint(t, "Preview"))
	s.frames(4)
	s.click(s.textPoint(t, "the CSV export"))
	s.frames(3)
	if text := s.frameText(); !strings.Contains(text, "New thread") || !strings.Contains(text, "· L8\n") {
		t.Fatalf("a click on the row on line 8 didn't open a thread on it:\n%s", text)
	}
	row, _ := s.runStarting("HalfUp")
	if card, ok := s.runStarting("· L8"); !ok || abs32(card.Y-row.Y) > 12 {
		t.Errorf("the new thread isn't level with its row: row %v, card %v", row, card)
	}
	s.golden("markdown-table-comment")
	s.keyCommand(t, "<Esc>")
	s.frames(2)
	s.click(s.textPoint(t, "Mode"))
	s.frames(3)
	if text := s.frameText(); !strings.Contains(text, "· L5\n") {
		t.Fatalf("a click on the header didn't open a thread on its line:\n%s", text)
	}
	s.keyCommand(t, "<Esc>")
	s.frames(2)

	// j/k step through the rows: the title, the intro, the header, then each row; the delimiter's line is skipped.
	s.click(s.textPoint(t, "After the table."))
	s.frames(2)
	s.keyCommand(t, "<Esc>")
	s.frames(2)
	for _, k := range []string{"k", "k"} {
		s.keyCommand(t, k)
	}
	s.keyCommand(t, "c")
	s.frames(2)
	if text := s.frameText(); !strings.Contains(text, "· L8\n") {
		t.Errorf("k twice from the paragraph after the table and c didn't open a thread on line 8:\n%s", text)
	}
	s.keyCommand(t, "<Esc>")
	s.frames(2)
	for range 2 {
		s.keyCommand(t, "k")
	}
	s.keyCommand(t, "c")
	s.frames(2)
	if text := s.frameText(); !strings.Contains(text, "· L5\n") {
		t.Errorf("k twice more and c didn't open a thread on the header's line 5:\n%s", text)
	}
}

// A Lua presenter can draw a file as one block with a part per row (a node's lines): a thread on a row's line sits
// beside that row, and a click on a row comments on its line.
func TestLuaPresenterParts(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "prices.csv"), []byte("sku,price\nA-1,10\nB-2,20\nC-3,30\nD-4,40\n"), 0o644)
	s := threadsScene(t, dir, [4]string{"prices.csv", "4", "C-3,30", "Is C-3 right?"})
	s.run("lua", registry.Args{"args": `oriel.editor.presenter{ name = "csv", label = "Table", comments = true, ` +
		`when = function(p) return p:match("%.csv$") ~= nil end, ` +
		`build = function(ctx) ctx.block(1, #ctx.lines, function() local rows = {} ` +
		`for i, l in ipairs(ctx.lines) do local r = oriel.ui.row({ oriel.ui.text("row " .. i .. ": " .. l) }); r.lines = { i, i }; rows[#rows + 1] = r end ` +
		`return oriel.ui.column(rows) end) end }`})
	s.run("editor.open", registry.Args{"path": "prices.csv"})
	s.frames(2)
	s.run("editor.present", registry.Args{"name": "csv"})
	s.frames(4)
	row, ok1 := s.runStarting("row 4: C-3,30")
	card, ok2 := s.runStarting("· L4 ·")
	first, _ := s.runStarting("row 1:")
	if !ok1 || !ok2 || abs32(card.Y-row.Y) > 12 || abs32(card.Y-first.Y) < 20 {
		t.Errorf("the thread on L4 isn't level with its row: row 1 %v, row 4 %v, card %v", first, row, card)
	}
	s.click(s.textPoint(t, "row 5: D-4,40"))
	s.frames(3)
	if text := s.frameText(); !strings.Contains(text, "New thread") || !strings.Contains(text, "· L5\n") {
		t.Errorf("a click on row 5 didn't open a thread on line 5:\n%s", text)
	}
}
