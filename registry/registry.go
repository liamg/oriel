package registry

import (
	"context"
	"fmt"
	"github.com/liamg/oriel/internal/clock"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/liamg/oriel/edit/input"
	"github.com/liamg/oriel/registry/keymap"
	"github.com/liamg/oriel/theme"
)

// Registry is where every tier contributes, and where contributions are looked up, one area each. Registering an
// id that is already registered overrides it until the override is disposed. What's drawn (views, decorations,
// pickers) is registry/uireg's, over these contributions.
type Registry interface {
	Commands
	Keys
	Inputs
	Settings
	Themes
	Languages
	Events
	Contributions
	Help
	Diagnostics
	Faults
	// Placeholder stands for commands, keys, events, omnibox prefixes and contributions something provides when one
	// is first used.
	Placeholder(p Placeholder) Disposable
	// Holder is what provides the placeholder standing for a contribution of kind and name, if one does: what opens
	// a view calls it before it finds the view.
	Holder(kind, name string) (provide func() error, ok bool)
}

// Commands are what runs by id.
type Commands interface {
	Command(c Command) Disposable
	Run(ctx Ctx, id string, args Args) error
	// Available says whether id would run with args: it's registered, not turned off, and its Available agrees.
	// The rail and the start view dim what isn't; the omnibox leaves it out.
	Available(id string, args Args) bool
	Commands() []Command // the registration that wins for each id, sorted by id; shared, not to be changed
}

// Keys are the keymaps.
type Keys interface {
	Keymap(k Keymap) Disposable
	Keymaps() []Keymap // the registration that wins for each mode and keys, sorted by mode, then keys
	// KeymapsFor is Keymaps' entries that run command, in the same order; shared, not to be changed.
	KeymapsFor(command string) []Keymap
	// Lookup finds the mapping for keys in the first of modes that has one, and whether any of modes has a longer
	// mapping that starts with keys.
	Lookup(modes []string, keys []string) (exact *Keymap, more bool)
}

// Inputs are the input engines the editor's input option can pick.
type Inputs interface {
	Input(e input.Engine) Disposable
	InputByID(id string) (input.Engine, bool)
	Inputs() []string // engine ids, sorted
}

// Settings are the options.
type Settings interface {
	Option(o OptionSpec) Disposable // panics if the default doesn't fit the spec
	Options() Options
}

// Themes are the themes.
type Themes interface {
	Theme(s theme.Source) Disposable
	Themes() ([]theme.Source, int) // in registration order, a later theme with the same name winning; and a generation that moves when they change
}

// Languages are the languages files are read as.
type Languages interface {
	Language(l Language) Disposable
	Languages() ([]Language, int) // the language that wins each name, in the order they were registered, and a generation
}

// Events are the hooks and the events they hear.
type Events interface {
	Hook(h Hook) Disposable
	DeclareEvent(e EventInfo) Disposable
	Hooks() []HookInfo   // by event, then priority
	Events() []EventInfo // declared events, by name
	// Fire runs event's hooks (registry/hook.go): a veto or decision, and the hooks' errors.
	Fire(ctx Ctx, event, subject string, ev any) (Verdict, []error)
}

// Contributions are anything else registered by kind and name.
type Contributions interface {
	Contribute(c Contribution) Disposable
	Contributions(kind string) []Contribution            // by name; shared, not to be changed
	Contribution(kind, name string) (Contribution, bool) // the one that wins name
	ContributionsGen() int                               // a generation that moves whenever a contribution comes or goes
	ContributionKinds() []string                         // every kind there's a contribution of, sorted
}

// Help are the help pages.
type Help interface {
	HelpPages(s HelpSource) Disposable
	HelpSources() ([]HelpSource, int)
}

// Diagnostics are the problems and line coverage found in files.
type Diagnostics interface {
	// SetDiagnostics replaces owner's diagnostics for file (an absolute path); nil clears them.
	SetDiagnostics(owner, file string, ds []Diagnostic)
	// Diagnostics are every diagnostic by file, then line, and a generation that moves when they change;
	// DiagnosticsFor are one file's. Both are shared until they change: callers mustn't change them.
	Diagnostics() ([]Diagnostic, int)
	DiagnosticsFor(file string) []Diagnostic
	// SetCoverage replaces owner's line coverage for file: hits by 0-based line, 0 for a line never run. nil clears it.
	SetCoverage(owner, file string, hits map[int]int)
	ClearCoverage(owner string)
	CoverageFor(file string) map[int]int // every owner's, a line keeping its largest count
}

