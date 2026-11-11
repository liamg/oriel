package egress

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// cards is a scripted user: each card waits for an answer from the test.
type cards struct {
	mu      sync.Mutex
	waiting []chan Answer
	opened  chan struct{} // closed when a card opens, then replaced
}

func (c *cards) ApproveEgress(ctx context.Context, _ string, _ int) (Answer, error) {
	ch := make(chan Answer, 1)
	c.mu.Lock()
	c.waiting = append(c.waiting, ch)
	if c.opened != nil {
		close(c.opened)
	}
	c.opened = make(chan struct{})
	c.mu.Unlock()
	select {
	case a := <-ch:
		return a, nil
	case <-ctx.Done():
		return Answer{}, ctx.Err()
	}
}

func (c *cards) open() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.waiting)
}

// await waits for n cards to be open.
func (c *cards) await(t *testing.T, n int) {
	t.Helper()
	hung := time.After(10 * time.Second)
	for {
		c.mu.Lock()
		if c.opened == nil {
			c.opened = make(chan struct{})
		}
		open, opened := len(c.waiting), c.opened
		c.mu.Unlock()
		if open >= n {
			return
		}
		select {
		case <-opened:
		case <-hung:
			t.Fatalf("%d cards open, want %d", open, n)
		}
	}
}

// answer answers the oldest open card, waiting for one to open.
func (c *cards) answer(t *testing.T, a Answer) {
	t.Helper()
	c.await(t, 1)
	c.mu.Lock()
	ch := c.waiting[0]
	c.waiting = c.waiting[1:]
	c.mu.Unlock()
	ch <- a
}

func newSession(t *testing.T) (*Session, *cards) {
	t.Helper()
	c := &cards{}
	return &Session{GrantsPath: filepath.Join(t.TempDir(), "egress.json"), Approver: c}, c
}

func allow(s *Session, host string, port int) bool {
	return s.Allow(context.Background(), host, port)
}

func TestNetworkLogSaysWhy(t *testing.T) {
	s, _ := newSession(t)

	// A built-in destination is decided by its rule; the reason is the
	// session's own, whatever the sender claims.
	if !allow(s, "GitHub.com.", 443) {
		t.Fatal("decide: refused")
	}
	s.Record(Event{Host: "github.com", Port: 443, Allowed: true, Proto: "tls", BytesUp: 10, BytesDown: 20, Millis: 1500, Reason: "trust me"})
	s.Record(Event{Host: "github.com", Port: 22, Allowed: true, Proto: "ssh"})

	conns := s.Conns()
	if len(conns) != 2 || conns[0].Host != "github.com" || conns[0].Proto != "ssh" || conns[0].Port != 22 {
		t.Fatalf("newest first, with host, port and protocol: %+v", conns)
	}
	if c := conns[1]; c.Reason != "rule: built-in list" || c.Millis != 1500 || c.BytesDown != 20 {
		t.Errorf("the rule that allowed it: %+v", c)
	}
	if conns[0].Reason != "" {
		t.Errorf("a connection nobody decided has no reason: %+v", conns[0])
	}
}

func TestNetworkRuleHoldsForTheSession(t *testing.T) {
	s, _ := newSession(t)
	if !allow(s, "github.com", 443) {
		t.Fatal("allowed by the built-in list to begin with")
	}
	if err := s.SetRule("github.com", 443, false, false); err != nil {
		t.Fatal(err)
	}
	if allow(s, "github.com", 443) {
		t.Error("blocked from the page, still allowed")
	}
	s.Record(Event{Host: "github.com", Port: 443})
	if c := s.Conns()[0]; c.Reason != "rule: this session" {
		t.Errorf("reason: %+v", c)
	}
	_ = s.SetRule("github.com", 443, true, false)
	if !allow(s, "github.com", 443) || len(s.Grants()) != 1 {
		t.Errorf("allowed again, one rule kept: %+v", s.Grants())
	}
}

func TestAllHostsForTheSession(t *testing.T) {
	s, c := newSession(t)
	decide := func(host string) chan bool {
		out := make(chan bool, 1)
		go func() { out <- allow(s, host, 8080) }()
		return out
	}
	first, second := decide("a.example.com"), decide("b.example.com")
	c.await(t, 2)
	c.answer(t, Answer{Key: AnyHost})
	if !<-first || !<-second {
		t.Error("both waiting connections allowed")
	}
	if !<-decide("c.example.org") {
		t.Error("a new host allowed with no card")
	}
}

