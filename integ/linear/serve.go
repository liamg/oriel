package linear

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/liamg/oriel/agent/jsonrpc"
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

// TicketAnswer is ticket's answer: the ticket, and what the launcher's card and the agent get of it.
type TicketAnswer struct {
	Ticket
	Summary string   `json:"summary"`
	Facts   []string `json:"facts"`
	Context string   `json:"context"`
}

// Moved is start's and review's answer.
type Moved struct {
	Moved bool   `json:"moved"`
	State string `json:"state"`
}

// Server answers the plugin's Lua.
type Server struct {
	Client Client
}

// Handle answers one request: assigned, ticket, start, review or link.
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
		out = map[string]any{"issues": issues}
	case "ticket":
		var t Ticket
		if t, err = c.Ticket(ctx, p.Key); err == nil {
			out = TicketAnswer{Ticket: t, Summary: t.Summary(), Facts: t.Facts(), Context: t.Context()}
		}
	case "start", "review":
		var m Moved
		if method == "start" {
			m.Moved, m.State, err = c.Start(ctx, p.Key)
		} else {
			m.Moved, m.State, err = c.Review(ctx, p.Key)
		}
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
