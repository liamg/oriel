//go:build (linux && !android) || freebsd || openbsd

package gio

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"

	"gioui.org/io/event"
	"github.com/godbus/dbus/v5"

	"github.com/liamg/oriel/render"
)

const (
	atspiRoot = dbus.ObjectPath(atspiPrefix + "/root")
	atspiNull = dbus.ObjectPath("/org/a11y/atspi/null")
)

type atspiRef struct {
	Name string
	Path dbus.ObjectPath
}

type atspiRect struct{ X, Y, W, H int32 }

type atspiRelation struct {
	Type    uint32
	Targets []atspiRef
}

type atspiAction struct{ Name, Description, KeyBinding string }

// atspiApp is the process's application on the accessibility bus, with each window's tree under it. It joins the
// bus only while the session says accessibility is on (org.a11y.Status.IsEnabled).
type atspiApp struct {
	name    string
	mu      sync.Mutex
	wins    map[uint32]*access
	next    uint32
	bus     *dbus.Conn // the accessibility bus, while it's on
	self    string     // our name on it
	desktop atspiRef
	id      int32
}

var atspiShared = sync.OnceValue(func() *atspiApp {
	a := newATSPIApp(filepath.Base(os.Args[0]))
	go a.watch(os.Getenv("DBUS_SESSION_BUS_ADDRESS"))
	return a
})

func newATSPIApp(name string) *atspiApp { return &atspiApp{name: name, wins: map[uint32]*access{}} }

type atspiBridge struct {
	app *atspiApp
	win uint32
	ax  *access
}

func newAccessBridge(ax *access) accessBridge { return atspiShared().add(ax) }

func (a *atspiApp) add(ax *access) *atspiBridge {
	a.mu.Lock()
	a.next++
	b := &atspiBridge{app: a, win: a.next, ax: ax}
	a.wins[b.win] = ax
	bus := a.bus
	a.mu.Unlock()
	if bus != nil {
		a.emitRoot(bus, "add", b.win)
	}
	return b
}

func (b *atspiBridge) view(event.Event) {}

func (b *atspiBridge) update(c accessChange) {
	bus := b.app.conn()
	if bus == nil || c.empty() {
		return
	}
	for _, ev := range atspiEvents(c, &b.ax.tree) {
		b.app.emit(bus, b.win, ev)
	}
}

func (b *atspiBridge) wanted() bool { return b.app.conn() != nil }

func (b *atspiBridge) activate(on bool) {
	bus := b.app.conn()
	if bus == nil {
		return
	}
	member := "Window.Deactivate"
	if on {
		member = "Window.Activate"
	}
	b.app.emit(bus, b.win, atspiEvent{Member: member})
	d := int32(0)
	if on {
		d = 1
	}
	b.app.emit(bus, b.win, atspiEvent{Member: "Object.StateChanged", Kind: "active", Detail1: d})
}

func (b *atspiBridge) close() {
	a := b.app
	a.mu.Lock()
	delete(a.wins, b.win)
	bus := a.bus
	a.mu.Unlock()
	if bus != nil {
		a.emitRoot(bus, "remove", b.win)
	}
}

func (a *atspiApp) conn() *dbus.Conn {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.bus
}

// watch follows the session's accessibility switch, joining the bus while it's on. It returns when the session
// bus goes.
func (a *atspiApp) watch(session string) {
	if session == "" {
		return
	}
	conn, err := dbus.Connect(session)
	if err != nil {
		return
	}
	defer conn.Close()
	defer a.leave()
	ch := make(chan *dbus.Signal, 8)
	conn.Signal(ch)
	if conn.AddMatchSignal(dbus.WithMatchObjectPath("/org/a11y/bus"), dbus.WithMatchInterface("org.freedesktop.DBus.Properties"),
		dbus.WithMatchMember("PropertiesChanged")) != nil {
		return
	}
	bus := conn.Object("org.a11y.Bus", "/org/a11y/bus")
	if v, err := bus.GetProperty("org.a11y.Status.IsEnabled"); err == nil {
		if on, _ := v.Value().(bool); on {
			a.join(bus)
		}
	}
	for s := range ch {
		if s.Name != "org.freedesktop.DBus.Properties.PropertiesChanged" || len(s.Body) < 2 {
			continue
		}
		if iface, _ := s.Body[0].(string); iface != "org.a11y.Status" {
			continue
		}
		changed, _ := s.Body[1].(map[string]dbus.Variant)
		if v, ok := changed["IsEnabled"]; ok {
			if on, _ := v.Value().(bool); on {
				a.join(bus)
			} else {
				a.leave()
			}
		}
	}
}

