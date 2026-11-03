package agentrun

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
)

// carryOn is what an agent whose turn a lost login stopped is told once it's back.
const carryOn = "You're signed in again: carry on with what you were doing."

// lostLogin is a provider's login on the host that has stopped working, and the cards that wait on it.
type lostLogin struct {
	login core.Login
	asks  map[core.SessionID]*loginAsk
	// refused is the login the provider's files held as it went (Install.Login): it was refused, not signed out,
	// so it doesn't count as signed in again. "" when they held none; marked closes once it's read.
	refused string
	marked  chan struct{}
}

type loginAsk struct{ cancel context.CancelFunc }

// SignedOut says whether provider's login here has stopped working.
func (x *Runner) SignedOut(provider string) bool {
	x.loginMu.Lock()
	defer x.loginMu.Unlock()
	for _, l := range x.lost {
		if l.login.Provider == provider {
			return true
		}
	}
	return false
}

// LostLogins are the logins here that have stopped working, in a stable order.
func (x *Runner) LostLogins() []core.Login {
	x.loginMu.Lock()
	defer x.loginMu.Unlock()
	var out []core.Login
	for _, l := range x.lost {
		out = append(out, l.login)
	}
	slices.SortFunc(out, func(a, b core.Login) int { return strings.Compare(a.Provider+a.Account, b.Provider+b.Account) })
	return out
}

// LoginLost flags a provider's login as gone: the machine's state says so, and every session here with that provider's agent,
// and from, the one it went in, waits on a card until it's back.
func (x *Runner) LoginLost(from core.SessionID, ev agent.AuthExpired) {
	l := core.Login{Provider: ev.Provider, Account: ev.Account, Detail: ev.Detail, Terminal: ev.Terminal, Shell: ev.Shell,
		HostCommand: ev.HostCommand}
	if p, ok := x.deps.Providers.Get(ev.Provider); ok {
		_, starts := p.(agent.LoginStarter)
		l.Browser = starts && agent.FeaturesOf(p).Login
	}
	key := ev.Provider + "\x00" + ev.Account
	x.loginMu.Lock()
	if x.lost == nil {
		x.lost = map[string]*lostLogin{}
	}
	old := x.lost[key]
	changed := old == nil || !old.login.Same(l)
	if old == nil {
		nl := &lostLogin{login: l, asks: map[core.SessionID]*loginAsk{}, marked: make(chan struct{})}
		x.lost[key] = nl
		x.env.Go("a lost login", func() { x.markRefused(nl) })
	} else {
		old.login = l
	}
	x.loginMu.Unlock()
	if changed {
		x.deps.MachineChanged()
		x.env.Log.Warn("agent login lost", "provider", ev.Provider, "account", ev.Account, "detail", ev.Detail)
	}
	for _, se := range x.st.Snapshot().Sessions {
		if se.ID != from && (se.Archived || se.Agent.Provider != ev.Provider || !se.Agent.Running) {
			continue
		}
		if h, _ := x.sessionFor(se.ID, false); h != nil {
			h.askLogin(key)
		}
	}
}

// retryLogin starts a sandboxed agent that lost its sign-in again, once, on the host's handed over afresh, and says
// whether it does: the sandbox's copy may be one the host has renewed since, which the API answers as revoked. A turn
// it was in carries on.
func (h *session) retryLogin(ev agent.AuthExpired) bool {
	if !h.snapshot().Agent.Sandboxed {
		return false
	}
	h.mu.Lock()
	if h.loginRetried {
		h.mu.Unlock()
		return false
	}
	h.loginRetried = true
	relogin := h.relogin
	h.relogin = false
	h.mu.Unlock()
	h.r.env.Go("a sandbox's sign-in again", func() {
		h.r.detect(ev.Provider) // reads the host's sign-in afresh and hands it to the sandboxes
		h.restart("its sign-in, handed over again from this machine")
		if relogin {
			h.note(carryOn)
		}
	})
	return true
}

// askLogins puts the card of each of provider's lost logins on the session.
func (h *session) askLogins(provider string) {
	h.r.loginMu.Lock()
	var keys []string
	for key, l := range h.r.lost {
		if l.login.Provider == provider {
			keys = append(keys, key)
		}
	}
	h.r.loginMu.Unlock()
	for _, k := range keys {
		h.askLogin(k)
	}
}

