package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/liamg/oriel/edit/input"
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
	Language(l Language) Disposable
	Hook(h Hook) Disposable
	DeclareEvent(e EventInfo) Disposable
	Contribute(c Contribution) Disposable
	Input(e input.Engine) Disposable // an input engine the editor's input option can pick
	Decorations(id string, p DecorationProvider) Disposable
	Picker(s PickerSource) Disposable
	HelpPages(s HelpSource) Disposable
	HelpLive(name string, fn func(c *ui.Ctx)) Disposable

	Run(ctx Ctx, id string, args Args) error
	// Available says whether id would run with args: it's registered, not turned off, and its Available agrees.
	// The rail and the start view dim what isn't; the omnibox leaves it out.
	Available(id string, args Args) bool
	Commands() []Command // the registration that wins for each id, sorted by id
	Keymaps() []Keymap   // the registration that wins for each mode and keys, sorted by mode, then keys
	// Lookup finds the mapping for keys in the first of modes that has one, and whether any of modes has a longer
	// mapping that starts with keys.
	Lookup(modes []string, keys []string) (exact *Keymap, more bool)
	ViewByID(id string) (View, bool)
	Views() []string                     // every registered view id, sorted
	ViewSource(id string) (string, bool) // who registered the view that wins id
	Themes() ([]theme.Source, int)       // in registration order, a later theme with the same name winning; and a generation that moves when they change
	Languages() ([]Language, int)        // the language that wins each name, in the order they were registered, and a generation
	Hooks() []HookInfo                   // by event, then priority
	Events() []EventInfo                 // declared events, by name
	// Fire runs event's hooks (registry/hook.go): a veto or decision, and the hooks' errors.
	Fire(ctx Ctx, event, subject string, ev any) (Verdict, []error)
	Contributions(kind string) []Contribution // by name
	InputByID(id string) (input.Engine, bool)
	DecorationProviders() []NamedProvider // the winning provider for each id, sorted by id
	Pickers() []PickerSource              // the winning source for each id, by Order then id
	HelpSources() ([]HelpSource, int)
	HelpLiveByName(name string) (func(c *ui.Ctx), bool)
	Inputs() []string // engine ids, sorted
	Options() Options

	// SetDiagnostics replaces owner's diagnostics for file (an absolute path); nil clears them.
	SetDiagnostics(owner, file string, ds []Diagnostic)
	Diagnostics() ([]Diagnostic, int) // by file, then line; and a generation that moves when they change
	DiagnosticsFor(file string) []Diagnostic
	// SetCoverage replaces owner's line coverage for file: hits by 0-based line, 0 for a line never run. nil clears it.
	SetCoverage(owner, file string, hits map[int]int)
	ClearCoverage(owner string)
	CoverageFor(file string) map[int]int // every owner's, a line keeping its largest count

	// OnFault hears about every contribution turned off: one that panicked, or kept failing.
	OnFault(fn func(Fault)) Disposable
	// OnFailure hears about every contribution's error, and warnings: a hook over its time budget.
	OnFailure(fn func(Failure)) Disposable
	// Report says how a contribution's call went, nil for a success: FailLimit errors in a row turn it off.
	Report(kind, id string, err error)
	// TurnOff turns a contribution off with the error that caused it; TurnOn turns it back on.
	TurnOff(kind, id string, err error)
	TurnOn(kind, id string)
	TurnedOff() []Off // sorted by kind, then id
}

type Disposable interface{ Dispose() }

type Ctx interface{ context.Context }

type Args map[string]any

// String is the string argument k, or "".
func (a Args) String(k string) string { s, _ := a[k].(string); return s }

// Int is the numeric argument k: an int from Go, a float64 from Lua or JSON; 0 if it's neither.
func (a Args) Int(k string) int {
	switch n := a[k].(type) {
	case int:
		return n
	case float64:
		return int(n)
	}
	return 0
}

// Bool is the boolean argument k.
func (a Args) Bool(k string) bool { b, _ := a[k].(bool); return b }

type Command struct {
	ID    string // "git.commit"
	Title string
	Run   func(ctx Ctx, args Args) error
	// Available says whether the command can run with args now; nil means it always can. view.open of a view
	// nobody has registered can't.
	Available func(args Args) bool
	Source    string // who registered it: "builtin:git", "init.lua:14", a plugin's id
}

// Keymap binds keys, in vim notation, to a command in one mode. One with no command names a group of the
// keymaps under its keys, for which-key: {Keys: "<Leader>a", Desc: "+agent"}; it's never run.
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

// ViewCtx is what a view is built with: the window's size and, for a view open in the layout, which instance
// of it this is, the project it's for, and whether it has focus.
type ViewCtx struct {
	Width, Height float32
	Instance      string
	Focused       bool
	Project       string // the project the instance was opened for (view.open's project); "" for a session's view
}

// EdgeToEdge is a view drawn right to its pane's edges, with no padding around it: an editor, a terminal.
type EdgeToEdge interface {
	EdgeToEdge()
}

// Titled is a view whose instances name themselves, for their tabs: an editor names its buffer.
type Titled interface {
	Title(instance string) string
}

// Moded is a view with a mode of its own the status line's mode chip shows while it has focus: a terminal's
// TERMINAL or TERM-NORMAL. Style is a theme token for the chip.
type Moded interface {
	ModeLabel(instance string) (label, style string)
}

