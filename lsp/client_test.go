package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/jsonrpc"
	"github.com/liamg/oriel/lsp/protocol"
	"github.com/liamg/oriel/text"
)

// server is a scripted server: it answers initialize with caps, other requests with answer, and keeps
// everything the client sends.
type server struct {
	t      *testing.T
	caps   string
	answer func(method string, params json.RawMessage) (any, error)

	mu    sync.Mutex
	got   []jsonrpc.Message
	more  chan struct{} // closed when a message is kept, then replaced
	conn  *jsonrpc.Conn
	ready chan struct{}
	quit  chan struct{}
}

func newServer(t *testing.T, caps string) *server {
	return &server{t: t, caps: caps, ready: make(chan struct{}), quit: make(chan struct{}), more: make(chan struct{})}
}

func (s *server) launch(ctx context.Context, spec ServerSpec, root string, stderr io.Writer) (Process, error) {
	a, b := net.Pipe()
	s.conn = NewConn(b, jsonrpc.Handlers{
		Request: jsonrpc.Answer(func(ctx context.Context, method string, params json.RawMessage) (any, error) {
			s.keep(jsonrpc.Message{Method: method, Params: params, ID: json.RawMessage("0")})
			switch method {
			case "initialize":
				return json.RawMessage(`{"capabilities":` + s.caps + `}`), nil
			case "shutdown":
				return nil, nil
			}
			if s.answer != nil {
				return s.answer(method, params)
			}
			return nil, nil
		}),
		Notify: func(method string, params json.RawMessage) {
			s.keep(jsonrpc.Message{Method: method, Params: params})
			if method == "initialized" {
				close(s.ready)
			}
			if method == "exit" {
				close(s.quit)
			}
		},
	})
	return &pipeProcess{Conn: a, quit: s.quit, srv: s.conn}, nil
}

func (s *server) keep(m jsonrpc.Message) {
	s.mu.Lock()
	s.got = append(s.got, m)
	close(s.more)
	s.more = make(chan struct{})
	s.mu.Unlock()
}

// await is what find picks from the messages kept, waiting for more until it picks one.
func (s *server) await(what string, find func([]jsonrpc.Message) (json.RawMessage, bool)) json.RawMessage {
	s.t.Helper()
	hung := time.After(10 * time.Second)
	for {
		s.mu.Lock()
		p, ok := find(s.got)
		more := s.more
		s.mu.Unlock()
		if ok {
			return p
		}
		select {
		case <-more:
		case <-hung:
			s.t.Fatalf("the client never sent %s", what)
		}
	}
}

// last is the params of the last message with method, waiting for it to arrive.
func (s *server) last(method string) json.RawMessage {
	s.t.Helper()
	return s.await(method, func(got []jsonrpc.Message) (json.RawMessage, bool) {
		for i := len(got) - 1; i >= 0; i-- {
			if got[i].Method == method {
				return got[i].Params, true
			}
		}
		return nil, false
	})
}

// nth is the params of the nth (from 1) message with method, waiting for it to arrive.
func (s *server) nth(method string, n int) json.RawMessage {
	s.t.Helper()
	return s.await(fmt.Sprintf("%d %s", n, method), func(got []jsonrpc.Message) (json.RawMessage, bool) {
		k := 0
		for _, m := range got {
			if m.Method == method {
				if k++; k == n {
					return m.Params, true
				}
			}
		}
		return nil, false
	})
}

func (s *server) count(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, m := range s.got {
		if m.Method == method {
			n++
		}
	}
	return n
}

// call sends the client a request, as the server.
func (s *server) call(method string, params, result any) error {
	return s.conn.Call(context.Background(), method, params, result)
}

func (s *server) notify(method string, params string) {
	s.conn.Notify(method, json.RawMessage(params))
}

type pipeProcess struct {
	net.Conn
	quit chan struct{}
	srv  *jsonrpc.Conn
}

func (p *pipeProcess) Wait() error {
	select {
	case <-p.quit:
		p.srv.Close()
		return nil
	case <-p.srv.Done():
		return errors.New("exit status 2")
	}
}

func (p *pipeProcess) Kill() error { return p.srv.Close() }

// crash ends the server without a shutdown.
func (s *server) crash() { s.conn.Close() }

