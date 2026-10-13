package lsp_test

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/lsp"
	"github.com/liamg/oriel/lsp/lsptest"
	"github.com/liamg/oriel/text"
)

// Re-record with the servers on PATH: go test ./lsp -run TestSession -record
var record = flag.Bool("record", false, "run TestSession against the real servers and rewrite testdata/transcripts")

// live runs the sessions against the real servers without recording.
func live() bool { return *record || os.Getenv("ORIEL_LSP_LIVE") == "1" }

// fixture is a small project and the server for it.
type fixture struct {
	name      string // testdata/<name>, transcripts/<name>.jsonl
	server    string // a built-in spec's name
	language  string
	main, lib string
	indent    string     // one level, as the files use it
	version   [][]string // commands whose output is the transcript's version
	format    lsp.FormatOptions
	settings  map[string]any // merged over the built-in spec's
}

var fixtures = []fixture{
	{name: "go", server: "gopls", language: "go", main: "main.go", lib: "lib.go", indent: "\t",
		version: [][]string{{"gopls", "version"}}, format: lsp.FormatOptions{TabSize: 4},
		settings: map[string]any{"gopls": map[string]any{"symbolScope": "workspace"}}}, // the standard library's paths name the machine
	{name: "ts", server: "typescript-language-server", language: "typescript", main: "main.ts", lib: "lib.ts", indent: "  ",
		version: [][]string{{"typescript-language-server", "--version"}, {"tsc", "--version"}}, format: lsp.FormatOptions{TabSize: 2, InsertSpaces: true}},
	{name: "py", server: "pyright", language: "python", main: "main.py", lib: "lib.py", indent: "    ",
		version: [][]string{{"pyright", "--version"}}, format: lsp.FormatOptions{TabSize: 4, InsertSpaces: true}},
}

func (fx fixture) spec(t *testing.T) lsp.ServerSpec {
	for _, s := range lsp.Builtins() {
		if s.Name == fx.server {
			if fx.settings != nil {
				var m map[string]any
				if len(s.Settings) > 0 {
					json.Unmarshal(s.Settings, &m)
				}
				s.Settings, _ = json.Marshal(merge(m, fx.settings))
			}
			return s
		}
	}
	t.Fatalf("no built-in server %q", fx.server)
	return lsp.ServerSpec{}
}

func merge(a, b map[string]any) map[string]any {
	if a == nil {
		a = map[string]any{}
	}
	for k, v := range b {
		if bm, ok := v.(map[string]any); ok {
			am, _ := a[k].(map[string]any)
			a[k] = merge(am, bm)
			continue
		}
		a[k] = v
	}
	return a
}

// project copies a fixture to a fresh directory; a file named x.in becomes x.
func project(t *testing.T, name string) string {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join("testdata", name)
	ents, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, strings.TrimSuffix(e.Name(), ".in")), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// result is what a session saw, in Oriel's terms.
type result struct {
	root, main, lib string
	mainText        string
	libText         string

	diags      []lsp.Diagnostic
	states     []lsp.State
	sawWork    bool
	completion *lsp.CompletionList
	resolved   *lsp.Completion
	hover      *lsp.Hover
	sig        *lsp.SignatureHelp
	actions    []lsp.Action
	hints      []lsp.InlayHint
	hintsErr   error
	def        []lsp.Location
	refs       []lsp.Location
	prepare    *lsp.PrepareRename
	rename     *lsp.WorkspaceEdit
	symbols    []lsp.Symbol
	wsSymbols  []lsp.Symbol
	callItems  []lsp.CallItem
	incoming   []lsp.Call
	outgoing   []lsp.Call
	format     []lsp.TextEdit
	formatErr  error
	folding    []lsp.FoldingRange
	tokens     []lsp.SemanticToken
	tokensErr  error
}

// at is the position of the nth (0-based) match of re's first group, or of re, in s, plus extra bytes.
func at(t *testing.T, s, re string, n, extra int) text.Pos {
	t.Helper()
	ms := regexp.MustCompile(re).FindAllStringSubmatchIndex(s, -1)
	if n >= len(ms) {
		t.Fatalf("no match %d of %q", n, re)
	}
	start := ms[n][0]
	if len(ms[n]) > 2 && ms[n][2] >= 0 {
		start = ms[n][2]
	}
	snap := text.New([]byte(s), text.Author{}).Snapshot()
	return snap.PosAt(start + extra)
}

