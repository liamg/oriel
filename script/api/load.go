package api

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/script"
	"github.com/liamg/oriel/ui/widget"
)

// luaLoads are a VM's oriel.load handles, by id, for oriel.ui.async and busy to read.
type luaLoads struct {
	mu   sync.Mutex
	n    int
	byID map[string]*plugin.Load[any]
}

func (a *APIs) loads() *luaLoads {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.loading == nil {
		a.loading = &luaLoads{byID: map[string]*plugin.Load[any]{}}
	}
	return a.loading
}

func (ls *luaLoads) add(l *plugin.Load[any]) string {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	ls.n++
	id := fmt.Sprintf("load.%d", ls.n)
	ls.byID[id] = l
	return id
}

// state is the load a node names with load = handle.
func (ls *luaLoads) state(n script.Table) (widget.LoadState, bool) {
	h, ok := n["load"].(script.Table)
	if !ok {
		return widget.LoadState{}, false
	}
	ls.mu.Lock()
	l, ok := ls.byID[str(h, "id")]
	ls.mu.Unlock()
	if !ok {
		return widget.LoadState{}, false
	}
	return l.State(), true
}

//oriel:api doc="A load for oriel.ui.async and busy: fn(done) starts the work on the loop and calls done(value) when it has it, or done(nil, err) when it failed (from an oriel.http.request answer, say). opts: timeout (seconds; a load still running then fails). Returns a handle: h:start() (once; does nothing while loading or loaded), h:reload() (keeps the value on show), h:reset(), h:value(), h:state() ({ phase = idle|pending|ready|failed, error, stale, note, elapsed })." optional=opts returns=table
func (r Root) Load(fn *script.Fn, opts script.Table) (script.Table, error) {
	a := r.a
	if fn == nil {
		return nil, errors.New("oriel.load needs a function that does the work")
	}
	o := plugin.LoadOpts{}
	if t, ok := opts["timeout"].(float64); ok {
		o.Timeout = time.Duration(t * float64(time.Second))
	}
	l := plugin.NewLoad(a.Host, func(ctx context.Context, report func(string, float64)) (any, error) {
		type result struct {
			v   any
			err error
		}
		got := make(chan result, 1)
		a.Host.Do(func() {
			var once sync.Once
			done := script.GoFunc(func(c *script.Args) ([]any, error) {
				once.Do(func() {
					res := result{v: c.Any(0)}
					if msg := c.Any(1); msg != nil {
						res.err = errors.New(fmt.Sprint(msg))
					}
					got <- res
				})
				return nil, nil
			})
			if _, err := a.VM.Call(fn, done); err != nil {
				once.Do(func() { got <- result{err: err} })
			}
		})
		select {
		case r := <-got:
			return r.v, r.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}, o)
	id := a.loads().add(l)
	method := func(fn func()) script.GoFunc {
		return script.Method(func(*script.Args) ([]any, error) {
			fn()
			a.Host.Redraw()
			return nil, nil
		})
	}
	return script.Table{
		"id":     id,
		"start":  method(l.Start),
		"reload": method(l.Reload),
		"reset":  method(l.Reset),
		"value": script.GoFunc(script.Method(func(*script.Args) ([]any, error) {
			v, _ := l.Value()
			return []any{v}, nil
		})),
		"state": script.GoFunc(script.Method(func(*script.Args) ([]any, error) {
			s := l.State()
			t := script.Table{"phase": s.Phase.String(), "stale": s.Stale, "note": s.Note}
			if s.Err != nil {
				t["error"] = s.Err.Error()
			}
			if u := a.Host.UI(); u != nil && !s.Since.IsZero() {
				t["elapsed"] = u.Now().Sub(s.Since).Seconds()
			}
			return []any{t}, nil
		})),
	}, nil
}
