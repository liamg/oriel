package api

import (
	"cmp"
	"fmt"
	"strings"
	"time"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/settingsfile"
	"github.com/liamg/oriel/script"
)

// pendingOption is a value Lua set for an option nothing has registered yet (a plugin's, a later feature's): it's
// kept, and set when the option appears.
type pendingOption struct {
	layer registry.Layer
	at    registry.At
	value any
	where string
}

// optKey is an option's value at one layer and place.
type optKey struct {
	name  string
	layer registry.Layer
	at    registry.At
}

// optPrev is what was there before the config set it.
type optPrev struct {
	v      any
	had    bool
	source string
}

// touch keeps what an option held at l before the config first sets it. The caller holds a.mu.
func (a *APIs) touch(o registry.Options, name string, l registry.Layer, at registry.At) {
	k := optKey{name, l, at}
	if _, ok := a.touched[k]; ok {
		return
	}
	p := optPrev{}
	p.v, p.had = o.Lookup(name, l, at)
	if _, wl, _ := o.Get(name, at); p.had && wl == l {
		p.source = o.Source(name, at)
	}
	a.touched[k] = p
}

// Revert puts back every option value the config set, for a config that failed to load.
func (a *APIs) Revert() {
	o := a.Host.Options()
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, p := range a.touched {
		if p.had {
			o.SetFrom(k.name, k.layer, k.at, p.v, p.source)
		} else {
			o.Unset(k.name, k.layer, k.at)
		}
	}
	a.touched = map[optKey]optPrev{}
}

// Handover unsets the option values a's config set and next's doesn't, as next replaces it.
func (a *APIs) Handover(next *APIs) {
	o := a.Host.Options()
	a.mu.Lock()
	defer a.mu.Unlock()
	next.mu.Lock()
	defer next.mu.Unlock()
	for k := range a.touched {
		if _, ok := next.touched[k]; !ok {
			o.Unset(k.name, k.layer, k.at)
		}
	}
}

// options binds oriel.option (your settings, at the Lua layer), oriel.buffer_option (the current buffer's) and
// oriel.session_option (the session's), each by an option's name or vim alias.
func (a *APIs) options(vm *script.VM) {
	o := a.Host.Options()
	for _, ns := range []string{"option", "buffer_option", "session_option"} {
		where := func() (registry.Layer, registry.At) {
			here := a.Host.Here()
			switch {
			case ns == "buffer_option" && here.Buffer != "":
				return registry.LayerBuffer, registry.At{Buffer: here.Buffer}
			case ns == "session_option" && here.Session != "":
				return registry.LayerSession, registry.At{Session: here.Session}
			}
			return registry.LayerLua, registry.At{}
		}
		vm.Metatable(ns, func(_ *script.Args, key string) (any, error) {
			v, _, err := o.Get(key, a.Host.Here())
			if err != nil {
				if p, ok := a.pending[key]; ok {
					return p.value, nil
				}
				return nil, nil
			}
			return v, nil
		}, func(args *script.Args, key string, v any) error {
			l, at := where()
			sp, ok := o.Spec(key)
			if !ok {
				a.mu.Lock()
				a.pending[key] = pendingOption{l, at, v, args.Where()}
				a.mu.Unlock()
				return nil
			}
			if v == nil {
				a.mu.Lock()
				a.touch(o, sp.Name, l, at)
				a.mu.Unlock()
				o.Unset(sp.Name, l, at)
				return nil
			}
			cv, err := optionValue(sp, v)
			if err != nil {
				return err
			}
			a.mu.Lock()
			a.touch(o, sp.Name, l, at)
			a.mu.Unlock()
			return o.SetFrom(sp.Name, l, at, cv, args.Where())
		})
	}
	vm.Callable("option", func(c *script.Args) ([]any, error) {
		if err := a.check(plugin.CapUI); err != nil {
			return nil, err
		}
		return nil, a.declare(c.Table(0), c.Where())
	})
}

var optionTypes = map[string]registry.OptionType{"bool": registry.Bool, "int": registry.Int, "float": registry.Float, "string": registry.String,
	"enum": registry.Enum, "duration": registry.Duration, "key": registry.Key, "list": registry.List, "map": registry.Map, "table": registry.Table}

// zeros are each type's default when a declaration gives none.
var zeros = map[registry.OptionType]any{registry.Bool: false, registry.Int: 0, registry.Float: 0.0, registry.String: "", registry.Duration: time.Duration(0),
	registry.Key: "", registry.List: []string{}, registry.Map: map[string]string{}, registry.Table: map[string]any{}}

