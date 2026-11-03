package agentsock

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

// Client is the container's side of the session socket.
type Client struct {
	token string
	http  *http.Client
}

// NewSocket is a client for the session at the unix socket path.
func NewSocket(path, token string) *Client {
	return NewDial(func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}, token)
}

// NewDial is a client for the session at the end of dial.
func NewDial(dial func(context.Context) (net.Conn, error), token string) *Client {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) }}
	return &Client{token: token, http: &http.Client{Transport: tr}}
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://oriel"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set(TokenHeader, c.token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(b)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// GatewayTools lists the gateway's tools and the list's version.
func (c *Client) GatewayTools(ctx context.Context) ([]json.RawMessage, int, error) {
	var out struct {
		Tools   []json.RawMessage `json:"tools"`
		Version int               `json:"version"`
	}
	err := c.do(ctx, http.MethodGet, ToolsPath, nil, &out)
	return out.Tools, out.Version, err
}

// GatewayCall runs a gateway tool; it blocks while the user decides.
func (c *Client) GatewayCall(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.do(ctx, http.MethodPost, CallPath, map[string]any{"name": name, "arguments": args}, &out)
	return out, err
}

// EgressDecide asks whether the sandbox may connect to host:port.
func (c *Client) EgressDecide(ctx context.Context, host string, port int) (allow bool, why string, err error) {
	var out struct {
		Allow bool   `json:"allow"`
		Why   string `json:"why"`
	}
	err = c.do(ctx, http.MethodPost, DecidePath, map[string]any{"host": host, "port": port}, &out)
	return out.Allow, out.Why, err
}

// EgressLog reports one finished connection.
func (c *Client) EgressLog(ev any) error {
	return c.do(context.Background(), http.MethodPost, LogPath, ev, nil)
}

// SSHStatus reports whether the sandbox's ssh-agent grant is open.
func (c *Client) SSHStatus(ctx context.Context) (bool, error) {
	var out struct {
		Live bool `json:"live"`
	}
	err := c.do(ctx, http.MethodGet, SSHStatusPath, nil, &out)
	return out.Live, err
}
