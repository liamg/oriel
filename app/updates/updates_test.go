package updates_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/updates"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/update"
)

// updatesScene is the app updating itself from r.
func updatesScene(t *testing.T, r *rig.Releases, change ...func(*apptest.Config)) *apptest.Scene {
	return apptest.New(t, apptest.Options{Config: func(cfg *apptest.Config) {
		cfg.Updates = &updates.Updates{Updater: r.Updater(), Relaunch: r.Relaunch}
		for _, f := range change {
			f(cfg)
		}
	}})
}

func updatesOf(s *apptest.Scene) *updates.Update {
	return apptest.PluginOf[*updates.Update](s, "update")
}

// The app's side of updating: :UpdateCheck opens the card with the release's notes; Update runs the update on the
// Updating page, each step a line, and restarts onto it.
func TestUpdateCardUpdatesAndRestarts(t *testing.T) {
	r := rig.NewReleases(t)
	s := updatesScene(t, r)
	updatesOf(s).WaitStartup() // up to date as it started: the release comes out after, for the check asked for
	r.Publish("2.0.0", "## Highlights\n\n- Faster session start\n- **Rollback** on a failed health check")

	s.Run("update.check", nil)
	s.Frame()
	s.Until("the card with the release", func() bool { return strings.Contains(s.Text(), "Oriel 2.0.0") })
	s.WantFrame("WHAT'S NEW", "you have 1.0.0 · stable channel", "Highlights", "Faster session start", "Rollback", "Later", "Update")
	s.Lint("the update card")

	s.Click(apptest.Centre(s.Within("Oriel 2.0.0", "Update").Bounds), 0)
	s.Until("the restart", func() bool { return s.Relaunching() })
	if b, _ := os.ReadFile(r.Self.Target); string(b) != "oriel 2.0.0" {
		t.Fatalf("this copy is %q", b)
	}
	s.Until("the Updating page, step by step", func() bool {
		return s.Has("UPDATING ORIEL", "1.0.0  →  2.0.0", "Found Oriel 2.0.0 on the stable channel: you have 1.0.0",
			"Downloading oriel-", "Verified oriel-", "Extracting oriel-", "Checking the new version starts: oriel version says 2.0.0",
			"Swapping in the new oriel; the old one is kept for a rollback", "Oriel 2.0.0 is installed", "Restarting into Oriel 2.0.0")
	})
	s.Lint("the Updating page")
	if err := s.Relaunch(); err != nil || r.Relaunched.Load() != 1 {
		t.Fatalf("closing the window after the restart relaunched %d times: %v", r.Relaunched.Load(), err)
	}
}

// Automatic, the default: a start checks before anything else, and a newer release is installed on the Updating page
// and restarted onto.
func TestAutomaticUpdateAtStart(t *testing.T) {
	r := rig.NewReleases(t)
	r.Publish("2.0.0", "")
	s := updatesScene(t, r)
	s.Until("the restart onto 2.0.0", func() bool { return s.Relaunching() })
	if b, _ := os.ReadFile(r.Self.Target); string(b) != "oriel 2.0.0" {
		t.Fatalf("this copy is %q", b)
	}
	s.Until("the Updating page", func() bool {
		return s.Has("UPDATING ORIEL", "Oriel 2.0.0 is installed", "Restarting into Oriel 2.0.0")
	})
}

