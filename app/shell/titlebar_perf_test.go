//go:build perf

package shell_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/core/eventstore"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
)

// It counts the frames a live window draws, which anything else that wakes it adds to on a loaded machine: a
// measure of responsiveness, run with make perf.
// Keys switch in under one frame: the frame drawn after ⌘3 already shows the third session in front.
func TestSessionKeysSwitchInOneFrameBudget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store := eventstore.New()
	openTabFixture(t, store, tabFixture)
	win := apptest.NewWindow(t, apptest.WindowOpts{WorkDir: t.TempDir(), Config: func(cfg *apptest.Config) { cfg.Store = store }}).Win
	first, err := win.WaitFrame(ctx, func(f headless.Frame) bool { return strings.Contains(f.Text(), "review #4410") })
	if err != nil {
		t.Fatal(err)
	}
	front, _ := glyphColour(&first.List, "pricing-rounding")
	before := len(win.Frames())
	win.Send(render.KeyEvent{Key: "3", Mods: render.ModSuper})
	// A frame already being drawn when the key arrives can't show it; the one after it must.
	n := 0
	f, err := win.WaitFrame(ctx, func(f headless.Frame) bool {
		n++
		on, _ := glyphColour(&f.List, "review #4410")
		return n > before && on == front
	})
	if err != nil {
		t.Fatalf("no frame shows the third tab in front: %v", err)
	}
	if late := n - before; late > 2 {
		t.Errorf("the third tab came to the front %d frames after ⌘3; the one after the frame in flight should show it", late)
	}
	if off, _ := glyphColour(&f.List, "pricing-rounding"); off == front {
		t.Errorf("the first tab is still drawn in front")
	}
	if store.State().Focus != "s3" {
		t.Errorf("focus = %s", store.State().Focus)
	}
}