func TestSharedCard(t *testing.T) {
	s, c := newSession(t)
	out := make(chan bool, 2)
	for range 2 {
		go func() { out <- allow(s, "one.example.com", 443) }()
	}
	for s.callers("one.example.com:443") < 2 {
		runtime.Gosched() // the second joins the card the first opened
	}
	c.answer(t, Answer{Key: Once})
	if !<-out || !<-out {
		t.Fatal("both connections to one destination allowed by one card")
	}
	if n := c.open(); n != 0 {
		t.Errorf("%d more cards opened", n)
	}
}

// A connection that comes once a card is answered asks again: it never takes the answer given to the ones before it.
// Each card is answered before the next connection starts, so a card shared by them would show as one card, not three.
func TestAConnectionAfterTheAnswerAsksAgain(t *testing.T) {
	s, c := newSession(t)
	for i, key := range []string{"deny", Once, "deny"} {
		out := make(chan bool, 1)
		go func() { out <- allow(s, "again.example.com", 443) }()
		c.answer(t, Answer{Key: key}) // waits for this connection's own card
		if got := <-out; got != (key == Once) {
			t.Fatalf("connection %d, answered %s: allowed %v", i+1, key, got)
		}
	}
}

func TestNobodyAnswers(t *testing.T) {
	s, _ := newSession(t)
	s.Wait = 50 * time.Millisecond
	if allow(s, "slow.example.com", 443) {
		t.Fatal("allowed with no answer")
	}
	s.Record(Event{Host: "slow.example.com", Port: 443})
	if r := s.Conns()[0].Reason; r != "nobody answered the card" {
		t.Errorf("reason %q", r)
	}
}

func TestNetworkLogIsCapped(t *testing.T) {
	s, _ := newSession(t)
	for range MaxLog + 5 {
		s.Record(Event{Host: "a", Port: 1})
	}
	if n := len(s.Conns()); n != MaxLog {
		t.Errorf("kept %d", n)
	}
}

type resolveTo []netip.Addr

func (r resolveTo) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return r, nil
}

// dialVia sends a dial request for host:port to the session, as the proxy does.
func dialVia(t *testing.T, s *Session, host string, port int) (net.Conn, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(s.ServeDial))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return DialViaSession(ctx, func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", srv.Listener.Addr().String())
	}, "", host, port)
}

// callers is how many Decides are waiting on key's card.
func (s *Session) callers(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a := s.asks[key]; a != nil {
		return a.callers
	}
	return 0
}

func (s *Session) scope(key string) Scope {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dials[key].scope
}

func TestEgressDialNeedsAnAllow(t *testing.T) {
	s, _ := newSession(t)
	s.Resolver = resolveTo{netip.MustParseAddr("1.1.1.1")}
	if c, err := dialVia(t, s, "example.com", 443); err == nil || !strings.Contains(err.Error(), "403") {
		if c != nil {
			_ = c.Close()
		}
		t.Fatalf("dialled without a decision: %v", err)
	}
}

// Only an allow marked private reaches a private address, exact name or not.
func TestEgressDialPrivateNeedsTheFlag(t *testing.T) {
	s, _ := newSession(t)
	s.Resolver = resolveTo{netip.MustParseAddr("10.1.78.104")}
	_ = s.SetRule("*.example.com", 443, true, false)
	if !allow(s, "litellm.example.com", 443) {
		t.Fatal("the wildcard allows it")
	}
	_, err := dialVia(t, s, "litellm.example.com", 443)
	if err == nil || !strings.Contains(err.Error(), "private address") {
		t.Fatalf("a wildcard reached a private address: %v", err)
	}

	_ = s.SetRule("litellm.example.com", 443, true, false)
	allow(s, "litellm.example.com", 443)
	if s.scope("litellm.example.com:443") != Public {
		t.Fatal("an exact allow should not reach private addresses by itself")
	}
	if _, err := dialVia(t, s, "litellm.example.com", 443); err == nil || !strings.Contains(err.Error(), "private address") {
		t.Fatalf("an exact allow reached a private address: %v", err)
	}

	_ = s.SetRule("litellm.example.com", 443, true, true)
	allow(s, "litellm.example.com", 443)
	if s.scope("litellm.example.com:443") != Private {
		t.Fatal("an allow marked private should reach private addresses")
	}
}

