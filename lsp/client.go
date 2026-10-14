package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/liamg/oriel/lsp/jsonrpc"
	"github.com/liamg/oriel/lsp/protocol"
	"github.com/liamg/oriel/text"
)

var (
	ErrNotSupported = errors.New("lsp: the server doesn't support this")
	ErrNotOpen      = errors.New("lsp: the document isn't open")
)

// State is where a server is in its life.
type State int

const (
	Starting State = iota
	Ready
	Busy // indexing or loading: work in progress
	Stopped
	Crashed
)

var stateNames = [...]string{"starting", "ready", "busy", "stopped", "crashed"}

func (s State) String() string {
	if int(s) < len(stateNames) {
		return stateNames[s]
	}
	return fmt.Sprintf("State(%d)", int(s))
}

// Status is a server's state for the status line: the work it reports while busy, and why it crashed.
type Status struct {
	Server  string
	Root    string
	Version string // from the server's initialize answer
	State   State
	Work    []Work // in the order it started
	Err     error
}

// Work is a piece of work in progress a server reports ($/progress).
type Work struct {
	Token   string
	Title   string
	Message string
	Percent int // -1 when unknown
}

// MessageLevel is a window/showMessage or window/logMessage type.
type MessageLevel int

const (
	MessageError MessageLevel = iota + 1
	MessageWarning
	MessageInfo
	MessageLog
	MessageDebug
)

// Options are what a client needs from the app. Callbacks run on the client's goroutine, one at a time, in
// the order the server sent what they report.
type Options struct {
	Launch        Launcher                          // nil runs Exec
	Stderr        io.Writer                         // the server's stderr and log messages; nil drops them
	ReadFile      func(path string) ([]byte, error) // for files that aren't open; nil reads the disk
	Client        string                            // the name the server sees; "oriel" if empty
	Version       string                            // Oriel's version, sent with the name
	ProcessID     *int                              // sent so the server can exit if the app dies; nil sends null
	OnDiagnostics func(DiagnosticSet)
	OnStatus      func(Status)
	OnApplyEdit   func(label string, e WorkspaceEdit) (applied bool, reason string) // nil refuses
	OnMessage     func(server string, level MessageLevel, msg string)               // window/showMessage
	OnLog         func(server string, level MessageLevel, msg string)               // window/logMessage; nil writes to Stderr
	OnRefresh     func(server, what string)                                         // "semanticTokens", "inlayHint", "codeLens", "diagnostic"
}

// Client is a connection to one running server for one root. It is safe for concurrent use.
type Client struct {
	spec ServerSpec
	root string
	opts Options

	proc Process
	conn *jsonrpc.Conn

	caps    protocol.ServerCapabilities
	enc     string
	sync    protocol.TextDocumentSync
	version string

	sendMu sync.Mutex // keeps document notifications in version order

	mu       sync.Mutex
	docs     map[string]*document
	diags    map[string]DiagnosticSet
	status   Status
	work     map[string]*Work
	order    []string // work tokens in the order they began
	regs     map[string]json.RawMessage
	stopping bool

	done chan struct{}
}

type document struct {
	snap     text.Snapshot
	version  int32
	language string
}

