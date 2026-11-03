package editor_test

import (
	"image"
	"image/draw"
	"os"
	"strings"
	"testing"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/render/snapshot"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// anchoredBlock decorates the start of a line with a block, through an anchor made once on the buffer.
type anchoredBlock struct {
	line   int
	anchor text.AnchorID
	text   string
}

func (a *anchoredBlock) Decorations(ctx uireg.DecorationCtx) []uireg.Decoration {
	if a.anchor == 0 {
		a.anchor = ctx.Buffer.Anchor(ctx.Snap.LineStart(a.line), text.Left)
	}
	return []uireg.Decoration{{Kind: uireg.Block, Anchor: a.anchor, Node: ui.TextNode(a.text)}}
}

// The layout engine places blocks at anchors and survives edits above them.
func TestDecorationBlockFollowsItsAnchor(t *testing.T) {
	s := editorScene(t)
	s.Registry().Decorations("test.thread", &anchoredBlock{line: 45, text: "THREAD ON CASE 0"})
	s.Frame()
	s.Frame()
	after := func() string {
		lines := strings.Split(s.Text(), "\n")
		for i, l := range lines {
			if l == "THREAD ON CASE 0" {
				return lines[i-1]
			}
		}
		return "(no block)"
	}
	if got := after(); got != ":" {
		t.Fatalf("the block follows %q, want the end of line 46 (\"case 0:\")\n%s", got, s.Text())
	}
	keys(s, "ggOone<CR>two<Esc>")
	keys(s, "40Gzt")
	waitHighlights(s)
	if got := after(); got != ":" {
		t.Errorf("after two lines went in above, the block follows %q", got)
	}
	keys(s, "ggdd40Gzt")
	waitHighlights(s)
	if got := after(); got != ":" {
		t.Errorf("after a line came out above, the block follows %q", got)
	}
}

// A provider that panics is turned off, and the editor keeps drawing.
func TestPanickingDecorationProvider(t *testing.T) {
	s := editorScene(t)
	s.Registry().Decorations("test.bad", uireg.DecorationFunc(func(uireg.DecorationCtx) []uireg.Decoration { panic("bad provider") }))
	s.Frame()
	s.Frame()
	if !strings.Contains(s.Text(), "roundHalfEven") {
		t.Error("the editor stopped drawing")
	}
	off := s.Registry().TurnedOff()
	if len(off) != 1 || off[0].Kind != registry.KindDecoration || off[0].ID != "test.bad" {
		t.Errorf("turned off: %+v", off)
	}
}

// mockupDecorations are 01-ide's: the agent's added lines, their note, the open thread on line 46, and staticcheck's
// warning on line 50.
func mockupDecorations(ctx uireg.DecorationCtx) []uireg.Decoration {
	var ds []uireg.Decoration
	for l := 41; l <= 47; l++ {
		ds = append(ds, uireg.Decoration{Kind: uireg.LineBg, Line: l, Style: "diff.add", Edge: "green"})
	}
	ds = append(ds,
		uireg.Decoration{Kind: uireg.VirtualText, Line: 41, Text: "← agent · 2m ago"},
		uireg.Decoration{Kind: uireg.Sign, Line: 45, Style: "status.working"},
		uireg.Decoration{Kind: uireg.Block, Line: 45, Node: threadCard()},
		uireg.Decoration{Kind: uireg.Underline, Line: 49, From: 11, To: 16, Style: "warn"},
		uireg.Decoration{Kind: uireg.VirtualText, Line: 49, Text: "● S1023: redundant return (staticcheck)", Style: "warn"},
	)
	return ds
}

// threadCard is a stand-in for the thread drawn there: a card of the same size and colours.
func threadCard() ui.Node {
	return &ui.Func{Key: "thread", Fn: func(c *ui.Ctx) {
		th := c.Theme()
		body := ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13}, Color: th.C(theme.FgMuted)}
		c.Column("card", ui.Style{W: ui.Flex(1), H: ui.Px(176), Pad: ui.PadXY(14, 12), Gap: 10, Fill: th.C(theme.BgSurface),
			Stroke: th.C(theme.LineStrong), Border: 1, Radius: 8}, func(c *ui.Ctx) {
			c.Text("head", "Thread · L46", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12, Weight: typeset.SemiBold},
				Color: th.C(theme.Fg)})
			c.Text("1", "ties go to even — but what about negative values?", body)
			c.Text("2", "Right — Floor rounds toward −∞. Switching to Truncate with sign handling.", body)
		})
	}}
}