// declare registers an option from oriel.option{ name, type, default, help, values, min, max, step, follows, section, on_change }.
// A plugin's are named plugin.<its name>.… and shown in its own section of the settings page.
func (a *APIs) declare(t script.Table, where string) error {
	name := str(t, "name")
	typ, ok := optionTypes[cmp.Or(str(t, "type"), "string")]
	if name == "" || !ok {
		return fmt.Errorf("an option needs a name and a type (bool, int, float, string, enum, duration, key, list, map or table): %v", t)
	}
	sp := registry.OptionSpec{Name: name, Section: str(t, "section"), Type: typ, Values: plugin.Strings(t["values"]), Help: str(t, "help"),
		Min: num(t["min"]), Max: num(t["max"]), Step: num(t["step"]), Follows: str(t, "follows"), Scopes: registry.ScopeUser, Source: where}
	if !a.Plugin.Trusted() {
		_, plugin, _ := strings.Cut(a.Plugin.ID, "/")
		if !strings.HasPrefix(name, "plugin."+plugin+".") {
			return fmt.Errorf("%s: a plugin's options are named plugin.%s.<option>", name, plugin)
		}
		sp.Section = cmp.Or(sp.Section, plugin)
	}
	sp.Default = zeros[typ]
	if typ == registry.Enum && len(sp.Values) > 0 {
		sp.Default = sp.Values[0]
	}
	if v, ok := t["default"]; ok && v != nil {
		cv, err := optionValue(sp, v)
		if err != nil {
			return err
		}
		sp.Default = cv
	}
	if err := sp.Check(sp.Default); err != nil {
		return fmt.Errorf("%s: default: %w", name, err)
	}
	a.own(a.Host.Option(sp))
	if fn, ok := t["on_change"].(*script.Fn); ok {
		o := a.Host.Options()
		a.own(o.Watch(name, func() {
			a.Host.Do(func() {
				v, _, _ := o.Get(name, registry.At{})
				a.callback(script.Table{"on_change": fn}, "on_change", v)
			})
		}))
	}
	a.ApplyPending()
	return nil
}

// ApplyPending sets the values Lua gave options that have been registered since, and reports how many are still
// waiting.
func (a *APIs) ApplyPending() int {
	o := a.Host.Options()
	a.mu.Lock()
	defer a.mu.Unlock()
	for key, p := range a.pending {
		sp, ok := o.Spec(key)
		if !ok {
			continue
		}
		delete(a.pending, key)
		if cv, err := optionValue(sp, p.value); err == nil {
			a.touch(o, sp.Name, p.layer, p.at)
			o.SetFrom(sp.Name, p.layer, p.at, cv, p.where)
		}
	}
	return len(a.pending)
}

// Pending are the options Lua set that nothing has registered, with where.
func (a *APIs) Pending() map[string]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := map[string]string{}
	for k, p := range a.pending {
		out[k] = p.where
	}
	return out
}

// optionValue is a Lua value as an option of sp's type holds it.
func optionValue(sp registry.OptionSpec, v any) (any, error) {
	switch sp.Type {
	case registry.Int:
		if f, ok := v.(float64); ok && f == float64(int(f)) {
			return int(f), nil
		}
	case registry.Float:
		if f, ok := v.(float64); ok {
			return f, nil
		}
	case registry.Duration:
		switch d := v.(type) {
		case string:
			return registry.ParseDuration(d)
		case float64:
			return time.Duration(d * float64(time.Millisecond)), nil
		}
	case registry.List:
		if t, ok := v.(script.Table); ok && len(t) == 0 {
			return []string{}, nil // {} reads as an empty table, not a list
		}
		if l, ok := settingsfile.Strings(v); ok {
			return l, nil
		}
		return nil, fmt.Errorf("%s takes a list of strings, not %v", sp.Name, v)
	case registry.Map:
		if m, ok := settingsfile.StringMap(v); ok {
			return m, nil
		}
		return nil, fmt.Errorf("%s takes a table of strings by name, not %v", sp.Name, v)
	case registry.Table:
		if m, ok := v.(script.Table); ok {
			return map[string]any(m), nil
		}
		return nil, fmt.Errorf("%s takes a table, not %v", sp.Name, v)
	default:
		return v, nil
	}
	return nil, fmt.Errorf("%s takes a number, not %v", sp.Name, v)
}
