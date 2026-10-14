package worker

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
)

// carryOn is what an agent whose turn a lost login stopped is told once it's back.
const carryOn = "You're signed in again: carry on with what you were doing."

// lostLogin is a provider's login on this worker that has stopped working, and the cards that wait on it.
type lostLogin struct {
	login core.Login
	asks  map[core.SessionID]*loginAsk
}

type loginAsk struct{ cancel context.CancelFunc }

// signedOut says whether provider's login here has stopped working.
func (w *Worker) signedOut(provider string) bool {
	w.loginMu.Lock()
	defer w.loginMu.Unlock()
	for _, l := range w.lost {
		if l.login.Provider == provider {
			return true
		}
	}
	return false
}

// lostLogins are the logins here that have stopped working, in a stable order.
func (w *Worker) lostLogins() []core.Login {
	w.loginMu.Lock()
	defer w.loginMu.Unlock()
	var out []core.Login
	for _, l := range w.lost {
		out = append(out, l.login)
	}
	slices.SortFunc(out, func(a, b core.Login) int { return strings.Compare(a.Provider+a.Account, b.Provider+b.Account) })
	return out
}

// loginLost flags a provider's login as gone: the worker says so, and every session here with that provider's agent,
// and from, the one it went in, waits on a card until it's back.
func (w *Worker) loginLost(from core.SessionID, ev agent.AuthExpired) {
	l := core.Login{Provider: ev.Provider, Account: ev.Account, Worker: w.opts.ID, Detail: ev.Detail, Terminal: ev.Terminal, Shell: ev.Shell}
	key := ev.Provider + "\x00" + ev.Account
	w.loginMu.Lock()
	if w.lost == nil {
		w.lost = map[string]*lostLogin{}
	}
	old := w.lost[key]
	changed := old == nil || old.login != l
	if old == nil {
		w.lost[key] = &lostLogin{login: l, asks: map[core.SessionID]*loginAsk{}}
	} else {
		old.login = l
	}
	w.loginMu.Unlock()
	if changed {
		_ = w.emit(core.WorkerChanged{Worker: w.workerState()})
		w.log.Warn("agent login lost", "provider", ev.Provider, "account", ev.Account, "detail", ev.Detail)
	}
	for _, se := range w.store.State().Sessions {
		if se.ID != from && (se.Archived || se.Agent.Provider != ev.Provider || !se.Agent.Running) {
			continue
		}
		if h, _ := w.hostFor(se.ID, false); h != nil {
			h.askLogin(key)
		}
	}
}

// askLogins puts the card of each of provider's lost logins on the session.
func (h *host) askLogins(provider string) {
	h.w.loginMu.Lock()
	var keys []string
	for key, l := range h.w.lost {
		if l.login.Provider == provider {
			keys = append(keys, key)
		}
	}
	h.w.loginMu.Unlock()
	for _, k := range keys {
		h.askLogin(k)
	}
}

// askLogin puts the login's card on the session, unless it has it; Check again (any answer) checks the login.
func (h *host) askLogin(key string) {
	w := h.w
	ctx, cancel := context.WithCancel(h.ctx)
	mine := &loginAsk{cancel: cancel}
	w.loginMu.Lock()
	l := w.lost[key]
	if l == nil || l.asks[h.id] != nil {
		w.loginMu.Unlock()
		cancel()
		return
	}
	l.asks[h.id] = mine
	login := l.login
	w.loginMu.Unlock()
	go func() {
		defer func() {
			w.loginMu.Lock()
			if l := w.lost[key]; l != nil && l.asks[h.id] == mine {
				delete(l.asks, h.id)
			}
			w.loginMu.Unlock()
			cancel()
		}()
		for {
			if _, ok := h.askUser(ctx, core.Ask{Kind: core.AskLogin, Summary: login.Detail, Login: &login}); !ok {
				return
			}
			if w.checkLogin(login.Provider) {
				return
			}
		}
	}()
}

// checkLogin asks the provider whether it's signed in, and clears its flags if it is.
func (w *Worker) checkLogin(provider string) bool {
	p, ok := w.opts.Providers.Get(provider)
	if !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	in, err := p.Detect(ctx)
	if err != nil || !in.LoggedIn {
		w.log.Info("agent login checked: still signed out", "provider", provider, "note", in.Note)
		return false
	}
	w.loginBack(provider)
	return true
}

// loginBack clears a provider's flags: the cards go, and its sessions go on.
func (w *Worker) loginBack(provider string) {
	w.loginMu.Lock()
	var asks []*loginAsk
	cleared := false
	for key, l := range w.lost {
		if l.login.Provider != provider {
			continue
		}
		for _, a := range l.asks {
			asks = append(asks, a)
		}
		delete(w.lost, key)
		cleared = true
	}
	w.loginMu.Unlock()
	if !cleared {
		return
	}
	_ = w.emit(core.WorkerChanged{Worker: w.workerState()})
	for _, a := range asks {
		a.cancel()
	}
	for _, se := range w.store.State().Sessions {
		if se.Archived || se.Agent.Provider != provider {
			continue
		}
		h, _ := w.hostFor(se.ID, false)
		if h == nil {
			continue
		}
		h.mu.Lock()
		relogin, refused := h.relogin, h.refused
		h.relogin, h.refused = false, false
		h.mu.Unlock()
		switch {
		case refused:
			_ = h.start()
		case relogin:
			h.note(carryOn)
		default:
			h.kick()
		}
	}
}

// refusedForLogin flags the login an agent couldn't start for, if that's why.
func (h *host) refusedForLogin(err error) {
	var ae agent.AuthExpired
	if !errors.As(err, &ae) {
		return
	}
	h.mu.Lock()
	h.refused = true
	h.mu.Unlock()
	h.w.loginLost(h.id, ae)
}