// askLogin puts the login's card on the session, unless it has it; Check again (any answer) checks the login.
func (h *session) askLogin(key string) {
	x := h.r
	ctx, cancel := context.WithCancel(h.ctx)
	mine := &loginAsk{cancel: cancel}
	x.loginMu.Lock()
	l := x.lost[key]
	if l == nil || l.asks[h.id] != nil {
		x.loginMu.Unlock()
		cancel()
		return
	}
	l.asks[h.id] = mine
	login := l.login
	x.loginMu.Unlock()
	if h.snapshot().Agent.Sandboxed {
		login.Terminal = "" // its terminal is the sandbox's, whose sign-in is the host's: signing in there forks it
	}
	go func() {
		defer x.env.Recover("a login ask", nil)
		defer func() {
			x.loginMu.Lock()
			if l := x.lost[key]; l != nil && l.asks[h.id] == mine {
				delete(l.asks, h.id)
			}
			x.loginMu.Unlock()
			cancel()
		}()
		for {
			if _, ok := h.askUser(ctx, core.Ask{Kind: core.AskLogin, Summary: login.Detail, Login: &login}); !ok {
				return
			}
			if x.CheckLogin(login.Provider, false) {
				return
			}
		}
	}()
}

// StartLogin begins provider's sign-in from Oriel (agent.LoginStarter), and returns where the user signs in. Once it
// succeeds the provider's flags clear, as Check again would.
func (x *Runner) StartLogin(ctx context.Context, provider string) (string, error) {
	p, ok := x.deps.Providers.Get(provider)
	if !ok {
		return "", fmt.Errorf("no agent provider %q", provider)
	}
	ls, ok := p.(agent.LoginStarter)
	if !ok || !agent.FeaturesOf(p).Login {
		return "", fmt.Errorf("%s doesn't sign in from Oriel", provider)
	}
	l, err := ls.StartLogin(ctx)
	if err != nil {
		return "", err
	}
	x.env.Go("a sign-in", func() {
		if err := <-l.Done; err != nil {
			x.env.Log.Warn("agent sign-in", "provider", provider, "err", err)
			return
		}
		x.CheckLogin(provider, true)
	})
	return l.URL, nil
}

// detect asks the provider what's installed and who's signed in.
func (x *Runner) detect(provider string) (agent.Install, bool) {
	p, ok := x.deps.Providers.Get(provider)
	if !ok {
		return agent.Install{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	in, err := p.Detect(ctx)
	return in, err == nil
}

// markRefused records the login the provider's files hold as it's lost.
func (x *Runner) markRefused(l *lostLogin) {
	defer close(l.marked)
	in, ok := x.detect(l.login.Provider)
	x.loginMu.Lock()
	if ok && in.LoggedIn {
		l.refused = in.Login
		if l.refused == "" {
			l.refused = "?" // one it can't tell from the next
		}
	}
	x.loginMu.Unlock()
}

// CheckLogin asks the provider whether it's signed in, and clears its flags if it is, with a login other than the one
// refused. seen says a login has just worked (an agent's request was answered), which is proof enough: the provider's
// files may not show it, as a sandbox's /login signs in only there.
func (x *Runner) CheckLogin(provider string, seen bool) bool {
	if seen {
		x.loginBack(provider)
		return true
	}
	in, ok := x.detect(provider)
	if !ok || !in.LoggedIn {
		x.env.Log.Info("agent login checked: still signed out", "provider", provider, "note", in.Note)
		return false
	}
	for _, l := range x.lostOf(provider) {
		<-l.marked
		x.loginMu.Lock()
		refused := l.refused
		x.loginMu.Unlock()
		if refused != "" && (in.Login == "" || in.Login == refused) {
			x.env.Log.Info("agent login checked: the login on disk is the one refused", "provider", provider)
			return false
		}
	}
	x.loginBack(provider)
	return true
}

// lostOf are provider's lost logins.
func (x *Runner) lostOf(provider string) []*lostLogin {
	x.loginMu.Lock()
	defer x.loginMu.Unlock()
	var out []*lostLogin
	for _, l := range x.lost {
		if l.login.Provider == provider {
			out = append(out, l)
		}
	}
	return out
}

// loginBack clears a provider's flags: the cards go, and its sessions go on.
func (x *Runner) loginBack(provider string) {
	x.loginMu.Lock()
	var asks []*loginAsk
	cleared := false
	for key, l := range x.lost {
		if l.login.Provider != provider {
			continue
		}
		for _, a := range l.asks {
			asks = append(asks, a)
		}
		delete(x.lost, key)
		cleared = true
	}
	x.loginMu.Unlock()
	if !cleared {
		return
	}
	x.deps.MachineChanged()
	for _, a := range asks {
		a.cancel()
	}
	for _, se := range x.st.Snapshot().Sessions {
		if se.Archived || se.Agent.Provider != provider {
			continue
		}
		h, _ := x.sessionFor(se.ID, false)
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
func (h *session) refusedForLogin(err error) {
	var ae agent.AuthExpired
	if !errors.As(err, &ae) {
		return
	}
	h.mu.Lock()
	h.refused = true
	h.mu.Unlock()
	h.r.LoginLost(h.id, ae)
}
