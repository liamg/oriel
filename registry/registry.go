package registry

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/liamg/oriel/registry/keymap"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// Registry is where every tier contributes, and where contributions are looked up.
// Registering an id that is already registered overrides it until the override is disposed.
type Registry interface {
	Command(c Command) Disposable
	Keymap(k Keymap) Disposable
	Option(o OptionSpec) Disposable // panics if the default doesn't fit the spec
	View(id string, v View) Disposable
	Theme(s theme.Source) Disposable

	Run(ctx Ctx, id string, args Args) error
	Commands() []Command // the registration that wins for each id, sorted by id
	Keymaps() []Keymap   // the registration that wins for each mode and keys, sorted by mode, then keys
	// Lookup finds the mapping for keys in the first of modes that has one, and whether any of modes has a longer
	// mapping that starts with keys.
	Lookup(modes []string, keys []string) (exact *Keymap, more bool)
	ViewByID(id string) (View, bool)
	Themes() []theme.Source // in registration order; a later theme with the same name wins
	Options() Options
}

type Disposable interface{ Dispose() }

type Ctx interface{ context.Context }

type Args map[string]any

type Command struct {
	ID     string // "git.commit"
	Title  string
	Run    func(ctx Ctx, args Args) error
	Source string // who registered it: "builtin:git", "init.lua:14", a plugin's id
}

// Keymap binds keys, in vim notation, to a command in one mode.
type Keymap struct {
	Mode    string // "n", "i", or a panel's own mode ("requests")
	Keys    string // "x", "<C-w>", "<Leader>rr"; the registry rewrites it in canonical notation, <Leader> expanded
	Command string
	Args    Args
	Desc    string
	Source  string
}

type View interface {
	Build(ctx ViewCtx) ui.Node // pure: state in, tree out
}

type ViewCtx struct {
	Width, Height float32
}

type ViewFunc func(ViewCtx) ui.Node

func (f ViewFunc) Build(ctx ViewCtx) ui.Node { return f(ctx) }

var tokens atomic.Int64

type entry[T any] struct {
	token int64
	value T
}

// stacks holds every registration of each id; the last one wins.
type stacks[K comparable, T any] map[K][]entry[T]

func (s stacks[K, T]) top(k K) (T, bool) {
	es := s[k]
	if len(es) == 0 {
		var zero T
		return zero, false
	}
	return es[len(es)-1].value, true
}

func (s stacks[K, T]) push(k K, v T) int64 {
	tok := tokens.Add(1)
	s[k] = append(s[k], entry[T]{tok, v})
	return tok
}

func (s stacks[K, T]) remove(k K, tok int64) {
	s[k] = slices.DeleteFunc(s[k], func(e entry[T]) bool { return e.token == tok })
	if len(s[k]) == 0 {
		delete(s, k)
	}
}

type disposeFunc func()

func (f disposeFunc) Dispose() { f() }

func disposeOnce(fn func()) Disposable {
	var once sync.Once
	return disposeFunc(func() { once.Do(fn) })
}

const sep = "\x00"

type keymapKey struct{ mode, seq string }

type reg struct {
	mu       sync.Mutex
	commands stacks[string, Command]
	keymaps  stacks[keymapKey, Keymap]
	prefixes map[keymapKey]int // how many mapped sequences in a mode extend each proper prefix
	views    stacks[string, View]
	themes   []entry[theme.Source]
	options  *options
}

func New() Registry {
	r := &reg{
		commands: stacks[string, Command]{},
		keymaps:  stacks[keymapKey, Keymap]{},
		prefixes: map[keymapKey]int{},
		views:    stacks[string, View]{},
		options:  newOptions(),
	}
	for _, o := range builtinOptions {
		r.Option(o)
	}
	return r
}

var builtinOptions = []OptionSpec{
	{Name: "keymap.leader", Type: Key, Default: " ", Scopes: ScopeUser, Source: "builtin:registry",
		Help: "What <Leader> stands for in keymaps, read when each keymap is defined."},
	{Name: "keymap.timeout", Type: Bool, Default: true, Scopes: ScopeUser, Aliases: []string{"timeout", "to"}, Source: "builtin:registry",
		Help: "Stop waiting for the rest of a mapped key sequence after keymap.timeoutlen."},
	{Name: "keymap.timeoutlen", Type: Duration, Default: time.Second, Scopes: ScopeUser, Aliases: []string{"timeoutlen", "tm"}, Source: "builtin:registry",
		Help: "How long to wait for a mapped key sequence to complete."},
}

