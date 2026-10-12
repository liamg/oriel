package registry

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/liamg/oriel/ui"
)

// Registry is where every tier contributes, and where contributions are looked up.
// Registering an id that is already registered overrides it until the override is disposed.
type Registry interface {
	Command(c Command) Disposable
	Keymap(k Keymap) Disposable
	View(id string, v View) Disposable

	Run(ctx Ctx, id string, args Args) error
	Key(mode, keys string) (Keymap, bool)
	ViewByID(id string) (View, bool)
}

type Disposable interface{ Dispose() }

type Ctx interface{ context.Context }

type Args map[string]any

type Command struct {
	ID    string // "git.commit"
	Title string
	Run   func(ctx Ctx, args Args) error
}

// Keymap binds keys, in vim notation, to a command in one mode.
type Keymap struct {
	Mode    string // "n", "i", or a panel's own mode
	Keys    string // "x", "<C-w>"
	Command string
	Args    Args
}

type View interface {
	Build(ctx ViewCtx) ui.Node // pure: state in, tree out
}

type ViewCtx struct {
	Width, Height float32
}

type ViewFunc func(ViewCtx) ui.Node

func (f ViewFunc) Build(ctx ViewCtx) ui.Node { return f(ctx) }

type keymapKey struct{ mode, keys string }

type entry[T any] struct {
	token int
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

type reg struct {
	mu       sync.Mutex
	next     int
	commands stacks[string, Command]
	keymaps  stacks[keymapKey, Keymap]
	views    stacks[string, View]
}

func New() Registry {
	return &reg{commands: stacks[string, Command]{}, keymaps: stacks[keymapKey, Keymap]{}, views: stacks[string, View]{}}
}

type disposeFunc func()

func (f disposeFunc) Dispose() { f() }

func push[K comparable, T any](r *reg, s stacks[K, T], k K, v T) Disposable {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next++
	tok := r.next
	s[k] = append(s[k], entry[T]{tok, v})
	var once sync.Once
	return disposeFunc(func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			s[k] = slices.DeleteFunc(s[k], func(e entry[T]) bool { return e.token == tok })
			if len(s[k]) == 0 {
				delete(s, k)
			}
		})
	})
}

func (r *reg) Command(c Command) Disposable { return push(r, r.commands, c.ID, c) }

func (r *reg) Keymap(k Keymap) Disposable {
	return push(r, r.keymaps, keymapKey{k.Mode, k.Keys}, k)
}

func (r *reg) View(id string, v View) Disposable { return push(r, r.views, id, v) }

func (r *reg) Run(ctx Ctx, id string, args Args) error {
	r.mu.Lock()
	c, ok := r.commands.top(id)
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("no command %q", id)
	}
	return c.Run(ctx, args)
}

func (r *reg) Key(mode, keys string) (Keymap, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.keymaps.top(keymapKey{mode, keys})
}

func (r *reg) ViewByID(id string) (View, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.views.top(id)
}
