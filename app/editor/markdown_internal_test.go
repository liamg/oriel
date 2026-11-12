package editor

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/ui"
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
			want: []mdBlock{{Kind: mdPara, Text: "| abc | def |\n| --- |\n| bar |", Line: 1, End: 3}},
		},
		{
			name: "pipes with no delimiter row",
			src:  "a | b\nc | d",
			want: []mdBlock{{Kind: mdPara, Text: "a | b\nc | d", Line: 1, End: 2}},
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

// mdParse keeps each block's source lines, so a thread on a line finds its block: a paragraph runs to its last line
// (its newlines kept, for a comment's line breaks), a list item takes its continuation lines, and a fence its body; a
// fence indented under a list item is the item's, its lines out of the fence's indent.
//
// ref:   gfm/spec.md#Fenced code blocks
// quote: "If the leading code fence is indented N spaces, then up to N spaces of indentation are removed from each line of the content (if present)."
func TestMarkdownBlocksKeepTheirLines(t *testing.T) {
	src := "# Title\n\nFirst line\nsecond line.\n\n- one\n  more\n- two\n\n```mermaid\nflowchart LR\n```\n---\n- three:\n  ```go\n    x := 1\n  y\n  ```\n"
	want := []mdBlock{
		{Kind: mdHeading, Level: 1, Text: "Title", Line: 1, End: 1},
		{Kind: mdPara, Text: "First line\nsecond line.", Line: 3, End: 4},
		{Kind: mdItem, Mark: "•", Text: "one\nmore", Line: 6, End: 7},
		{Kind: mdItem, Mark: "•", Text: "two", Line: 8, End: 8},
		{Kind: mdCode, Lang: "mermaid", Code: []string{"flowchart LR"}, Line: 10, End: 12},
		{Kind: mdRule, Line: 13, End: 13},
		{Kind: mdItem, Mark: "•", Text: "three:", Line: 14, End: 14},
		{Kind: mdCode, Level: 1, Lang: "go", Code: []string{"  x := 1", "y"}, Line: 15, End: 18},
	}
	got := mdParse(src)
	if len(got) != len(want) {
		t.Fatalf("%d blocks: %+v", len(got), got)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Kind != w.Kind || g.Text != w.Text || g.Level != w.Level || g.Mark != w.Mark || g.Lang != w.Lang || strings.Join(g.Code, "\n") != strings.Join(w.Code, "\n") ||
			g.Line != w.Line || g.End != w.End {
			t.Errorf("block %d = %+v, want %+v", i, g, w)
		}
	}
}

const readmeMD = `<p align="center">
  <img src="logo.png" alt="Logo" width="120">
</p>

<h1 align="center">Oriel</h1>

Badges: [![CI](https://example.com/ci.svg)](https://example.com) ![Shot](shot.png "a title")

Press <kbd>Ctrl</kbd>+<b>S</b> to save.<br>Done &amp; dusted, ` + "`<div>`" + ` kept.

<!-- a comment

over lines -->

<details>
<summary>More</summary>

Hidden body.

</details>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="dark.png">
  <img src="light.png" alt="Diagram" width="50%">
</picture>

<sub>small print</sub> and <span class="x">unknown</span> tags.
`

// md, "Images": "`![alt](src)` (inside a link too) and the HTML GitHub renders in a README. A line starting
// with a block tag […] or a comment starts an HTML block, up to a blank line: its headings, paragraphs and list items
// become blocks, `align=\"center\"` or `\"right\"` aligns them and their images, `<img src alt width height>` is an
// image (a `<picture>`'s `<img>` stands for its `<source>`s), `<br>` breaks the paragraph, and a `<summary>` is a bold
// \"▸\" line over its body. Inline, `<b>`/`<strong>` are bold, `<code>`/`<kbd>` code, `<a>`, `<sub>`, `<sup>`, `<i>`
// and tags it doesn't know are their text, and comments are hidden. Entities are decoded."
func TestMarkdownImagesAndHTML(t *testing.T) {
	type got struct {
		Kind   mdKind
		Level  int
		Text   string
		Align  string
		Images []mdImage
	}
	var bs []got
	for _, b := range mdParse(readmeMD) {
		bs = append(bs, got{b.Kind, b.Level, b.Text, b.Align, b.Images})
	}
	want := []got{
		{Kind: mdPara, Align: "center", Images: []mdImage{{Src: "logo.png", Alt: "Logo", W: 120}}},
		{Kind: mdHeading, Level: 1, Text: "Oriel", Align: "center"},
		{Kind: mdPara, Text: "Badges:"}, // the images after it, where they're written
		{Kind: mdPara, Images: []mdImage{{Src: "https://example.com/ci.svg", Alt: "CI"}, {Src: "shot.png", Alt: "Shot"}}},
		{Kind: mdPara, Text: "Press `Ctrl`+**S** to save. Done & dusted, `<div>` kept."},
		{Kind: mdPara, Text: "**▸ More**"},
		{Kind: mdPara, Text: "Hidden body."},
		{Kind: mdPara, Images: []mdImage{{Src: "light.png", Alt: "Diagram", WPct: .5}}},
		{Kind: mdPara, Text: "small print and unknown tags."},
	}
	if !reflect.DeepEqual(bs, want) {
		t.Errorf("blocks:\n%+v\nwant\n%+v", bs, want)
	}
	for _, b := range bs {
		if strings.Contains(b.Text, "comment") || strings.Contains(b.Text, "<") && !strings.Contains(b.Text, "`<div>`") {
			t.Errorf("a block shows HTML: %q", b.Text)
		}
	}
}

func pngBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func solidImage(w, h int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

// md, "Images": "A relative `src` is found from the markdown file's folder (a thread's: its project's), and a
// `data:` URI is decoded. A remote (`http(s)`) image is fetched in the background […]. An SVG is drawn as vectors
// […]. A missing file or one that won't decode is a chip with its alt text and source, its tooltip saying why."
func TestMarkdownPictureSources(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "docs"), 0o755)
	os.WriteFile(filepath.Join(dir, "docs", "a b.png"), pngBytes(t, solidImage(30, 20, color.NRGBA{R: 255, A: 255})), 0o644)
	os.WriteFile(filepath.Join(dir, "logo.svg"), []byte(testBadge), 0o644)
	os.WriteFile(filepath.Join(dir, "broken.svg"), []byte("<svg width="), 0o644)
	data := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes(t, solidImage(4, 3, color.NRGBA{G: 255, A: 255})))
	svgData := "data:image/svg+xml;utf8," + url.PathEscape(testBadge)
	for _, c := range []struct {
		src, dir string
		w        float32
		why      string
	}{
		{"docs/a%20b.png", dir, 30, ""},
		{"./docs/a b.png?raw=true", dir, 30, ""},
		{data, "", 4, ""},
		{"logo.svg", dir, 90, ""},
		{svgData, "", 90, ""},
		{"docs/a b.png", "", 0, "There's no folder to find docs/a b.png in"},
	} {
		pc, why := settledPicture(testPictures(t, offlineNet(t)), c.src, c.dir)
		var w float32
		switch {
		case pc.Img != nil:
			w = float32(pc.Img.Bounds().Dx())
		case pc.Doc != nil:
			w = pc.Doc.Size.X
		}
		if w != c.w || why != c.why {
			t.Errorf("%.40s: %v wide, why %q; want %v, %q", c.src, w, why, c.w, c.why)
		}
	}
	for _, src := range []string{filepath.Join(dir, "nope.png"), "broken.svg"} {
		if pc, why := settledPicture(testPictures(t, offlineNet(t)), src, dir); why == "" || pc.Img != nil || pc.Doc != nil {
			t.Errorf("%s has no reason", src)
		}
	}
}