func (r *reg) Command(c Command) Disposable {
	r.mu.Lock()
	defer r.mu.Unlock()
	tok := r.commands.push(c.ID, c)
	return disposeOnce(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.commands.remove(c.ID, tok)
	})
}

func (r *reg) Keymap(k Keymap) Disposable {
	var seq []string
	leader, _, _ := r.options.Get("keymap.leader", At{})
	for _, t := range keymap.Parse(k.Keys) {
		if t == "<Leader>" {
			seq = append(seq, keymap.Parse(leader.(string))...)
		} else {
			seq = append(seq, t)
		}
	}
	k.Keys = keymap.Format(seq)
	key := keymapKey{k.Mode, strings.Join(seq, sep)}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.keymaps.top(key); !ok {
		r.addPrefixes(k.Mode, seq, 1)
	}
	tok := r.keymaps.push(key, k)
	return disposeOnce(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.keymaps.remove(key, tok)
		if _, ok := r.keymaps.top(key); !ok {
			r.addPrefixes(k.Mode, seq, -1)
		}
	})
}

func (r *reg) addPrefixes(mode string, seq []string, n int) {
	for i := 1; i < len(seq); i++ {
		p := keymapKey{mode, strings.Join(seq[:i], sep)}
		if r.prefixes[p] += n; r.prefixes[p] == 0 {
			delete(r.prefixes, p)
		}
	}
}

func (r *reg) Option(o OptionSpec) Disposable {
	d, err := r.options.register(o)
	if err != nil {
		panic(fmt.Sprintf("registry: option %s: %v", o.Name, err))
	}
	return d
}

func (r *reg) View(id string, v View) Disposable {
	r.mu.Lock()
	defer r.mu.Unlock()
	tok := r.views.push(id, v)
	return disposeOnce(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.views.remove(id, tok)
	})
}

func (r *reg) Theme(s theme.Source) Disposable {
	r.mu.Lock()
	defer r.mu.Unlock()
	tok := tokens.Add(1)
	r.themes = append(r.themes, entry[theme.Source]{tok, s})
	return disposeOnce(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.themes = slices.DeleteFunc(r.themes, func(e entry[theme.Source]) bool { return e.token == tok })
	})
}

func (r *reg) Themes() []theme.Source {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]theme.Source, len(r.themes))
	for i, e := range r.themes {
		out[i] = e.value
	}
	return out
}

func (r *reg) Run(ctx Ctx, id string, args Args) error {
	r.mu.Lock()
	c, ok := r.commands.top(id)
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("no command %q", id)
	}
	if c.Run == nil {
		return fmt.Errorf("command %q has no Run", id)
	}
	return c.Run(ctx, args)
}

func (r *reg) Commands() []Command {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Command, 0, len(r.commands))
	for id := range r.commands {
		c, _ := r.commands.top(id)
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b Command) int { return strings.Compare(a.ID, b.ID) })
	return out
}

func (r *reg) Keymaps() []Keymap {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Keymap, 0, len(r.keymaps))
	for k := range r.keymaps {
		km, _ := r.keymaps.top(k)
		out = append(out, km)
	}
	slices.SortFunc(out, func(a, b Keymap) int {
		if c := strings.Compare(a.Mode, b.Mode); c != 0 {
			return c
		}
		return strings.Compare(a.Keys, b.Keys)
	})
	return out
}

func (r *reg) Lookup(modes []string, keys []string) (*Keymap, bool) {
	seq := strings.Join(keys, sep)
	r.mu.Lock()
	defer r.mu.Unlock()
	var exact *Keymap
	more := false
	for _, m := range modes {
		if km, ok := r.keymaps.top(keymapKey{m, seq}); ok && exact == nil {
			exact = &km
		}
		if r.prefixes[keymapKey{m, seq}] > 0 {
			more = true
		}
	}
	return exact, more
}

func (r *reg) ViewByID(id string) (View, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.views.top(id)
}

func (r *reg) Options() Options { return r.options }