// A release that comes out while Oriel runs is downloaded first, quietly. Ask then offers it; automatic installs it
// for the next restart, and says so, without restarting by itself.
func TestUpdateDownloadsBeforeItSays(t *testing.T) {
	for _, mode := range []string{updates.UpdateAsk, updates.UpdateAutomatic} {
		t.Run(mode, func(t *testing.T) {
			r := rig.NewReleases(t)
			s := updatesScene(t, r)
			updatesOf(s).WaitStartup() // an automatic start's own check, done: Check below isn't turned away as one runs
			if err := s.Registry().Options().Set("updates.mode", registry.LayerUser, registry.At{}, mode); err != nil {
				t.Fatal(err)
			}
			r.Publish("1.1.0", "")
			updatesOf(s).Check()
			cached := filepath.Join(updatesOf(s).Updater().CacheDir, "1.1.0", "oriel-"+strings.ReplaceAll(update.Platform(), "/", "-"))
			if _, err := os.Stat(cached); err != nil {
				t.Fatalf("not downloaded first: %v", err)
			}
			switch mode {
			case updates.UpdateAsk:
				if !hasNotice(s.Store().State(), "update.available") {
					t.Fatal("no offer")
				}
				if _, ok := updatesOf(s).Ready(); ok {
					t.Fatal("ask mode installed by itself")
				}
			case updates.UpdateAutomatic:
				s.Until("the install", func() bool { _, ok := updatesOf(s).Ready(); return ok })
				s.Until("the notice", func() bool { return hasNotice(s.Store().State(), "update.ready") })
				s.Frame()
				s.Until("the status line", func() bool { return strings.Contains(s.Text(), "update ready · restart") })
				if s.Relaunching() {
					t.Fatal("restarted mid-work by itself")
				}
			}
		})
	}

	r := rig.NewReleases(t)
	s := updatesScene(t, r)
	updatesOf(s).WaitStartup()
	r.Publish("1.0.0", "")
	updatesOf(s).Check()
	if hasNotice(s.Store().State(), "update.available") || hasNotice(s.Store().State(), "update.ready") {
		t.Fatal("offered the version already running")
	}
}

// The first start of a newer version says what it updated from, once.
func TestUpdatedSaysSo(t *testing.T) {
	r := rig.NewReleases(t)
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "version"), []byte("0.9.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := func(c *apptest.Config) { c.StateDir = state }
	s := updatesScene(t, r, cfg)
	if !hasNotice(s.Store().State(), "update.done") {
		t.Fatal("no notice of the update")
	}
	if b, _ := os.ReadFile(filepath.Join(state, "version")); strings.TrimSpace(string(b)) != "1.0.0" {
		t.Errorf("the version kept is %q", b)
	}
	s2 := updatesScene(t, r, cfg)
	if hasNotice(s2.Store().State(), "update.done") {
		t.Error("said so again")
	}
}

// Settings → updates: the mode and the channel.
func TestUpdateOptions(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	for name, want := range map[string]string{"updates.mode": "automatic", "updates.channel": "stable"} {
		if v, _, err := s.Registry().Options().Get(name, registry.At{}); err != nil || v != want {
			t.Errorf("%s = %v, %v; want %s", name, v, err, want)
		}
	}
	if s.Registry().Available("update.install", nil) {
		t.Error("a build without updates offers update.install")
	}
}

// An update that goes past several versions shows what each changed, newest first, under its version: not only the
// newest's notes. One whose changes were all internal says so; a release older than the running one isn't shown.
func TestUpdateCardShowsEveryVersionItGoesPast(t *testing.T) {
	r := rig.NewReleases(t)
	s := updatesScene(t, r)
	updatesOf(s).WaitStartup()
	r.Publish("0.9.0", "## Fixes\n\n- An old fix")
	r.Publish("1.1.0", "## Features\n\n- **Inbox:** Mark all read")
	r.Publish("1.2.0", "No notable changes.")
	r.Publish("2.0.0", "## Fixes\n\n- **LSP:** Language servers stop when their session ends")

	s.Run("update.check", nil)
	s.Frame()
	s.Until("the card with the release", func() bool { return strings.Contains(s.Text(), "Oriel 2.0.0") })
	text := strings.ReplaceAll(s.FullText(), "\n", " ")
	at := -1
	for _, want := range []string{"WHAT'S NEW", "2.0.0", "Language servers stop when their session ends", "1.2.0", "No notable changes.",
		"1.1.0", "Mark all read"} {
		i := strings.Index(text[at+1:], want)
		if i < 0 {
			t.Fatalf("the card lacks %q after what came before it:\n%s", want, text)
		}
		at += 1 + i
	}
	if strings.Contains(text, "0.9.0") || strings.Contains(text, "An old fix") {
		t.Errorf("the card shows a release older than the running one:\n%s", text)
	}
	s.Lint("the update card over three versions")
	s.Golden("update-card-versions")
}

// shown says a run of text reading text shows in the frame: drawn, and not clipped out of view.
func shown(s *apptest.Scene, text string) bool {
	for _, r := range s.List().Runs() {
		if r.Text == text && r.Visible() {
			return true
		}
	}
	return false
}

