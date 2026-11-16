package apptest

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/script"
	"github.com/liamg/oriel/script/api"
)

// SettleReads draws frames until what they read from disk (kit.Reads) is in.
func (s *Scene) SettleReads() {
	s.T.Helper()
	rd, ok := kit.ReadsOf(s.Registry())
	if !ok {
		return
	}
	for deadline := time.Now().Add(Timeout); ; time.Sleep(time.Millisecond) {
		s.Frame()
		if !rd.Busy() {
			return
		}
		if time.Now().After(deadline) {
			s.T.Fatal("the window's reads never came in")
		}
	}
}

// HasCommand says a command id is registered.
func (s *Scene) HasCommand(id string) bool {
	return slices.ContainsFunc(s.Registry().Commands(), func(c registry.Command) bool { return c.ID == id })
}

// InboxCard is the card id from the inbox source named source.
func (s *Scene) InboxCard(source, id string) (uireg.InboxCard, bool) {
	for _, src := range registry.ContributionsOf[uireg.InboxSource](s.Registry()) {
		if src.Name == source {
			for _, card := range src.Cards() {
				if card.ID == id {
					return card, true
				}
			}
		}
	}
	return uireg.InboxCard{}, false
}

// Notices are the notification history's titles and texts, for a failure's message.
func (s *Scene) Notices() []string { return rig.NoticeTexts(s.Store().State().Notifications) }

// Reopen does what the window does when it's asked to open on another directory (a session's): it moves onto it,
// underneath the plugins, and lets git read its work tree, which it finds off the loop. It says whether it was asked.
func (s *Scene) Reopen() bool {
	s.T.Helper()
	asked, err := s.Headless.Reopen()
	if err != nil {
		s.T.Fatal(err)
	}
	if asked {
		s.arrived()
		s.SettleGit()
	}
	return asked
}

// arrived waits for the window to lay out the directory it moved onto, once what holds it lets go.
func (s *Scene) arrived() {
	s.T.Helper()
	w, ok := kit.WindowOf(s.Registry())
	if !ok {
		return
	}
	deadline := time.Now().Add(Timeout)
	for {
		s.RunQueued()
		if w.Arrived() {
			s.Frame()
			return
		}
		if time.Now().After(deadline) {
			s.T.Fatal("the window never laid out the directory it moved onto")
		}
		time.Sleep(time.Millisecond)
	}
}

// Retheme resolves ui.theme and ui.variant, as the window does when a theme changes.
func (s *Scene) Retheme() {
	s.T.Helper()
	if err := s.Headless.Retheme(); err != nil {
		s.T.Fatal(err)
	}
}

// WriteConfig writes files under the config directory.
func (s *Scene) WriteConfig(files map[string]string) {
	s.T.Helper()
	for name, src := range files {
		p := filepath.Join(s.ConfigDir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			s.T.Fatal(err)
		}
	}
}

// PluginVM is a Lua VM with the plugin API p is granted, as an installed plugin's.
func (s *Scene) PluginVM(p plugin.Caller) *script.VM {
	vm := script.New()
	apis := api.InstallPlugin(vm, s.HostAs("builtin:lua"), p)
	s.T.Cleanup(func() { apis.Dispose(); vm.Close() })
	return vm
}

// LastNotice is the newest notification whose ID starts with prefix.
func LastNotice(st core.State, prefix string) (core.Notification, bool) {
	for _, n := range slices.Backward(st.Notifications) {
		if strings.HasPrefix(n.ID, prefix) {
			return n, true
		}
	}
	return core.Notification{}, false
}