func TestEgressCardPrivate(t *testing.T) {
	for _, private := range []bool{false, true} {
		s, c := newSession(t)
		got := make(chan bool, 1)
		go func() { got <- allow(s, "nas.lan", 5000) }()
		c.answer(t, Answer{Key: ForSession, Private: private})
		if !<-got {
			t.Fatal("allowed on the card")
		}
		if (s.scope("nas.lan:5000") == Private) != private {
			t.Errorf("private=%t: scope %v", private, s.scope("nas.lan:5000"))
		}
		if r := s.Grants(); len(r) != 1 || r[0].Private != private {
			t.Errorf("private=%t: grants %+v", private, r)
		}
	}
}

func TestEgressCardAlways(t *testing.T) {
	s, c := newSession(t)
	var got []string
	s.Always = func(r Rule, everywhere bool) error {
		got = append(got, fmt.Sprintf("%s:%d %t", r.Host, r.Port, everywhere))
		return nil
	}
	done := make(chan bool, 1)
	go func() { done <- allow(s, "x.example.com", 443) }()
	c.answer(t, Answer{Key: Everywhere})
	if !<-done || len(got) != 1 || got[0] != "x.example.com:443 true" {
		t.Fatalf("always: %v", got)
	}
}

// The host machine is reached at the environment's endpoints, never by a
// wildcard or a card.
func TestEgressHostGateway(t *testing.T) {
	s, _ := newSession(t)
	s.Env = func() []Rule {
		return EnvRules(map[string]string{"ANTHROPIC_BASE_URL": "http://" + HostGateway + ":4000"})
	}
	_ = s.SetRule("*", 0, true, false)
	_ = s.SetRule(HostGateway, 22, true, false)
	for port, want := range map[int]bool{4000: true, 22: false, 80: false} {
		if got := allow(s, HostGateway, port); got != want {
			t.Errorf("port %d: allow %v", port, got)
		}
	}
}

func TestEgressGrantsSurviveConcurrentWrites(t *testing.T) {
	s, _ := newSession(t)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.UpdateGrants(func(r []Rule) []Rule {
				return append(r, Rule{Host: fmt.Sprintf("h%d.example.com", i), Allow: true})
			})
		}()
	}
	wg.Wait()
	if n := len(s.Grants()); n != 20 {
		t.Fatalf("kept %d of 20 grants", n)
	}
}

func TestEgressWhyIsBounded(t *testing.T) {
	s, _ := newSession(t)
	for i := range maxWhy + 10 {
		s.note(fmt.Sprintf("h%d:443", i), "x")
	}
	if n := len(s.why); n != maxWhy {
		t.Fatalf("remembers %d reasons", n)
	}
}

// Whatever the rule, a name resolving to loopback never reaches this machine.
func TestEgressDialRefusesLoopback(t *testing.T) {
	s, _ := newSession(t)
	s.Resolver = resolveTo{netip.MustParseAddr("127.0.0.1")}
	_ = s.SetRule("rebind.example.com", 443, true, false)
	allow(s, "rebind.example.com", 443)
	if _, err := dialVia(t, s, "rebind.example.com", 443); err == nil || !strings.Contains(err.Error(), "not reachable") {
		t.Fatalf("reached loopback: %v", err)
	}
}

// The model endpoint reaches its private addresses whichever allow wins.
func TestEgressEndpointIsPrivate(t *testing.T) {
	s, _ := newSession(t)
	s.Env = func() []Rule {
		return EnvRules(map[string]string{"ANTHROPIC_BASE_URL": "https://litellm.internal.example.com"})
	}
	_ = s.SetRule("*", 0, true, false)
	if !allow(s, "litellm.internal.example.com", 443) {
		t.Fatal("not allowed")
	}
	if g := s.scope("litellm.internal.example.com:443"); g != Private {
		t.Fatalf("scope %v, want private", g)
	}
}

// Behind a proxy, the session decides and the proxy holds the connection.
func TestProxyAsksTheSession(t *testing.T) {
	s, c := newSession(t)
	p := &Proxy{Decider: s, DecideTimeout: 5 * time.Second}
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	done := make(chan int, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodGet, "http://blocked.example.com/x", nil)
		conn, err := net.Dial("tcp", srv.Listener.Addr().String())
		if err != nil {
			done <- 0
			return
		}
		defer func() { _ = conn.Close() }()
		_ = req.WriteProxy(conn)
		resp, err := http.ReadResponse(bufio.NewReader(conn), req)
		if err != nil {
			done <- 0
			return
		}
		done <- resp.StatusCode
	}()
	c.answer(t, Answer{Key: "deny"})
	if code := <-done; code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", code)
	}
}