func startClient(t *testing.T, s *server, opts Options) *Client {
	t.Helper()
	opts.Launch = s.launch
	c, err := Start(context.Background(), ServerSpec{Name: "fake", Settings: json.RawMessage(`{"fake":{"a":{"b":1}},"x":2}`)}, t.TempDir(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c.Shutdown(ctx)
	})
	<-s.ready
	return c
}

func decode[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestInitialize(t *testing.T) {
	s := newServer(t, `{}`)
	c := startClient(t, s, Options{})
	p := decode[map[string]any](t, s.last("initialize"))
	caps := p["capabilities"].(map[string]any)
	general := caps["general"].(map[string]any)
	// ref: lsp/types/textDocuments.md#textDocuments
	// quote: "The value is an array of position encodings the client supports, with decreasing preference (e.g. the encoding at index `0` is the most preferred one)."
	if got := general["positionEncodings"]; !reflect.DeepEqual(got, []any{"utf-8", "utf-16"}) {
		t.Errorf("positionEncodings = %v", got)
	}
	td := caps["textDocument"].(map[string]any)
	item := td["completion"].(map[string]any)["completionItem"].(map[string]any)
	// ref: lsp/language/completion.md#textDocument_completion
	// quote: "Client supports snippets as insert text."
	if item["snippetSupport"] != true {
		t.Errorf("snippetSupport = %v", item["snippetSupport"])
	}
	// ref: lsp/language/hover.md#textDocument_hover
	// quote: "The order describes the preferred format of the client."
	if got := td["hover"].(map[string]any)["contentFormat"]; !reflect.DeepEqual(got, []any{"markdown", "plaintext"}) {
		t.Errorf("hover contentFormat = %v", got)
	}
	if p["rootUri"] != string(URI(c.Root())) || p["processId"] != nil {
		t.Errorf("rootUri = %v, processId = %v", p["rootUri"], p["processId"])
	}
	// ref: lsp/general/initialize.md#initialize
	// quote: "If omitted it defaults to 'utf-16'."
	if c.Encoding() != protocol.UTF16 {
		t.Errorf("encoding = %q, want utf-16 when the server names none", c.Encoding())
	}
	// ref: lsp/messages/initialized.md#initialized
	// quote: "The initialized notification is sent from the client to the server after the client received the result of the `initialize` request"
	if s.count("initialized") != 1 {
		t.Error("no initialized notification")
	}
	if got := string(s.last("workspace/didChangeConfiguration")); got != `{"settings":{"fake":{"a":{"b":1}},"x":2}}` {
		t.Errorf("didChangeConfiguration = %s", got)
	}
}

func TestEncodingNegotiated(t *testing.T) {
	s := newServer(t, `{"positionEncoding":"utf-8"}`)
	if c := startClient(t, s, Options{}); c.Encoding() != protocol.UTF8 {
		t.Errorf("encoding = %q", c.Encoding())
	}
}

// open opens a document holding content on a new client whose server takes the sync kind given.
func open(t *testing.T, caps string, content string) (*Client, *server, *text.Buffer, string) {
	s := newServer(t, caps)
	c := startClient(t, s, Options{})
	path := filepath.Join(c.Root(), "a.go")
	buf := text.New([]byte(content), text.Author{Kind: text.User})
	if err := c.DidOpen(path, "go", buf.Snapshot()); err != nil {
		t.Fatal(err)
	}
	return c, s, buf, path
}

func change(t *testing.T, buf *text.Buffer, rs ...text.Replace) text.Change {
	t.Helper()
	ch, err := buf.Apply(text.Edit{Ranges: rs})
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

func TestDidOpen(t *testing.T) {
	_, s, _, path := open(t, `{"textDocumentSync":2}`, "x")
	p := decode[protocol.DidOpenTextDocumentParams](t, s.last("textDocument/didOpen"))
	// ref: lsp/textDocument/didOpen.md#textDocument_didOpen
	// quote: "The document open notification is sent from the client to the server to signal newly opened text documents."
	if p.TextDocument != (protocol.TextDocumentItem{URI: URI(path), LanguageID: "go", Version: 1, Text: "x"}) {
		t.Errorf("didOpen = %+v", p.TextDocument)
	}
}

// ref: lsp/textDocument/didChange.md#textDocument_didChange
// quote: "c1 moves the document from S to S' and c2 from S' to"
func TestDidChangeIncremental(t *testing.T) {
	c, s, buf, path := open(t, `{"textDocumentSync":2}`, "a🎉b\nsecond é line\n")
	// One replace, after a character that is 4 bytes and 2 UTF-16 units.
	if err := c.DidChange(path, change(t, buf, text.Replace{Range: text.Range{Start: 5, End: 6}, Text: []byte("B")})); err != nil {
		t.Fatal(err)
	}
	p := decode[protocol.DidChangeTextDocumentParams](t, s.last("textDocument/didChange"))
	want := []protocol.TextDocumentContentChangeEvent{{Range: &protocol.Range{Start: protocol.Position{Line: 0, Character: 3}, End: protocol.Position{Line: 0, Character: 4}}, Text: "B"}}
	if p.TextDocument.Version != 2 || !reflect.DeepEqual(p.ContentChanges, want) {
		t.Errorf("didChange = %+v %+v", p.TextDocument, p.ContentChanges)
	}

	// Two replaces, as multi-cursor typing makes: the second is in the text the first left.
	snap := buf.Snapshot()
	i := strings.Index(snap.String(), "é")
	ch := change(t, buf, text.Replace{Range: text.Range{Start: 0, End: 0}, Text: []byte(">>")}, text.Replace{Range: text.Range{Start: i, End: i + 2}, Text: []byte("e")})
	if len(ch.Replaces) != 2 {
		t.Fatalf("replaces = %v", ch.Replaces)
	}
	if err := c.DidChange(path, ch); err != nil {
		t.Fatal(err)
	}
	p = decode[protocol.DidChangeTextDocumentParams](t, s.nth("textDocument/didChange", 2))
	if p.TextDocument.Version != 3 || len(p.ContentChanges) != 2 {
		t.Fatalf("didChange = %+v", p)
	}
	// Applied in order, the events must turn the text before into the text after.
	if got := applyEvents(t, snap.String(), protocol.UTF16, p.ContentChanges); got != buf.Snapshot().String() {
		t.Errorf("events give %q, want %q", got, buf.Snapshot().String())
	}
}

// applyEvents mirrors a document as a server does.
func applyEvents(t *testing.T, s, enc string, events []protocol.TextDocumentContentChangeEvent) string {
	for _, e := range events {
		if e.Range == nil {
			s = e.Text
			continue
		}
		snap := text.New([]byte(s), text.Author{}).Snapshot()
		from := snap.Offset(fromProto(snap, enc, e.Range.Start))
		to := snap.Offset(fromProto(snap, enc, e.Range.End))
		s = s[:from] + e.Text + s[to:]
	}
	return s
}

// ref: lsp/specification.md#textDocument_synchronization
// quote: "Documents are synced by always sending the full content"
func TestDidChangeFull(t *testing.T) {
	c, s, buf, path := open(t, `{"textDocumentSync":1}`, "abc")
	c.DidChange(path, change(t, buf, text.Replace{Range: text.Range{Start: 1, End: 2}, Text: []byte("X")}))
	p := decode[protocol.DidChangeTextDocumentParams](t, s.last("textDocument/didChange"))
	if len(p.ContentChanges) != 1 || p.ContentChanges[0].Range != nil || p.ContentChanges[0].Text != "aXc" {
		t.Errorf("full change = %+v", p.ContentChanges)
	}
}

// ref: lsp/textDocument/didChange.md#textDocument_didChange
// quote: "The new text of the whole document."
func TestDidChangeOutOfOrder(t *testing.T) {
	c, s, buf, path := open(t, `{"textDocumentSync":2}`, "abc")
	first := change(t, buf, text.Replace{Range: text.Range{Start: 0, End: 0}, Text: []byte("1")})
	second := change(t, buf, text.Replace{Range: text.Range{Start: 0, End: 0}, Text: []byte("2")})
	c.DidChange(path, second) // arrives first: it doesn't follow what was sent, so the whole text goes
	p := decode[protocol.DidChangeTextDocumentParams](t, s.last("textDocument/didChange"))
	if len(p.ContentChanges) != 1 || p.ContentChanges[0].Range != nil || p.ContentChanges[0].Text != "21abc" {
		t.Errorf("out of order change = %+v", p.ContentChanges)
	}
	c.DidChange(path, first) // older than what was sent: dropped
	third := change(t, buf, text.Replace{Range: text.Range{Start: 0, End: 0}, Text: []byte("3")})
	c.DidChange(path, third)
	p = decode[protocol.DidChangeTextDocumentParams](t, s.nth("textDocument/didChange", 2))
	if len(p.ContentChanges) != 1 || p.ContentChanges[0].Text != "3" {
		t.Errorf("the change after the dropped one = %+v, want the third", p.ContentChanges)
	}
}

func TestDidSaveAndClose(t *testing.T) {
	c, s, buf, path := open(t, `{"textDocumentSync":{"openClose":true,"change":2,"save":{"includeText":true}}}`, "abc")
	if err := c.DidSave(path, buf.Snapshot()); err != nil {
		t.Fatal(err)
	}
	// ref: lsp/textDocument/didSave.md#textDocument_didSave
	// quote: "Optional the content when saved. Depends on the includeText value"
	if p := decode[protocol.DidSaveTextDocumentParams](t, s.last("textDocument/didSave")); p.Text == nil || *p.Text != "abc" {
		t.Errorf("didSave = %+v", p)
	}
	if err := c.DidClose(path); err != nil {
		t.Fatal(err)
	}
	s.last("textDocument/didClose")
	if err := c.DidClose(path); !errors.Is(err, ErrNotOpen) {
		t.Errorf("second close = %v", err)
	}
}

// DidSave is a document notification like the others: it waits its turn behind one being sent.
func TestDidSaveWaitsForSendOrder(t *testing.T) {
	c, s, buf, path := open(t, `{"textDocumentSync":{"openClose":true,"change":2,"save":{}}}`, "abc")
	c.sendMu.Lock()
	saved := make(chan error, 1)
	go func() { saved <- c.DidSave(path, buf.Snapshot()) }()
	for range 100 {
		runtime.Gosched() // give DidSave every chance to get past the order
	}
	select {
	case <-saved:
		c.sendMu.Unlock()
		t.Fatal("DidSave sent while another document notification held the order")
	default:
	}
	c.sendMu.Unlock()
	if err := <-saved; err != nil {
		t.Fatal(err)
	}
	s.last("textDocument/didSave")
}

// Diagnostics sent while initialize is answered are read with the encoding without racing its write (run with -race).
func TestDiagnosticsDuringInitialize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.go")
	if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newServer(t, `{"positionEncoding":"utf-8"}`)
	sets := make(chan DiagnosticSet, 8)
	launch := s.launch
	opts := Options{OnDiagnostics: func(d DiagnosticSet) { sets <- d }, Launch: func(ctx context.Context, spec ServerSpec, root string, stderr io.Writer) (Process, error) {
		p, err := launch(ctx, spec, root, stderr)
		s.conn.Notify("textDocument/publishDiagnostics", json.RawMessage(`{"uri":"`+string(URI(path))+`","diagnostics":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"message":"m"}]}`))
		return p, err
	}}
	c, err := Start(context.Background(), ServerSpec{Name: "fake"}, t.TempDir(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c.Shutdown(ctx)
	})
	select {
	case <-sets:
	case <-time.After(5 * time.Second):
		t.Fatal("no diagnostics")
	}
}

// ref: lsp/language/publishDiagnostics.md#textDocument_publishDiagnostics
// quote: "Diagnostics notifications are sent from the server to the client to signal results of validation runs."
func TestPublishDiagnostics(t *testing.T) {
	sets := make(chan DiagnosticSet, 4)
	s := newServer(t, `{"textDocumentSync":2}`)
	c := startClient(t, s, Options{OnDiagnostics: func(d DiagnosticSet) { sets <- d }})
	path := filepath.Join(c.Root(), "a.ts")
	c.DidOpen(path, "typescript", text.New([]byte("const s = \"🎉\" + nope;\n"), text.Author{}).Snapshot())
	s.notify("textDocument/publishDiagnostics", `{"uri":"`+string(URI(path))+`","version":1,"diagnostics":[{"range":{"start":{"line":0,"character":17},"end":{"line":0,"character":21}},`+
		`"severity":1,"code":2304,"source":"ts","message":"Cannot find name 'nope'.","tags":[1],"relatedInformation":[{"location":{"uri":"`+string(URI(path))+`","range":{"start":{"line":0,"character":6},"end":{"line":0,"character":7}}},"message":"here"}]}]}`)
	got := <-sets
	want := Diagnostic{Range: Range{Start: text.Pos{Line: 0, Col: 19}, End: text.Pos{Line: 0, Col: 23}}, Severity: SevError, Code: "2304", Source: "ts",
		Message: "Cannot find name 'nope'.", Unnecessary: true, Related: []Related{{Location: Location{Path: path, Range: Range{Start: text.Pos{Col: 6}, End: text.Pos{Col: 7}}}, Message: "here"}}}
	if got.Path != path || got.Version != 1 || got.Server != "fake" || len(got.Diagnostics) != 1 {
		t.Fatalf("set = %+v", got)
	}
	d := got.Diagnostics[0]
	d.raw = nil
	if !reflect.DeepEqual(d, want) {
		t.Errorf("diagnostic = %+v\nwant %+v", d, want)
	}
	if len(c.Diagnostics(path)) != 1 {
		t.Error("Diagnostics doesn't keep the set")
	}
	// ref: lsp/language/publishDiagnostics.md#textDocument_publishDiagnostics
	// quote: "If the computed set is empty it has to push the empty array to clear former diagnostics."
	s.notify("textDocument/publishDiagnostics", `{"uri":"`+string(URI(path))+`","diagnostics":[]}`)
	if got := <-sets; len(got.Diagnostics) != 0 || got.Version != -1 || len(c.Diagnostics(path)) != 0 {
		t.Errorf("clearing set = %+v", got)
	}
}

// ref: lsp/types/workDoneProgress.md#workDoneProgressBegin
// quote: "To start progress reporting a `$/progress` notification with the following payload must be sent:"
func TestProgressStatus(t *testing.T) {
	var mu sync.Mutex
	var seen []Status
	changed := make(chan struct{}, 1)
	s := newServer(t, `{}`)
	c := startClient(t, s, Options{OnStatus: func(st Status) {
		mu.Lock()
		seen = append(seen, st)
		mu.Unlock()
		select {
		case changed <- struct{}{}:
		default:
		}
	}})
	// ref: lsp/window/workDoneProgressCreate.md#window_workDoneProgress_create
	// quote: "The `window/workDoneProgress/create` request is sent from the server to the client to ask the client to create a work done progress."
	if err := s.call("window/workDoneProgress/create", map[string]any{"token": "t1"}, nil); err != nil {
		t.Fatal(err)
	}
	s.notify("$/progress", `{"token":"t1","value":{"kind":"begin","title":"Indexing","percentage":0}}`)
	s.notify("$/progress", `{"token":"t1","value":{"kind":"report","message":"3/10","percentage":30}}`)
	waitFor(t, changed, func() bool {
		st := c.Status()
		return st.State == Busy && len(st.Work) == 1 && st.Work[0].Percent == 30
	})
	if w := c.Status().Work[0]; w != (Work{Token: "t1", Title: "Indexing", Message: "3/10", Percent: 30}) {
		t.Errorf("work = %+v", w)
	}
	// ref: lsp/types/workDoneProgress.md#workDoneProgressEnd
	// quote: "Signaling the end of a progress reporting is done using the following payload:"
	s.notify("$/progress", `{"token":"t1","value":{"kind":"end"}}`)
	waitFor(t, changed, func() bool { return c.Status().State == Ready && len(c.Status().Work) == 0 })
	mu.Lock()
	defer mu.Unlock()
	var states []State
	for _, st := range seen {
		if len(states) == 0 || states[len(states)-1] != st.State {
			states = append(states, st.State)
		}
	}
	if !reflect.DeepEqual(states, []State{Starting, Ready, Busy, Ready}) {
		t.Errorf("states = %v", states)
	}
}

// waitFor waits for cond, looking again each time changed says something did.
func waitFor(t *testing.T, changed <-chan struct{}, cond func() bool) {
	t.Helper()
	hung := time.After(10 * time.Second)
	for !cond() {
		select {
		case <-changed:
		case <-hung:
			t.Fatal("timed out")
		}
	}
}

// ref: lsp/workspace/configuration.md#workspace_configuration
// quote: "If the client can't provide a configuration setting for a given scope then `null` needs to be present in the returned array."
func TestConfiguration(t *testing.T) {
	s := newServer(t, `{}`)
	startClient(t, s, Options{})
	var got []json.RawMessage
	err := s.call("workspace/configuration", map[string]any{"items": []map[string]string{{"section": "fake.a"}, {"section": "missing"}, {}}}, &got)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`{"b":1}`, `null`, `{"fake":{"a":{"b":1}},"x":2}`}
	for i := range want {
		if i >= len(got) || string(got[i]) != want[i] {
			t.Fatalf("configuration = %s, want %s", got, want)
		}
	}
}