// Faults are the contributions turned off, and their failures.
type Faults interface {
	// OnFault hears about every contribution turned off: one that panicked, or kept failing.
	OnFault(fn func(Fault)) Disposable
	// OnFailure hears about every contribution's error, and warnings: a hook over its time budget.
	OnFailure(fn func(Failure)) Disposable
	// Report says how a contribution's call went, nil for a success: FailLimit errors in a row turn it off.
	Report(kind, id string, err error)
	// TurnOff turns a contribution off with the error that caused it; TurnOn turns it back on.
	TurnOff(kind, id string, err error)
	TurnOn(kind, id string)
	IsOff(kind, id string) error // why it's turned off; nil while it's on
	TurnedOff() []Off            // sorted by kind, then id
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

// Strings is the list argument k: a list of strings from Go or Lua, or one string of comma-separated items.
func (a Args) Strings(k string) []string {
	var out []string
	switch v := a[k].(type) {
	case []string:
		out = append(out, v...)
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	case string:
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

type Command struct {
	ID    string // "git.commit"
	Title string
	Run   func(ctx Ctx, args Args) error
	// Available says whether the command can run with args now; nil means it always can. view.open of a view
	// nobody has registered can't.
	Available func(args Args) bool
	// When makes it available only while the session in front's kind can do this (registry.FrontCan).
	When   Capability
	Source string // who registered it: "builtin:git", "init.lua:14", a plugin's id
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
	// Nowait runs it as soon as it matches, without waiting for a longer mapping in a mode after its own to be typed
	// (vim's <nowait>): a key layer's "[" over the editor's "[c".
	Nowait bool
}

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

// push adds v for k as tok, a token unique in its registry.
func (s stacks[K, T]) push(k K, v T, tok int64) int64 {
	s[k] = append(s[k], entry[T]{tok, v})
	return tok
}

func (s stacks[K, T]) remove(k K, tok int64) {
	s[k] = slices.DeleteFunc(s[k], func(e entry[T]) bool { return e.token == tok })
	if len(s[k]) == 0 {
		delete(s, k)
	}
}

// DisposeFunc is fn as a Disposable: what undoes a registration, a subscription's cancel. It runs fn once, however
// often it's disposed.
func DisposeFunc(fn func()) Disposable { return disposeFunc(sync.OnceFunc(fn)) }

type disposeFunc func()

func (f disposeFunc) Dispose() { f() }

const sep = "\x00"

type keymapKey struct{ mode, seq string }

type reg struct {
	tokens   atomic.Int64 // each registration's, in the order they came
	budget   atomic.Int64 // hooks.budget, read once each time it may have changed; 0 until then
	mu       sync.Mutex
	commands stacks[string, Command]
	keymaps  stacks[keymapKey, Keymap]
	sorted   []Keymap            // Keymaps, until one is added or removed
	byCmd    map[string][]Keymap // KeymapsFor, made with sorted
	prefixes map[keymapKey]int   // how many mapped sequences in a mode extend each proper prefix
	inputs   stacks[string, input.Engine]
	// what the list methods give, made again after a registration changes them; nil until asked
	cmdList    []Command
	contribBy  map[string][]Contribution
	help       []entry[HelpSource]
	helpGen    int
	themes     []entry[theme.Source]
	themeGen   int
	languages  stacks[string, Language]
	hooks      hooks
	contribs   stacks[string, Contribution]
	contribGen int
	langGen    int
	options    *options
	faults     faults
	diags      map[diagKey][]Diagnostic
	diagGen    int
	diagIdx    *diagIndex
	cover      map[diagKey]map[int]int
	holders    []*placeholder
	clock      clock.Clock // what hooks' calls are timed by
}

// Config is a registry's own setup: what it times hooks by.
type Config struct {
	Clock clock.Clock // the real one if nil; a test's moves only when it says
}

func New() Registry { return NewWith(Config{}) }

// NewWith is New, with o.
func NewWith(o Config) Registry {
	r := &reg{
		clock:     clock.Or(o.Clock),
		commands:  stacks[string, Command]{},
		keymaps:   stacks[keymapKey, Keymap]{},
		prefixes:  map[keymapKey]int{},
		inputs:    stacks[string, input.Engine]{},
		languages: stacks[string, Language]{},
		contribs:  stacks[string, Contribution]{},
		options:   newOptions(),
	}
	for _, o := range builtinOptions {
		r.Option(o)
	}
	stale := func() { r.budget.Store(0) }
	r.options.Watch("hooks.budget", stale)
	r.options.Registered(stale)
	return r
}

var builtinOptions = []OptionSpec{
	{Name: "keymap.leader", Type: Key, Default: " ", Scopes: ScopeUser, Source: "builtin:registry",
		Help: "What <Leader> stands for in keymaps, read when each keymap is defined."},
	{Name: "keymap.timeout", Type: Bool, Default: true, Scopes: ScopeUser, Aliases: []string{"timeout", "to"}, Source: "builtin:registry",
		Help: "Stop waiting for the rest of a mapped key sequence after keymap.timeoutlen."},
	{Name: "keymap.timeoutlen", Type: Duration, Default: time.Second, Scopes: ScopeUser, Aliases: []string{"timeoutlen", "tm"}, Source: "builtin:registry",
		Help: "How long to wait for a mapped key sequence to complete."},
	{Name: "hooks.budget", Type: Duration, Default: 2 * time.Millisecond, Scopes: ScopeUser, Source: "builtin:registry",
		Help: "How long a hook may take, on two calls running, before it's a warning: hooks run on the hot path."},
}

func (r *reg) Command(c Command) Disposable {
	r.mu.Lock()
	defer r.mu.Unlock()
	tok := r.commands.push(c.ID, c, r.tokens.Add(1))
	r.cmdList = nil
	return DisposeFunc(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.commands.remove(c.ID, tok)
		r.cmdList = nil
	})
}

func (r *reg) Keymap(k Keymap) Disposable {
	seq := r.keySeq(k.Keys)
	k.Keys = keymap.Format(seq)
	key := keymapKey{k.Mode, strings.Join(seq, sep)}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.keymaps.top(key); !ok {
		r.addPrefixes(k.Mode, seq, 1)
	}
	tok := r.keymaps.push(key, k, r.tokens.Add(1))
	r.sorted = nil
	return DisposeFunc(func() {
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

func (r *reg) Input(e input.Engine) Disposable {
	r.mu.Lock()
	defer r.mu.Unlock()
	tok := r.inputs.push(e.ID(), e, r.tokens.Add(1))
	return DisposeFunc(func() {
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
	tok := r.tokens.Add(1)
	r.themes = append(r.themes, entry[theme.Source]{tok, s})
	r.themeGen++
	return DisposeFunc(func() {
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
	waiter := claim(ctx)
	err := r.run(ctx, id, args)
	waiter.report(err)
	return err
}

func (r *reg) run(ctx Ctx, id string, args Args) error {
	r.mu.Lock()
	c, ok := r.commands.top(id)
	r.mu.Unlock()
	if !ok {
		if ph := r.commandHolder(id); ph != nil {
			if err := r.provide(ph); err != nil {
				return err
			}
			return r.run(ctx, id, args)
		}
		return &NoCommandError{ID: id}
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
	if !ok && r.commandHolder(id) != nil {
		return true
	}
	if !ok || c.Run == nil || r.faults.isOff(KindCommand, id) != nil {
		return false
	}
	if c.When != "" && !FrontCan(r, c.When) {
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
	if r.cmdList == nil {
		out := make([]Command, 0, len(r.commands))
		for id := range r.commands {
			c, _ := r.commands.top(id)
			out = append(out, c)
		}
		slices.SortFunc(out, func(a, b Command) int { return strings.Compare(a.ID, b.ID) })
		r.cmdList = out
	}
	return r.cmdList[:len(r.cmdList):len(r.cmdList)]
}

func (r *reg) Keymaps() []Keymap {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sortKeymaps()
	return slices.Clone(r.sorted)
}

func (r *reg) KeymapsFor(command string) []Keymap {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sortKeymaps()
	ks := r.byCmd[command]
	return ks[:len(ks):len(ks)]
}

// sortKeymaps makes sorted and byCmd again after a keymap came or went; r.mu is held.
func (r *reg) sortKeymaps() {
	if r.sorted != nil {
		return
	}
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
	r.byCmd = map[string][]Keymap{}
	for _, km := range r.sorted {
		r.byCmd[km.Command] = append(r.byCmd[km.Command], km)
	}
}

func (r *reg) Lookup(modes []string, keys []string) (*Keymap, bool) {
	exclusive := map[string]func(Keymap) bool{}
	for _, c := range r.Contributions("keys.exclusive") {
		e := c.(ExclusiveKeys)
		active := true
		if e.Active != nil {
			Call(func() error { active = e.Active(); return nil })
		}
		if !active {
			continue
		}
		allow := e.Allow
		if allow == nil {
			allow = func(Keymap) bool { return false }
		}
		exclusive[e.Mode] = allow
	}
	seq := strings.Join(keys, sep)
	type found struct {
		km     *Keymap
		prefix bool
	}
	r.mu.Lock()
	fs := make([]found, len(modes))
	for i, m := range modes {
		if km, ok := r.keymaps.top(keymapKey{m, seq}); ok && km.Command != "" {
			fs[i].km = &km
		}
		fs[i].prefix = r.prefixes[keymapKey{m, seq}] > 0
	}
	r.mu.Unlock()
	var exact *Keymap
	more := false
	var allow func(Keymap) bool // the exclusive mode's, once keys have passed it
	for i, m := range modes {
		f := fs[i]
		if allow != nil {
			lent := false
			if f.km != nil && exact == nil {
				Call(func() error { lent = allow(*f.km); return nil })
			}
			if lent {
				exact = f.km
			}
			continue
		}
		if f.km != nil && exact == nil {
			exact = f.km
			if f.km.Nowait && !more && !f.prefix {
				return exact, false
			}
		}
		more = more || f.prefix
		if a, ok := exclusive[m]; ok {
			allow = a
		}
	}
	return exact, more
}

func (r *reg) Options() Options { return r.options }