// Start launches a server for root and initializes it.
func Start(ctx context.Context, spec ServerSpec, root string, opts Options) (*Client, error) {
	launch := opts.Launch
	if spec.Connect != nil {
		launch = spec.Connect
	}
	if launch == nil {
		launch = Exec
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	c := &Client{spec: spec, root: root, opts: opts, docs: map[string]*document{}, diags: map[string]DiagnosticSet{},
		work: map[string]*Work{}, regs: map[string]json.RawMessage{}, done: make(chan struct{}),
		status: Status{Server: spec.Name, Root: root, State: Starting}}
	proc, err := launch(ctx, spec, root, stderr)
	if err != nil {
		return nil, fmt.Errorf("lsp: starting %s: %w", spec.Name, err)
	}
	c.proc = proc
	c.conn = jsonrpc.NewConn(proc, jsonrpc.Handlers{Request: c.handleRequest, Notify: c.handleNotify})
	go c.watch()
	c.notifyStatus()
	if err := c.initialize(ctx); err != nil {
		proc.Kill()
		c.conn.Close()
		return nil, fmt.Errorf("lsp: initializing %s: %w", spec.Name, err)
	}
	return c, nil
}

func (c *Client) initialize(ctx context.Context) error {
	name := c.opts.Client
	if name == "" {
		name = "oriel"
	}
	rootURI := URI(c.root)
	params := protocol.InitializeParams{
		ProcessID:             c.opts.ProcessID,
		ClientInfo:            &protocol.ClientInfo{Name: name, Version: c.opts.Version},
		RootPath:              &c.root,
		RootURI:               &rootURI,
		InitializationOptions: c.spec.Init,
		Capabilities:          clientCapabilities(),
		WorkspaceFolders:      []protocol.WorkspaceFolder{{URI: rootURI, Name: filepath.Base(c.root)}},
	}
	var res protocol.InitializeResult
	if err := c.conn.Call(ctx, "initialize", params, &res); err != nil {
		return err
	}
	c.caps = res.Capabilities
	c.enc = res.Capabilities.PositionEncoding
	if c.enc == "" {
		c.enc = protocol.UTF16
	}
	c.sync = protocol.DecodeSync(res.Capabilities.TextDocumentSync)
	if res.ServerInfo != nil {
		c.version = res.ServerInfo.Version
	}
	if err := c.conn.Notify("initialized", struct{}{}); err != nil {
		return err
	}
	if len(c.spec.Settings) > 0 {
		c.conn.Notify("workspace/didChangeConfiguration", protocol.DidChangeConfigurationParams{Settings: c.spec.Settings})
	}
	c.mu.Lock()
	c.status.Version = c.version
	if c.status.State == Starting {
		c.status.State = Ready
		if len(c.work) > 0 {
			c.status.State = Busy
		}
	}
	c.mu.Unlock()
	c.notifyStatus()
	return nil
}

// Spec is the server the client runs.
func (c *Client) Spec() ServerSpec { return c.spec }

// Root is the project root it serves.
func (c *Client) Root() string { return c.root }

// Encoding is the position encoding agreed with the server: "utf-8", "utf-16" or "utf-32".
func (c *Client) Encoding() string { return c.enc }

// Capabilities are what the server said it can do.
func (c *Client) Capabilities() protocol.ServerCapabilities { return c.caps }

// Done is closed once the server has exited.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err is why the server exited: nil after Shutdown, or what a crash left.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status.Err
}

// Status is the server's current status.
func (c *Client) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusLocked()
}

func (c *Client) statusLocked() Status {
	s := c.status
	s.Work = nil
	for _, t := range c.order {
		s.Work = append(s.Work, *c.work[t])
	}
	return s
}

func (c *Client) notifyStatus() {
	if c.opts.OnStatus != nil {
		c.opts.OnStatus(c.Status())
	}
}

func (c *Client) watch() {
	err := c.proc.Wait()
	select {
	case <-c.conn.Done():
	case <-time.After(time.Second):
	}
	c.conn.Close()
	c.mu.Lock()
	if c.stopping {
		c.status.State = Stopped
	} else {
		c.status.State = Crashed
		if err == nil {
			err = errors.New("exited")
		}
		c.status.Err = fmt.Errorf("lsp: %s: %w", c.spec.Name, err)
	}
	clear(c.work)
	c.order = nil
	c.mu.Unlock()
	c.notifyStatus()
	close(c.done)
}

// Shutdown asks the server to stop, and kills it if it hasn't by the time ctx ends.
func (c *Client) Shutdown(ctx context.Context) error {
	c.mu.Lock()
	c.stopping = true
	c.mu.Unlock()
	err := c.conn.Call(ctx, "shutdown", nil, nil)
	if err == nil {
		c.conn.Notify("exit", nil)
	}
	select {
	case <-c.done:
	case <-ctx.Done():
		c.proc.Kill()
		<-c.done
	}
	return err
}

