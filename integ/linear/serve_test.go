package linear

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent/jsonrpc"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/integ/linear/lineartest"
	"github.com/liamg/oriel/registry"
)

// host is the app's side of the process: its host methods, the plugin granted what its oriel.toml declares.
type host struct {
	client  *http.Client
	secrets ext.Secrets
}

var _ ext.Network = host{}

func (h host) Log() *slog.Logger    { return slog.New(slog.DiscardHandler) }
func (h host) Notify(core.Notice)   {}
func (h host) Net() *http.Client    { return h.client }
func (h host) Secrets() ext.Secrets { return h.secrets }

// serving runs Serve as the plugin's process would, and is the Lua's end of it.
func serving(t *testing.T, h host) *jsonrpc.Conn {
	t.Helper()
	m, err := ext.LoadManifest(filepath.Join("plugin"))
	if err != nil {
		t.Fatal(err)
	}
	g := ext.NewGrants()
	g.Grant(m.ID, m.Permissions)
	p := ext.Plugin{ID: m.ID, Guard: g}
	methods := ext.HostMethods(h)
	toProc, procIn := io.Pipe()
	procOut, fromProc := io.Pipe()
	go Serve(toProc, fromProc)
	conn := jsonrpc.NewConn(procOut, procIn, jsonrpc.Handlers{Request: func(r *jsonrpc.Request) {
		go func() { r.Reply(methods.Call(context.Background(), p, r.Method, r.Params)) }()
	}}, jsonrpc.Options{Version: "2.0"})
	t.Cleanup(func() { conn.Close() })
	return conn
}

// security.md: "The plugin calls oriel.http.get(url, { auth = "LINEAR_TOKEN" }); the host injects the header" — the
// process asks the host for each request, and the key goes only to api.linear.app.
func TestServeThroughTheHost(t *testing.T) {
	l := lineartest.New(t)
	secrets := &ext.SecretFile{Path: filepath.Join(t.TempDir(), "secrets.json")}
	conn := serving(t, host{client: l.Client(), secrets: secrets})
	ctx := t.Context()

	err := conn.Call(ctx, "assigned", nil, nil)
	var rpc *jsonrpc.Error
	if !errors.As(err, &rpc) || rpc.Code != CodeNoKey || rpc.Message != "no Linear API key yet" {
		t.Fatalf("with no key: %v", err)
	}

	secrets.SetSecret(Secret, lineartest.Key)
	var got struct {
		Tickets []registry.Ticket `json:"tickets"`
	}
	if err := conn.Call(ctx, "assigned", nil, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Tickets) != 4 || got.Tickets[0].Key != "INF-2231" || got.Tickets[0].Stage != registry.TicketTodo ||
		got.Tickets[0].PR == nil || got.Tickets[0].PR.Number != 4398 || got.Tickets[0].PriorityLabel != "High" {
		t.Errorf("assigned %+v", got.Tickets)
	}
	if b := got.Tickets[1]; b.Key != "INF-2240" || len(b.BlockedBy) != 1 {
		t.Errorf("INF-2240 %+v", b)
	}
	if s := got.Tickets[2]; s.Key != "INF-2219" || s.Stage != registry.TicketStarted {
		t.Errorf("INF-2219 %+v", s)
	}
	var tk registry.Ticket
	if err := conn.Call(ctx, "ticket", Params{Key: "INF-2231"}, &tk); err != nil {
		t.Fatal(err)
	}
	if tk.Key != "INF-2231" || !strings.Contains(tk.Context, "## Comments (4)") || len(tk.Facts) != 4 || tk.Summary == "" {
		t.Errorf("ticket %+v", tk)
	}
	var moved registry.TicketMove
	if err := conn.Call(ctx, "start", Params{Key: "INF-2231"}, &moved); err != nil || !moved.Moved || moved.State != "In Progress" {
		t.Errorf("start: %+v, %v", moved, err)
	}
	if err := conn.Call(ctx, "done", Params{Key: "INF-2231"}, &moved); err != nil || !moved.Moved || moved.State != "Done" {
		t.Errorf("done: %+v, %v", moved, err)
	}
	if err := conn.Call(ctx, "ticket", Params{}, nil); err == nil {
		t.Error("a ticket with no key")
	}
	for _, a := range l.Auths() {
		if a != "api.linear.app "+lineartest.Key {
			t.Errorf("a request went as %q", a)
		}
	}
	raw, _ := json.Marshal(tk)
	if strings.Contains(string(raw), lineartest.Key) {
		t.Error("the answer has the key in it")
	}
}
