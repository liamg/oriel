package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/liamg/oriel/lsp/protocol"
	"github.com/liamg/oriel/text"
)

func touch(t *testing.T, path string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindRoot(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "go.work"))
	touch(t, filepath.Join(dir, "mod", "go.mod"))
	touch(t, filepath.Join(dir, "mod", "pkg", "a.go"))
	touch(t, filepath.Join(dir, "other", "b.go"))
	gopls := ServerSpec{Roots: []string{"go.work", "go.mod", ".git"}}
	if got := gopls.FindRoot(filepath.Join(dir, "mod", "pkg", "a.go")); got != dir {
		t.Errorf("root = %s, want the go.work's %s", got, dir)
	}
	modFirst := ServerSpec{Roots: []string{"go.mod"}}
	if got := modFirst.FindRoot(filepath.Join(dir, "mod", "pkg", "a.go")); got != filepath.Join(dir, "mod") {
		t.Errorf("root = %s", got)
	}
	if got := modFirst.FindRoot(filepath.Join(dir, "other", "b.go")); got != filepath.Join(dir, "other") {
		t.Errorf("root with no marker = %s, want the file's directory", got)
	}
}

func TestBuiltins(t *testing.T) {
	names := map[string]bool{}
	for _, s := range Builtins() {
		if names[s.Name] || len(s.Cmd) == 0 || len(s.Language) == 0 || len(s.Roots) == 0 || s.Source != "builtin:lsp" {
			t.Errorf("bad built-in %+v", s)
		}
		names[s.Name] = true
		if len(s.Settings) > 0 && !json.Valid(s.Settings) || len(s.Init) > 0 && !json.Valid(s.Init) {
			t.Errorf("%s: invalid JSON", s.Name)
		}
	}
	// Built in: gopls, typescript-language-server or vtsls, pyright or basedpyright, rust-analyzer, clangd,
	// lua-language-server, yaml/json-ls, bash-ls, terraform-ls.
	for _, n := range []string{"gopls", "typescript-language-server", "vtsls", "pyright", "basedpyright", "rust-analyzer", "clangd",
		"lua-language-server", "yaml-language-server", "vscode-json-language-server", "bash-language-server", "terraform-ls"} {
		if !names[n] {
			t.Errorf("no built-in %s", n)
		}
	}
}

// ref: lsp/types/textDocumentItem.md#textDocumentItem
// quote: "Text documents have a language identifier to identify a document on the server side when it handles more than one language to avoid re-interpreting the file extension."
func TestLanguageID(t *testing.T) {
	for _, c := range [][3]string{{"go", "a.go", "go"}, {"tsx", "a.tsx", "typescriptreact"}, {"javascript", "a.jsx", "javascriptreact"},
		{"javascript", "a.js", "javascript"}, {"bash", "a.sh", "shellscript"}, {"python", "a.py", "python"}} {
		if got := LanguageID(c[0], c[1]); got != c[2] {
			t.Errorf("LanguageID(%s, %s) = %s, want %s", c[0], c[1], got, c[2])
		}
	}
}

func TestURI(t *testing.T) {
	p := "/tmp/a b/é.go"
	u := URI(p)
	if u != "file:///tmp/a%20b/%C3%A9.go" || Path(u) != p {
		t.Errorf("URI = %s, back = %s", u, Path(u))
	}
}

// ref: lsp/types/position.md#position
// quote: "If the character value is greater than the line length it defaults back"
func TestFromProtoClamps(t *testing.T) {
	s := text.New([]byte("é🎉\nab"), text.Author{}).Snapshot()
	for _, c := range []struct {
		enc  string
		p    protocol.Position
		want text.Pos
	}{
		{protocol.UTF16, protocol.Position{Line: 0, Character: 2}, text.Pos{Line: 0, Col: 2}}, // inside the surrogate pair
		{protocol.UTF16, protocol.Position{Line: 0, Character: 3}, text.Pos{Line: 0, Col: 6}},
		{protocol.UTF16, protocol.Position{Line: 0, Character: 9}, text.Pos{Line: 0, Col: 6}},
		{protocol.UTF8, protocol.Position{Line: 0, Character: 1}, text.Pos{Line: 0, Col: 0}}, // inside é
		{protocol.UTF8, protocol.Position{Line: 0, Character: 40}, text.Pos{Line: 0, Col: 6}},
		{protocol.UTF32, protocol.Position{Line: 0, Character: 2}, text.Pos{Line: 0, Col: 6}},
		{protocol.UTF16, protocol.Position{Line: 7, Character: 0}, text.Pos{Line: 1, Col: 2}},
	} {
		if got := fromProto(s, c.enc, c.p); got != c.want {
			t.Errorf("fromProto(%s, %v) = %v, want %v", c.enc, c.p, got, c.want)
		}
	}
}

