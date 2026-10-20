package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func diagramsScene(t *testing.T) *errScene {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "DESIGN.md"), []byte(diagramsMD), 0o644)
	os.WriteFile(filepath.Join(dir, "shot.png"), pngBytes(t, photo(360, 120)), 0o644)
	s := threadsScene(t, dir, [4]string{"DESIGN.md", "5", "```mermaid", "Should tiers round first?"})
	s.run("editor.open", registry.Args{"path": "DESIGN.md"})
	s.frames(3)
	s.run("MarkdownPreview", nil)
	s.frames(4)
	return s
}

// editor.md, "Images and mermaid diagrams in markdown, drawn in place": a mermaid flowchart and sequence diagram are
// drawn as diagrams, scaled to the column; a type that isn't drawn, or one that doesn't parse, stays code and says so.
func TestMarkdownPreviewDiagrams(t *testing.T) {
	s := diagramsScene(t)
	s.lint("a markdown preview with diagrams")
	s.golden("markdown-diagrams")
	for _, want := range []string{"MERMAID · FLOWCHART", "Usage meter", "Tiered plan?", "Yes", "Round each tier", "Invoice",
		"MERMAID · SEQUENCE", "Finance", "Billing", "Close the month", "LOOP", "[Each invoice]", "half-even",
		"MERMAID · GANTT ISN'T DRAWN", "title Rollout", "MERMAID · CAN'T READ LINE 2", "Should tiers round first?"} {
		if !s.drew(want) {
			t.Errorf("the preview lacks %q:\n%s", want, s.frameText())
		}
	}
	if s.drew("flowchart TD") || s.drew("sequenceDiagram") {
		t.Errorf("a drawn diagram's source shows:\n%s", s.frameText())
	}
	flow := s.accessRole(t, "image", "Flowchart: Usage meter, Tiered plan?, Round each tier, Round the line, Invoice")
	seq := s.accessRole(t, "image", "Sequence diagram: Finance, Billing")
	heading, _ := s.runStarting("Flow")
	if flow.Bounds.Min.X < heading.X-30 || flow.Bounds.Dx() > 700 || seq.Bounds.Min.Y <= flow.Bounds.Max.Y {
		t.Errorf("the diagrams are at %v and %v, the column at %v", flow.Bounds, seq.Bounds, heading)
	}
	at := centre(flow.Bounds)
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: at})
	s.frame()
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerScroll, Pos: at, Scroll: render.Point{Y: 640}})
	s.frames(2)
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: 1200, Y: 900}})
	s.frames(2)
	s.lint("the preview scrolled to its sequence diagram")
	s.golden("markdown-diagrams-sequence")
}

// rendering.md, "Images": a diagram is zoomable like an image, in its document's lightbox group with the images.
func TestMarkdownDiagramOpensInTheLightbox(t *testing.T) {
	s := diagramsScene(t)
	s.click(centre(s.accessRole(t, "image", "Flowchart: Usage meter, Tiered plan?, Round each tier, Round the line, Invoice").Bounds))
	s.frames(2)
	if !s.drew("1 / 3") || !s.drew("Flowchart: Usage meter") {
		t.Fatalf("a click on the flowchart doesn't show it in the document's lightbox group:\n%s", s.frameText())
	}
	s.golden("markdown-diagram-lightbox")
	s.Key("Right")
	s.frames(2)
	if !s.drew("2 / 3") || !s.drew("360×120") {
		t.Errorf("→ doesn't go to the screenshot:\n%s", s.frameText())
	}
	s.Key("Right")
	s.frames(2)
	if !s.drew("3 / 3") || !s.drew("Sequence diagram: Finance, Billing") {
		t.Errorf("→ doesn't go to the sequence diagram:\n%s", s.frameText())
	}
	s.Key("Esc")
	s.frames(2)
	if s.drew("3 / 3") {
		t.Error("Escape leaves the lightbox up")
	}
}

// A thread's comment draws its markdown compactly, diagrams included.
func TestThreadMarkdownDiagram(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	s := threadsScene(t, dir, [4]string{"main.go", "3", "func main() {}", "How it runs:\n\n```mermaid\ngraph LR\n  main --> serve --> done\n```"})
	s.run("editor.open", registry.Args{"path": "main.go"})
	s.frames(4)
	if !s.drew("serve") || s.drew("graph LR") {
		t.Errorf("the thread doesn't draw its diagram:\n%s", s.frameText())
	}
	if !strings.Contains(s.frameText(), "How it runs:") {
		t.Errorf("the thread lacks its text:\n%s", s.frameText())
	}
}

// Text a diagram has to cut short (a word too long for any box) shows in full in a tooltip under the pointer.
func TestMarkdownDiagramTruncatedTooltip(t *testing.T) {
	long := "pricing.RoundingModeHalfEvenWithBankersTiesAppliedPerInvoiceLineBeforeTheTotal"
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "DESIGN.md"), []byte("# Rounding\n\n```mermaid\nflowchart TD\n  meter --> r[\""+long+"\"] --> invoice\n```\n"), 0o644)
	s := threadsScene(t, dir)
	s.run("editor.open", registry.Args{"path": "DESIGN.md"})
	s.frames(3)
	s.run("MarkdownPreview", nil)
	s.frames(4)
	at, ok := s.runStarting("pricing.Rounding")
	if !ok || !s.drew("…") {
		t.Fatalf("the long label isn't drawn cut short:\n%s", s.frameText())
	}
	if s.drew(long) {
		t.Fatal("the long label is drawn whole before any hover")
	}
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: at.X + 20, Y: at.Y - 4}})
	s.frame()
	s.now = s.now.Add(time.Second)
	s.frames(2)
	if !s.drew(long) {
		t.Errorf("hovering the cut label shows no tooltip with it whole:\n%s", s.frameText())
	}
}
