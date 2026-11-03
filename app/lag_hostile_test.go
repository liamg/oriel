//go:build !race

package app_test

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

func hostileScene(t *testing.T, o apptest.Options) *apptest.Scene {
	apptest.FrameTimed(t)
	if o.Size == (render.Point{}) {
		o.Size = render.Point{X: 1600, Y: 1000}
	}
	return apptest.New(t, o)
}

// A 10,000-character token in the agent console, new each round so its first frame shapes it afresh: that frame
// may cost more but mustn't stall, and the ones after are cheap.
func TestLongTokenInTheConsoleBudget(t *testing.T) {
	s := hostileScene(t, apptest.Options{})
	s.Now = apptest.At(14, 30, 0)
	round := 0
	setup := func() {
		round++
		token := strings.Repeat(fmt.Sprintf("%c", 'A'+round), 10_000)
		se := core.SessionState{ID: "blob", Name: "blob", Kind: core.KindWork, Status: core.StatusWorking,
			Agent: core.AgentState{Provider: "claude", Running: true, Working: true, TurnStarted: s.Now},
			Transcript: []core.Item{
				{ID: "u", Kind: core.ItemUser, Time: s.Now, Text: "Decode this."},
				{ID: "a", Kind: core.ItemAssistant, Time: s.Now, Text: "Here it is: " + token},
				{ID: "b", Kind: core.ItemTool, Tool: "Bash", Title: "Bash base64", Done: true, Output: token, Time: s.Now},
			}}
		s.Apply(core.SessionSynced{Session: se}, core.SessionFocused{ID: se.ID})
	}
	setup()
	s.Run("view.open", registry.Args{"id": kit.AgentConsoleView, "place": "tab", "title": "Agent"})
	s.Frames(3)
	round = 1
	s.HoldsFrameBudget("a 10k-char token in the console", apptest.HostileP99, 200, setup, func(int) { s.Now = s.Now.Add(16 * time.Millisecond) })
}

// Holding ↓ through grep's previews over many files.
func TestGrepPreviewsWhileHoldingDownBudget(t *testing.T) {
	dir := t.TempDir()
	for i := range 200 {
		var b strings.Builder
		fmt.Fprintf(&b, "package p%d\n\n", i)
		for j := range 400 {
			fmt.Fprintf(&b, "func Handle%d_%d(n int) int { return n * %d }\n\n", i, j, j)
		}
		os.MkdirAll(filepath.Join(dir, fmt.Sprintf("p%d", i)), 0o755)
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("p%d", i), "h.go"), []byte(b.String()), 0o644)
	}
	s := hostileScene(t, apptest.Options{WorkDir: dir})
	s.Frame()
	s.Run("picker.open", registry.Args{"source": "grep", "query": "Handle"})
	s.Until("grep's results", func() bool { return strings.Contains(s.Text(), "func Handle") }) // which files fill its 1000 matches varies
	s.HoldsFrameBudget("holding ↓ through grep previews", apptest.HostileP99, 120, func() { s.Type("Up") }, func(int) { s.UI().Key(render.KeyEvent{Key: "Down"}) })
}

// A 12-megapixel PNG while the window is resized.
func TestHugeImageDuringResizeBudget(t *testing.T) {
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 4000, 3000))
	for y := 0; y < 3000; y += 3 {
		for x := 0; x < 4000; x += 3 {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: uint8(x + y), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "photo.png"), buf.Bytes(), 0o644)
	s := hostileScene(t, apptest.Options{WorkDir: dir})
	s.Run("editor.open", registry.Args{"path": "photo.png"})
	s.SettleReads()
	size := s.Size()
	s.HoldsFrameBudget("a 12MP PNG during a resize", apptest.HostileP99, 120, func() { s.Resize(size) }, func(i int) {
		s.Resize(render.Point{X: size.X - float32(i%60)*4, Y: size.Y - float32(i%60)*3})
	})
}

// A terminal flooded with output.
func TestTerminalFloodBudget(t *testing.T) {
	apptest.FrameTimed(t)
	s := apptest.TermScene(t, registry.Args{"cmd": []string{"sh", "-c", "while :; do echo 'a line of output from a build that never stops talking 0123456789'; done"}})
	s.Resize(render.Point{X: 1600, Y: 1000})
	s.Until("the flood", func() bool { return strings.Contains(s.Text(), "never stops talking") })
	s.HoldsFrameBudget("a terminal flood", apptest.HostileP99, 200, nil, func(int) { time.Sleep(time.Millisecond) })
}
