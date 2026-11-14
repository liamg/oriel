package registry

import (
	"cmp"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/liamg/oriel/registry/keymap"
)

// Placeholder stands for what something will provide the first time one of its triggers is used: a command whose id
// matches a glob, a key, an event, an omnibox prefix, a contribution asked for by name. The first use drops the
// placeholder, calls Provide, then goes on as if what it stands for had been there all along.
type Placeholder struct {
	Name     string         // what provides it, for messages and the load command: a plugin's id
	Commands []string       // globs over command ids: "Jira*"
	Keys     []Keymap       // Mode and Keys (and Desc); the command is the placeholder's own
	Events   []EventTrigger // as a hook would be on them
	Omnibox  []string       // omnibox prefixes
	// Contributions are globs over contributions' names, by kind ({KindView: {"debug"}}), for Holder.
	Contributions map[string][]string
	Titles        map[string]string // what a command named in full says until it's provided; "Loads <name>" if none
	Provide       func() error
	Source        string
}

// EventTrigger is an event, and a glob over its subject ("" for any), that a placeholder waits for.
type EventTrigger struct{ Event, Pattern string }

// KeyTrigger reads a lazy key: "<leader>f" in normal mode, or a mode first, "global <D-j>".
func KeyTrigger(s string) Keymap {
	if m, k, ok := strings.Cut(strings.TrimSpace(s), " "); ok && k != "" {
		return Keymap{Mode: m, Keys: strings.TrimSpace(k)}
	}
	return Keymap{Mode: "n", Keys: strings.TrimSpace(s)}
}

// EventTriggerOf reads a lazy event: "SessionStart", or with a glob over its subject, "BufRead *.go".
func EventTriggerOf(s string) EventTrigger {
	e, p, _ := strings.Cut(strings.TrimSpace(s), " ")
	return EventTrigger{Event: e, Pattern: strings.TrimSpace(p)}
}

// OmniboxHolder is a placeholder's omnibox prefix, which the pickers (registry/uireg) offer until the first query
// on it calls Provide: then the source with Prefix that took its place answers.
type OmniboxHolder struct {
	Name, Prefix string
	Provide      func() error
	Source       string
}

func (OmniboxHolder) ContributionKind() string   { return "placeholder.omnibox" }
func (h OmniboxHolder) ContributionName() string { return LoadCommand(h.Name) + ":" + h.Prefix }

// LoadCommand is the command a placeholder adds to provide it now: "load:acme/jira". Its keys run it with the keys
// and modes they were typed in.
func LoadCommand(name string) string { return "load:" + name }

// NoCommandError is a Run of an id nothing registers, nor stands in for.
type NoCommandError struct{ ID string }

func (e *NoCommandError) Error() string { return fmt.Sprintf("no command %q", e.ID) }

// IsNoCommand says err is the Run of id finding no command: not a command's own error.
func IsNoCommand(err error, id string) bool {
	var e *NoCommandError
	return errors.As(err, &e) && e.ID == id
}

type placeholder struct {
	p    Placeholder
	once sync.Once
	err  error
	drop func()
}

