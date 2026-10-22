package editor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/editor"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// diagramsMD is a design doc with a flowchart, a screenshot, a sequence diagram, and two fences that aren't drawn: a
// gantt chart and a flowchart with a mistake in it.
const diagramsMD = "# Rounding\n\n## Flow\n\n" +
	"```mermaid\nflowchart TD\n  A[Usage meter] --> B{Tiered plan?}\n  B -->|Yes| C[Round each tier]\n  B -->|No| D[Round the line]\n" +
	"  C --> E[(Invoice)]\n  D --> E\n```\n\n![The invoice screen](shot.png)\n\n## Delivery\n\n" +
	"```mermaid\nsequenceDiagram\n  actor U as Finance\n  participant B as Billing\n  U->>+B: Close the month\n  loop Each invoice\n    B->>B: Round lines\n  end\n" +
	"  Note right of B: half-even\n  B-->>-U: Totals\n```\n\n## Later\n\n" +
	"```mermaid\ngantt\n  title Rollout\n  Ship :a1, 2026-11-01, 3d\n```\n\n" +
	"```mermaid\nflowchart LR\n  A --> \n```\n"

func diagramsScene(t *testing.T) *apptest.Scene {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "DESIGN.md"), []byte(diagramsMD), 0o644)
	os.WriteFile(filepath.Join(dir, "shot.png"), editor.PNGBytes(t, photo(360, 120)), 0o644)
	s := threadsScene(t, dir, [4]string{"DESIGN.md", "5", "```mermaid", "Should tiers round first?"})
	s.Run("editor.open", registry.Args{"path": "DESIGN.md"})
	s.Frames(3)
	s.Run("MarkdownPreview", nil)
	s.Frames(4)
	return s
}

// Mermaid diagrams in markdown are drawn in place: a mermaid flowchart and sequence diagram are
// drawn as diagrams, scaled to the column; a type that isn't drawn, or one that doesn't parse, stays code and says so.
func TestMarkdownPreviewDiagrams(t *testing.T) {
	s := diagramsScene(t)
	s.Lint("a markdown preview with diagrams")
	s.Golden("markdown-diagrams")
	for _, want := range []string{"MERMAID · FLOWCHART", "Usage meter", "Tiered plan?", "Yes", "Round each tier", "Invoice",
		"MERMAID · SEQUENCE", "Finance", "Billing", "Close the month", "LOOP", "[Each invoice]", "half-even",
		"MERMAID · GANTT ISN'T DRAWN", "title Rollout", "MERMAID · CAN'T READ LINE 2", "Should tiers round first?"} {
		if !drew(s, want) {
			t.Errorf("the preview lacks %q:\n%s", want, s.Text())
		}
	}
	if drew(s, "flowchart TD") || drew(s, "sequenceDiagram") {
		t.Errorf("a drawn diagram's source shows:\n%s", s.Text())
	}
	flow := s.NodeRole("image", "Flowchart: Usage meter, Tiered plan?, Round each tier, Round the line, Invoice")
	seq := s.NodeRole("image", "Sequence diagram: Finance, Billing")
	heading, _ := s.TextStart("Flow")
	if flow.Bounds.Min.X < heading.X-30 || flow.Bounds.Dx() > 700 || seq.Bounds.Min.Y <= flow.Bounds.Max.Y {
		t.Errorf("the diagrams are at %v and %v, the column at %v", flow.Bounds, seq.Bounds, heading)
	}
	at := apptest.Centre(flow.Bounds)
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: at})
	s.Frame()
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerScroll, Pos: at, Scroll: render.Point{Y: 640}})
	s.Frames(2)
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: 1200, Y: 900}})
	s.Frames(2)
	s.Lint("the preview scrolled to its sequence diagram")
	s.Golden("markdown-diagrams-sequence")
}

// A diagram is zoomable like an image, in its document's lightbox group with the images.
func TestMarkdownDiagramOpensInTheLightbox(t *testing.T) {
	s := diagramsScene(t)
	s.Click(apptest.Centre(s.NodeRole("image", "Flowchart: Usage meter, Tiered plan?, Round each tier, Round the line, Invoice").Bounds), 0)
	s.Frames(2)
	if !drew(s, "1 / 3") || !drew(s, "Flowchart: Usage meter") {
		t.Fatalf("a click on the flowchart doesn't show it in the document's lightbox group:\n%s", s.Text())
	}
	s.Golden("markdown-diagram-lightbox")
	s.Key("Right")
	s.Frames(2)
	if !drew(s, "2 / 3") || !drew(s, "360×120") {
		t.Errorf("→ doesn't go to the screenshot:\n%s", s.Text())
	}
	s.Key("Right")
	s.Frames(2)
	if !drew(s, "3 / 3") || !drew(s, "Sequence diagram: Finance, Billing") {
		t.Errorf("→ doesn't go to the sequence diagram:\n%s", s.Text())
	}
	s.Key("Esc")
	s.Frames(2)
	if drew(s, "3 / 3") {
		t.Error("Escape leaves the lightbox up")
	}
}

// A thread's comment draws its markdown compactly, diagrams included.
func TestThreadMarkdownDiagram(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	s := threadsScene(t, dir, [4]string{"main.go", "3", "func main() {}", "How it runs:\n\n```mermaid\ngraph LR\n  main --> serve --> done\n```"})
	s.Run("editor.open", registry.Args{"path": "main.go"})
	s.Frames(4)
	if !drew(s, "serve") || drew(s, "graph LR") {
		t.Errorf("the thread doesn't draw its diagram:\n%s", s.Text())
	}
	if !strings.Contains(s.Text(), "How it runs:") {
		t.Errorf("the thread lacks its text:\n%s", s.Text())
	}
}

// Text a diagram has to cut short (a word too long for any box) shows in full in a tooltip under the pointer.
func TestMarkdownDiagramTruncatedTooltip(t *testing.T) {
	long := "pricing.RoundingModeHalfEvenWithBankersTiesAppliedPerInvoiceLineBeforeTheTotal"
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "DESIGN.md"), []byte("# Rounding\n\n```mermaid\nflowchart TD\n  meter --> r[\""+long+"\"] --> invoice\n```\n"), 0o644)
	s := threadsScene(t, dir)
	s.Run("editor.open", registry.Args{"path": "DESIGN.md"})
	s.Frames(3)
	s.Run("MarkdownPreview", nil)
	s.Frames(4)
	at, ok := s.TextStart("pricing.Rounding")
	if !ok || !drew(s, "…") {
		t.Fatalf("the long label isn't drawn cut short:\n%s", s.Text())
	}
	if drew(s, long) {
		t.Fatal("the long label is drawn whole before any hover")
	}
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: at.X + 20, Y: at.Y - 4}})
	s.Frame()
	s.Now = s.Now.Add(time.Second)
	s.Frames(2)
	if !drew(s, long) {
		t.Errorf("hovering the cut label shows no tooltip with it whole:\n%s", s.Text())
	}
}