// join connects to the accessibility bus, exports the tree and embeds it in the desktop's.
func (a *atspiApp) join(session dbus.BusObject) {
	if a.conn() != nil {
		return
	}
	var addr string
	if err := session.Call("org.a11y.Bus.GetAddress", 0).Store(&addr); err != nil {
		return
	}
	bus, err := dbus.Connect(addr)
	if err != nil {
		return
	}
	a.mu.Lock()
	a.self = bus.Names()[0]
	a.mu.Unlock()
	if err := a.export(bus); err != nil {
		bus.Close()
		return
	}
	var desk atspiRef
	if err := bus.Object("org.a11y.atspi.Registry", atspiRoot).Call("org.a11y.atspi.Socket.Embed", 0,
		atspiRef{a.self, atspiRoot}).Store(&desk); err != nil {
		bus.Close()
		return
	}
	a.mu.Lock()
	a.bus, a.desktop = bus, desk
	wins := slices.Collect(maps.Values(a.wins))
	a.mu.Unlock()
	for _, w := range wins { // each window's tree, built from now on
		if w.wake != nil {
			w.wake()
		}
	}
}

func (a *atspiApp) leave() {
	a.mu.Lock()
	bus := a.bus
	a.bus = nil
	a.mu.Unlock()
	if bus != nil {
		bus.Close()
	}
}

func (a *atspiApp) ref(win uint32, id uint64) atspiRef {
	return atspiRef{a.self, dbus.ObjectPath(atspiPath(win, id))}
}

func (a *atspiApp) emit(bus *dbus.Conn, win uint32, ev atspiEvent) {
	var data any = int32(0)
	switch d := ev.Data.(type) {
	case atspiNodeRef:
		data = a.ref(win, uint64(d))
	case string, uint32:
		data = d
	}
	bus.Emit(dbus.ObjectPath(atspiPath(win, ev.ID)), "org.a11y.atspi.Event."+ev.Member, ev.Kind, ev.Detail1, ev.Detail2,
		dbus.MakeVariant(data), map[string]dbus.Variant{})
}

func (a *atspiApp) emitRoot(bus *dbus.Conn, kind string, win uint32) {
	a.mu.Lock()
	i := slices.Index(a.order(), win)
	a.mu.Unlock()
	bus.Emit(atspiRoot, "org.a11y.atspi.Event.Object.ChildrenChanged", kind, int32(max(i, 0)), int32(0),
		dbus.MakeVariant(a.ref(win, 0)), map[string]dbus.Variant{})
}

// order is the windows by number; a.mu is held.
func (a *atspiApp) order() []uint32 {
	var ws []uint32
	for w := range a.wins {
		ws = append(ws, w)
	}
	slices.Sort(ws)
	return ws
}

// atspiTarget is the object a call is on: the application, or a node of a window.
type atspiTarget struct {
	root bool
	win  uint32
	ax   *access
	id   uint64
	e    accessEntry
}

func (a *atspiApp) target(m dbus.Message) (atspiTarget, *dbus.Error) {
	p, _ := m.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)
	if p == atspiRoot {
		return atspiTarget{root: true}, nil
	}
	win, id, ok := atspiParsePath(string(p))
	if ok {
		a.mu.Lock()
		ax := a.wins[win]
		a.mu.Unlock()
		if ax != nil {
			if e, ok := ax.tree.get(id); ok {
				return atspiTarget{win: win, ax: ax, id: id, e: e}, nil
			}
		}
	}
	return atspiTarget{}, dbus.NewError("org.freedesktop.DBus.Error.UnknownObject", []any{"no such object: " + string(p)})
}

func (a *atspiApp) children(t atspiTarget) []atspiRef {
	if t.root {
		a.mu.Lock()
		defer a.mu.Unlock()
		var out []atspiRef
		for _, w := range a.order() {
			out = append(out, a.ref(w, 0))
		}
		return out
	}
	out := make([]atspiRef, len(t.e.kids))
	for i, k := range t.e.kids {
		out[i] = a.ref(t.win, k)
	}
	return out
}

func (a *atspiApp) parent(t atspiTarget) atspiRef {
	switch {
	case t.root:
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.desktop
	case t.id == 0:
		return atspiRef{a.self, atspiRoot}
	}
	return a.ref(t.win, t.e.parent)
}