// Marked is a view whose instances carry a small marker on their tabs: an editor's file's git status letter. Style
// is a theme token.
type Marked interface {
	Mark(instance string) (text, style string)
}

// Persisted is a view whose instances come back when the app starts again on the same directory: the layout keeps
// each one's state, and Revive makes a new instance from it, which takes the old one's place.
type Persisted interface {
	Persist(instance string) (state json.RawMessage, ok bool)
	Revive(state json.RawMessage) (instance string, err error)
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
	mu        sync.Mutex
	commands  stacks[string, Command]
	keymaps   stacks[keymapKey, Keymap]
	sorted    []Keymap          // Keymaps, until one is added or removed
	prefixes  map[keymapKey]int // how many mapped sequences in a mode extend each proper prefix
	views     stacks[string, View]
	inputs    stacks[string, input.Engine]
	decor     stacks[string, DecorationProvider]
	pickers   stacks[string, PickerSource]
	help      []entry[HelpSource]
	helpGen   int
	live      stacks[string, func(c *ui.Ctx)]
	themes    []entry[theme.Source]
	themeGen  int
	languages stacks[string, Language]
	hooks     hooks
	contribs  stacks[string, Contribution]
	langGen   int
	options   *options
	faults    faults
	diags     map[diagKey][]Diagnostic
	diagGen   int
	cover     map[diagKey]map[int]int
}

func New() Registry {
	r := &reg{
		commands:  stacks[string, Command]{},
		keymaps:   stacks[keymapKey, Keymap]{},
		prefixes:  map[keymapKey]int{},
		views:     stacks[string, View]{},
		inputs:    stacks[string, input.Engine]{},
		decor:     stacks[string, DecorationProvider]{},
		pickers:   stacks[string, PickerSource]{},
		languages: stacks[string, Language]{},
		contribs:  stacks[string, Contribution]{},
		live:      stacks[string, func(c *ui.Ctx)]{},
		options:   newOptions(),
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
	r.sorted = nil
	return disposeOnce(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.keymaps.remove(key, tok)
		r.sorted = nil
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

func (r *reg) Input(e input.Engine) Disposable {
	r.mu.Lock()
	defer r.mu.Unlock()
	tok := r.inputs.push(e.ID(), e)
	return disposeOnce(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.inputs.remove(e.ID(), tok)
	})
}

func (r *reg) InputByID(id string) (input.Engine, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inputs.top(id)
}

func (r *reg) Inputs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.inputs))
	for id := range r.inputs {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

func (r *reg) Theme(s theme.Source) Disposable {
	r.mu.Lock()
	defer r.mu.Unlock()
	tok := tokens.Add(1)
	r.themes = append(r.themes, entry[theme.Source]{tok, s})
	r.themeGen++
	return disposeOnce(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.themes = slices.DeleteFunc(r.themes, func(e entry[theme.Source]) bool { return e.token == tok })
		r.themeGen++
	})
}

func (r *reg) Themes() ([]theme.Source, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]theme.Source, len(r.themes))
	for i, e := range r.themes {
		out[i] = e.value
	}
	return out, r.themeGen
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
	if err := r.faults.isOff(KindCommand, id); err != nil {
		return fmt.Errorf("command %q is turned off: %w", id, err)
	}
	err := Call(func() error { return c.Run(ctx, args) })
	if p, ok := err.(*Panic); ok {
		r.faults.fault(Fault{Kind: KindCommand, ID: id, Source: c.Source, Err: p})
	}
	return err
}

func (r *reg) Available(id string, args Args) bool {
	r.mu.Lock()
	c, ok := r.commands.top(id)
	r.mu.Unlock()
	if !ok || c.Run == nil || r.faults.isOff(KindCommand, id) != nil {
		return false
	}
	if c.Available == nil {
		return true
	}
	ok = false
	Call(func() error { ok = c.Available(args); return nil })
	return ok
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
	if r.sorted == nil {
		r.sorted = make([]Keymap, 0, len(r.keymaps))
		for k := range r.keymaps {
			km, _ := r.keymaps.top(k)
			r.sorted = append(r.sorted, km)
		}
		slices.SortFunc(r.sorted, func(a, b Keymap) int {
			if c := strings.Compare(a.Mode, b.Mode); c != 0 {
				return c
			}
			return strings.Compare(a.Keys, b.Keys)
		})
	}
	return slices.Clone(r.sorted)
}

func (r *reg) Lookup(modes []string, keys []string) (*Keymap, bool) {
	seq := strings.Join(keys, sep)
	r.mu.Lock()
	defer r.mu.Unlock()
	var exact *Keymap
	more := false
	for _, m := range modes {
		if km, ok := r.keymaps.top(keymapKey{m, seq}); ok && exact == nil && km.Command != "" {
			exact = &km
		}
		if r.prefixes[keymapKey{m, seq}] > 0 {
			more = true
		}
	}
	return exact, more
}

// ViewByID returns the view under a guard: a panic while it builds turns it off instead of escaping.
func (r *reg) ViewByID(id string) (View, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.views.top(id)
	if !ok {
		return nil, false
	}
	return guardedView{r, id, v}, true
}

func (r *reg) Options() Options { return r.options }

func (r *reg) Views() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.views))
	for id := range r.views {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

func (r *reg) ViewSource(id string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.views.top(id)
	if !ok {
		return "", false
	}
	if sv, ok := v.(interface{ ViewSource() string }); ok {
		return sv.ViewSource(), true
	}
	return "", true
}