func (r *reg) Placeholder(p Placeholder) Disposable {
	ph := &placeholder{p: p}
	var ds []Disposable
	load := LoadCommand(p.Name)
	ds = append(ds, r.Command(Command{ID: load, Title: "Load " + p.Name + " now", Source: p.Source,
		Run: func(ctx Ctx, a Args) error { return r.provideKeys(ctx, ph, a) }}))
	for _, g := range p.Commands {
		if !strings.ContainsAny(g, `*?[\`) {
			ds = append(ds, r.Command(Command{ID: g, Title: cmp.Or(p.Titles[g], "Loads "+p.Name), Source: p.Source, Run: func(ctx Ctx, a Args) error {
				if err := r.provide(ph); err != nil {
					return err
				}
				return r.run(ctx, g, a)
			}}))
		}
	}
	for _, k := range p.Keys {
		seq := r.keySeq(k.Keys)
		var modes []string
		for _, o := range p.Keys {
			if o.Mode != k.Mode && keymap.Format(r.keySeq(o.Keys)) == keymap.Format(seq) {
				modes = append(modes, o.Mode)
			}
		}
		ds = append(ds, r.Keymap(Keymap{Mode: k.Mode, Keys: k.Keys, Command: load, Desc: cmp.Or(k.Desc, "loads "+p.Name), Source: p.Source,
			Args: Args{"keys": keymap.Format(seq), "modes": append([]string{k.Mode}, modes...)}}))
	}
	for _, pre := range p.Omnibox {
		ds = append(ds, r.Contribute(OmniboxHolder{Name: p.Name, Prefix: pre, Source: p.Source,
			Provide: func() error { return r.provide(ph) }}))
	}
	r.mu.Lock()
	r.holders = append(r.holders, ph)
	r.mu.Unlock()
	ph.drop = sync.OnceFunc(func() {
		r.mu.Lock()
		r.holders = slices.DeleteFunc(r.holders, func(o *placeholder) bool { return o == ph })
		r.mu.Unlock()
		for _, d := range ds {
			d.Dispose()
		}
	})
	return DisposeFunc(ph.drop)
}

// provide drops ph and calls its Provide, once.
func (r *reg) provide(ph *placeholder) error {
	ph.once.Do(func() {
		ph.drop()
		ph.err = Call(ph.p.Provide)
		if ph.err != nil {
			ph.err = fmt.Errorf("loading %s: %w", ph.p.Name, ph.err)
		}
	})
	return ph.err
}

// provideKeys provides ph, then runs what its keys in a's modes map to now.
func (r *reg) provideKeys(ctx Ctx, ph *placeholder, a Args) error {
	if err := r.provide(ph); err != nil {
		return err
	}
	keys := a.String("keys")
	if keys == "" {
		return nil
	}
	k, _ := r.Lookup(a.Strings("modes"), keymap.Parse(keys))
	if k == nil {
		return fmt.Errorf("%s loaded, but doesn't map %s", ph.p.Name, keys)
	}
	return r.Run(ctx, k.Command, k.Args)
}

// commandHolder is the placeholder standing for command id, if any.
func (r *reg) commandHolder(id string) *placeholder {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ph := range r.holders {
		for _, g := range ph.p.Commands {
			if ok, _ := path.Match(g, id); ok {
				return ph
			}
		}
	}
	return nil
}

func (r *reg) Holder(kind, name string) (func() error, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ph := range r.holders {
		for _, g := range ph.p.Contributions[kind] {
			if ok, _ := path.Match(g, name); ok {
				return func() error { return r.provide(ph) }, true
			}
		}
	}
	return nil, false
}

// provideEvent provides every placeholder waiting for event on subject.
func (r *reg) provideEvent(event, subject string) {
	r.mu.Lock()
	var due []*placeholder
	for _, ph := range r.holders {
		if slices.ContainsFunc(ph.p.Events, func(t EventTrigger) bool { return t.Event == event && matchSubject(t.Pattern, subject) }) {
			due = append(due, ph)
		}
	}
	r.mu.Unlock()
	for _, ph := range due {
		if err := r.provide(ph); err != nil {
			r.Report(KindCommand, LoadCommand(ph.p.Name), err)
		}
	}
}

// matchSubject says a hook's glob matches an event's subject: its base name, or the whole of it.
func matchSubject(pattern, subject string) bool {
	if pattern == "" {
		return true
	}
	if ok, _ := path.Match(pattern, path.Base(subject)); ok {
		return true
	}
	ok, _ := path.Match(pattern, subject)
	return ok
}

// keySeq is keys in notation, split, with <Leader> expanded.
func (r *reg) keySeq(keys string) []string {
	var seq []string
	leader, _, _ := r.options.Get("keymap.leader", At{})
	for _, t := range keymap.Parse(keys) {
		if t == "<Leader>" {
			seq = append(seq, keymap.Parse(leader.(string))...)
		} else {
			seq = append(seq, t)
		}
	}
	return seq
}
