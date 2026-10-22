//go:build (linux && !android) || freebsd || openbsd

package gio

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/liamg/oriel/render"
)

func TestATSPIOverDBusDaemon(t *testing.T) { testATSPI(t, startDBusDaemon(t)) }

func TestATSPIOverTestBus(t *testing.T) { testATSPI(t, startTestBus(t)) }

// a11yDesktop plays the session's accessibility services on one bus: org.a11y.Bus with its switch, and the
// registry an application embeds itself in.
type a11yDesktop struct {
	conn     *dbus.Conn
	mu       sync.Mutex
	enabled  bool
	embedded []atspiRef
}

func startDesktop(t *testing.T, addr string) *a11yDesktop {
	conn, err := dbus.Connect(addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	d := &a11yDesktop{conn: conn}
	for _, name := range []string{"org.a11y.Bus", "org.a11y.atspi.Registry"} {
		if _, err := conn.RequestName(name, dbus.NameFlagDoNotQueue); err != nil {
			t.Fatal(err)
		}
	}
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(conn.ExportMethodTable(map[string]any{
		"GetAddress": func() (string, *dbus.Error) { return addr, nil },
	}, "/org/a11y/bus", "org.a11y.Bus"))
	must(conn.ExportMethodTable(map[string]any{
		"Get": func(iface, name string) (dbus.Variant, *dbus.Error) {
			d.mu.Lock()
			defer d.mu.Unlock()
			if iface == "org.a11y.Status" && name == "IsEnabled" {
				return dbus.MakeVariant(d.enabled), nil
			}
			return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.UnknownProperty", nil)
		},
	}, "/org/a11y/bus", "org.freedesktop.DBus.Properties"))
	must(conn.ExportMethodTable(map[string]any{
		"Embed": func(app atspiRef) (atspiRef, *dbus.Error) {
			d.mu.Lock()
			defer d.mu.Unlock()
			d.embedded = append(d.embedded, app)
			return atspiRef{conn.Names()[0], atspiRoot}, nil
		},
	}, atspiRoot, "org.a11y.atspi.Socket"))
	return d
}

func (d *a11yDesktop) enable(on bool) error {
	d.mu.Lock()
	d.enabled = on
	d.mu.Unlock()
	return d.conn.Emit("/org/a11y/bus", "org.freedesktop.DBus.Properties.PropertiesChanged", "org.a11y.Status",
		map[string]dbus.Variant{"IsEnabled": dbus.MakeVariant(on)}, []string{})
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func testATSPI(t *testing.T, addr string) {
	desk := startDesktop(t, addr)
	app := newATSPIApp("oriel-test")
	ax := newAccess(func() accessGeom {
		return accessGeom{Origin: render.Point{X: 100, Y: 50}, Scale: 2, Dpi: 2, Active: true}
	})
	first := fixtureTree()
	ax.tree.apply(render.AccessDiff(nil, first))
	b := app.add(ax)
	ax.bridge = b
	done := make(chan struct{})
	go func() { app.watch(addr); close(done) }()
	t.Cleanup(func() {
		desk.conn.Close()
		app.leave()
	})

	// Off until the session turns accessibility on: no connection, nothing exported.
	time.Sleep(100 * time.Millisecond)
	if app.conn() != nil {
		t.Fatal("joined the accessibility bus while it's off")
	}
	b.update(ax.tree.apply(render.AccessDiff(first, fixtureTree())))

	if err := desk.enable(true); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the app to embed itself", func() bool {
		desk.mu.Lock()
		defer desk.mu.Unlock()
		return len(desk.embedded) == 1 && app.conn() != nil
	})
	desk.mu.Lock()
	appRef := desk.embedded[0]
	desk.mu.Unlock()
	if appRef.Path != atspiRoot {
		t.Fatalf("embedded %v", appRef)
	}

	// A screen reader on its own connection walks the tree.
	at, err := dbus.Connect(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer at.Close()
	call := func(ref atspiRef, method string, args ...any) *dbus.Call {
		t.Helper()
		c := at.Object(ref.Name, ref.Path).Call(method, 0, args...)
		if c.Err != nil {
			t.Fatalf("%s on %s: %v", method, ref.Path, c.Err)
		}
		return c
	}
	prop := func(ref atspiRef, name string) any {
		t.Helper()
		v, err := at.Object(ref.Name, ref.Path).GetProperty(name)
		if err != nil {
			t.Fatalf("%s on %s: %v", name, ref.Path, err)
		}
		return v.Value()
	}
	var role string
	call(appRef, "org.a11y.atspi.Accessible.GetRoleName").Store(&role)
	if role != "application" || prop(appRef, "org.a11y.atspi.Accessible.Name") != "oriel-test" {
		t.Errorf("app: %q %v", role, prop(appRef, "org.a11y.atspi.Accessible.Name"))
	}
	var outline strings.Builder
	refs := map[string]atspiRef{}
	var walk func(ref atspiRef, depth int)
	walk = func(ref atspiRef, depth int) {
		var role, name string
		var kids []atspiRef
		var states []uint32
		call(ref, "org.a11y.atspi.Accessible.GetRoleName").Store(&role)
		name = prop(ref, "org.a11y.atspi.Accessible.Name").(string)
		call(ref, "org.a11y.atspi.Accessible.GetState").Store(&states)
		call(ref, "org.a11y.atspi.Accessible.GetChildren").Store(&kids)
		if n := prop(ref, "org.a11y.atspi.Accessible.ChildCount").(int32); int(n) != len(kids) {
			t.Errorf("%s: ChildCount %d, %d children", ref.Path, n, len(kids))
		}
		fmt.Fprintf(&outline, "%s%s %q", strings.Repeat("  ", depth), role, name)
		if states[0]&(1<<stateFocused) != 0 {
			outline.WriteString(" focused")
		}
		outline.WriteString("\n")
		refs[name] = ref
		for i, k := range kids {
			var parent atspiRef
			if err := at.Object(k.Name, k.Path).StoreProperty("org.a11y.atspi.Accessible.Parent", &parent); err != nil {
				t.Fatal(err)
			}
			var idx int32
			call(k, "org.a11y.atspi.Accessible.GetIndexInParent").Store(&idx)
			if parent != ref || int(idx) != i {
				t.Errorf("%s: parent %v index %d, want %v %d", k.Path, parent, idx, ref, i)
			}
			walk(k, depth+1)
		}
	}
	var frames []atspiRef
	call(appRef, "org.a11y.atspi.Accessible.GetChildren").Store(&frames)
	if len(frames) != 1 {
		t.Fatalf("frames: %v", frames)
	}
	walk(frames[0], 0)
	want := `frame "Oriel"
  landmark "Main"
    tool bar "Actions"
      push button "Open Directory…"
      push button "Run"
    entry "Search" focused
    list "Recent"
      list item "oriel"
      list item "crit"
    page tab list ""
      page tab "Files"
    check box "Wrap"
    link "Docs"
    menu "File"
      menu item "Save"
    slider "Zoom"
    image "Logo"
    tree "Files"
      tree item "src"
      tree item "docs"
    label "Welcome back"
    panel ""
  status bar "Ready"
  dialog "Settings"
`
	if outline.String() != want {
		t.Errorf("tree:\n%s\nwant:\n%s", outline.String(), want)
	}

	// Geometry: the window is at (100, 50) on screen, two pixels to a unit.
	var box atspiRect
	call(refs["Open Directory…"], "org.a11y.atspi.Component.GetExtents", uint32(0)).Store(&box)
	if box != (atspiRect{120, 140, 240, 40}) {
		t.Errorf("button's screen extents: %v", box)
	}
	var hit atspiRef
	call(frames[0], "org.a11y.atspi.Component.GetAccessibleAtPoint", int32(130), int32(150), uint32(0)).Store(&hit)
	if hit != refs["Open Directory…"] {
		t.Errorf("hit %v", hit)
	}

	// The text field's text and caret.
	search := refs["Search"]
	var s string
	call(search, "org.a11y.atspi.Text.GetText", int32(0), int32(-1)).Store(&s)
	if s != "héllo 😀 wörld" || prop(search, "org.a11y.atspi.Text.CaretOffset") != int32(7) ||
		prop(search, "org.a11y.atspi.Text.CharacterCount") != int32(13) {
		t.Errorf("text %q caret %v count %v", s, prop(search, "org.a11y.atspi.Text.CaretOffset"),
			prop(search, "org.a11y.atspi.Text.CharacterCount"))
	}

	// DoAction presses the button, on the app's loop.
	button := refs["Open Directory…"]
	var name string
	call(button, "org.a11y.atspi.Action.GetName", int32(0)).Store(&name)
	var pressed bool
	call(button, "org.a11y.atspi.Action.DoAction", int32(0)).Store(&pressed)
	if name != "click" || !pressed {
		t.Errorf("action %q, pressed %v", name, pressed)
	}
	select {
	case a := <-ax.actions:
		if a != (render.AccessAction{ID: 3, Action: render.ActionClick}) {
			t.Errorf("action %+v", a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no action from DoAction")
	}
	call(refs["Run"], "org.a11y.atspi.Action.DoAction", int32(0)).Store(&pressed)
	if pressed {
		t.Error("a disabled button pressed")
	}

	// Focus moving sends its events.
	sigs := make(chan *dbus.Signal, 64)
	at.Signal(sigs)
	if err := at.AddMatchSignal(dbus.WithMatchInterface("org.a11y.atspi.Event.Object")); err != nil {
		t.Fatal(err)
	}
	next := fixtureTree()
	find(next, 5).Focused = false
	find(next, 11).Focused = true
	b.update(ax.tree.apply(render.AccessDiff(first, next)))
	got := map[string]bool{}
	for end := time.After(5 * time.Second); len(got) < 2; {
		select {
		case sg := <-sigs:
			if sg.Name == "org.a11y.atspi.Event.Object.StateChanged" && sg.Body[0] == "focused" {
				got[fmt.Sprintf("%s %v", sg.Path, sg.Body[1])] = true
			}
		case <-end:
			t.Fatalf("focus events: %v", got)
		}
	}
	if !got[fmt.Sprintf("%s 1", refs["Wrap"].Path)] || !got[fmt.Sprintf("%s 0", search.Path)] {
		t.Errorf("focus events: %v", got)
	}

	// Turned off, the app leaves the bus.
	if err := desk.enable(false); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the app to leave", func() bool { return app.conn() == nil })
}