// settledPicture is mdPicture once its read is in: asked again until it isn't loading.
func settledPicture(pics *kit.Pictures, src, dir string) (pc *kit.Picture, why string) {
	for range 5000 {
		if pc, why = mdPicture(pics, src, dir); !pc.Loading {
			break
		}
		time.Sleep(time.Millisecond)
	}
	return pc, why
}

// testBadge is a 90×20 badge in shields.io's flat style.
const testBadge = `<svg xmlns="http://www.w3.org/2000/svg" width="90" height="20"><linearGradient id="s" x2="0" y2="100%"><stop offset="0" stop-color="#bbb" stop-opacity=".1"/><stop offset="1" stop-opacity=".1"/></linearGradient><clipPath id="r"><rect width="90" height="20" rx="3" fill="#fff"/></clipPath><g clip-path="url(#r)"><rect width="51" height="20" fill="#555"/><rect x="51" width="39" height="20" fill="#007ec6"/><rect width="90" height="20" fill="url(#s)"/></g><g fill="#fff" text-anchor="middle" font-family="Verdana,sans-serif" font-size="110"><text x="265" y="140" transform="scale(.1)" textLength="410">license</text><text x="695" y="140" transform="scale(.1)" textLength="290">MIT</text></g></svg>`

// testPictures is a window's pictures, fetched with client, until the test ends.
func testPictures(t *testing.T, client *http.Client) *kit.Pictures {
	p := kit.NewPictures(client, nil)
	t.Cleanup(p.Close)
	return p
}