// extents is the node's box in coord's space: 0 the screen, 1 the window, 2 its parent's box.
func (a *atspiApp) extents(t atspiTarget, coord uint32) atspiRect {
	if t.root {
		return atspiRect{}
	}
	g := t.ax.geom()
	box := func(r render.Rect) (float32, float32, float32, float32) {
		return r.Min.X * g.Scale, r.Min.Y * g.Scale, r.Dx() * g.Scale, r.Dy() * g.Scale
	}
	x, y, w, h := box(t.e.node.Bounds)
	switch coord {
	case 0:
		x, y = x+g.Origin.X, y+g.Origin.Y
	case 2:
		if p, ok := t.ax.tree.get(t.e.parent); ok && t.id != 0 {
			px, py, _, _ := box(p.node.Bounds)
			x, y = x-px, y-py
		}
	}
	return atspiRect{int32(x), int32(y), int32(w), int32(h)}
}

func (a *atspiApp) at(t atspiTarget, x, y int32, coord uint32) (uint64, bool) {
	if t.root {
		return 0, false
	}
	g := t.ax.geom()
	px, py := float32(x), float32(y)
	switch coord {
	case 0:
		px, py = px-g.Origin.X, py-g.Origin.Y
	case 2:
		r := a.extents(t, 1)
		px, py = px+float32(r.X), py+float32(r.Y)
	}
	if g.Scale > 0 {
		px, py = px/g.Scale, py/g.Scale
	}
	p := render.Point{X: px, Y: py}
	if !inside(t.e.node.Bounds, p) {
		return 0, false
	}
	return t.ax.tree.hit(p), true
}

func atspiFail(msg string) *dbus.Error {
	return dbus.NewError("org.freedesktop.DBus.Error.Failed", []any{msg})
}

