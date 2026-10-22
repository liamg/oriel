package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
)

// transport carries messages to one server. Incoming requests and
// notifications go to the handle func it was built with.
type transport interface {
	// request sends a request and returns its response.
	request(ctx context.Context, m *message) (*message, error)
	// send sends a notification or a response.
	send(ctx context.Context, m *message) error
	close() error
}

// Client is a session with one MCP server. Dial* initializes the session
// before returning it.
type Client struct {
	t      transport
	nextID atomic.Int64

	mu           sync.Mutex
	info         ServerInfo
	instructions string
	protocol     string
	onTools      func()
}

// ServerInfo is what the server reported on initialize.
func (c *Client) ServerInfo() ServerInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.info
}

// Instructions is the server's usage guidance from initialize, if any.
func (c *Client) Instructions() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.instructions
}

// OnToolsChanged sets f to run, on its own goroutine, whenever the server
// sends notifications/tools/list_changed.
func (c *Client) OnToolsChanged(f func()) {
	c.mu.Lock()
	c.onTools = f
	c.mu.Unlock()
}

// ListTools returns every tool, following pagination.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	var all []Tool
	cursor := ""
	for {
		var params any
		if cursor != "" {
			params = map[string]string{"cursor": cursor}
		}
		var res listToolsResult
		if err := c.call(ctx, "tools/list", params, &res); err != nil {
			return nil, err
		}
		all = append(all, res.Tools...)
		if res.NextCursor == "" || res.NextCursor == cursor {
			return all, nil
		}
		cursor = res.NextCursor
	}
}

// CallTool calls a tool. A tool failure is a result with IsError set; the
// error is for protocol and transport failures.
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (CallResult, error) {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	var res CallResult
	err := c.call(ctx, "tools/call", callParams{Name: name, Arguments: args}, &res)
	return res, err
}

// Close ends the session.
func (c *Client) Close() error {
	return c.t.close()
}

func (c *Client) call(ctx context.Context, method string, params, out any) error {
	m := &message{
		JSONRPC: "2.0",
		ID:      json.RawMessage(strconv.FormatInt(c.nextID.Add(1), 10)),
		Method:  method,
	}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		m.Params = b
	}
	resp, err := c.t.request(ctx, m)
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return resp.Error
	}
	if out != nil {
		return json.Unmarshal(resp.Result, out)
	}
	return nil
}

func (c *Client) notify(ctx context.Context, method string) error {
	return c.t.send(ctx, &message{JSONRPC: "2.0", Method: method})
}

func (c *Client) initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      ServerInfo{Name: "oriel", Version: clientVersion()},
	}
	var res initializeResult
	if err := c.call(ctx, "initialize", params, &res); err != nil {
		return err
	}
	c.mu.Lock()
	c.info, c.instructions, c.protocol = res.ServerInfo, res.Instructions, res.ProtocolVersion
	c.mu.Unlock()
	if err := c.notify(ctx, "notifications/initialized"); err != nil {
		return err
	}
	if s, ok := c.t.(interface{ initialized() }); ok {
		s.initialized()
	}
	return nil
}

// handle answers server-initiated messages.
func (c *Client) handle(m *message) {
	if m.Method == "" {
		return
	}
	if m.ID == nil {
		c.notified(m.Method)
		return
	}
	resp := &message{JSONRPC: "2.0", ID: m.ID}
	if res, err := c.answer(m.Method); err != nil {
		errors.As(err, &resp.Error)
	} else {
		resp.Result, _ = json.Marshal(res)
	}
	go func() { _ = c.t.send(context.Background(), resp) }()
}

// answer is the reply to a request from the server.
func (c *Client) answer(method string) (any, error) {
	if method == "ping" {
		return struct{}{}, nil
	}
	return nil, &Error{Code: CodeMethodNotFound, Message: "method not found: " + method}
}

func (c *Client) notified(method string) {
	if method != "notifications/tools/list_changed" {
		return
	}
	c.mu.Lock()
	f := c.onTools
	c.mu.Unlock()
	if f != nil {
		go f()
	}
}

func clientVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "dev"
}
