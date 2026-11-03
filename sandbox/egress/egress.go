// Package egress is the forward proxy that is a sandboxed agent's only route
// out. It never intercepts TLS: HTTPS arrives as CONNECT host:port and plain
// HTTP as absolute-form requests, and each destination is put to a Decider.
package egress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Decision is what a Decider answers for one destination.
type Decision int

const (
	Deny Decision = iota
	Allow
)

// Decider rules on a destination. It may block for a long time (a human is
// being asked); ctx is cancelled if the client goes away.
type Decider interface {
	Decide(ctx context.Context, host string, port int) Decision
}

// Event is one connection attempt, for the session's log.
type Event struct {
	Host      string
	Port      int
	Allowed   bool
	At        time.Time
	BytesUp   int64
	BytesDown int64
	Err       string
	Method    string // CONNECT or the HTTP method
	// Proto is what the connection carried: http, tls, ssh, or tcp for
	// anything else. A tunnel that carried nothing is named by its port.
	Proto string
	// Path and Status are known for plain HTTP only; a tunnel is opaque.
	Path   string
	Status int
	Millis int64
	// Reason is why it was allowed or refused; the session fills it in.
	Reason string
}

// Failed reports a connection that never got through: refused, or allowed
// but not reached.
func (e Event) Failed() bool {
	return !e.Allowed || (e.Err != "" && e.Status == 0 && e.BytesDown == 0)
}

// protoOf names a tunnel by the first bytes the client sent through it.
func protoOf(head []byte, port int) string {
	switch {
	case len(head) >= 2 && head[0] == 0x16 && head[1] == 0x03:
		return "tls"
	case strings.HasPrefix(string(head), "SSH-"):
		return "ssh"
	case len(head) > 0:
		return "tcp"
	case port == 443:
		return "tls"
	case port == 22:
		return "ssh"
	}
	return "tcp"
}

// headReader keeps the first bytes read through it.
type headReader struct {
	r    io.Reader
	head []byte
}

func (h *headReader) Read(b []byte) (int, error) {
	n, err := h.r.Read(b)
	if need := 4 - len(h.head); need > 0 && n > 0 {
		h.head = append(h.head, b[:min(n, need)]...)
	}
	return n, err
}

// Proxy is an http.Handler serving CONNECT and absolute-form HTTP requests.
type Proxy struct {
	Decider Decider
	// Log is called once per connection when it ends or is refused.
	Log func(Event)
	// Dial reaches upstream; nil uses a net.Dialer with a 30s timeout.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// DecideTimeout bounds each decision; 0 means 60s. No answer in time is Deny.
	DecideTimeout time.Duration
	// Rewrite maps a host to the one actually dialled, after the decision.
	Rewrite map[string]string

	once      sync.Once
	transport *http.Transport
}

const (
	defaultDecideTimeout = 60 * time.Second
	defaultDialTimeout   = 30 * time.Second
)

var hopHeaders = []string{
	"Connection", "Proxy-Connection", "Proxy-Authorization", "Proxy-Authenticate",
	"Keep-Alive", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodConnect:
		p.serveConnect(w, r)
	case r.URL.IsAbs() && r.URL.Host != "":
		p.serveHTTP(w, r)
	default:
		http.Error(w, "oriel: not a proxy request", http.StatusBadRequest)
	}
}

// ListenAndServe serves on addr until ctx ends.
func (p *Proxy) ListenAndServe(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return p.Serve(ctx, ln)
}

// Serve serves on ln until ctx ends.
func (p *Proxy) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{Handler: p, ReadHeaderTimeout: 30 * time.Second}
	stop := context.AfterFunc(ctx, func() { _ = srv.Close() })
	defer stop()
	err := srv.Serve(ln)
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func (p *Proxy) serveConnect(w http.ResponseWriter, r *http.Request) {
	host, port, err := splitHost(r.Host, 0)
	if err != nil {
		http.Error(w, "oriel: "+err.Error(), http.StatusBadRequest)
		return
	}
	ev := Event{Host: host, Port: port, At: time.Now(), Method: http.MethodConnect, Proto: protoOf(nil, port)}
	defer func() {
		ev.Millis = time.Since(ev.At).Milliseconds()
		p.log(ev)
	}()

	if !p.decide(r.Context(), host, port, &ev) {
		p.deny(w, host, port)
		return
	}
	ev.Allowed = true

	up, err := p.dial(r.Context(), "tcp", p.target(host, port))
	if err != nil {
		ev.Err = err.Error()
		http.Error(w, "oriel: "+err.Error(), http.StatusBadGateway)
		return
	}
	client, brw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		_ = up.Close()
		ev.Err = err.Error()
		http.Error(w, "oriel: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		_ = client.Close()
		_ = up.Close()
		ev.Err = err.Error()
		return
	}
	sent := &headReader{r: brw.Reader}
	ev.BytesUp, ev.BytesDown, err = splice(client, sent, up)
	ev.Proto = protoOf(sent.head, port)
	if err != nil {
		ev.Err = err.Error()
	}
}

// splice copies both ways until both directions end, half-closing where the
// connection allows and closing both outright where it does not.
func splice(client net.Conn, clientR io.Reader, up net.Conn) (upN, downN int64, err error) {
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		once sync.Once
	)
	closeBoth := func() {
		once.Do(func() {
			_ = client.Close()
			_ = up.Close()
		})
	}
	record := func(e error) {
		if e == nil || errors.Is(e, net.ErrClosed) {
			return
		}
		mu.Lock()
		if err == nil {
			err = e
		}
		mu.Unlock()
	}
	pipe := func(dst net.Conn, src io.Reader, n *int64) {
		defer wg.Done()
		c, e := io.Copy(dst, src)
		atomic.AddInt64(n, c)
		record(e)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok && e == nil {
			if cw.CloseWrite() == nil {
				return
			}
		}
		closeBoth()
	}
	wg.Add(2)
	go pipe(up, clientR, &upN)
	go pipe(client, up, &downN)
	wg.Wait()
	closeBoth()
	return upN, downN, err
}