func (a *atspiApp) export(bus *dbus.Conn) error {
	type msg = dbus.Message
	type derr = *dbus.Error
	node := func(m msg) (atspiTarget, derr) {
		t, err := a.target(m)
		if err == nil && t.root {
			return t, atspiFail("not a node")
		}
		return t, err
	}
	text := func(m msg) (atspiTarget, []rune, derr) {
		t, err := node(m)
		if err != nil {
			return t, nil, err
		}
		if t.e.node.Role != render.RoleTextBox {
			return t, nil, atspiFail("not text")
		}
		return t, []rune(t.e.node.Value), nil
	}
	act := func(t atspiTarget, x render.AccessAction) bool {
		x.ID = t.id
		return t.ax.act(x)
	}
	accessible := map[string]any{
		"GetChildAtIndex": func(m msg, i int32) (atspiRef, derr) {
			t, err := a.target(m)
			if err != nil {
				return atspiRef{}, err
			}
			kids := a.children(t)
			if i < 0 || int(i) >= len(kids) {
				return atspiRef{a.self, atspiNull}, nil
			}
			return kids[i], nil
		},
		"GetChildren": func(m msg) ([]atspiRef, derr) {
			t, err := a.target(m)
			if err != nil {
				return nil, err
			}
			return a.children(t), nil
		},
		"GetIndexInParent": func(m msg) (int32, derr) {
			t, err := a.target(m)
			switch {
			case err != nil:
				return -1, err
			case t.root:
				return -1, nil
			case t.id == 0:
				a.mu.Lock()
				defer a.mu.Unlock()
				return int32(slices.Index(a.order(), t.win)), nil
			}
			return int32(t.ax.tree.index(t.id)), nil
		},
		"GetRelationSet": func(m msg) ([]atspiRelation, derr) { return []atspiRelation{}, nil },
		"GetRole": func(m msg) (uint32, derr) {
			t, err := a.target(m)
			if err != nil || t.root {
				return atspiRoleApplication, err
			}
			return atspiRole(t.e.node.Role), nil
		},
		"GetRoleName": func(m msg) (string, derr) {
			t, err := a.target(m)
			if err != nil || t.root {
				return atspiRoleNames[atspiRoleApplication], err
			}
			return atspiRoleNames[atspiRole(t.e.node.Role)], nil
		},
		"GetState": func(m msg) ([]uint32, derr) {
			t, err := a.target(m)
			if err != nil || t.root {
				return atspiStateSet(nil), err
			}
			return atspiStateSet(atspiStates(&t.e.node, t.ax.geom().Active)), nil
		},
		"GetAttributes": func(m msg) (map[string]string, derr) {
			t, err := a.target(m)
			out := map[string]string{"toolkit": "oriel"}
			if err == nil && !t.root && t.e.node.Description != "" {
				out["help"] = t.e.node.Description
			}
			return out, err
		},
		"GetApplication": func(m msg) (atspiRef, derr) { return atspiRef{a.self, atspiRoot}, nil },
		"GetInterfaces": func(m msg) ([]string, derr) {
			t, err := a.target(m)
			if err != nil {
				return nil, err
			}
			if t.root {
				return []string{"org.a11y.atspi.Accessible", "org.a11y.atspi.Application"}, nil
			}
			return atspiInterfaces(&t.e.node), nil
		},
	}
	accessible["GetLocalizedRoleName"] = accessible["GetRoleName"]
	component := map[string]any{
		"Contains": func(m msg, x, y int32, coord uint32) (bool, derr) {
			t, err := node(m)
			if err != nil {
				return false, err
			}
			_, in := a.at(t, x, y, coord)
			return in, nil
		},
		"GetAccessibleAtPoint": func(m msg, x, y int32, coord uint32) (atspiRef, derr) {
			t, err := node(m)
			if err != nil {
				return atspiRef{}, err
			}
			if id, in := a.at(t, x, y, coord); in {
				return a.ref(t.win, id), nil
			}
			return atspiRef{a.self, atspiNull}, nil
		},
		"GetExtents": func(m msg, coord uint32) (atspiRect, derr) {
			t, err := a.target(m)
			if err != nil {
				return atspiRect{}, err
			}
			return a.extents(t, coord), nil
		},
		"GetPosition": func(m msg, coord uint32) (int32, int32, derr) {
			t, err := a.target(m)
			if err != nil {
				return 0, 0, err
			}
			r := a.extents(t, coord)
			return r.X, r.Y, nil
		},
		"GetSize": func(m msg) (int32, int32, derr) {
			t, err := a.target(m)
			if err != nil {
				return 0, 0, err
			}
			r := a.extents(t, 1)
			return r.W, r.H, nil
		},
		"GetLayer": func(m msg) (uint32, derr) {
			t, err := node(m)
			if err == nil && t.id == 0 {
				return 7, nil // ATSPI_LAYER_WINDOW
			}
			return 3, err // ATSPI_LAYER_WIDGET
		},
		"GetMDIZOrder": func(m msg) (int16, derr) { return 0, nil },
		"GrabFocus": func(m msg) (bool, derr) {
			t, err := node(m)
			if err != nil {
				return false, err
			}
			return act(t, render.AccessAction{Action: render.ActionFocus}), nil
		},
		"GetAlpha": func(m msg) (float64, derr) { return 1, nil },
		"ScrollTo": func(m msg, kind uint32) (bool, derr) { return false, nil },
	}
	action := map[string]any{
		"GetName": func(m msg, i int32) (string, derr) {
			t, err := node(m)
			if err != nil {
				return "", err
			}
			if as := atspiActions(&t.e.node); i >= 0 && int(i) < len(as) {
				return string(as[i]), nil
			}
			return "", nil
		},
		"GetDescription": func(m msg, i int32) (string, derr) { return "", nil },
		"GetKeyBinding":  func(m msg, i int32) (string, derr) { return "", nil },
		"GetActions": func(m msg) ([]atspiAction, derr) {
			t, err := node(m)
			if err != nil {
				return nil, err
			}
			out := []atspiAction{}
			for _, x := range atspiActions(&t.e.node) {
				out = append(out, atspiAction{Name: string(x)})
			}
			return out, nil
		},
		"DoAction": func(m msg, i int32) (bool, derr) {
			t, err := node(m)
			if err != nil {
				return false, err
			}
			as := atspiActions(&t.e.node)
			if i < 0 || int(i) >= len(as) {
				return false, nil
			}
			return act(t, render.AccessAction{Action: as[i]}), nil
		},
	}
	action["GetLocalizedName"] = action["GetName"]
	textAt := func(granular bool, dir int) func(m msg, off int32, kind uint32) (string, int32, int32, derr) {
		return func(m msg, off int32, kind uint32) (string, int32, int32, derr) {
			t, _, err := text(m)
			if err != nil {
				return "", 0, 0, err
			}
			s, start, end := atspiTextAt(t.e.node.Value, int(off), kind, granular, dir)
			return s, int32(start), int32(end), nil
		}
	}
	sel := func(t atspiTarget, start, end int32) bool {
		return act(t, render.AccessAction{Action: render.ActionSelect, Selection: render.TextRange{Start: int(start), End: int(end)}})
	}
	setText := func(t atspiTarget, s string) bool {
		return act(t, render.AccessAction{Action: render.ActionSetValue, Value: s})
	}
	noAttrs := func(m msg) (map[string]string, derr) { return map[string]string{}, nil }
	textIface := map[string]any{
		"GetStringAtOffset":   textAt(true, 0),
		"GetTextAtOffset":     textAt(false, 0),
		"GetTextBeforeOffset": textAt(false, -1),
		"GetTextAfterOffset":  textAt(false, 1),
		"GetText": func(m msg, start, end int32) (string, derr) {
			_, rs, err := text(m)
			if err != nil {
				return "", err
			}
			if end < 0 || int(end) > len(rs) {
				end = int32(len(rs))
			}
			start = min(max(start, 0), end)
			return string(rs[start:end]), nil
		},
		"SetCaretOffset": func(m msg, off int32) (bool, derr) {
			t, _, err := text(m)
			if err != nil {
				return false, err
			}
			return sel(t, off, off), nil
		},
		"GetCharacterAtOffset": func(m msg, off int32) (int32, derr) {
			_, rs, err := text(m)
			if err != nil || off < 0 || int(off) >= len(rs) {
				return 0, err
			}
			return rs[off], nil
		},
		"GetAttributeValue": func(m msg, off int32, name string) (string, derr) { return "", nil },
		"GetAttributes": func(m msg, off int32) (map[string]string, int32, int32, derr) {
			_, rs, err := text(m)
			return map[string]string{}, 0, int32(len(rs)), err
		},
		"GetAttributeRun": func(m msg, off int32, defaults bool) (map[string]string, int32, int32, derr) {
			_, rs, err := text(m)
			return map[string]string{}, 0, int32(len(rs)), err
		},
		"GetDefaultAttributes":   noAttrs,
		"GetDefaultAttributeSet": noAttrs,
		"GetCharacterExtents": func(m msg, off int32, coord uint32) (int32, int32, int32, int32, derr) {
			t, _, err := text(m)
			if err != nil {
				return 0, 0, 0, 0, err
			}
			r := a.extents(t, coord)
			return r.X, r.Y, r.W, r.H, nil
		},
		"GetRangeExtents": func(m msg, start, end int32, coord uint32) (int32, int32, int32, int32, derr) {
			t, _, err := text(m)
			if err != nil {
				return 0, 0, 0, 0, err
			}
			r := a.extents(t, coord)
			return r.X, r.Y, r.W, r.H, nil
		},
		"GetOffsetAtPoint": func(m msg, x, y int32, coord uint32) (int32, derr) { return -1, nil },
		"GetNSelections": func(m msg) (int32, derr) {
			t, _, err := text(m)
			if err != nil || t.e.node.Selection.Start == t.e.node.Selection.End {
				return 0, err
			}
			return 1, nil
		},
		"GetSelection": func(m msg, n int32) (int32, int32, derr) {
			t, _, err := text(m)
			if err != nil || n != 0 {
				return 0, 0, err
			}
			s := t.e.node.Selection
			return int32(min(s.Start, s.End)), int32(max(s.Start, s.End)), nil
		},
		"AddSelection": func(m msg, start, end int32) (bool, derr) {
			t, _, err := text(m)
			if err != nil {
				return false, err
			}
			return sel(t, start, end), nil
		},
		"SetSelection": func(m msg, n, start, end int32) (bool, derr) {
			t, _, err := text(m)
			if err != nil || n != 0 {
				return false, err
			}
			return sel(t, start, end), nil
		},
		"RemoveSelection": func(m msg, n int32) (bool, derr) {
			t, _, err := text(m)
			if err != nil || n != 0 {
				return false, err
			}
			c := int32(t.e.node.Selection.End)
			return sel(t, c, c), nil
		},
	}
	editable := map[string]any{
		"SetTextContents": func(m msg, s string) (bool, derr) {
			t, _, err := text(m)
			if err != nil {
				return false, err
			}
			return setText(t, s), nil
		},
		"InsertText": func(m msg, at int32, s string, n int32) (bool, derr) {
			t, _, err := text(m)
			if err != nil {
				return false, err
			}
			if rs := []rune(s); n >= 0 && int(n) < len(rs) {
				s = string(rs[:n])
			}
			return setText(t, spliced(t.e.node.Value, int(at), int(at), s)), nil
		},
		"DeleteText": func(m msg, start, end int32) (bool, derr) {
			t, _, err := text(m)
			if err != nil {
				return false, err
			}
			return setText(t, spliced(t.e.node.Value, int(start), int(end), "")), nil
		},
		"CopyText":  func(m msg, start, end int32) derr { return nil },
		"CutText":   func(m msg, start, end int32) (bool, derr) { return false, nil },
		"PasteText": func(m msg, at int32) (bool, derr) { return false, nil },
	}
	props := map[string]any{
		"Get": func(m msg, iface, name string) (dbus.Variant, derr) {
			all, err := a.props(m, iface)
			if err != nil {
				return dbus.Variant{}, err
			}
			v, ok := all[name]
			if !ok {
				return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.UnknownProperty", []any{name})
			}
			return v, nil
		},
		"GetAll": func(m msg, iface string) (map[string]dbus.Variant, derr) { return a.props(m, iface) },
		"Set": func(m msg, iface, name string, v dbus.Variant) derr {
			t, err := a.target(m)
			if err != nil {
				return err
			}
			switch {
			case t.root && iface == "org.a11y.atspi.Application" && name == "Id":
				id, _ := v.Value().(int32)
				a.mu.Lock()
				a.id = id
				a.mu.Unlock()
				return nil
			case !t.root && iface == "org.a11y.atspi.Value" && name == "CurrentValue":
				want, _ := v.Value().(float64)
				cur, _ := strconv.ParseFloat(t.e.node.Value, 64)
				switch {
				case want > cur:
					act(t, render.AccessAction{Action: render.ActionIncrement})
				case want < cur:
					act(t, render.AccessAction{Action: render.ActionDecrement})
				}
				return nil
			}
			return dbus.NewError("org.freedesktop.DBus.Error.PropertyReadOnly", []any{name})
		},
	}
	for iface, table := range map[string]map[string]any{
		"org.a11y.atspi.Accessible": accessible, "org.a11y.atspi.Component": component,
		"org.a11y.atspi.Action": action, "org.a11y.atspi.Text": textIface, "org.a11y.atspi.EditableText": editable,
		"org.a11y.atspi.Application": {
			"GetLocale":                func(m msg, kind uint32) (string, derr) { return "", nil },
			"GetApplicationBusAddress": func(m msg) (string, derr) { return "", nil },
		},
		"org.freedesktop.DBus.Properties": props,
	} {
		if err := bus.ExportSubtreeMethodTable(table, atspiPrefix, iface); err != nil {
			return err
		}
	}
	return nil
}