// offlineNet is a client that reaches nothing: the pictures here are files and data: URIs.
func offlineNet(*testing.T) *http.Client {
	return &http.Client{Transport: offline{}}
}

type offline struct{}

func (offline) RoundTrip(r *http.Request) (*http.Response, error) {
	return nil, errors.New("tests don't reach the network: " + r.URL.String())
}

// A markdown source drawn frame after frame is parsed once.
func TestMarkdownParsedOnce(t *testing.T) {
	ts, err := typeset.NewBundled()
	if err != nil {
		t.Fatal(err)
	}
	u := ui.New(ts, ui.TextStyle{})
	const src = "# Title\n\nSome **bold** text.\n"
	var got [][]mdBlock
	for range 2 {
		u.Layout(render.Point{X: 400, Y: 300}, func(c *ui.Ctx) { got = append(got, mdParsed(c, src)) })
	}
	if len(got[0]) == 0 || &got[0][0] != &got[1][0] {
		t.Error("the same source was parsed again on the next frame")
	}
}

// An image is drawn where it's written in its paragraph, not above the paragraph's text. Images are inlines, and
// spec/gfm/spec.md, "Inlines": "Inlines are parsed sequentially from the beginning of the character stream to the end
// (left to right, in left-to-right languages)." A line that's only images is a block of its own, on its line.
func TestMarkdownImagesStayWhereTheyreWritten(t *testing.T) {
	type got struct {
		Text      string
		Images    []string
		Line, End int
	}
	for _, c := range []struct {
		name, src string
		want      []got
	}{
		{"a line of its own", "Before the shot:\n![shot](a.png)\nand after it.", []got{
			{Text: "Before the shot:", Line: 1, End: 1}, {Images: []string{"a.png"}, Line: 2, End: 2}, {Text: "and after it.", Line: 3, End: 3}}},
		{"inside a line", "see ![a](a.png) then ![b](b.png) <img src=\"c.png\"> done", []got{
			{Text: "see", Line: 1, End: 1}, {Images: []string{"a.png"}, Line: 1, End: 1}, {Text: "then", Line: 1, End: 1},
			{Images: []string{"b.png", "c.png"}, Line: 1, End: 1}, {Text: "done", Line: 1, End: 1}}},
		{"at the end", "Look at this\n![x](x.png)", []got{{Text: "Look at this", Line: 1, End: 1}, {Images: []string{"x.png"}, Line: 2, End: 2}}},
		{"only text", "no images\nhere", []got{{Text: "no images\nhere", Line: 1, End: 2}}},
	} {
		var bs []got
		for _, b := range mdParse(c.src) {
			g := got{Text: b.Text, Line: b.Line, End: b.End}
			for _, im := range b.Images {
				g.Images = append(g.Images, im.Src)
			}
			bs = append(bs, g)
		}
		if !reflect.DeepEqual(bs, c.want) {
			t.Errorf("%s:\n%+v\nwant\n%+v", c.name, bs, c.want)
		}
	}
}