// session runs the scripted session against a server: open, diagnostics, completion, hover, signature help,
// code actions, inlay hints, navigation, rename, symbols, call hierarchy, formatting, then shutdown.
func session(t *testing.T, fx fixture, launch lsp.Launcher, root string) *result {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	r := &result{root: root, main: filepath.Join(root, fx.main), lib: filepath.Join(root, fx.lib)}
	mainB, _ := os.ReadFile(r.main)
	libB, _ := os.ReadFile(r.lib)
	r.mainText, r.libText = string(mainB), string(libB)

	var mu sync.Mutex
	diags := make(chan lsp.DiagnosticSet, 100)
	m := lsp.NewManager(lsp.ManagerOptions{
		Servers:   func() []lsp.ServerSpec { return []lsp.ServerSpec{fx.spec(t)} },
		Installed: func(lsp.ServerSpec) bool { return true },
		Client: lsp.Options{
			Launch:        launch,
			OnDiagnostics: func(s lsp.DiagnosticSet) { diags <- s },
			OnStatus: func(s lsp.Status) {
				mu.Lock()
				defer mu.Unlock()
				if n := len(r.states); n == 0 || r.states[n-1] != s.State {
					r.states = append(r.states, s.State)
				}
				if len(s.Work) > 0 {
					r.sawWork = true
				}
			},
		},
	})
	buf := text.New(mainB, text.Author{Kind: text.User})
	c, err := m.Open(ctx, r.main, fx.language, buf.Snapshot())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := m.Open(ctx, r.lib, fx.language, text.New(libB, text.Author{}).Snapshot()); err != nil {
		t.Fatalf("open lib: %v", err)
	}

	// The seeded error: wait for the server to report it.
	for r.diags == nil {
		select {
		case s := <-diags:
			if s.Path == r.main && strings.Contains(diagText(s.Diagnostics), "greting") {
				r.diags = s.Diagnostics
			}
		case <-ctx.Done():
			t.Fatal("no diagnostics for the seeded error")
		}
	}

	// Type a prefix on the blank line and complete it, then take it back.
	blank := at(t, r.mainText, `greeting\(n\);?\n(?:\s*\}\n)?(\n)`, 0, 0)
	typed := fx.indent + "gre"
	ch, err := buf.Apply(text.Edit{Ranges: []text.Replace{{Range: text.Range{Start: buf.Snapshot().Offset(blank), End: buf.Snapshot().Offset(blank)}, Text: []byte(typed)}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Change(r.main, ch); err != nil {
		t.Fatal(err)
	}
	r.completion, err = c.Complete(ctx, lsp.Loc{Path: r.main, Pos: text.Pos{Line: blank.Line, Col: len(typed)}})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	for _, it := range r.completion.Items {
		if it.Label == "greeting" {
			res, err := c.ResolveCompletion(ctx, it)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			r.resolved = &res
			break
		}
	}
	start := buf.Snapshot().Offset(blank)
	ch, err = buf.Apply(text.Edit{Ranges: []text.Replace{{Range: text.Range{Start: start, End: start + len(typed)}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Change(r.main, ch); err != nil {
		t.Fatal(err)
	}

	use := lsp.Loc{Path: r.main, Pos: at(t, r.mainText, `\+= (greeting)\(`, 0, 2)}
	if r.hover, err = c.Hover(ctx, use); err != nil {
		t.Fatalf("hover: %v", err)
	}
	if r.sig, err = c.SignatureHelp(ctx, lsp.Loc{Path: r.main, Pos: at(t, r.mainText, `greeting\((n)\)`, 0, 0)}); err != nil {
		t.Fatalf("signature help: %v", err)
	}
	d := r.diags[0]
	for _, x := range r.diags {
		if strings.Contains(x.Message, "greting") {
			d = x
		}
	}
	if r.actions, err = c.CodeActions(ctx, r.main, d.Range, []lsp.Diagnostic{d}); err != nil {
		t.Fatalf("code actions: %v", err)
	}
	for i, a := range r.actions {
		if a.Edit == nil && a.Kind != "" {
			if r.actions[i], err = c.ResolveAction(ctx, r.main, a); err != nil {
				t.Fatalf("resolve action: %v", err)
			}
		}
	}
	snap := buf.Snapshot()
	whole := lsp.Range{End: snap.PosAt(snap.Len())}
	r.hints, r.hintsErr = c.InlayHints(ctx, r.main, whole)
	if r.def, err = c.Definition(ctx, use); err != nil {
		t.Fatalf("definition: %v", err)
	}
	if r.refs, err = c.References(ctx, use, true); err != nil {
		t.Fatalf("references: %v", err)
	}
	if r.prepare, err = c.PrepareRename(ctx, use); err != nil {
		t.Fatalf("prepare rename: %v", err)
	}
	if r.rename, err = c.Rename(ctx, use, "salute"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if r.symbols, err = c.DocumentSymbols(ctx, r.lib); err != nil {
		t.Fatalf("document symbols: %v", err)
	}
	if r.wsSymbols, err = c.WorkspaceSymbols(ctx, "greeting"); err != nil {
		t.Fatalf("workspace symbols: %v", err)
	}
	decl := lsp.Loc{Path: r.lib, Pos: at(t, r.libText, `(?:func|def|function) (greeting)`, 0, 0)}
	if r.callItems, err = c.PrepareCallHierarchy(ctx, decl); err != nil {
		t.Fatalf("prepare call hierarchy: %v", err)
	}
	if len(r.callItems) > 0 {
		if r.incoming, err = c.IncomingCalls(ctx, r.callItems[0]); err != nil {
			t.Fatalf("incoming calls: %v", err)
		}
	}
	shout, err := c.PrepareCallHierarchy(ctx, lsp.Loc{Path: r.lib, Pos: at(t, r.libText, `(?:func|def|function) (shout)`, 0, 0)})
	if err != nil {
		t.Fatalf("prepare call hierarchy: %v", err)
	}
	if len(shout) > 0 {
		if r.outgoing, err = c.OutgoingCalls(ctx, shout[0]); err != nil {
			t.Fatalf("outgoing calls: %v", err)
		}
	}
	r.format, r.formatErr = c.Format(ctx, r.main, fx.format)
	if r.folding, err = c.FoldingRanges(ctx, r.main); err != nil && !errors.Is(err, lsp.ErrNotSupported) {
		t.Fatalf("folding ranges: %v", err)
	}
	r.tokens, r.tokensErr = c.SemanticTokens(ctx, r.main)

	if err := m.Close(r.main); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(r.lib); err != nil {
		t.Fatal(err)
	}
	if err := m.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if st := c.Status(); st.State != lsp.Stopped || c.Err() != nil {
		t.Errorf("after shutdown: %v, %v", st.State, c.Err())
	}
	return r
}

func diagText(ds []lsp.Diagnostic) string {
	var b strings.Builder
	for _, d := range ds {
		b.WriteString(d.Message + "\n")
	}
	return b.String()
}

// run gets a fixture's session: replayed from its transcript, or live (and recorded with -record).
func run(t *testing.T, fx fixture) *result {
	root := project(t, fx.name)
	path := filepath.Join("testdata", "transcripts", fx.name+".jsonl")
	if !live() {
		tr, err := lsptest.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		fake := lsptest.NewFake(tr, 10*time.Second)
		r := session(t, fx, fake.Launcher(), root)
		if err := fake.Wait(); err != nil {
			t.Fatalf("replay: %v", err)
		}
		return r
	}
	spec := fx.spec(t)
	if _, err := exec.LookPath(spec.Cmd[0]); err != nil {
		t.Skipf("%s isn't installed", spec.Cmd[0])
	}
	if !*record {
		return session(t, fx, lsp.Exec, root)
	}
	rec := &lsptest.Recorder{}
	r := session(t, fx, rec.Launcher(lsp.Exec), root)
	var versions []string
	for _, v := range fx.version {
		out, err := exec.Command(v[0], v[1:]...).Output()
		if err != nil {
			t.Fatal(err)
		}
		versions = append(versions, v[0]+" "+strings.TrimSpace(string(out)))
	}
	h := lsptest.Header{Server: fx.server, Version: strings.Join(versions, ", "), Cmd: spec.Cmd}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := rec.Transcript(h).Write(f); err != nil {
		t.Fatal(err)
	}
	return r
}
