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
	Key  // keys in vim notation
	List // a list of strings
	Map  // strings by name
)

// Values are held as bool, int, float64, string (String, Enum and Key), time.Duration, []string or map[string]string.
var goTypes = map[OptionType]string{Bool: "bool", Int: "int", Float: "float64", String: "string", Enum: "string", Duration: "time.Duration", Key: "string",
	List: "[]string", Map: "map[string]string"}

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
	LayerFile // a file's own conventions: .editorconfig, .gitattributes, a modeline; keyed by buffer
	LayerBuffer
	LayerWindow
)

var layerNames = [...]string{"default", "user", "lua", "project", "session", "file", "buffer", "window"}

func (l Layer) String() string { return layerNames[l] }

func (l Layer) scope() Scope {
	switch l {
	case LayerUser, LayerLua:
		return ScopeUser
	case LayerProject:
		return ScopeProject
	case LayerSession:
		return ScopeSession
	case LayerFile, LayerBuffer:
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
	case LayerFile, LayerBuffer:
		return a.Buffer
	case LayerWindow:
		return a.Window
	}
	return ""
}

type OptionSpec struct {
	Name    string // "editor.line_numbers"; the section is the prefix
	Section string // the settings page's section, when it differs from the prefix
	Type    OptionType
	Values  []string // an Enum's values
	// Choices are the values a String or a List's items suggest, worked out when asked (the themes there are
	// now); other values are still allowed, as a theme file can come later.
	Choices  func() []string
	Min, Max float64 // an Int's or Float's range, when Max > Min
	Default  any
	// Validate refuses a value of the right type that's still wrong: a malformed rule in a list. ListOf and MapOf
	// make one that an editor can also ask about a single item or entry.
	Validate func(v any) error
	// KeyLabel and ValueLabel name a Map's two sides: "name" and "server".
	KeyLabel, ValueLabel string
	Help                 string
	Scopes               Scope
	Aliases              []string // vim names
	Source               string
}

// Check says why v isn't a value s can hold: the wrong type, out of range, or not one of an Enum's values.
func (s OptionSpec) Check(v any) error {
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
	case List:
		_, ok = v.([]string)
	case Map:
		_, ok = v.(map[string]string)
	}
	if !ok {
		return fmt.Errorf("%s is a %s, not %T", s.Name, goTypes[s.Type], v)
	}
	if s.Validate != nil {
		if err := s.Validate(v); err != nil {
			return fmt.Errorf("%s: %w", s.Name, err)
		}
	}
	return nil
}

// ListOf is a List's Validate that checks each item with check.
func ListOf(check func(item string) error) func(any) error {
	return func(v any) error {
		for _, s := range v.([]string) {
			if err := check(s); err != nil {
				return err
			}
		}
		return nil
	}
}

// MapOf is a Map's Validate that checks each entry with check, in key order.
func MapOf(check func(key, value string) error) func(any) error {
	return func(v any) error {
		m := v.(map[string]string)
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			if err := check(k, m[k]); err != nil {
				return err
			}
		}
		return nil
	}
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
	// SetFrom sets a value with where it came from: "init.lua:12", a settings file's path.
	SetFrom(name string, l Layer, at At, v any, source string) error
	Unset(name string, l Layer, at At)
	Source(name string, at At) string               // where the value Get returns was set; "" for a default
	Lookup(name string, l Layer, at At) (any, bool) // the value set at exactly l, if any
	Spec(name string) (OptionSpec, bool)            // by name or alias
	Specs() []OptionSpec                            // sorted by name
	Watch(name string, fn func()) (cancel func())
}

type valueKey struct {
	name  string
	layer Layer
	at    string
}

type value struct {
	v      any
	source string
}

type options struct {
	mu      sync.Mutex
	specs   stacks[string, OptionSpec]
	aliases map[string]string
	values  map[valueKey]value
	watches map[string]map[int]func()
	next    int
}

func newOptions() *options {
	return &options{specs: stacks[string, OptionSpec]{}, aliases: map[string]string{}, values: map[valueKey]value{}, watches: map[string]map[int]func(){}}
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
	v, l := o.winner(s, at)
	if l == LayerDefault {
		return s.Default, l, nil
	}
	return v.v, l, nil
}

func (o *options) winner(s OptionSpec, at At) (value, Layer) {
	for l := LayerWindow; l > LayerDefault; l-- {
		k := at.key(l)
		if l >= LayerProject && k == "" {
			continue
		}
		if v, ok := o.values[valueKey{s.Name, l, k}]; ok {
			return v, l
		}
	}
	return value{}, LayerDefault
}

func (o *options) Source(name string, at At) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	s, ok := o.spec(name)
	if !ok {
		return ""
	}
	v, _ := o.winner(s, at)
	return v.source
}

func (o *options) Lookup(name string, l Layer, at At) (any, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	s, ok := o.spec(name)
	if !ok {
		return nil, false
	}
	v, ok := o.values[valueKey{s.Name, l, at.key(l)}]
	return v.v, ok
}

func (o *options) Spec(name string) (OptionSpec, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.spec(name)
}

func (o *options) Set(name string, l Layer, at At, v any) error { return o.SetFrom(name, l, at, v, "") }

func (o *options) SetFrom(name string, l Layer, at At, v any, source string) error {
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
	if err := s.Check(v); err != nil {
		o.mu.Unlock()
		return err
	}
	o.values[valueKey{s.Name, l, at.key(l)}] = value{v, source}
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
	if err := s.Check(s.Default); err != nil {
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
