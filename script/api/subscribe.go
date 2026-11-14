package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/registry/services"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
	"github.com/liamg/oriel/watch"
)

//oriel:api doc="Subscribe to a source's readings, kept fresh by a freshness manager while the subscription lasts and read by nothing once no one subscribes: name is a source (oriel.sources() lists them: files, git.status, git.counted, git.remote, and plugins' own). spec: session ('current' by default, a session id, or 'app' for the app's own manager), the source's arguments (dir for git.*, path and tree for files), max_age (seconds, or a duration such as '5m': how stale a reading may be; 0 reads a watched source as soon as its changes settle), background ('pause' by default: nothing while the window is behind another app, then one catch-up read; 'slow', every slow seconds; 'keep'), on_change = fn(reading, err). Returns a handle: h:get() (the latest reading, or nil and why), h:pause(), h:resume(), h:refresh(), h:close(). It ends with the plugin." returns=table
func (r Root) Subscribe(name string, spec script.Table) (script.Table, error) {
	a := r.a
	w, ok := services.WatchesOf(a.Host)
	if !ok {
		return nil, errors.New("oriel.subscribe: no freshness managers")
	}
	named, ok := w.Source(name)
	if !ok {
		return nil, fmt.Errorf("oriel.subscribe: no source %q (oriel.sources() lists them)", name)
	}
	var id core.SessionID
	switch s := str(spec, "session"); s {
	case "", "current":
		id = a.Host.State().Focus
	case "app":
	default:
		id = core.SessionID(s)
	}
	m := w.For(id)
	if m == nil {
		return nil, fmt.Errorf("oriel.subscribe: no session %q", id)
	}
	args := map[string]any{}
	for k, v := range spec {
		args[k] = v
	}
	src, err := named.Make(id, args)
	if err != nil {
		return nil, fmt.Errorf("oriel.subscribe %s: %w", name, err)
	}
	need, err := needOf(spec)
	if err != nil {
		return nil, fmt.Errorf("oriel.subscribe %s: %w", name, err)
	}
	fn, _ := spec["on_change"].(*script.Fn)
	var sub watch.Sub[any]
	sub = watch.SubscribeFunc(m, src, need, "lua: "+a.Plugin.ID, func() {
		if fn == nil {
			return
		}
		a.Host.Do(func() {
			v, _, ok := sub.Get()
			if !ok {
				a.later(fn, nil, errText(sub.Err()))
				return
			}
			a.later(fn, luaValue(v))
		})
	})
	a.own(registry.DisposeFunc(sub.Close))
	method := func(f func()) script.GoFunc {
		return script.Method(func(*script.Args) ([]any, error) {
			f()
			return nil, nil
		})
	}
	return script.Table{
		"get": script.GoFunc(script.Method(func(*script.Args) ([]any, error) {
			v, _, ok := sub.Get()
			if !ok {
				return []any{nil, errText(sub.Err())}, nil
			}
			return []any{luaValue(v)}, nil
		})),
		"pause":   method(sub.Pause),
		"resume":  method(sub.Resume),
		"refresh": method(sub.Refresh),
		"close":   method(sub.Close),
	}, nil
}