// ref: lsp/workspace/applyEdit.md#workspace_applyEdit
// quote: "The `workspace/applyEdit` request is sent from the server to the client to modify resource on the client side."
func TestApplyEdit(t *testing.T) {
	var got WorkspaceEdit
	s := newServer(t, `{"positionEncoding":"utf-16"}`)
	c := startClient(t, s, Options{OnApplyEdit: func(label string, e WorkspaceEdit) (bool, string) { got = e; return true, "" }})
	path := filepath.Join(c.Root(), "a.txt")
	os.WriteFile(path, []byte("é𝄞x\n"), 0o644) // not open: read from disk to convert
	var res protocol.ApplyWorkspaceEditResult
	err := s.call("workspace/applyEdit", map[string]any{"edit": map[string]any{"documentChanges": []any{
		map[string]any{"kind": "create", "uri": URI(filepath.Join(c.Root(), "new.txt"))},
		map[string]any{"textDocument": map[string]any{"uri": URI(path), "version": nil}, "edits": []any{
			map[string]any{"range": map[string]any{"start": map[string]any{"line": 0, "character": 3}, "end": map[string]any{"line": 0, "character": 4}}, "newText": "y"}}},
	}}}, &res)
	if err != nil || !res.Applied {
		t.Fatalf("applyEdit = %+v, %v", res, err)
	}
	want := WorkspaceEdit{Changes: []DocumentChange{
		{Op: "create", Path: filepath.Join(c.Root(), "new.txt")},
		{Path: path, Edits: []TextEdit{{Range: Range{Start: text.Pos{Col: 6}, End: text.Pos{Col: 7}}, NewText: "y"}}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("edit = %+v\nwant %+v", got, want)
	}
}

func TestApplyEditRefused(t *testing.T) {
	s := newServer(t, `{}`)
	startClient(t, s, Options{})
	var res protocol.ApplyWorkspaceEditResult
	if err := s.call("workspace/applyEdit", map[string]any{"edit": map[string]any{}}, &res); err != nil || res.Applied || res.FailureReason == "" {
		t.Errorf("applyEdit with no handler = %+v, %v", res, err)
	}
}

func TestServerRequests(t *testing.T) {
	s := newServer(t, `{}`)
	c := startClient(t, s, Options{})
	// ref: lsp/messages/registerCapability.md#client_registerCapability
	// quote: "The `client/registerCapability` request is sent from the server to the client to register for a new capability on the client side."
	reg := map[string]any{"registrations": []any{map[string]any{"id": "1", "method": "workspace/didChangeWatchedFiles", "registerOptions": map[string]any{"watchers": []any{}}}}}
	if err := s.call("client/registerCapability", reg, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Registrations()["workspace/didChangeWatchedFiles"]; !ok {
		t.Errorf("registrations = %v", c.Registrations())
	}
	var folders []protocol.WorkspaceFolder
	if err := s.call("workspace/workspaceFolders", nil, &folders); err != nil || len(folders) != 1 || folders[0].URI != URI(c.Root()) {
		t.Errorf("workspaceFolders = %+v, %v", folders, err)
	}
	var e *jsonrpc.Error
	if err := s.call("unknown/request", nil, nil); !errors.As(err, &e) || e.Code != jsonrpc.MethodNotFound {
		t.Errorf("unknown request = %v", err)
	}
}

// ref: lsp/specification.md#dollarRequests
// quote: "If a server or client receives a request starting with '\\$/' it must error the request with error code `MethodNotFound` (e.g. `-32601`)."
func TestDollarRequestsAreMethodNotFound(t *testing.T) {
	a, b := net.Pipe()
	answers := NewConn(a, jsonrpc.Handlers{Request: jsonrpc.Answer(func(context.Context, string, json.RawMessage) (any, error) {
		return "ok", nil
	})})
	asks := NewConn(b, jsonrpc.Handlers{})
	t.Cleanup(func() { answers.Close(); asks.Close() })
	var got string
	if err := asks.Call(context.Background(), "x", nil, &got); err != nil || got != "ok" {
		t.Fatalf("x = %q, %v", got, err)
	}
	var e *jsonrpc.Error
	if err := asks.Call(context.Background(), "$/x", nil, nil); !errors.As(err, &e) || e.Code != jsonrpc.MethodNotFound {
		t.Errorf("$/x = %v", err)
	}
}

func TestCrash(t *testing.T) {
	var mu sync.Mutex
	var last Status
	s := newServer(t, `{}`)
	c := startClient(t, s, Options{OnStatus: func(st Status) { mu.Lock(); last = st; mu.Unlock() }})
	s.crash()
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done isn't closed after a crash")
	}
	if c.Status().State != Crashed || c.Err() == nil {
		t.Errorf("after a crash: %v, %v", c.Status().State, c.Err())
	}
	mu.Lock()
	defer mu.Unlock()
	if last.State != Crashed {
		t.Errorf("last status = %v", last.State)
	}
	if _, err := c.Hover(context.Background(), Loc{}); err == nil {
		t.Error("a request to a crashed server succeeded")
	}
}

// ref: lsp/messages/shutdown.md#shutdown
// quote: "Clients should also wait with sending the `exit` notification until they have received a response from the `shutdown` request."
func TestShutdown(t *testing.T) {
	s := newServer(t, `{}`)
	c := startClient(t, s, Options{})
	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.count("shutdown") != 1 || s.count("exit") != 1 || c.Status().State != Stopped || c.Err() != nil {
		t.Errorf("after shutdown: %d %d %v %v", s.count("shutdown"), s.count("exit"), c.Status().State, c.Err())
	}
}

// ref: lsp/textDocument/didChange.md#textDocument_didChange
// quote: "computed on the state S and c2 is computed on the state S'."
func TestIncrementalOrders(t *testing.T) {
	before := text.New([]byte("ab🎉cd\nef\n"), text.Author{}).Snapshot()
	tests := []struct {
		name string
		rs   []text.Replace
		ok   bool
	}{
		{"last first", []text.Replace{{Range: text.Range{Start: 8, End: 9}, Text: []byte("D")}, {Range: text.Range{Start: 0, End: 1}, Text: []byte("AA")}}, true},
		{"first first", []text.Replace{{Range: text.Range{Start: 0, End: 1}, Text: []byte("AA")}, {Range: text.Range{Start: 9, End: 10}, Text: []byte("D")}}, true},
		{"line joined", []text.Replace{{Range: text.Range{Start: 9, End: 12}, Text: nil}}, true},
		{"crossing", []text.Replace{{Range: text.Range{Start: 2, End: 2}, Text: []byte("x")}, {Range: text.Range{Start: 0, End: 1}, Text: nil}, {Range: text.Range{Start: 5, End: 5}, Text: []byte("y")}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := before.String()
			for _, r := range tc.rs {
				want = want[:r.Range.Start] + string(r.Text) + want[r.Range.End:]
			}
			for _, enc := range []string{protocol.UTF8, protocol.UTF16, protocol.UTF32} {
				events, ok := incremental(before, enc, tc.rs)
				if ok != tc.ok {
					t.Fatalf("%s: ok = %v", enc, ok)
				}
				if ok {
					if got := applyEvents(t, before.String(), enc, events); got != want {
						t.Errorf("%s: events give %q, want %q", enc, got, want)
					}
				}
			}
		})
	}
}

// ref: lsp/textDocument/didChange.md#textDocument_didChange
// quote: "c1 moves the document from S to S' and c2 from S' to"
func TestChangesReachTheServerBeforeALaterRequest(t *testing.T) {
	c, s, buf, path := open(t, `{"textDocumentSync":1,"hoverProvider":true}`, "abc")
	for _, r := range []string{"1", "2", "3"} {
		if err := c.DidChange(path, change(t, buf, text.Replace{Range: text.Range{Start: 0, End: 0}, Text: []byte(r)})); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.Hover(context.Background(), Loc{Path: path}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var versions []int32
	for _, m := range s.got {
		switch m.Method {
		case "textDocument/didChange":
			versions = append(versions, decode[protocol.DidChangeTextDocumentParams](t, m.Params).TextDocument.Version)
		case "textDocument/hover":
			if !reflect.DeepEqual(versions, []int32{2, 3, 4}) {
				t.Errorf("the hover reached the server after changes %v, want [2 3 4]", versions)
			}
			return
		}
	}
	t.Error("the server never saw the hover")
}
