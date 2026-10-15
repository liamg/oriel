package agentbus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/websocket"
)

// Environment variables a harness reads, as agentbus's own client does.
const (
	EnvURL  = "AGENTBUS_URL"
	EnvHost = "AGENTBUS_HOST"
)

// Options shape a connection.
type Options struct {
	URL      string // ws://agentbus.example.ts.net/v1/ws
	Instance Instance
	Accepts  *Accepts
	Presence string
	Header   http.Header // sent with the handshake: a fake bus reads who you are from it
	// OnFrame gets the frames that aren't replies to this client's calls: offers, cancels, presence and state.
	OnFrame func(Frame)
}

// Client is one connection to the bus. It doesn't reconnect; Run does.
type Client struct {
	opts Options
	ws   *websocket.Conn
	you  From

	ref     atomic.Uint64
	mu      sync.Mutex
	waiting map[string]chan Frame
	done    chan struct{}
	err     error
}

// Error is the bus refusing something.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// maxFrame is the largest frame read.
const maxFrame = 1 << 20

// Dial connects and says hello.
func Dial(ctx context.Context, opts Options) (*Client, error) {
	if opts.URL == "" {
		return nil, errors.New("no URL")
	}
	u, err := url.Parse(opts.URL)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", opts.URL, err)
	}
	origin := "http://" + u.Host
	if u.Scheme == "wss" {
		origin = "https://" + u.Host
	}
	cfg, err := websocket.NewConfig(opts.URL, origin)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", opts.URL, err)
	}
	for k, v := range opts.Header {
		cfg.Header[k] = v
	}
	ws, err := cfg.DialContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", opts.URL, err)
	}
	ws.MaxPayloadBytes = maxFrame
	c := &Client{opts: opts, ws: ws, waiting: map[string]chan Frame{}, done: make(chan struct{})}
	go c.readLoop()
	hello := Frame{Type: TypeHello, Instance: &opts.Instance, Accepts: opts.Accepts, Presence: opts.Presence, PID: os.Getpid()}
	w, err := c.call(ctx, hello)
	if err != nil {
		c.Close()
		return nil, err
	}
	if w.You != nil {
		c.you = *w.You
	}
	return c, nil
}

// You is who the bus says this connection is.
func (c *Client) You() From { return c.you }

// Done is closed when the connection ends.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err is why the connection ended.
func (c *Client) Err() error {
	<-c.done
	return c.err
}

func (c *Client) Close() error { return c.ws.Close() }

func (c *Client) readLoop() {
	defer close(c.done)
	for {
		var b []byte
		if err := websocket.Message.Receive(c.ws, &b); err != nil {
			c.err = err
			return
		}
		var f Frame
		if json.Unmarshal(b, &f) != nil {
			continue
		}
		c.mu.Lock()
		ch, ok := c.waiting[f.Ref]
		if ok && f.Ref != "" {
			delete(c.waiting, f.Ref)
		}
		c.mu.Unlock()
		if ok && f.Ref != "" {
			ch <- f
			continue
		}
		if c.opts.OnFrame != nil {
			c.opts.OnFrame(f)
		}
	}
}

// call sends f and waits for the frame that carries its ref.
func (c *Client) call(ctx context.Context, f Frame) (Frame, error) {
	f.Ref = strconv.FormatUint(c.ref.Add(1), 10)
	ch := make(chan Frame, 1)
	c.mu.Lock()
	c.waiting[f.Ref] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.waiting, f.Ref)
		c.mu.Unlock()
	}()
	b, err := json.Marshal(f)
	if err != nil {
		return Frame{}, err
	}
	if err := websocket.Message.Send(c.ws, string(b)); err != nil {
		return Frame{}, err
	}
	select {
	case r := <-ch:
		if r.Type == TypeError {
			return r, &Error{Code: r.Code, Message: r.Error}
		}
		return r, nil
	case <-c.done:
		return Frame{}, fmt.Errorf("connection closed: %w", c.err)
	case <-ctx.Done():
		return Frame{}, ctx.Err()
	}
}

// SetStatus changes presence or what the instance accepts; empty values leave them.
func (c *Client) SetStatus(ctx context.Context, presence string, accepts *Accepts) error {
	_, err := c.call(ctx, Frame{Type: TypeStatus, Presence: presence, Accepts: accepts})
	return err
}

// Who lists the instances connected.
func (c *Client) Who(ctx context.Context, filter *WhoFilter) ([]Online, error) {
	r, err := c.call(ctx, Frame{Type: TypeWho, Filter: filter})
	return r.Instances, err
}

