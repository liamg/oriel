package registry

import (
	"fmt"
	"runtime/debug"
	"slices"
	"strings"
	"sync"

	"github.com/liamg/oriel/ui"
)

// Kinds of contribution a fault can come from.
const (
	KindCommand = "command"
	KindView    = "view"
	KindHook    = "hook"
)

// Panic is a contribution's panic, as an error.
type Panic struct {
	Value any
	Stack []byte
}

func (p *Panic) Error() string { return fmt.Sprintf("panic: %v", p.Value) }

// Call runs fn, turning a panic into a *Panic.
func Call(fn func() error) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = &Panic{Value: v, Stack: debug.Stack()}
		}
	}()
	return fn()
}

// Fault is a contribution that panicked or kept failing, and was turned off.
type Fault struct {
	Kind   string // KindCommand, KindView, KindHook
	ID     string
	Source string // who registered it
	Err    error  // a *Panic, or the last error
}

// Off is a contribution that's turned off, and why.
type Off struct {
	Kind, ID string
	Err      error
}

type offKey struct{ kind, id string }

// faults is what's turned off, and who hears about it.
type faults struct {
	mu   sync.Mutex
	off  map[offKey]error
	subs []entry[func(Fault)]
}

// fault turns a contribution off and tells every subscriber.
func (f *faults) fault(ft Fault) {
	f.mu.Lock()
	if f.off == nil {
		f.off = map[offKey]error{}
	}
	f.off[offKey{ft.Kind, ft.ID}] = ft.Err
	var fns []func(Fault)
	for _, e := range f.subs {
		fns = append(fns, e.value)
	}
	f.mu.Unlock()
	for _, fn := range fns {
		fn(ft)
	}
}

func (f *faults) isOff(kind, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.off[offKey{kind, id}]
}

// OnFault calls fn each time a contribution is turned off. fn may run on any goroutine.
func (r *reg) OnFault(fn func(Fault)) Disposable {
	r.faults.mu.Lock()
	defer r.faults.mu.Unlock()
	tok := tokens.Add(1)
	r.faults.subs = append(r.faults.subs, entry[func(Fault)]{tok, fn})
	return disposeOnce(func() {
		r.faults.mu.Lock()
		defer r.faults.mu.Unlock()
		r.faults.subs = slices.DeleteFunc(r.faults.subs, func(e entry[func(Fault)]) bool { return e.token == tok })
	})
}

func (r *reg) TurnOff(kind, id string, err error) {
	r.faults.fault(Fault{Kind: kind, ID: id, Source: r.sourceOf(kind, id), Err: err})
}

func (r *reg) TurnOn(kind, id string) {
	r.faults.mu.Lock()
	defer r.faults.mu.Unlock()
	delete(r.faults.off, offKey{kind, id})
}

func (r *reg) TurnedOff() []Off {
	r.faults.mu.Lock()
	defer r.faults.mu.Unlock()
	out := make([]Off, 0, len(r.faults.off))
	for k, err := range r.faults.off {
		out = append(out, Off{Kind: k.kind, ID: k.id, Err: err})
	}
	slices.SortFunc(out, func(a, b Off) int {
		if c := strings.Compare(a.Kind, b.Kind); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

func (r *reg) sourceOf(kind, id string) string {
	if kind != KindCommand {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	c, _ := r.commands.top(id)
	return c.Source
}

// guardedView turns its view off when it panics, building or drawing; a view that's off draws a placeholder.
type guardedView struct {
	r  *reg
	id string
	v  View
}

func (g guardedView) Build(vc ViewCtx) ui.Node {
	if err := g.r.faults.isOff(KindView, g.id); err != nil {
		return offNode(g.id, err)
	}
	var n ui.Node
	if err := Call(func() error { n = g.v.Build(vc); return nil }); err != nil {
		g.r.TurnOff(KindView, g.id, err)
		return offNode(g.id, err)
	}
	f, ok := n.(*ui.Func)
	if !ok {
		return n
	}
	return &ui.Func{Key: f.Key, Fn: func(c *ui.Ctx) {
		c.Guard("guard", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Dir: ui.Column}, f.Fn, func(c *ui.Ctx, v any, stack []byte) {
			err := &Panic{Value: v, Stack: stack}
			g.r.TurnOff(KindView, g.id, err)
			c.Node(offNode(g.id, err))
		})
	}}
}

func offNode(id string, err error) ui.Node {
	return ui.TextNode(fmt.Sprintf("view %s is turned off: %v", id, err))
}