// DidOpen tells the server a document is open, with its text.
func (c *Client) DidOpen(path, language string, s text.Snapshot) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	c.mu.Lock()
	c.docs[path] = &document{snap: s, version: 1, language: language}
	c.mu.Unlock()
	if !c.sync.OpenClose {
		return nil
	}
	return c.conn.Notify("textDocument/didOpen", protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{
		URI: URI(path), LanguageID: LanguageID(language, path), Version: 1, Text: s.String()}})
}

// DidChange sends a buffer change: incrementally where the server takes that, else the whole text. A change
// that doesn't follow the last one sent (they can arrive out of order) sends the whole text; one older than
// it is dropped.
func (c *Client) DidChange(path string, ch text.Change) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	c.mu.Lock()
	d := c.docs[path]
	if d == nil {
		c.mu.Unlock()
		return ErrNotOpen
	}
	if ch.After.Version() <= d.snap.Version() {
		c.mu.Unlock()
		return nil
	}
	var events []protocol.TextDocumentContentChangeEvent
	ok := false
	if c.sync.Change == protocol.SyncIncremental && ch.Before.Version() == d.snap.Version() {
		events, ok = incremental(ch.Before, c.enc, ch.Replaces)
	}
	if !ok {
		events = []protocol.TextDocumentContentChangeEvent{{Text: ch.After.String()}}
	}
	d.snap = ch.After
	d.version++
	v := d.version
	c.mu.Unlock()
	if c.sync.Change == protocol.SyncNone {
		return nil
	}
	return c.conn.Notify("textDocument/didChange", protocol.DidChangeTextDocumentParams{
		TextDocument:   protocol.VersionedTextDocumentIdentifier{URI: URI(path), Version: v},
		ContentChanges: events})
}

// incremental turns a change's replaces into content changes against before. Replaces apply in order, each in
// the text the one before left. Last first, each is untouched by those before it, so its place in before holds;
// first first, each is shifted by those before it, so they're moved back to before's offsets and sent last first.
// Anything else isn't sent incrementally.
func incremental(before text.Snapshot, enc string, rs []text.Replace) ([]protocol.TextDocumentContentChangeEvent, bool) {
	descending, ascending := true, true
	for i := 1; i < len(rs); i++ {
		descending = descending && rs[i].Range.End <= rs[i-1].Range.Start
		ascending = ascending && rs[i].Range.Start >= rs[i-1].Range.Start+len(rs[i-1].Text)
	}
	orig := make([]text.Range, len(rs))
	delta := 0
	for i, r := range rs {
		orig[i] = r.Range
		if !descending {
			orig[i] = text.Range{Start: r.Range.Start - delta, End: r.Range.End - delta}
			delta += len(r.Text) - r.Range.Len()
		}
		if orig[i].Start < 0 || orig[i].End > before.Len() || orig[i].Start > orig[i].End {
			return nil, false
		}
	}
	if !descending && !ascending {
		return nil, false
	}
	event := func(i int) protocol.TextDocumentContentChangeEvent {
		r := protocol.Range{Start: toProto(before, enc, before.PosAt(orig[i].Start)), End: toProto(before, enc, before.PosAt(orig[i].End))}
		return protocol.TextDocumentContentChangeEvent{Range: &r, Text: string(rs[i].Text)}
	}
	out := make([]protocol.TextDocumentContentChangeEvent, 0, len(rs))
	for i := range rs {
		if descending {
			out = append(out, event(i))
		} else {
			out = append(out, event(len(rs)-1-i))
		}
	}
	return out, true
}