// A layer-3 permission denial is an ordinary tool error, and its message says who
// denied it and what to do instead, for the agent to read.
func TestProxyRefusalSaysWhoAndWhy(t *testing.T) {
	s, c := newSession(t)
	p := &Proxy{Decider: s, DecideTimeout: 5 * time.Second}
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	done := make(chan string, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodGet, "http://blocked.example.com/x", nil)
		conn, err := net.Dial("tcp", srv.Listener.Addr().String())
		if err != nil {
			done <- err.Error()
			return
		}
		defer func() { _ = conn.Close() }()
		_ = req.WriteProxy(conn)
		resp, err := http.ReadResponse(bufio.NewReader(conn), req)
		if err != nil {
			done <- err.Error()
			return
		}
		b, _ := io.ReadAll(resp.Body)
		done <- string(b)
	}()
	c.answer(t, Answer{Key: "deny", By: "the user", Note: "use the vendored copy"})
	body := <-done
	for _, want := range []string{"blocked.example.com is not allowed", "denied by the user", "use the vendored copy"} {
		if !strings.Contains(body, want) {
			t.Errorf("refusal %q lacks %q", body, want)
		}
	}
}

// An open session asks nobody and allows every destination, private addresses too: with filtering off, connections
// still go out through the host, so its VPN and tailnet stay reachable. The host machine itself stays limited to the
// endpoints the sandbox was given.
func TestOpenSessionAllowsEverything(t *testing.T) {
	s, c := newSession(t)
	s.Open = true
	s.Resolver = resolveTo{netip.MustParseAddr("10.1.3.31")}
	if !allow(s, "llm.internal.example.com", 443) {
		t.Fatal("an open session refused a destination")
	}
	if c.open() != 0 {
		t.Error("an open session asked")
	}
	if s.scope("llm.internal.example.com:443") != Private {
		t.Error("an open session's dial can't reach a private address")
	}
	if allow(s, HostGateway, 5432) {
		t.Error("an open session reached a host port it wasn't given")
	}
}

// An allow the card could not save still lets the connection through, but says so, and tells SaveFailed.
func TestEgressCardSaveFailure(t *testing.T) {
	for _, key := range []string{ForSession, Always, AnyHost} {
		s, c := newSession(t)
		s.GrantsPath = filepath.Join(t.TempDir(), "missing", "egress.json")
		s.Always = func(Rule, bool) error { return errors.New("disk full") }
		var failed []error
		s.SaveFailed = func(_ Rule, err error) { failed = append(failed, err) }
		done := make(chan bool, 1)
		go func() { done <- allow(s, "x.example.com", 443) }()
		c.answer(t, Answer{Key: key})
		if !<-done {
			t.Fatalf("%s: refused", key)
		}
		if len(failed) != 1 {
			t.Fatalf("%s: SaveFailed told %d times", key, len(failed))
		}
		if why := s.Why("x.example.com", 443); !strings.Contains(why, "not saved") {
			t.Errorf("%s: why = %q", key, why)
		}
	}
}

// What the agent's provider reaches is allowed without asking, under the user's rules: a setting that blocks one of
// its hosts still blocks it.
func TestTheAgentsHostsAreAllowed(t *testing.T) {
	s, _ := newSession(t)
	s.Agent = func() []Rule { return HostRules([]string{"*.chatgpt.com"}) }
	if !allow(s, "ab.chatgpt.com", 443) {
		t.Error("the provider's host was refused")
	}
	if r := s.Levels().Builtin; !slices.ContainsFunc(r, func(r Rule) bool { return r.Host == "*.chatgpt.com" && r.Source == "the agent's provider" }) {
		t.Errorf("built-in rules = %+v", r)
	}
	s.Settings = func() [][]Rule {
		return [][]Rule{{{Host: "ab.chatgpt.com", Port: 443, Allow: false, Source: "settings"}}}
	}
	if allow(s, "ab.chatgpt.com", 443) {
		t.Error("a setting's block didn't hold over the provider's host")
	}
}