//oriel:api doc="Add a source oriel.subscribe can name, kept fresh only while something subscribes to it: spec has name, and files (paths; { path = dir, tree = true } for a folder and all under it), or poll (seconds, or a duration such as '5m': the most often it's read), and read = fn(args, changed), which returns the reading (a plain value or table). args are the subscription's spec; changed are the files that changed since the last read. read runs on the loop: keep it quick."
func (r Root) Source(spec script.Table, where string) error {
	a := r.a
	name := str(spec, "name")
	w, ok := services.WatchesOf(a.Host)
	if !ok {
		return errors.New("oriel.source: no freshness managers")
	}
	if name == "" {
		return errors.New("oriel.source needs a name")
	}
	if strings.HasPrefix(name, "git.") || name == "files" {
		return fmt.Errorf("oriel.source: %q is taken", name)
	}
	read, _ := spec["read"].(*script.Fn)
	if read == nil {
		return fmt.Errorf("oriel.source %s: no read function", name)
	}
	var files []watch.Files
	for _, f := range listOf(spec["files"]) {
		switch f := f.(type) {
		case string:
			files = append(files, watch.Files{Path: a.Plugin.Path(f)})
		case script.Table:
			deep, _ := f["tree"].(bool)
			files = append(files, watch.Files{Path: a.Plugin.Path(str(f, "path")), Tree: deep})
		}
	}
	every, err := durationOf(spec["poll"])
	if err != nil {
		return fmt.Errorf("oriel.source %s: poll: %w", name, err)
	}
	if len(files) == 0 && every == 0 {
		return fmt.Errorf("oriel.source %s: needs files to watch, or poll", name)
	}
	plugin := a.Plugin.ID
	a.own(w.Register(services.Named{Name: name, Doc: "a plugin's: " + where, Make: func(id core.SessionID, args map[string]any) (watch.Source[any], error) {
		key, _ := json.Marshal(args)
		return watch.Source[any]{Key: "lua:" + plugin + ":" + name + ":" + string(key), Files: files, Poll: every > 0,
			Interval: func(maxAge time.Duration) time.Duration { return max(maxAge, every) },
			Read: func(rd watch.Reader) (any, error) {
				changed := make([]any, len(rd.Changed()))
				for i, p := range rd.Changed() {
					changed[i] = p
				}
				t := script.Table{}
				for k, v := range args {
					t[k] = v
				}
				type result struct {
					v   any
					err error
				}
				got := make(chan result, 1)
				a.Host.Do(func() {
					out, err := a.VM.Call(read, t, changed)
					var v any
					if len(out) > 0 {
						v = out[0]
					}
					got <- result{v, err}
				})
				select {
				case res := <-got:
					return res.v, res.err
				case <-rd.Context().Done():
					return nil, rd.Context().Err()
				}
			}}, nil
	}}))
	return nil
}

//oriel:api doc="The names of the sources oriel.subscribe takes." returns=table
func (r Root) Sources() []any {
	var out []any
	if w, ok := services.WatchesOf(r.a.Host); ok {
		for _, n := range w.Names() {
			out = append(out, n)
		}
	}
	return out
}

// needOf is a subscription's Need from its spec.
func needOf(spec script.Table) (watch.Need, error) {
	var n watch.Need
	var err error
	if n.MaxAge, err = durationOf(spec["max_age"]); err != nil {
		return n, fmt.Errorf("max_age: %w", err)
	}
	if n.Slow, err = durationOf(spec["slow"]); err != nil {
		return n, fmt.Errorf("slow: %w", err)
	}
	switch b := str(spec, "background"); b {
	case "", "pause":
		n.Background = watch.Pause
	case "slow":
		n.Background = watch.Slow
	case "keep":
		n.Background = watch.Keep
	default:
		return n, fmt.Errorf("background %q: pause, slow or keep", b)
	}
	return n, nil
}

// durationOf is seconds as a number, or a duration as a string ("5m"); 0 for none.
func durationOf(v any) (time.Duration, error) {
	switch v := v.(type) {
	case nil:
		return 0, nil
	case float64:
		return time.Duration(v * float64(time.Second)), nil
	case string:
		return time.ParseDuration(v)
	}
	return 0, fmt.Errorf("%v is neither seconds nor a duration", v)
}

func listOf(v any) []any {
	switch v := v.(type) {
	case []any:
		return v
	case script.Table:
		var keys []string
		for k := range v {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		var out []any
		for _, k := range keys {
			out = append(out, v[k])
		}
		return out
	case string:
		return []any{v}
	}
	return nil
}

// luaValue is a reading as Lua takes it: maps as tables, all the way down.
func luaValue(v any) any {
	switch v := v.(type) {
	case map[string]any:
		t := script.Table{}
		for k, x := range v {
			t[k] = luaValue(x)
		}
		return t
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = luaValue(x)
		}
		return out
	}
	return v
}

func errText(err error) any {
	if err == nil {
		return nil
	}
	return err.Error()
}