// DidSave tells the server a document was saved.
func (c *Client) DidSave(path string, s text.Snapshot) error {
	c.mu.Lock()
	_, open := c.docs[path]
	c.mu.Unlock()
	if !open {
		return ErrNotOpen
	}
	if !c.sync.Save {
		return nil
	}
	p := protocol.DidSaveTextDocumentParams{TextDocument: protocol.TextDocumentIdentifier{URI: URI(path)}}
	if c.sync.IncludeText {
		t := s.String()
		p.Text = &t
	}
	return c.conn.Notify("textDocument/didSave", p)
}

// DidClose tells the server a document is closed.
func (c *Client) DidClose(path string) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	c.mu.Lock()
	_, open := c.docs[path]
	delete(c.docs, path)
	c.mu.Unlock()
	if !open {
		return ErrNotOpen
	}
	if !c.sync.OpenClose {
		return nil
	}
	return c.conn.Notify("textDocument/didClose", protocol.DidCloseTextDocumentParams{TextDocument: protocol.TextDocumentIdentifier{URI: URI(path)}})
}

// Open lists the documents open on this client.
func (c *Client) Open() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for p := range c.docs {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

// Diagnostics are the last diagnostics the server pushed for a file.
func (c *Client) Diagnostics(path string) []Diagnostic {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.diags[path].Diagnostics
}

// Registrations are the capabilities the server registered dynamically, by method.
func (c *Client) Registrations() map[string]json.RawMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]json.RawMessage, len(c.regs))
	for k, v := range c.regs {
		out[k] = v
	}
	return out
}

func (c *Client) handleRequest(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "workspace/configuration":
		var p protocol.ConfigurationParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: err.Error()}
		}
		out := make([]json.RawMessage, len(p.Items))
		for i, it := range p.Items {
			out[i] = section(c.spec.Settings, it.Section)
		}
		return out, nil
	case "client/registerCapability":
		var p struct {
			Registrations []struct {
				ID              string          `json:"id"`
				Method          string          `json:"method"`
				RegisterOptions json.RawMessage `json:"registerOptions"`
			} `json:"registrations"`
		}
		json.Unmarshal(params, &p)
		c.mu.Lock()
		for _, r := range p.Registrations {
			c.regs[r.Method] = r.RegisterOptions
		}
		c.mu.Unlock()
		return nil, nil
	case "client/unregisterCapability":
		var p struct {
			Unregisterations []struct {
				Method string `json:"method"`
			} `json:"unregisterations"`
		}
		json.Unmarshal(params, &p)
		c.mu.Lock()
		for _, r := range p.Unregisterations {
			delete(c.regs, r.Method)
		}
		c.mu.Unlock()
		return nil, nil
	case "window/workDoneProgress/create":
		return nil, nil
	case "workspace/workspaceFolders":
		return []protocol.WorkspaceFolder{{URI: URI(c.root), Name: filepath.Base(c.root)}}, nil
	case "workspace/applyEdit":
		var p protocol.ApplyWorkspaceEditParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: err.Error()}
		}
		if c.opts.OnApplyEdit == nil {
			return protocol.ApplyWorkspaceEditResult{FailureReason: "the editor doesn't apply server edits"}, nil
		}
		e := c.conv().workspaceEdit(p.Edit)
		applied, reason := c.opts.OnApplyEdit(p.Label, e)
		return protocol.ApplyWorkspaceEditResult{Applied: applied, FailureReason: reason}, nil
	case "window/showMessageRequest":
		var p protocol.LogMessageParams
		json.Unmarshal(params, &p)
		if c.opts.OnMessage != nil {
			c.opts.OnMessage(c.spec.Name, MessageLevel(p.Type), p.Message)
		}
		return nil, nil
	case "window/showDocument":
		return protocol.ShowDocumentResult{}, nil
	case "workspace/semanticTokens/refresh", "workspace/inlayHint/refresh", "workspace/codeLens/refresh",
		"workspace/diagnostic/refresh", "workspace/inlineValue/refresh":
		if c.opts.OnRefresh != nil {
			what := method[len("workspace/") : len(method)-len("/refresh")]
			c.opts.OnRefresh(c.spec.Name, what)
		}
		return nil, nil
	}
	return nil, jsonrpc.ErrMethodNotFound
}

