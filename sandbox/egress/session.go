package egress

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The answers a network approval card gives.
const (
	Once       = "once"
	ForSession = "session"
	Always     = "always"
	Everywhere = "everywhere"
	// AnyHost allows every host for the rest of the session, and every card
	// still waiting with it.
	AnyHost = "anyhost"
)

// Answer is the user's decision on a destination. A Key other than the
// allows above refuses it.
type Answer struct {
	Key string `json:"key"`
	// By is who refused ("the user", "rule net(…)"), and Note what they said: the refusal tells the agent both.
	By   string `json:"by,omitempty"`
	Note string `json:"note,omitempty"`
	// Private lets the allow reach private and tailnet addresses.
	Private bool `json:"private,omitempty"`
}

// Approver puts a destination no rule covers in front of the user: the
// network approval card. It returns when they answer, or with an error when
// ctx ends first; the connection is held open meanwhile.
type Approver interface {
	ApproveEgress(ctx context.Context, host string, port int) (Answer, error)
}

// Session is the host side of one session's egress: it decides each
// destination the proxy asks about, by rule or by asking the user, and dials
// what it allowed.
type Session struct {
	// GrantsPath is the file the session's own rules are kept in.
	GrantsPath string
	// Settings are the user's rules, most local first: the projects', then
	// the user's own (including what was allowed always). Source is set.
	Settings func() [][]Rule
	// Env are the endpoints the sandbox's environment points at (EnvRules).
	Env func() []Rule
	// Approver asks the user; nil refuses what no rule covers.
	Approver Approver
	// Always records an allow for good, on every machine when everywhere.
	Always func(r Rule, everywhere bool) error
	// Changed is told when the session's rules change.
	Changed func()
	// Failed is told of each connection that never got through, with its reason.
	Failed func(Event)
	// Resolver looks hosts up for a dial; nil is the system's.
	Resolver Resolver
	// Wait bounds a card; 0 means 60s.
	Wait time.Duration

	mu    sync.Mutex
	asks  map[string]*ask
	log   []Event
	why   map[string]string
	dials map[string]dialGrant
	// grantsMu serialises rewrites of the session's grants.
	grantsMu sync.Mutex
}

type ask struct {
	done    chan struct{}
	callers int // the Decides waiting on the card
	once    sync.Once
	cancel  context.CancelFunc
	ok      bool
	private bool
}

// MaxLog is how many finished connections a session remembers.
const MaxLog = 2000

// maxWhy is how many destinations' reasons a session remembers.
const maxWhy = 2000

// defaultWait is how long a connection is held open for the user to decide.
const defaultWait = 60 * time.Second

// dialGrantFor is how long an allow lets the proxy have the session dial.
const dialGrantFor = 30 * time.Second

type dialGrant struct {
	until time.Time
	scope Scope
}

