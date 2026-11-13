package gitlab

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/liamg/oriel/jsonrpc"
	"github.com/liamg/oriel/registry"
)

// CodeNoKey is the JSON-RPC error of a request made before the token is set.
const CodeNoKey = -32010

// Repo is a project as the code host's functions are asked about it, with the site it's on.
type Repo struct {
	Session string `json:"session,omitempty"`
	Project string `json:"project,omitempty"`
	Remote  string `json:"remote"`
	Repo    string `json:"repo"` // its path: group/sub/project
	Site    Site   `json:"site"`
}

// Params are what the plugin's Lua sends: the code host's arguments by name.
type Params struct {
	Site   Site               `json:"site"`
	Host   string             `json:"host"` // probe's
	URL    string             `json:"url"`
	Repo   Repo               `json:"repo"`
	Branch string             `json:"branch"`
	PR     registry.PR        `json:"pr"`
	New    registry.NewPR     `json:"new"`
	Title  string             `json:"title"`
	Body   string             `json:"body"`
	Who    []string           `json:"who"`
	Opts   registry.MergeOpts `json:"opts"`
	Thread string             `json:"thread"`
}

// Request is the host's http.request: an authenticated one names the secret in auth.
type Request func(ctx context.Context, req map[string]any) (status int, body string, headers map[string]string, err error)

// Server answers the plugin's Lua.
type Server struct {
	Request Request
	members Members
}

// client is the Client for a site: its API's root, and its secret as the bearer token.
func (s *Server) client(site Site) (Client, error) {
	if site.API == "" || site.Secret == "" {
		return Client{}, errors.New("no GitLab site given")
	}
	return Client{Host: site.Host, Members: &s.members, Do: func(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
		req := map[string]any{"method": method, "url": strings.TrimRight(site.API, "/") + path, "auth": site.Secret,
			"headers": map[string]string{"Content-Type": "application/json"}}
		if body != nil {
			req["body"] = string(body)
		}
		status, resp, _, err := s.Request(ctx, req)
		if err != nil && strings.Contains(err.Error(), site.Secret+" isn't set") {
			return 0, nil, &jsonrpc.Error{Code: CodeNoKey, Message: "no GitLab token for " + cmp.Or(site.Host, "the site") + " yet"}
		}
		return status, []byte(resp), err
	}}, nil
}

// Probe says whether host is a GitLab, from one request without a token: its version endpoint answers, or refuses
// with GitLab's own header.
func (s *Server) Probe(ctx context.Context, host string) (bool, error) {
	if host == "" || strings.ContainsAny(host, "/?#@ ") {
		return false, errors.New("not a host name: " + host)
	}
	status, _, headers, err := s.Request(ctx, map[string]any{"method": "GET", "url": "https://" + host + "/api/v4/version"})
	if err != nil {
		return false, err
	}
	return status == 200 || status == 401 && headers["x-gitlab-meta"] != "", nil
}

// Handle answers one request, each in Oriel's shapes for a code host (registry.CodeHost): requested ({requests}),
// lookup (a ReviewDetail), checkout, for_branch ({pr}), create and edit (a PR), ready, request_reviewers, checks
// ({checks}), merge_methods, blockers ({blockers}), merge, threads ({threads}), reply ({id}) and probe ({gitlab}).
func (s *Server) Handle(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	var p Params
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: err.Error()}
		}
	}
	if method == "probe" {
		ok, err := s.Probe(ctx, p.Host)
		return map[string]any{"gitlab": ok}, err
	}
	if method == "checkout" {
		return Checkout(p.URL)
	}
	site := p.Site
	if site.API == "" {
		site = p.Repo.Site
	}
	c, err := s.client(site)
	if err != nil {
		return nil, &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: err.Error()}
	}
	path := p.Repo.Repo
	var out any
	switch method {
	case "requested":
		var rs []registry.ReviewRequest
		rs, err = c.Requested(ctx)
		out = map[string]any{"requests": rs}
	case "lookup":
		out, err = c.Lookup(ctx, p.URL)
	case "for_branch":
		var pr *registry.PR
		pr, err = c.ForBranch(ctx, path, p.Branch)
		out = map[string]any{"pr": pr}
	case "create":
		out, err = c.Create(ctx, path, p.New)
	case "edit":
		out, err = c.Edit(ctx, path, p.PR, p.Title, p.Body)
	case "ready":
		err = c.Ready(ctx, path, p.PR)
		out = map[string]any{"ok": err == nil}
	case "request_reviewers":
		err = c.RequestReviewers(ctx, path, p.PR, p.Who)
		out = map[string]any{"ok": err == nil}
	case "checks":
		var cs []registry.Check
		cs, err = c.Checks(ctx, path, p.PR)
		out = map[string]any{"checks": cs}
	case "merge_methods":
		out, err = c.MergeMethods(ctx, path)
	case "blockers":
		var bs []registry.MergeBlocker
		bs, err = c.Blockers(ctx, path, p.PR)
		out = map[string]any{"blockers": bs}
	case "merge":
		err = c.Merge(ctx, path, p.PR, p.Opts)
		out = map[string]any{"ok": err == nil}
	case "threads":
		var ts []registry.ReviewThread
		ts, err = c.Threads(ctx, path, p.PR)
		out = map[string]any{"threads": ts}
	case "reply":
		var id string
		id, err = c.Reply(ctx, path, p.PR, p.Thread, p.Body)
		out = map[string]any{"id": id}
	default:
		return nil, &jsonrpc.Error{Code: jsonrpc.MethodNotFound, Message: "no method " + method}
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Serve answers the Lua over r and w (the process's stdin and stdout) until it closes. Its requests to GitLab go
// back over the same connection, as http.request, for the host to make with the site's token.
func Serve(r io.Reader, w io.Writer) error {
	var conn *jsonrpc.Conn
	s := &Server{Request: func(ctx context.Context, req map[string]any) (int, string, map[string]string, error) {
		var res struct {
			Status  int               `json:"status"`
			Body    string            `json:"body"`
			Headers map[string]string `json:"headers"`
		}
		err := conn.Call(ctx, "http.request", req, &res)
		return res.Status, res.Body, res.Headers, err
	}}
	// The conn starts reading as it's made, so a request waits for conn to be set before it can call back.
	ready := make(chan struct{})
	conn = jsonrpc.NewConn(r, w, jsonrpc.Handlers{Request: func(req *jsonrpc.Request) {
		go func() {
			<-ready
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			req.Reply(s.Handle(ctx, req.Method, req.Params))
		}()
	}}, jsonrpc.Options{Version: "2.0"})
	close(ready)
	<-conn.Done()
	if err := conn.Err(); err != nil && !errors.Is(err, jsonrpc.ErrClosed) {
		return err
	}
	return nil
}