func (c *Client) handleNotify(method string, params json.RawMessage) {
	switch method {
	case "textDocument/publishDiagnostics":
		var p struct {
			URI         protocol.DocumentURI `json:"uri"`
			Version     *int                 `json:"version"`
			Diagnostics []json.RawMessage    `json:"diagnostics"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		path := Path(p.URI)
		v := c.conv()
		set := DiagnosticSet{Server: c.spec.Name, Path: path, Version: -1}
		if p.Version != nil {
			set.Version = *p.Version
		}
		for _, raw := range p.Diagnostics {
			if d, ok := v.diagnostic(path, raw); ok {
				set.Diagnostics = append(set.Diagnostics, d)
			}
		}
		c.mu.Lock()
		c.diags[path] = set
		c.mu.Unlock()
		if c.opts.OnDiagnostics != nil {
			c.opts.OnDiagnostics(set)
		}
	case "$/progress":
		c.progress(params)
	case "window/logMessage":
		var p protocol.LogMessageParams
		json.Unmarshal(params, &p)
		switch {
		case c.opts.OnLog != nil:
			c.opts.OnLog(c.spec.Name, MessageLevel(p.Type), p.Message)
		case c.opts.Stderr != nil:
			fmt.Fprintln(c.opts.Stderr, p.Message)
		}
	case "window/showMessage":
		var p protocol.LogMessageParams
		json.Unmarshal(params, &p)
		if c.opts.OnMessage != nil {
			c.opts.OnMessage(c.spec.Name, MessageLevel(p.Type), p.Message)
		}
	}
}

func (c *Client) progress(params json.RawMessage) {
	var p protocol.ProgressParams
	if json.Unmarshal(params, &p) != nil {
		return
	}
	var v protocol.WorkDoneProgress
	if json.Unmarshal(p.Value, &v) != nil || v.Kind == "" {
		return // a partial result, not work done
	}
	tok := p.Token.String()
	c.mu.Lock()
	switch v.Kind {
	case "begin":
		if _, ok := c.work[tok]; !ok {
			c.order = append(c.order, tok)
		}
		c.work[tok] = &Work{Token: tok, Title: v.Title, Message: v.Message, Percent: percent(v.Percentage)}
	case "report":
		if w := c.work[tok]; w != nil {
			if v.Message != "" {
				w.Message = v.Message
			}
			if v.Percentage != nil {
				w.Percent = percent(v.Percentage)
			}
		}
	case "end":
		delete(c.work, tok)
		c.order = slices.DeleteFunc(c.order, func(t string) bool { return t == tok })
	}
	if c.status.State == Ready || c.status.State == Busy {
		c.status.State = Ready
		if len(c.work) > 0 {
			c.status.State = Busy
		}
	}
	c.mu.Unlock()
	c.notifyStatus()
}

func percent(p *uint32) int {
	if p == nil {
		return -1
	}
	return int(*p)
}

// conv converts what the server sends to Oriel's positions, reading files that aren't open as it needs them.
func (c *Client) conv() *converter {
	return &converter{c: c, snaps: map[string]*text.Snapshot{}}
}

type converter struct {
	c     *Client
	snaps map[string]*text.Snapshot // nil: couldn't read
}

func (v *converter) snap(path string) *text.Snapshot {
	if s, ok := v.snaps[path]; ok {
		return s
	}
	v.c.mu.Lock()
	d := v.c.docs[path]
	v.c.mu.Unlock()
	var s *text.Snapshot
	if d != nil {
		s = &d.snap
	} else {
		read := v.c.opts.ReadFile
		if read == nil {
			read = os.ReadFile
		}
		if b, err := read(path); err == nil {
			snap := text.New(b, text.Author{Kind: text.File}).Snapshot()
			s = &snap
		}
	}
	v.snaps[path] = s
	return s
}

func (v *converter) pos(path string, p protocol.Position) text.Pos {
	if s := v.snap(path); s != nil {
		return fromProto(*s, v.c.enc, p)
	}
	return rawPos(p)
}

func (v *converter) rng(path string, r protocol.Range) Range {
	return Range{Start: v.pos(path, r.Start), End: v.pos(path, r.End)}
}

func (v *converter) location(l protocol.Location) Location {
	path := Path(l.URI)
	return Location{Path: path, Range: v.rng(path, l.Range)}
}

func (v *converter) edits(path string, es []protocol.TextEdit) []TextEdit {
	out := make([]TextEdit, 0, len(es))
	for _, e := range es {
		out = append(out, TextEdit{Range: v.rng(path, e.Range), NewText: e.NewText})
	}
	return out
}

func (v *converter) workspaceEdit(e protocol.WorkspaceEdit) WorkspaceEdit {
	var out WorkspaceEdit
	if e.DocumentChanges != nil {
		for _, raw := range e.DocumentChanges {
			te, op, err := protocol.DecodeDocumentChange(raw)
			switch {
			case err != nil:
			case te != nil:
				path := Path(te.TextDocument.URI)
				dc := DocumentChange{Path: path, Edits: v.edits(path, te.Edits)}
				if te.TextDocument.Version != nil {
					n := int(*te.TextDocument.Version)
					dc.Version = &n
				}
				out.Changes = append(out.Changes, dc)
			default:
				dc := DocumentChange{Op: op.Kind, Path: Path(op.URI)}
				if op.Kind == "rename" {
					dc.Path, dc.NewPath = Path(op.OldURI), Path(op.NewURI)
				}
				if o := op.Options; o != nil {
					dc.Overwrite, dc.IgnoreIfExists, dc.Recursive, dc.IgnoreIfNotExists = o.Overwrite, o.IgnoreIfExists, o.Recursive, o.IgnoreIfNotExists
				}
				out.Changes = append(out.Changes, dc)
			}
		}
		return out
	}
	uris := make([]string, 0, len(e.Changes))
	for u := range e.Changes {
		uris = append(uris, string(u))
	}
	slices.Sort(uris)
	for _, u := range uris {
		path := Path(protocol.DocumentURI(u))
		out.Changes = append(out.Changes, DocumentChange{Path: path, Edits: v.edits(path, e.Changes[protocol.DocumentURI(u)])})
	}
	return out
}

func (v *converter) diagnostic(path string, raw json.RawMessage) (Diagnostic, bool) {
	var d protocol.Diagnostic
	if json.Unmarshal(raw, &d) != nil {
		return Diagnostic{}, false
	}
	out := Diagnostic{Range: v.rng(path, d.Range), Severity: Severity(d.Severity), Source: d.Source, Message: d.Message,
		raw: append(json.RawMessage(nil), raw...)}
	if d.Code != nil {
		out.Code = d.Code.String()
	}
	if d.CodeDescription != nil {
		out.CodeURL = d.CodeDescription.Href
	}
	for _, t := range d.Tags {
		switch t {
		case 1:
			out.Unnecessary = true
		case 2:
			out.Deprecated = true
		}
	}
	for _, r := range d.RelatedInformation {
		out.Related = append(out.Related, Related{Location: v.location(r.Location), Message: r.Message})
	}
	return out, true
}

// protoDiagnostic is a diagnostic as the server sent it, or built from its fields if it didn't come from one.
func (c *Client) protoDiagnostic(path string, d Diagnostic) json.RawMessage {
	if d.raw != nil {
		return d.raw
	}
	p := protocol.Diagnostic{Severity: int(d.Severity), Source: d.Source, Message: d.Message}
	if s := c.snapshot(path); s != nil {
		p.Range = toProtoRange(*s, c.enc, d.Range)
	}
	if d.Code != "" {
		b, _ := json.Marshal(d.Code)
		p.Code = &protocol.IntOrString{Raw: b}
	}
	b, _ := json.Marshal(p)
	return b
}

func (c *Client) snapshot(path string) *text.Snapshot { return c.conv().snap(path) }
