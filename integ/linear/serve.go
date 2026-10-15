package linear

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/liamg/oriel/agent/jsonrpc"
	"github.com/liamg/oriel/registry"
)

// CodeNoKey is the JSON-RPC error of a request made before the API key is set.
const CodeNoKey = -32010

// Params are what the plugin's Lua sends.
type Params struct {
	Key    string `json:"key"`    // a ticket's: INF-2231
	URL    string `json:"url"`    // link: the pull request's
	Number int    `json:"number"` // link
	Title  string `json:"title"`  // link
}

// Server answers the plugin's Lua.
type Server struct {
	Client Client
}

// Handle answers one request, each in Oriel's shapes for a tracker (registry.TicketTracker): assigned
// ({tickets}), ticket (a registry.Ticket), start, review and done (a registry.TicketMove), or link.
func (s *Server) Handle(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	var p Params
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: err.Error()}
		}
	}
	if method != "assigned" && strings.TrimSpace(p.Key) == "" {
		return nil, &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: method + " needs a ticket's key"}
	}
	c := s.Client
	var out any
	var err error
	switch method {
	case "assigned":
		var issues []Issue
		issues, err = c.AssignedIssues(ctx)
		tickets := make([]registry.Ticket, len(issues))
		for i, is := range issues {
			tickets[i] = is.Ticket()
		}
		out = map[string]any{"tickets": tickets}
	case "ticket":
		var t Ticket
		if t, err = c.Ticket(ctx, p.Key); err == nil {
			out = t.Detail()
		}
	case "start", "review", "done":
		move := map[string]func(context.Context, string) (bool, string, error){"start": c.Start, "review": c.Review, "done": c.Done}[method]
		var m registry.TicketMove
		m.Moved, m.State, err = move(ctx, p.Key)
		out = m
	case "link":
		err = c.LinkPR(ctx, p.Key, p.URL, p.Number, p.Title)
		out = map[string]any{"linked": err == nil}
	default:
		return nil, &jsonrpc.Error{Code: jsonrpc.MethodNotFound, Message: "no method " + method}
	}
	if err != nil {
		return nil, noKey(err)
	}
	return out, nil
}

// noKey is err, said plainly when it's the host saying the API key isn't set.
func noKey(err error) error {
	if strings.Contains(err.Error(), Secret+" isn't set") {
		return &jsonrpc.Error{Code: CodeNoKey, Message: "no Linear API key yet"}
	}
	return err
}

// Serve answers the Lua over r and w (the process's stdin and stdout) until it closes. Its requests to Linear go
// back over the same connection, as http.request, for the host to make with the key.
func Serve(r io.Reader, w io.Writer) error {
	s := &Server{}
	var conn *jsonrpc.Conn
	s.Client = Client{Post: func(ctx context.Context, body []byte) (int, []byte, error) {
		var res struct {
			Status int    `json:"status"`
			Body   string `json:"body"`
		}
		err := conn.Call(ctx, "http.request", map[string]any{"method": "POST", "url": Endpoint, "body": string(body),
			"headers": map[string]string{"Content-Type": "application/json"},
			"auth":    Secret, "auth_scheme": "none"}, &res)
		return res.Status, []byte(res.Body), err
	}}
	conn = jsonrpc.NewConn(r, w, jsonrpc.Handlers{Request: func(req *jsonrpc.Request) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			req.Reply(s.Handle(ctx, req.Method, req.Params))
		}()
	}}, jsonrpc.Options{Version: "2.0"})
	<-conn.Done()
	if err := conn.Err(); err != nil && !errors.Is(err, jsonrpc.ErrClosed) {
		return err
	}
	return nil
}