// Grants are the rules the user made for this session.
func (s *Session) Grants() []Rule {
	var out []Rule
	if b, err := os.ReadFile(s.GrantsPath); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

// UpdateGrants rewrites the session's grants with f, one writer at a time.
func (s *Session) UpdateGrants(f func([]Rule) []Rule) error {
	s.grantsMu.Lock()
	defer s.grantsMu.Unlock()
	b, err := json.MarshalIndent(f(s.Grants()), "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.GrantsPath, b, 0o600); err != nil {
		return err
	}
	if s.Changed != nil {
		s.Changed()
	}
	return nil
}

// SetRule allows or blocks a destination for the rest of the session, from
// a row of the network log.
func (s *Session) SetRule(host string, port int, allow, private bool) error {
	host = normHost(host)
	return s.UpdateGrants(func(rules []Rule) []Rule {
		rules = slices.DeleteFunc(rules, func(x Rule) bool { return x.Host == host && x.Port == port })
		return append(rules, Rule{Host: host, Port: port, Allow: allow, Private: allow && private})
	})
}

func withSource(source string, rules []Rule) []Rule {
	out := make([]Rule, 0, len(rules))
	for _, r := range rules {
		r.Source = source
		out = append(out, r)
	}
	return out
}

func (s *Session) env() []Rule {
	if s.Env == nil {
		return nil
	}
	return withSource("the sandbox's environment", s.Env())
}

// Levels are the rules in force: the session's grants; the projects'
// settings, then the user's; then the model and telemetry endpoints the
// sandbox was given and the preset allowlist.
func (s *Session) Levels() Levels {
	var settings [][]Rule
	if s.Settings != nil {
		settings = s.Settings()
	}
	return Levels{
		Session:  withSource("this session", s.Grants()),
		Settings: settings,
		Builtin:  append(s.env(), withSource("built-in list", DefaultAllow)...),
	}
}

// Rules are the rules in force, most local first.
func (s *Session) Rules() []Rule {
	l := s.Levels()
	out := slices.Clone(l.Session)
	for _, rules := range l.Settings {
		out = append(out, rules...)
	}
	return append(out, l.Builtin...)
}

// note remembers why key's last decision went the way it did.
func (s *Session) note(key, why string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.why == nil {
		s.why = map[string]string{}
	}
	if _, ok := s.why[key]; !ok && len(s.why) >= maxWhy {
		for k := range s.why {
			delete(s.why, k)
			break
		}
	}
	s.why[key] = why
}

// Why is the reason host:port's last decision went the way it did; "" when none was made.
func (s *Session) Why(host string, port int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.why[fmt.Sprintf("%s:%d", normHost(host), port)]
}

// Decide rules on a destination for the proxy, asking the user about
// anything no rule covers. Concurrent asks for one destination share a card.
// It makes Session a Decider, for a proxy on the host.
func (s *Session) Decide(ctx context.Context, host string, port int) Decision {
	if s.Allow(ctx, host, port) {
		return Allow
	}
	return Deny
}

// Allow is Decide as a bool.
func (s *Session) Allow(ctx context.Context, host string, port int) bool {
	host = normHost(host)
	key := fmt.Sprintf("%s:%d", host, port)
	// The host machine is reachable only at the endpoints the sandbox was
	// given; no rule or card adds to them.
	if host == HostGateway {
		_, ok := Match(s.env(), host, port)
		s.note(key, map[bool]string{true: "rule: the sandbox's environment", false: "the host machine, at a port the sandbox was not given"}[ok])
		return ok
	}
	if rule, ok := Decide(s.Levels(), host, port); ok {
		s.note(key, "rule: "+rule.Source)
		if rule.Allow {
			// An endpoint the sandbox was given reaches private addresses
			// whichever allow won.
			env, _ := Match(s.env(), host, port)
			s.grantDial(key, rule.Private || env.Private)
		}
		return rule.Allow
	}
	s.mu.Lock()
	if s.asks == nil {
		s.asks = map[string]*ask{}
	}
	a, waiting := s.asks[key]
	if !waiting {
		a = &ask{done: make(chan struct{})}
		s.asks[key] = a
	}
	a.callers++
	s.mu.Unlock()
	if !waiting {
		go s.ask(key, host, port, a)
	}
	select {
	case <-a.done:
		if a.ok {
			s.grantDial(key, a.private)
		}
		return a.ok
	case <-ctx.Done():
		return false
	}
}

func (s *Session) ask(key, host string, port int, a *ask) {
	wait := s.Wait
	if wait <= 0 {
		wait = defaultWait
	}
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	s.mu.Lock()
	a.cancel = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.asks, key)
		s.mu.Unlock()
		s.settle(a, false, false)
	}()
	if s.Approver == nil {
		s.note(key, "nobody answered the card")
		return
	}
	ans, err := s.Approver.ApproveEgress(ctx, host, port)
	if err != nil {
		select {
		case <-a.done:
		default:
			s.note(key, "nobody answered the card")
		}
		return
	}
	rule := Rule{Host: host, Port: port, Allow: true, Private: ans.Private}
	ok, why := false, "denied by "+cmp.Or(ans.By, "the user")
	if n := strings.TrimSpace(ans.Note); n != "" {
		why += ", who said: " + n
	}
	switch ans.Key {
	case Once:
		ok, why = true, "allowed once on the card"
	case ForSession:
		ok, why = true, "allowed for the session on the card"
		_ = s.UpdateGrants(func(rules []Rule) []Rule { return append(rules, rule) })
	case Always, Everywhere:
		ok, why = true, "allowed always on the card"
		if s.Always != nil {
			_ = s.Always(rule, ans.Key == Everywhere)
		}
	case AnyHost:
		ok, why = true, "all hosts allowed for the session on the card"
		_ = s.UpdateGrants(func(rules []Rule) []Rule { return append(rules, Rule{Host: "*", Allow: true}) })
		s.allowWaiting(a)
	}
	private := ok && ans.Private && ans.Key != AnyHost
	if private {
		why += ", with private network access"
	}
	s.note(key, why)
	s.settle(a, ok, private)
}