// Send starts a request and returns its id.
func (c *Client) Send(ctx context.Context, typ string, to Target, ttl time.Duration, body any) (string, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	f := Frame{Type: typ, To: &to, Body: b}
	if ttl > 0 {
		f.TTL = ttl.String()
	}
	r, err := c.call(ctx, f)
	return r.ID, err
}

func (c *Client) Ask(ctx context.Context, to Target, ttl time.Duration, body AskBody) (string, error) {
	return c.Send(ctx, TypeAsk, to, ttl, body)
}

func (c *Client) RequestReview(ctx context.Context, to Target, ttl time.Duration, body ReviewRequestBody) (string, error) {
	return c.Send(ctx, TypeReviewRequest, to, ttl, body)
}

// Reply answers, claims or reports on a request offered to this instance.
func (c *Client) Reply(ctx context.Context, typ, replyTo string, body any) (string, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	r, err := c.call(ctx, Frame{Type: typ, ReplyTo: replyTo, Body: b})
	return r.ID, err
}

func (c *Client) Answer(ctx context.Context, askID string, body AnswerBody) error {
	_, err := c.Reply(ctx, TypeAnswer, askID, body)
	return err
}

func (c *Client) ClaimReview(ctx context.Context, reviewID string) error {
	_, err := c.Reply(ctx, TypeReviewClaim, reviewID, struct{}{})
	return err
}

func (c *Client) ReviewResult(ctx context.Context, reviewID string, body ReviewResultBody) error {
	_, err := c.Reply(ctx, TypeReviewResult, reviewID, body)
	return err
}

func (c *Client) Cancel(ctx context.Context, id string) error {
	_, err := c.call(ctx, Frame{Type: TypeCancel, ID: id})
	return err
}

// Get is a request's state and its replies.
func (c *Client) Get(ctx context.Context, id string) (Frame, error) {
	return c.call(ctx, Frame{Type: TypeGet, ID: id})
}

// Run keeps a connection up: it dials, calls fn, and dials again with backoff after a drop, until ctx ends. A bad
// request (the bus refusing the hello) ends it.
func Run(ctx context.Context, opts Options, fn func(context.Context, *Client) error) error {
	backoff := time.Second
	for {
		c, err := Dial(ctx, opts)
		if err == nil {
			backoff = time.Second
			cctx, cancel := context.WithCancel(ctx)
			go func() {
				select {
				case <-c.Done():
				case <-cctx.Done():
				}
				cancel()
			}()
			err = fn(cctx, c)
			cancel()
			c.Close()
			<-c.Done()
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var pe *Error
		if errors.As(err, &pe) && pe.Code == ErrBadRequest {
			return err
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

// ResolveURL is where the bus is: url if set, else $AGENTBUS_URL, else ws://<$AGENTBUS_HOST or "agentbus">.<the
// tailnet's MagicDNS suffix>/v1/ws.
func ResolveURL(ctx context.Context, url string) (string, error) {
	if url != "" {
		return url, nil
	}
	if u := os.Getenv(EnvURL); u != "" {
		return u, nil
	}
	host := os.Getenv(EnvHost)
	if host == "" {
		host = "agentbus"
	}
	if strings.Contains(host, ".") {
		return "ws://" + host + "/v1/ws", nil
	}
	suffix, err := magicDNSSuffix(ctx)
	if err != nil {
		return "", fmt.Errorf("find the tailnet's name (set plugin.agentbus.url to skip this): %w", err)
	}
	return "ws://" + host + "." + suffix + "/v1/ws", nil
}

func magicDNSSuffix(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	bin := "tailscale"
	if _, err := exec.LookPath(bin); err != nil {
		mac := "/Applications/Tailscale.app/Contents/MacOS/Tailscale"
		if _, err := os.Stat(mac); err != nil {
			return "", errors.New("tailscale CLI not found")
		}
		bin = mac
	}
	out, err := exec.CommandContext(ctx, bin, "status", "--json").Output()
	if err != nil {
		return "", fmt.Errorf("tailscale status: %w", err)
	}
	var st struct {
		MagicDNSSuffix string
		CurrentTailnet *struct{ MagicDNSSuffix string }
	}
	if err := json.Unmarshal(out, &st); err != nil {
		return "", err
	}
	if st.CurrentTailnet != nil && st.CurrentTailnet.MagicDNSSuffix != "" {
		return st.CurrentTailnet.MagicDNSSuffix, nil
	}
	if st.MagicDNSSuffix == "" {
		return "", errors.New("tailscale status has no MagicDNS suffix")
	}
	return strings.TrimSuffix(st.MagicDNSSuffix, "."), nil
}