func (p *Proxy) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Scheme != "http" {
		http.Error(w, "oriel: only http:// may be proxied without CONNECT", http.StatusBadRequest)
		return
	}
	host, port, err := splitHost(r.URL.Host, 80)
	if err != nil {
		http.Error(w, "oriel: "+err.Error(), http.StatusBadRequest)
		return
	}
	ev := Event{Host: host, Port: port, At: time.Now(), Method: r.Method, Proto: "http", Path: r.URL.RequestURI()}
	defer func() {
		ev.Millis = time.Since(ev.At).Milliseconds()
		p.log(ev)
	}()

	if !p.decide(r.Context(), host, port, &ev) {
		p.deny(w, host, port)
		return
	}
	ev.Allowed = true

	out := r.Clone(r.Context())
	out.RequestURI = ""
	out.URL.Host = p.target(host, port)
	out.Host = r.Host
	out.Trailer = nil
	stripHop(out.Header)
	up := &countingReader{r: r.Body}
	if r.Body != nil && r.Body != http.NoBody {
		out.Body = up
	}

	resp, err := p.roundTripper().RoundTrip(out)
	if err != nil {
		ev.Err = err.Error()
		http.Error(w, "oriel: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	stripHop(resp.Header)
	for k, vs := range resp.Header {
		w.Header()[k] = vs
	}
	w.WriteHeader(resp.StatusCode)
	ev.Status = resp.StatusCode
	n, err := io.Copy(flushWriter{w}, resp.Body)
	ev.BytesUp, ev.BytesDown = up.n.Load(), n
	if err != nil {
		ev.Err = err.Error()
	}
}

// decide asks the Decider, bounded by DecideTimeout and the client's lifetime.
// A Decider that ignores its ctx still cannot hold the connection past it.
func (p *Proxy) decide(ctx context.Context, host string, port int, ev *Event) bool {
	if p.Decider == nil {
		ev.Err = "no decider"
		return false
	}
	timeout := p.DecideTimeout
	if timeout <= 0 {
		timeout = defaultDecideTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	done := make(chan Decision, 1)
	go func() { done <- p.Decider.Decide(ctx, host, port) }()
	select {
	case d := <-done:
		return d == Allow
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			ev.Err = "no decision in time"
		} else {
			ev.Err = "client went away"
		}
		return false
	}
}

// Explainer is a Decider that says why it refused a destination, for the refusal the client reads.
type Explainer interface {
	Why(host string, port int) string
}

func (p *Proxy) deny(w http.ResponseWriter, host string, port int) {
	msg := fmt.Sprintf("oriel: %s is not allowed for this session", host)
	if e, ok := p.Decider.(Explainer); ok {
		if why := e.Why(host, port); why != "" {
			msg += ": " + why
		}
	}
	http.Error(w, msg, http.StatusForbidden)
}

func (p *Proxy) log(ev Event) {
	if p.Log != nil {
		p.Log(ev)
	}
}

func (p *Proxy) target(host string, port int) string {
	if to, ok := p.Rewrite[host]; ok {
		host = to
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

func (p *Proxy) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	if p.Dial != nil {
		return p.Dial(ctx, network, addr)
	}
	d := net.Dialer{Timeout: defaultDialTimeout}
	return d.DialContext(ctx, network, addr)
}

func (p *Proxy) roundTripper() http.RoundTripper {
	p.once.Do(func() {
		p.transport = &http.Transport{
			DialContext:           p.dial,
			DisableCompression:    true,
			MaxIdleConnsPerHost:   4,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: 5 * time.Minute,
		}
	})
	return p.transport
}

// splitHost normalises host[:port]: lower-case, no trailing dot, no IPv6
// brackets, and a port in 1..65535 (def when absent; 0 means it is required).
func splitHost(hostport string, def int) (string, int, error) {
	host, portStr, err := net.SplitHostPort(hostport)
	if err != nil {
		if def == 0 || strings.Contains(err.Error(), "too many colons") {
			return "", 0, fmt.Errorf("bad destination %q", hostport)
		}
		host, portStr = hostport, strconv.Itoa(def)
	}
	host = strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	if host == "" {
		return "", 0, fmt.Errorf("bad destination %q", hostport)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("bad port in %q", hostport)
	}
	return host, port, nil
}

func stripHop(h http.Header) {
	for _, v := range h.Values("Connection") {
		for f := range strings.SplitSeq(v, ",") {
			if f = strings.TrimSpace(f); f != "" {
				h.Del(f)
			}
		}
	}
	for _, k := range hopHeaders {
		h.Del(k)
	}
}

type countingReader struct {
	r io.ReadCloser
	n atomic.Int64
}

func (c *countingReader) Read(b []byte) (int, error) {
	n, err := c.r.Read(b)
	c.n.Add(int64(n))
	return n, err
}

func (c *countingReader) Close() error { return c.r.Close() }

// flushWriter flushes after every write so streamed responses stay streamed.
type flushWriter struct{ w http.ResponseWriter }

func (f flushWriter) Write(b []byte) (int, error) {
	n, err := f.w.Write(b)
	_ = http.NewResponseController(f.w).Flush()
	return n, err
}