// settle answers a waiting ask, once.
func (s *Session) settle(a *ask, ok, private bool) {
	a.once.Do(func() {
		a.ok, a.private = ok, private
		close(a.done)
	})
}

// allowWaiting allows every other card still waiting, once all hosts are
// allowed, and withdraws them.
func (s *Session) allowWaiting(except *ask) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, a := range s.asks {
		if a == except {
			continue
		}
		s.settleLocked(key, a)
	}
}

func (s *Session) settleLocked(key string, a *ask) {
	a.once.Do(func() {
		a.ok = true
		close(a.done)
	})
	if s.why == nil {
		s.why = map[string]string{}
	}
	s.why[key] = "all hosts allowed for the session on the card"
	if a.cancel != nil {
		a.cancel()
	}
}

// Waiting are the destinations a card is open for.
func (s *Session) Waiting() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Sorted(maps.Keys(s.asks))
}

// grantDial lets the proxy have the session dial key, briefly. private is
// whether the allow reaches private addresses.
func (s *Session) grantDial(key string, private bool) {
	g := dialGrant{until: time.Now().Add(dialGrantFor), scope: Public}
	if private {
		g.scope = Private
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dials == nil {
		s.dials = map[string]dialGrant{}
	}
	for k, old := range s.dials {
		if time.Now().After(old.until) {
			delete(s.dials, k)
		}
	}
	s.dials[key] = g
}

// ServeDial dials a destination from the host for the proxy, which cannot
// reach the networks the host can: the DialPath route. Only a destination
// just allowed is dialled, and only at an address its allow covers.
func (s *Session) ServeDial(w http.ResponseWriter, r *http.Request) {
	if !IsUpgrade(r) {
		http.Error(w, "not a dial request", http.StatusBadRequest)
		return
	}
	host := normHost(r.URL.Query().Get("host"))
	port, err := strconv.Atoi(r.URL.Query().Get("port"))
	if host == "" || err != nil || port < 1 || port > 65535 {
		http.Error(w, "host and port are required", http.StatusBadRequest)
		return
	}
	key := fmt.Sprintf("%s:%d", host, port)
	s.mu.Lock()
	g, ok := s.dials[key]
	s.mu.Unlock()
	if !ok || time.Now().After(g.until) {
		http.Error(w, fmt.Sprintf("%s is not allowed for this session", key), http.StatusForbidden)
		return
	}
	up, err := DialChecked(r.Context(), s.Resolver, host, port, g.scope)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	_ = ServeDial(w, up)
}

// Record takes one finished connection from the proxy. Its reason is the
// session's own: the sender is inside the sandbox.
func (s *Session) Record(ev Event) {
	s.mu.Lock()
	ev.Reason = s.why[fmt.Sprintf("%s:%d", ev.Host, ev.Port)]
	s.log = append(s.log, ev)
	if len(s.log) > MaxLog {
		s.log = s.log[len(s.log)-MaxLog:]
	}
	s.mu.Unlock()
	if s.Failed != nil && ev.Failed() {
		s.Failed(ev)
	}
}

// Conns are the finished connections, newest first.
func (s *Session) Conns() []Event {
	s.mu.Lock()
	out := slices.Clone(s.log)
	s.mu.Unlock()
	slices.Reverse(out)
	return out
}
