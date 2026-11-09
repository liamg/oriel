package app_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/sysload"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// spawns puts shims for the commands Oriel runs on PATH, each noting its run in a log, and counts the runs logged.
func spawns(t *testing.T) func() []string {
	t.Helper()
	dir, log := t.TempDir(), filepath.Join(t.TempDir(), "spawned")
	for _, name := range []string{"git", "gh", "rg", "claude", "codex", "docker", "ps", "sysctl", "vm_stat", "defaults", "gdbus", "gsettings"} {
		real, err := exec.LookPath(name)
		run := "exit 127"
		if err == nil {
			run = `exec "` + real + `" "$@"`
		}
		shim := "#!/bin/sh\necho \"" + name + " $*\" | tr ' ' , >> \"" + log + "\"\n" + run + "\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(shim), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		b, _ := os.ReadFile(log)
		return strings.Fields(string(b))
	}
}

// idleWindow is the app with three quiet sessions, one agent working, behind another app.
func idleWindow(t *testing.T) *apptest.Window {
	t.Helper()
	lw := rig.NewHost(t)
	r := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), Config: apptest.Connect(lw.W), Timeout: time.Minute})
	rig.WaitState(t, r.Store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	var working bool
	for _, name := range []string{"one", "two", "three"} {
		err := r.Do(func() error {
			return r.Host.Run(context.Background(), "session.start", registry.Args{"kind": "work", "name": name, "provider": "fake", "repos": lw.Repo})
		})
		if err != nil {
			t.Fatal(err)
		}
		a := lw.Fake.WaitStarted(t)
		rig.WaitState(t, r.Store, name+" in front", func(st core.State) bool { return st.Focus == core.SessionID(name) })
		if !working {
			a.Emit(agent.TurnStart{})
			working = true
		}
	}
	rig.WaitState(t, r.Store, "one working", func(st core.State) bool {
		se, ok := st.Session("one")
		return ok && se.Snapshot().Agent.Working
	})
	r.Win.Send(render.FocusEvent{Focused: false})
	return r
}

// Left alone with three quiet sessions, one agent working and the window behind another app, the app goes idle:
// nothing under way or due, and no poll running faster than a second.
func TestIdlePollsNoFasterThanASecond(t *testing.T) {
	r := idleWindow(t)
	r.Idle()
	w, ok := kit.WindowOf(r.Host)
	if !ok {
		t.Fatal("no window service")
	}
	var fastest time.Duration
	r.Do(func() error { fastest = w.Polls().FastestNow(); return nil })
	if fastest > 0 && fastest < time.Second {
		t.Errorf("a poll runs every %v while idle, want a second or slower", fastest)
	}
}

// The machine's load is read while a view showing it is drawn with the window in front, and not otherwise.
func TestMachineLoadIsReadWhileShown(t *testing.T) {
	var reads atomic.Int32
	load := sysload.NewSampler(time.Hour, func() sysload.Reading {
		reads.Add(1)
		return sysload.Reading{CPU: 34, Cores: 4}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go load.Run(ctx)
	lw := rig.NewHost(t)
	r := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), Config: func(cfg *apptest.Config) {
		apptest.Connect(lw.W)(cfg)
		cfg.Load = load
	}})
	wanted := func(what string, want bool) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); load.Wanted() != want; time.Sleep(5 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("%s: wanted %v, want %v", what, !want, want)
			}
		}
	}
	r.WaitText("CPU")
	wanted("the status line shows the CPU", true)
	r.Win.Send(render.FocusEvent{Focused: false})
	wanted("the window is behind another app", false)
	r.Win.Send(render.FocusEvent{Focused: true})
	wanted("the window is in front again", true)
	if reads.Load() == 0 {
		t.Error("the load was never read")
	}
}

// A hidden window draws no frames; what changed meanwhile is drawn once it shows.
func TestNoFramesWhileHidden(t *testing.T) {
	r := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir()})
	r.WaitText("")
	r.Win.Send(render.VisibleEvent{Visible: false})
	if err := r.Do(func() error { return nil }); err != nil { // the loop has taken the event
		t.Fatal(err)
	}
	before := len(r.Win.Frames())
	r.Host.Notify(core.Notice{ID: "hidden.test", Severity: core.SevInfo, Text: "changed while hidden"})
	time.Sleep(500 * time.Millisecond)
	if n := len(r.Win.Frames()) - before; n > 0 {
		t.Errorf("drew %d frames while hidden", n)
	}
	r.Win.Send(render.VisibleEvent{Visible: true})
	r.WaitText("changed while hidden")
}