// props are an interface's properties on the call's object.
func (a *atspiApp) props(m dbus.Message, iface string) (map[string]dbus.Variant, *dbus.Error) {
	t, err := a.target(m)
	if err != nil {
		return nil, err
	}
	v := dbus.MakeVariant
	a.mu.Lock()
	name, id := a.name, a.id
	a.mu.Unlock()
	if t.root {
		switch iface {
		case "org.a11y.atspi.Accessible":
			a.mu.Lock()
			n := len(a.wins)
			a.mu.Unlock()
			return map[string]dbus.Variant{"Name": v(name), "Description": v(""), "Parent": v(a.parent(t)),
				"ChildCount": v(int32(n)), "Locale": v(""), "AccessibleId": v(""), "HelpText": v("")}, nil
		case "org.a11y.atspi.Application":
			return map[string]dbus.Variant{"ToolkitName": v("oriel"), "Version": v(""), "AtspiVersion": v("2.1"),
				"Id": v(id)}, nil
		}
		return map[string]dbus.Variant{}, nil
	}
	n := &t.e.node
	switch iface {
	case "org.a11y.atspi.Accessible":
		return map[string]dbus.Variant{"Name": v(n.Name), "Description": v(""), "Parent": v(a.parent(t)),
			"ChildCount": v(int32(len(t.e.kids))), "Locale": v(""), "AccessibleId": v(strconv.FormatUint(t.id, 10)),
			"HelpText": v("")}, nil
	case "org.a11y.atspi.Action":
		return map[string]dbus.Variant{"NActions": v(int32(len(atspiActions(n))))}, nil
	case "org.a11y.atspi.Text":
		return map[string]dbus.Variant{"CharacterCount": v(int32(len([]rune(n.Value)))),
			"CaretOffset": v(int32(n.Selection.End))}, nil
	case "org.a11y.atspi.Value":
		cur, _ := strconv.ParseFloat(n.Value, 64)
		return map[string]dbus.Variant{"MinimumValue": v(0.0), "MaximumValue": v(max(cur, 100)),
			"MinimumIncrement": v(1.0), "CurrentValue": v(cur), "Text": v(n.Value)}, nil
	}
	return map[string]dbus.Variant{}, nil
}