// A position the text no longer has (asked for before an edit) is sent clamped, not a panic.
func TestToProtoClamps(t *testing.T) {
	s := text.New([]byte("é🎉\nab"), text.Author{}).Snapshot()
	for _, c := range []struct {
		p    text.Pos
		want protocol.Position
	}{
		{text.Pos{Line: 0, Col: 40}, protocol.Position{Line: 0, Character: 3}},
		{text.Pos{Line: 9, Col: 1}, protocol.Position{Line: 1, Character: 2}},
		{text.Pos{Line: 1, Col: -3}, protocol.Position{Line: 1, Character: 0}},
	} {
		if got := toProto(s, protocol.UTF16, c.p); got != c.want {
			t.Errorf("toProto(%v) = %v, want %v", c.p, got, c.want)
		}
	}
}

// ref: lsp/types/textEditArray.md#textEditArray
// quote: "If multiple inserts have the same position, the order in the array defines the order in which the inserted strings appear in the resulting text."
func TestEditFor(t *testing.T) {
	buf := text.New([]byte("abc\ndef\n"), text.Author{})
	e, err := EditFor(buf.Snapshot(), []TextEdit{
		{Range: rng(1, 0, 1, 3), NewText: "DEF"},
		{Range: rng(0, 1, 0, 1), NewText: "1"},
		{Range: rng(0, 1, 0, 1), NewText: "2"},
	}, text.Author{Kind: text.LSP})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := buf.Apply(e); err != nil {
		t.Fatal(err)
	}
	if got := buf.Snapshot().String(); got != "a12bc\nDEF\n" {
		t.Errorf("applied = %q", got)
	}
	if _, err := EditFor(buf.Snapshot(), []TextEdit{{Range: rng(0, 0, 0, 3)}, {Range: rng(0, 2, 0, 4)}}, text.Author{}); err == nil {
		t.Error("overlapping edits accepted")
	}
	if _, err := EditFor(buf.Snapshot(), []TextEdit{{Range: rng(9, 0, 9, 0)}}, text.Author{}); err == nil {
		t.Error("an edit outside the document accepted")
	}
}

// fleet launches a scripted server per start, keeping each.
type fleet struct {
	t       *testing.T
	caps    string
	mu      sync.Mutex
	servers []*server
	roots   []string
}

func (f *fleet) launch(ctx context.Context, spec ServerSpec, root string, stderr io.Writer) (Process, error) {
	s := newServer(f.t, f.caps)
	f.mu.Lock()
	f.servers = append(f.servers, s)
	f.roots = append(f.roots, root)
	f.mu.Unlock()
	return s.launch(ctx, spec, root, stderr)
}

func (f *fleet) server(i int) *server {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.servers[i]
}

