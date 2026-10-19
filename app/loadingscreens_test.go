package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
	"github.com/liamg/oriel/update"
)

func (s *errScene) repos() *reposPlugin {
	for _, e := range s.plugins {
		if x, ok := e.(*reposPlugin); ok {
			return x
		}
	}
	return nil
}

// ui.md, "Loading": the Add repo dialog is Gate. Until the path is checked, Add waits and ⌘↵ says why; once it's
// checked, Add adds it.
func TestAddRepoWaitsForItsCheck(t *testing.T) {
	lw := newLocalHost(t)
	other := newRepo(t, filepath.Join(t.TempDir(), "billing"))
	s := newSceneWith(t, "", "", mockupSize, lw.connect(t))
	s.now = at(14, 30, 0)
	s.run("repos.new", registry.Args{"path": other})
	s.frame()
	s.run("repos.new.submit", nil)
	s.frame()
	s.wantFrame(t, "To add: a moment: checking the path.")
	if _, ok := s.store.State().Repo("billing"); ok {
		t.Fatal("added before the path was checked")
	}
	s.now = s.now.Add(time.Second)
	s.until(t, "the check", func() bool { return strings.Contains(s.frameText(), "✓ git repo") })
	s.run("repos.new.submit", nil)
	waitState(t, s.store, "billing registered", func(st core.State) bool { _, ok := st.Repo("billing"); return ok })
}

// A clone shows git's progress as a bar: the phase and how far through it.
func TestCloneShowsItsProgress(t *testing.T) {
	lw := newLocalHost(t)
	s := newSceneWith(t, "", "", mockupSize, lw.connect(t))
	s.run("repos.new", registry.Args{"mode": "clone", "url": "https://github.com/liamg/oriel"})
	f := s.repos().dialog
	f.busy, f.progress = true, "Receiving objects:  45% (450/1000), 1.20 MiB | 2.00 MiB/s"
	s.frame()
	if n := s.access("Receiving objects 45%"); n.Role != "status" {
		t.Errorf("the bar reads as %+v", n)
	}
	s.wantFrame(t, "Cloning…")
}

// Review changes is busy from the click: a second click doesn't ask for a second review.
func TestAIReviewButtonBusyFromTheClick(t *testing.T) {
	r, _, codex, _ := reviewSession(t)
	var at render.Point
	r.latest("Review changes", func(f headless.Frame) bool {
		for _, op := range f.Ops {
			if op.Kind == render.OpGlyphs && op.Run.Text == "Review changes" {
				at = render.Point{X: op.At.X + op.Run.Advance/2, Y: op.At.Y - op.Run.Ascent/2}
				return true
			}
		}
		return false
	})
	for range 2 {
		r.win.Send(render.PointerEvent{Kind: render.PointerMove, Pos: at})
		r.win.Send(render.PointerEvent{Kind: render.PointerPress, Pos: at, Buttons: render.ButtonPrimary})
		r.win.Send(render.PointerEvent{Kind: render.PointerRelease, Pos: at, Buttons: render.ButtonPrimary})
	}
	waitReviewer(t, codex)
	select {
	case <-codex.Started:
		t.Fatal("a second click started a second review")
	case <-time.After(300 * time.Millisecond):
	}
}

// heldSource is a release source whose Latest waits for release.
type heldSource struct {
	update.Source
	release chan struct{}
}

func (h heldSource) Latest(ctx context.Context, channel string) (update.Release, error) {
	<-h.release
	return h.Source.Latest(ctx, channel)
}

// :UpdateCheck opens the card already checking, never saying the version running is the newest before it knows.
func TestUpdateCardIsBusyBeforeItOpens(t *testing.T) {
	r := newUpdateRig(t)
	r.publish("2.0.0", "")
	held := heldSource{Source: update.Dir(r.src), release: make(chan struct{})}
	s := newSceneWith(t, "", "", mockupSize, func(cfg *Config) {
		r.config(cfg)
		cfg.Updates.Updater.Source = held
	})
	s.now = at(14, 30, 0)
	s.run("UpdateCheck", nil)
	s.frame()
	if strings.Contains(s.frameText(), "is the newest") {
		t.Fatal("the card said 1.0.0 is the newest before the check answered")
	}
	s.wantFrame(t, "Checking for updates…", "Checking…")
	s.run("UpdateCheck", nil) // joins the check running
	close(held.release)
	s.until(t, "the release", func() bool { return strings.Contains(s.frameText(), "Oriel 2.0.0") })
}
