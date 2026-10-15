package app

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/script"
)

// icalPlugin is the calendar plugin (integ/ical/plugin) as an install would lay it out: its files, and its process
// built into bin/. It's granted what its manifest declares, with net narrowed or widened by net.
func icalPlugin(t *testing.T, net []string) ext.Plugin {
	t.Helper()
	dir, m := integPlugin(t, "ical", "oriel.toml", "init.lua", "eventkit.js")
	var grants []ext.Grant
	for _, g := range m.Permissions {
		if g.Cap == ext.CapNet {
			g.Scope = net
		}
		grants = append(grants, g)
	}
	g := ext.NewGrants()
	g.Grant(m.ID, grants)
	return ext.Plugin{ID: m.ID, Dir: dir, Guard: g}
}

// feed serves the recorded Google feed at a secret path, as a calendar does.
func feed(t *testing.T) (url, hostPort string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "integ", "ical", "testdata", "google.ics"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/calendar/ical/secret-x1/basic.ics" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/calendar")
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/calendar/ical/secret-x1/basic.ics", strings.TrimPrefix(srv.URL, "http://")
}

func loadPlugin(t *testing.T, s *errScene, p ext.Plugin) *script.VM {
	t.Helper()
	vm := s.pluginVM(t, p)
	src, err := os.ReadFile(filepath.Join(p.Dir, "init.lua"))
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Load(script.Chunk{Name: "init.lua", Source: src}); err != nil {
		t.Fatal(err)
	}
	return vm
}

// PL-10 against mockup 10: "Next meeting · 14:30 · Pricing sync · 18m ring" under the HUD's Today panel, from an iCal
// address read by the plugin's sandboxed process, on the window's clock. The meeting in progress before it is one
// I declined, so it's left out. The dial counts down as the clock moves, and turns to the meeting's end once it starts.
func TestICalNextMeeting(t *testing.T) {
	url, hostPort := feed(t)
	s := luaScene(t, "")
	loadPlugin(t, s, icalPlugin(t, []string{hostPort}))
	s.now = time.Date(2026, 10, 15, 13, 12, 0, 0, time.UTC) // 14:12 in London: the feed's times read in UTC here
	s.hold(render.ModAlt, 300*time.Millisecond)
	s.wantFrame(t, "NEXT MEETING", "No calendar yet: set plugin.ical.url to its iCal address")

	if err := s.reg.Options().SetFrom("plugin.ical.url", registry.LayerUser, registry.At{}, url, "settings.toml"); err != nil {
		t.Fatal(err)
	}
	s.run("hud.refresh", registry.Args{"name": "ical.next"})
	s.until(t, "the next meeting", func() bool { return strings.Contains(s.frameText(), "Pricing sync") })
	s.wantFrame(t, "NEXT MEETING · 13:30", "Pricing sync", "18m", "meet.google.com · 4 people")
	if strings.Contains(s.frameText(), "Vendor demo") {
		t.Error("the meeting I declined is shown")
	}
	if strings.Contains(s.frameText(), "secret-x1") {
		t.Error("the frame shows the calendar's secret address")
	}

	s.now = s.now.Add(10 * time.Minute)
	s.frame()
	s.wantFrame(t, "8m", "Pricing sync")

	s.now = time.Date(2026, 10, 15, 13, 40, 0, 0, time.UTC)
	s.until(t, "the meeting under way", func() bool { return strings.Contains(s.frameText(), "NOW · UNTIL 14:00") })
	s.wantFrame(t, "now", "Pricing sync")
}

// The address is the plugin's to read only where its net grant reaches: granted another host, its process's sandbox
// has no way to the calendar, and the HUD says it couldn't reach it, without the address.
func TestICalNeedsItsNetGrant(t *testing.T) {
	url, _ := feed(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	other := l.Addr().String()
	l.Close()
	s := luaScene(t, "")
	loadPlugin(t, s, icalPlugin(t, []string{other}))
	s.now = time.Date(2026, 10, 15, 13, 12, 0, 0, time.UTC)
	if err := s.reg.Options().SetFrom("plugin.ical.url", registry.LayerUser, registry.At{}, url, "settings.toml"); err != nil {
		t.Fatal(err)
	}
	s.hold(render.ModAlt, 300*time.Millisecond)
	s.until(t, "the failure", func() bool { return strings.Contains(s.frameText(), "could not reach the calendar") })
	if strings.Contains(s.frameText(), "Pricing sync") || strings.Contains(s.frameText(), "secret-x1") {
		t.Errorf("read the calendar without the grant, or showed its address:\n%s", s.frameText())
	}
}