func TestManager(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "a", "go.mod"))
	touch(t, filepath.Join(dir, "b", "go.mod"))
	f := &fleet{t: t, caps: `{"textDocumentSync":2}`}
	specs := []ServerSpec{
		{Name: "missing", Language: []string{"go"}, Cmd: []string{"not-installed"}},
		{Name: "fake", Language: []string{"go"}, Cmd: []string{"fake"}, Roots: []string{"go.mod"}},
	}
	var stderrFor []string
	m := NewManager(ManagerOptions{
		Servers:   func() []ServerSpec { return specs },
		Installed: func(s ServerSpec) bool { return s.Name != "missing" },
		Stderr: func(s ServerSpec, root string) io.Writer {
			stderrFor = append(stderrFor, s.Name+" "+root)
			return io.Discard
		},
		Client: Options{Launch: f.launch},
	})
	ctx := context.Background()
	if _, err := m.Open(ctx, filepath.Join(dir, "x.rs"), "rust", text.Snapshot{}); !errors.Is(err, ErrNoServer) {
		t.Errorf("open with no server = %v", err)
	}
	snap := func(s string) text.Snapshot { return text.New([]byte(s), text.Author{}).Snapshot() }
	a1, err := m.Open(ctx, filepath.Join(dir, "a", "1.go"), "go", snap("one"))
	if err != nil {
		t.Fatal(err)
	}
	a2, _ := m.Open(ctx, filepath.Join(dir, "a", "2.go"), "go", snap("two"))
	b1, _ := m.Open(ctx, filepath.Join(dir, "b", "1.go"), "go", snap("three"))
	if a1 != a2 || a1 == b1 || len(m.Clients()) != 2 || a1.Spec().Name != "fake" {
		t.Fatalf("clients: a1 %p a2 %p b1 %p, %d running", a1, a2, b1, len(m.Clients()))
	}
	if a1.Root() != filepath.Join(dir, "a") || b1.Root() != filepath.Join(dir, "b") {
		t.Errorf("roots = %s, %s", a1.Root(), b1.Root())
	}
	if len(stderrFor) != 2 || stderrFor[0] != "fake "+filepath.Join(dir, "a") {
		t.Errorf("stderr asked for %q", stderrFor)
	}
	if again, err := m.Open(ctx, filepath.Join(dir, "a", "2.go"), "go", snap("two")); again != a1 || err != nil || f.server(0).count("textDocument/didOpen") != 2 {
		t.Errorf("opening an open document again = %p, %v, %d opens", again, err, f.server(0).count("textDocument/didOpen"))
	}
	if c, ok := m.Client(filepath.Join(dir, "a", "2.go")); !ok || c != a1 {
		t.Error("Client doesn't find the document's client")
	}
	if got := len(m.Statuses()); got != 2 {
		t.Errorf("%d statuses", got)
	}

	// A change reaches the server; after a crash, opening on the root again starts a new server with the
	// documents that were open, as they are now.
	buf := text.New([]byte("one"), text.Author{})
	ch, _ := buf.Apply(text.Edit{Ranges: []text.Replace{{Range: text.Range{Start: 3, End: 3}, Text: []byte("!")}}})
	if err := m.Change(filepath.Join(dir, "a", "1.go"), ch); err != nil {
		t.Fatal(err)
	}
	f.server(0).last("textDocument/didChange")
	f.server(0).crash()
	<-a1.Done()
	if a1.Status().State != Crashed {
		t.Errorf("state = %v", a1.Status().State)
	}
	a3, err := m.Open(ctx, filepath.Join(dir, "a", "3.go"), "go", snap("new"))
	if err != nil {
		t.Fatal(err)
	}
	if a3 == a1 || len(f.servers) != 3 {
		t.Fatalf("no new server after the crash: %d started", len(f.servers))
	}
	f.server(2).nth("textDocument/didOpen", 3)
	var opened []string
	f.server(2).mu.Lock()
	for _, msg := range f.server(2).got {
		if msg.Method == "textDocument/didOpen" {
			var p protocol.DidOpenTextDocumentParams
			json.Unmarshal(msg.Params, &p)
			opened = append(opened, filepath.Base(Path(p.TextDocument.URI))+"="+p.TextDocument.Text)
		}
	}
	f.server(2).mu.Unlock()
	if want := []string{"1.go=one!", "2.go=two", "3.go=new"}; len(opened) != 3 || opened[0] != want[0] || opened[1] != want[1] || opened[2] != want[2] {
		t.Errorf("reopened %q, want %q", opened, want)
	}

	if err := m.Close(filepath.Join(dir, "b", "1.go")); err != nil {
		t.Fatal(err)
	}
	f.server(1).last("textDocument/didClose")
	if _, ok := m.Client(filepath.Join(dir, "b", "1.go")); ok {
		t.Error("a closed document still has a client")
	}
	restarted, err := m.Restart(ctx, b1)
	if err != nil || restarted == b1 || b1.Status().State != Stopped {
		t.Errorf("restart = %p, %v; old state %v", restarted, err, b1.Status().State)
	}
	if err := m.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	for _, c := range m.Clients() {
		if st := c.Status().State; st != Stopped && st != Crashed {
			t.Errorf("%s after shutdown: %v", c.Root(), st)
		}
	}
}

func TestSpecForOrder(t *testing.T) {
	m := NewManager(ManagerOptions{
		Servers: func() []ServerSpec {
			return []ServerSpec{{Name: "a", Language: []string{"x"}, Order: 1}, {Name: "b", Language: []string{"x"}}}
		},
		Installed: func(ServerSpec) bool { return true },
	})
	if s, _ := m.SpecFor("x"); s.Name != "b" {
		t.Errorf("SpecFor = %s, want b, the lower order", s.Name)
	}
}

func TestSpecForPrefersInstalled(t *testing.T) {
	m := NewManager(ManagerOptions{
		Servers:   func() []ServerSpec { return Builtins() },
		Installed: func(s ServerSpec) bool { return s.Name == "vtsls" },
	})
	if s, ok := m.SpecFor("tsx"); !ok || s.Name != "vtsls" {
		t.Errorf("SpecFor(tsx) = %s, %v; want vtsls, the one installed", s.Name, ok)
	}
	if _, ok := m.SpecFor("python"); ok {
		t.Error("SpecFor(python) found a server none of which is installed")
	}
}