// 01-ide's editor column, decorations and all, against the mockup: the tabs down to the agent dock. The bound is
// just over the current 0.033 and 7.05%; the thread's real contents and the tabs' icons are most of the rest.
func TestDecoratedEditorAgainstTheMockup(t *testing.T) {
	s := editorScene(t)
	s.Registry().Decorations("test.mockup", uireg.DecorationFunc(mockupDecorations))
	s.SettleGit() // the gutter's git marks, which come off the loop
	s.Frame()
	s.Golden("editor-decorated")
	img := raster.Render(s.List(), s.Size(), 1, s.UI().Theme().C(theme.BgBase))
	mock, err := snapshot.ReadPNG("../../testdata/mockups/01-ide.png")
	if err != nil {
		t.Fatal(err)
	}
	want := image.NewRGBA(image.Rect(0, 0, editorColumn.Dx(), editorColumn.Dy()-26))
	draw.Draw(want, want.Bounds(), mock, editorColumn.Min, draw.Src)
	got := image.NewRGBA(want.Bounds())
	draw.Draw(got, got.Bounds(), img, image.Point{}, draw.Src)
	d, side := snapshot.Compare(want, got, 0.1)
	t.Logf("decorated editor column against 01-ide: %s", d)
	if d.Mean > 0.036 || d.Off > 0.075 {
		os.WriteFile("testdata/editor-decorated-mockup.failed.png", encodePNG(side), 0o644)
		t.Errorf("the decorated editor column is %s off the mockup (testdata/editor-decorated-mockup.failed.png)", d)
	}
}

// tallBlock is a 200px block on a line, revealed or not.
type tallBlock struct {
	line   int
	reveal bool
}

func (b *tallBlock) Decorations(ctx uireg.DecorationCtx) []uireg.Decoration {
	return []uireg.Decoration{{Kind: uireg.Block, Line: b.line, Reveal: b.reveal,
		Node: &ui.Func{Key: "tall", Fn: func(c *ui.Ctx) { c.Box("b", ui.Style{W: ui.Flex(1), H: ui.Px(200)}, nil) }}}}
}

// A block that wants revealing scrolls the editor until all of it shows (a references peek near the bottom of the
// view), and blocks in view take rows from the text, so the cursor stays clear of them.
func TestRevealedBlockScrollsIntoView(t *testing.T) {
	s := editorScene(t)
	p := editorOf(s).Current()
	top := p.Top()
	bottom := top + editorOf(s).Rows(p.ID()) - 2
	b := &tallBlock{line: bottom}
	s.Registry().Decorations("test.peek", b)
	s.Frame()
	s.Frame()
	if got := editorOf(s).Current().Top(); got != top {
		t.Fatalf("a block that doesn't ask moved the view from %d to %d", top, got)
	}
	b.reveal = true
	for range 4 {
		s.Frame()
	}
	p = editorOf(s).Current()
	if p.Top() <= top || p.Top() > bottom {
		t.Fatalf("the view's top went from %d to %d; want it down, but not past the block's line %d", top, p.Top(), bottom)
	}
	if editorOf(s).BlockPx(p.ID()) < 199 {
		t.Errorf("blocks took %vpx, want the block's 200", editorOf(s).BlockPx(p.ID()))
	}
}

// twoBlocks are 200px blocks on two lines.
type twoBlocks struct{ a, b int }

func (b *twoBlocks) Decorations(ctx uireg.DecorationCtx) []uireg.Decoration {
	tall := func(key string) ui.Node {
		return &ui.Func{Key: key, Fn: func(c *ui.Ctx) { c.Box("b", ui.Style{W: ui.Flex(1), H: ui.Px(200)}, nil) }}
	}
	return []uireg.Decoration{{Kind: uireg.Block, Line: b.a, Node: tall("a")}, {Kind: uireg.Block, Line: b.b, Node: tall("b")}}
}

// A block past the bottom of the view takes no rows from the text: only what shows of each block does. Otherwise a
// block that the rows it takes push out of view gives them back, and the text below a block in view comes and goes
// every frame.
func TestBlockPastTheBottomKeepsTheRowsSteady(t *testing.T) {
	s := editorScene(t)
	p := editorOf(s).Current()
	top, rows := p.Top(), editorOf(s).Rows(p.ID())
	one := s.Registry().Decorations("test.threads", &twoBlocks{a: top + 2, b: -1})
	for range 4 {
		s.Frame()
	}
	// The second block goes on the last line the first leaves room for: in view, but taking its rows would push it out.
	one.Dispose()
	s.Registry().Decorations("test.threads", &twoBlocks{a: top + 2, b: top + editorOf(s).Rows(p.ID()) - 2})
	for range 4 {
		s.Frame()
	}
	var seen []int
	for range 6 {
		s.Frame()
		seen = append(seen, editorOf(s).Rows(p.ID()))
	}
	for _, n := range seen[1:] {
		if n != seen[0] {
			t.Fatalf("the rows for text go %v frame by frame; want them steady", seen)
		}
	}
	if seen[0] >= rows || seen[0] < 1 {
		t.Errorf("with a block in view the text has %d rows of %d", seen[0], rows)
	}
}
