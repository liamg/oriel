package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/jsonrpc"
)

// DialHTTP speaks the Streamable HTTP transport to url. hdr is added to every
// request.
func DialHTTP(ctx context.Context, url string, hdr http.Header) (*Client, error) {
	c := &Client{}
	t := &httpTransport{
		url:    url,
		hdr:    hdr,
		hc:     &http.Client{},
		handle: c.handle,
		reinit: c.initialize,
		client: c,
	}
	c.t = t
	if err := c.initialize(ctx); err != nil {
		_ = t.close()
		return nil, err
	}
	return c, nil
}

type httpTransport struct {
	url    string
	hdr    http.Header
	hc     *http.Client
	handle func(*message)
	reinit func(context.Context) error
	client *Client

	reinitMu sync.Mutex

	mu       sync.Mutex
	session  string
	listen   context.CancelFunc
	isClosed bool
}

func (t *httpTransport) sessionID() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.session
}

func (t *httpTransport) request(ctx context.Context, m *message) (*message, error) {
	body, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	sess := t.sessionID()
	resp, err := t.do(ctx, http.MethodPost, body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound && sess != "" && m.Method != "initialize" {
		_ = resp.Body.Close()
		if err := t.reset(ctx, sess); err != nil {
			return nil, fmt.Errorf("mcp: session expired, reinitializing: %w", err)
		}
		if resp, err = t.do(ctx, http.MethodPost, body); err != nil {
			return nil, err
		}
	}
	defer func() { _ = resp.Body.Close() }()
	if err := statusErr(resp); err != nil {
		return nil, err
	}
	if m.Method == "initialize" {
		if id := resp.Header.Get("Mcp-Session-Id"); id != "" {
			t.mu.Lock()
			t.session = id
			t.mu.Unlock()
		}
	}
	return t.readResponse(resp, m.ID)
}

func (t *httpTransport) send(ctx context.Context, m *message) error {
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	resp, err := t.do(ctx, http.MethodPost, body)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	return statusErr(resp)
}

// reset re-initializes after the server dropped session old. Concurrent
// callers seeing the same expiry reinitialize once.
func (t *httpTransport) reset(ctx context.Context, old string) error {
	t.reinitMu.Lock()
	defer t.reinitMu.Unlock()
	t.mu.Lock()
	if t.session != old {
		t.mu.Unlock()
		return nil
	}
	t.session = ""
	t.mu.Unlock()
	return t.reinit(ctx)
}

// do sends one HTTP request.
func (t *httpTransport) do(ctx context.Context, method string, body []byte) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, t.url, r)
	if err != nil {
		return nil, err
	}
	for k, vs := range t.hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
	} else {
		req.Header.Set("Accept", "text/event-stream")
	}
	t.client.mu.Lock()
	proto := t.client.protocol
	t.client.mu.Unlock()
	if proto != "" {
		req.Header.Set("MCP-Protocol-Version", proto)
	}
	if s := t.sessionID(); s != "" {
		req.Header.Set("Mcp-Session-Id", s)
	}
	return t.hc.Do(req)
}

func statusErr(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	msg := strings.TrimSpace(string(b))
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	return fmt.Errorf("mcp: http %d: %s", resp.StatusCode, msg)
}

// readResponse finds the response to id in a JSON or SSE body, dispatching
// anything else it carries.
func (t *httpTransport) readResponse(resp *http.Response, id json.RawMessage) (*message, error) {
	want := jsonrpc.IDKey(id)
	var found *message
	take := func(data []byte) bool {
		for _, m := range decodeMessages(data) {
			if m.IsResponse() && jsonrpc.IDKey(m.ID) == want {
				found = m
			} else {
				t.handle(m)
			}
		}
		return found != nil
	}
	ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	switch ct {
	case "text/event-stream":
		if err := readSSE(resp.Body, take); err != nil && found == nil {
			return nil, err
		}
	case "application/json":
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		take(b)
	default:
		return nil, fmt.Errorf("mcp: unexpected response (http %d, %q)", resp.StatusCode, ct)
	}
	if found == nil {
		return nil, errors.New("mcp: server sent no response")
	}
	return found, nil
}

// decodeMessages decodes a message or a batch of them.
func decodeMessages(data []byte) []*message {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '[' {
		var ms []*message
		if json.Unmarshal(data, &ms) != nil {
			return nil
		}
		return ms
	}
	var m message
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	return []*message{&m}
}

// readSSE calls f with each event's data until f returns true or r ends.
func readSSE(r io.Reader, f func(data []byte) bool) error {
	br := bufio.NewReader(r)
	var data bytes.Buffer
	for {
		line, err := br.ReadString('\n')
		if err != nil && line == "" {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			if data.Len() > 0 && f(data.Bytes()) {
				return nil
			}
			data.Reset()
		case strings.HasPrefix(line, ":"):
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
}

// initialized opens the GET stream for server-initiated messages.
func (t *httpTransport) initialized() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.isClosed {
		return
	}
	if t.listen != nil {
		t.listen()
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.listen = cancel
	go t.listenLoop(ctx)
}

func (t *httpTransport) listenLoop(ctx context.Context) {
	for ctx.Err() == nil {
		resp, err := t.do(ctx, http.MethodGet, nil)
		if err != nil {
			return
		}
		ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if resp.StatusCode != http.StatusOK || ct != "text/event-stream" {
			_ = resp.Body.Close()
			return
		}
		_ = readSSE(resp.Body, func(data []byte) bool {
			for _, m := range decodeMessages(data) {
				t.handle(m)
			}
			return false
		})
		_ = resp.Body.Close()
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
}

func (t *httpTransport) close() error {
	t.mu.Lock()
	if t.isClosed {
		t.mu.Unlock()
		return nil
	}
	t.isClosed = true
	if t.listen != nil {
		t.listen()
	}
	sess := t.session
	t.mu.Unlock()
	if sess == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if resp, err := t.do(ctx, http.MethodDelete, nil); err == nil {
		_ = resp.Body.Close()
	}
	return nil
}
