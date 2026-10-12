package registry

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"time"
)

type OptionType int

const (
	Bool OptionType = iota
	Int
	Float
	String
	Enum
	Duration
	Key // keys in vim notation
)

// Values are held as bool, int, float64, string (String, Enum and Key) or time.Duration.
var goTypes = map[OptionType]string{Bool: "bool", Int: "int", Float: "float64", String: "string", Enum: "string", Duration: "time.Duration", Key: "string"}

// Scope is where an option may be set, as a set of bits.
type Scope uint8

const (
	ScopeUser Scope = 1 << iota // the user file and init.lua
	ScopeProject
	ScopeSession
	ScopeBuffer
	ScopeWindow
)

// Layer is where a value came from. Later layers win.
type Layer int

const (
	LayerDefault Layer = iota
	LayerUser          // ~/.config/oriel/settings.toml
	LayerLua           // init.lua
	LayerProject       // <repo>/.oriel/settings.toml
	LayerSession
	LayerBuffer
	LayerWindow
)

var layerNames = [...]string{"default", "user", "lua", "project", "session", "buffer", "window"}

func (l Layer) String() string { return layerNames[l] }

func (l Layer) scope() Scope {
	switch l {
	case LayerUser, LayerLua:
		return ScopeUser
	case LayerProject:
		return ScopeProject
	case LayerSession:
		return ScopeSession
	case LayerBuffer:
		return ScopeBuffer
	case LayerWindow:
		return ScopeWindow
	}
	return 0
}

// At is where an option is read or set. A layer only reads the field it is keyed by.
type At struct {
	Project string // the project's root
	Session string
	Buffer  string
	Window  string
}

func (a At) key(l Layer) string {
	switch l {
	case LayerProject:
		return a.Project
	case LayerSession:
		return a.Session
	case LayerBuffer:
		return a.Buffer
	case LayerWindow:
		return a.Window
	}
	return ""
}

type OptionSpec struct {
	Name     string // "editor.line_numbers"; the section is the prefix
	Section  string // the settings page's section, when it differs from the prefix
	Type     OptionType
	Values   []string // an Enum's values
	Min, Max float64  // an Int's or Float's range, when Max > Min
	Default  any
	Help     string
	Scopes   Scope
	Aliases  []string // vim names
	Source   string
}

func (s OptionSpec) check(v any) error {
	ok := false
	switch s.Type {
	case Bool:
		_, ok = v.(bool)
	case Int:
		var n int
		if n, ok = v.(int); ok {
			return s.inRange(float64(n))
		}
	case Float:
		var f float64
		if f, ok = v.(float64); ok {
			return s.inRange(f)
		}
	case String, Key:
		_, ok = v.(string)
	case Enum:
		var e string
		if e, ok = v.(string); ok && !slices.Contains(s.Values, e) {
			return fmt.Errorf("%s is one of %s, not %q", s.Name, strings.Join(s.Values, ", "), e)
		}
	case Duration:
		_, ok = v.(time.Duration)
	}
	if !ok {
		return fmt.Errorf("%s is a %s, not %T", s.Name, goTypes[s.Type], v)
	}
	return nil
}

func (s OptionSpec) inRange(f float64) error {
	if s.Max > s.Min && (f < s.Min || f > s.Max || math.IsNaN(f)) {
		return fmt.Errorf("%s is between %v and %v, not %v", s.Name, s.Min, s.Max, f)
	}
	return nil
}

// Options holds every option's values, layer by layer.
type Options interface {
	// Get resolves name (or an alias) at a place: the value and the layer it came from.
	Get(name string, at At) (any, Layer, error)
	Set(name string, l Layer, at At, v any) error
	Unset(name string, l Layer, at At)
	Specs() []OptionSpec // sorted by name
	Watch(name string, fn func()) (cancel func())
}

type valueKey struct {
	name  string
	layer Layer
	at    string
}

type options struct {
	mu      sync.Mutex
	specs   stacks[string, OptionSpec]
	aliases map[string]string
	values  map[valueKey]any
	watches map[string]map[int]func()
	next    int
}

func newOptions() *options {
	return &options{specs: stacks[string, OptionSpec]{}, aliases: map[string]string{}, values: map[valueKey]any{}, watches: map[string]map[int]func(){}}
}

func (o *options) spec(name string) (OptionSpec, bool) {
	if n, ok := o.aliases[name]; ok {
		name = n
	}
	return o.specs.top(name)
}

func (o *options) Get(name string, at At) (any, Layer, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	s, ok := o.spec(name)
	if !ok {
		return nil, 0, fmt.Errorf("no option %q", name)
	}
	for l := LayerWindow; l > LayerDefault; l-- {
		k := at.key(l)
		if l >= LayerProject && k == "" {
			continue
		}
		if v, ok := o.values[valueKey{s.Name, l, k}]; ok {
			return v, l, nil
		}
	}
	return s.Default, LayerDefault, nil
}

func (o *options) Set(name string, l Layer, at At, v any) error {
	o.mu.Lock()
	s, ok := o.spec(name)
	switch {
	case !ok:
		o.mu.Unlock()
		return fmt.Errorf("no option %q", name)
	case l == LayerDefault || s.Scopes&l.scope() == 0:
		o.mu.Unlock()
		return fmt.Errorf("%s can't be set at the %s layer", s.Name, l)
	case l >= LayerProject && at.key(l) == "":
		o.mu.Unlock()
		return fmt.Errorf("setting %s at the %s layer needs a %s", s.Name, l, l)
	}
	if err := s.check(v); err != nil {
		o.mu.Unlock()
		return err
	}
	o.values[valueKey{s.Name, l, at.key(l)}] = v
	fns := o.watchers(s.Name)
	o.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
	return nil
}

func (o *options) Unset(name string, l Layer, at At) {
	o.mu.Lock()
	s, ok := o.spec(name)
	if !ok {
		o.mu.Unlock()
		return
	}
	k := valueKey{s.Name, l, at.key(l)}
	_, had := o.values[k]
	delete(o.values, k)
	fns := o.watchers(s.Name)
	o.mu.Unlock()
	if had {
		for _, fn := range fns {
			fn()
		}
	}
}

func (o *options) watchers(name string) []func() {
	ids := make([]int, 0, len(o.watches[name]))
	for id := range o.watches[name] {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	fns := make([]func(), len(ids))
	for i, id := range ids {
		fns[i] = o.watches[name][id]
	}
	return fns
}

func (o *options) Specs() []OptionSpec {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]OptionSpec, 0, len(o.specs))
	for name := range o.specs {
		s, _ := o.specs.top(name)
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b OptionSpec) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func (o *options) Watch(name string, fn func()) (cancel func()) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if n, ok := o.aliases[name]; ok {
		name = n
	}
	if o.watches[name] == nil {
		o.watches[name] = map[int]func(){}
	}
	id := o.next
	o.next++
	o.watches[name][id] = fn
	return func() {
		o.mu.Lock()
		defer o.mu.Unlock()
		delete(o.watches[name], id)
	}
}

func (o *options) register(s OptionSpec) (Disposable, error) {
	if err := s.check(s.Default); err != nil {
		return nil, fmt.Errorf("default: %w", err)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	tok := o.specs.push(s.Name, s)
	for _, a := range s.Aliases {
		o.aliases[a] = s.Name
	}
	return disposeOnce(func() {
		o.mu.Lock()
		defer o.mu.Unlock()
		o.specs.remove(s.Name, tok)
		if _, ok := o.specs.top(s.Name); !ok {
			for a, n := range o.aliases {
				if n == s.Name {
					delete(o.aliases, a)
				}
			}
		}
	}), nil
}