// An update past many versions has more notes than the card has room for: they scroll, by the wheel over them, to
// the oldest, while the card's buttons stay where they are.
func TestUpdateCardNotesScroll(t *testing.T) {
	r := rig.NewReleases(t)
	s := updatesScene(t, r)
	updatesOf(s).WaitStartup()
	for v := 1; v <= 8; v++ {
		r.Publish(fmt.Sprintf("1.%d.0", v), fmt.Sprintf("## Features\n\n- Feature %d.a\n- Feature %d.b\n\n## Fixes\n\n- Fix %d.a\n- Fix %d.b", v, v, v, v))
	}
	s.Run("update.check", nil)
	s.Frame()
	s.Until("the card with the release", func() bool { return strings.Contains(s.Text(), "Oriel 1.8.0") })
	s.Quiet()
	if !shown(s, "1.8.0") || shown(s, "1.1.0") {
		t.Fatalf("before scrolling: the newest shows %v, the oldest %v; want only the newest:\n%s", shown(s, "1.8.0"), shown(s, "1.1.0"), s.Text())
	}
	button := s.TextPoint("Update")
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: s.TextPoint("1.8.0")})
	s.Frame()
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerScroll, Pos: s.TextPoint("1.8.0"), Scroll: render.Point{Y: 4000}})
	s.Until("the oldest version scrolled into view", func() bool { return shown(s, "1.1.0") && shown(s, "Fix 1.b") })
	if shown(s, "1.8.0") {
		t.Error("the newest version still shows at the foot of the notes: they didn't scroll")
	}
	if got := s.TextPoint("Update"); got != button || !shown(s, "Update") || !shown(s, "Later") {
		t.Errorf("the card's buttons moved with the notes: Update at %v, was %v", got, button)
	}
}

func hasNotice(st core.State, id string) bool {
	for _, n := range st.Notifications {
		if n.ID == id {
			return true
		}
	}
	return false
}

// An update installed for the next restart, then a newer release out before that restart: updating again installs
// the newer one, and the restart runs it.
func TestUpdatingAgainBeforeTheRestartRunsTheNewest(t *testing.T) {
	r := rig.NewReleases(t)
	s := updatesScene(t, r)
	updatesOf(s).WaitStartup()
	r.Publish("1.1.0", "")
	updatesOf(s).Check() // automatic: installed for the next restart
	s.Until("1.1.0 installed", func() bool { v, ok := updatesOf(s).Ready(); return ok && v == "1.1.0" })

	r.Publish("1.2.0", "")
	s.Run("update.check", nil)
	s.Frame()
	s.Until("the card with 1.2.0", func() bool { return strings.Contains(s.Text(), "Oriel 1.2.0") })
	s.Click(apptest.Centre(s.Within("Oriel 1.2.0", "Update").Bounds), 0)
	s.Until("the restart", func() bool { return s.Relaunching() })
	if b, _ := os.ReadFile(r.Self.Target); string(b) != "oriel 1.2.0" {
		t.Fatalf("the restart runs %q", b)
	}
	if v, ok := updatesOf(s).Ready(); !ok || v != "1.2.0" {
		t.Fatalf("waiting for the restart: %q %v", v, ok)
	}
}

// Automatic: a release out while an older one waits for the restart is installed in its place, and the restart, from
// the status line, tells its hand-over on the Updating page, as one from the card does.
func TestANewerReleaseReplacesTheOneWaitingAndTheRestartShowsItsPage(t *testing.T) {
	r := rig.NewReleases(t)
	s := updatesScene(t, r)
	updatesOf(s).WaitStartup()
	r.Publish("1.1.0", "")
	updatesOf(s).Check()
	s.Until("1.1.0 installed", func() bool { v, ok := updatesOf(s).Ready(); return ok && v == "1.1.0" })
	r.Publish("1.2.0", "")
	updatesOf(s).Check()
	s.Until("1.2.0 installed in its place", func() bool { v, ok := updatesOf(s).Ready(); return ok && v == "1.2.0" })
	if b, _ := os.ReadFile(r.Self.Target); string(b) != "oriel 1.2.0" {
		t.Fatalf("this copy is %q", b)
	}

	s.Frame()
	s.Until("the status line", func() bool { return strings.Contains(s.Text(), "update ready · restart") })
	s.ClickText("update ready · restart")
	s.Until("the restart", func() bool { return s.Relaunching() })
	s.Until("the Updating page", func() bool {
		return s.Has("UPDATING ORIEL", "1.0.0  →  1.2.0", "Oriel 1.2.0 is installed", "Restarting into Oriel 1.2.0")
	})
}
