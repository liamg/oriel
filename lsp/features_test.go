package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/liamg/oriel/lsp/snippet"
	"github.com/liamg/oriel/text"
)

// withDoc starts a client whose server has caps and answers with answer, with a document open holding content.
func withDoc(t *testing.T, caps, content string, answer func(method string, params json.RawMessage) (any, error)) (*Client, *server, string) {
	s := newServer(t, caps)
	s.answer = answer
	c := startClient(t, s, Options{})
	path := filepath.Join(c.Root(), "a.ts")
	if err := c.DidOpen(path, "typescript", text.New([]byte(content), text.Author{}).Snapshot()); err != nil {
		t.Fatal(err)
	}
	return c, s, path
}

func answer(s string) func(string, json.RawMessage) (any, error) {
	return func(string, json.RawMessage) (any, error) { return json.RawMessage(s), nil }
}

func pos(line, col int) text.Pos { return text.Pos{Line: line, Col: col} }

func rng(l1, c1, l2, c2 int) Range { return Range{Start: pos(l1, c1), End: pos(l2, c2)} }

// ref: lsp/language/completion.md#textDocument_completion
// quote: "If a completion list specifies a default value and a completion item"
func TestCompletionDefaults(t *testing.T) {
	c, s, path := withDoc(t, `{"completionProvider":{"resolveProvider":true}}`, "x = 🎉fo\n", answer(`{"isIncomplete":true,
		"itemDefaults":{"editRange":{"start":{"line":0,"character":6},"end":{"line":0,"character":8}},"insertTextFormat":2,"data":{"d":1}},
		"items":[{"label":"foo","kind":3,"textEditText":"foo(${1:a})$0"},{"label":"for","insertTextFormat":1,"insertText":"for $1","data":{"own":true}}]}`))
	list, err := c.Complete(context.Background(), Loc{Path: path, Pos: pos(0, 10)})
	if err != nil {
		t.Fatal(err)
	}
	// The position goes in the server's encoding: the 🎉 is 4 bytes and 2 UTF-16 units.
	if p := decode[map[string]any](t, s.last("textDocument/completion")); p["position"].(map[string]any)["character"] != 8.0 {
		t.Errorf("completion position = %v", p["position"])
	}
	if !list.Incomplete || len(list.Items) != 2 {
		t.Fatalf("list = %+v", list)
	}
	foo, forItem := list.Items[0], list.Items[1]
	want := snippet.Snippet{Text: "foo(a)", Tabstops: []snippet.Tabstop{{Index: 1, Ranges: []snippet.Range{{Start: 4, End: 5}}}, {Index: 0, Ranges: []snippet.Range{{Start: 6, End: 6}}}}}
	if foo.Kind != 3 || foo.Kind.String() != "function" || !reflect.DeepEqual(foo.Insert, want) || *foo.Range != rng(0, 8, 0, 10) {
		t.Errorf("foo = %+v", foo)
	}
	if forItem.Insert.Text != "for $1" || forItem.Insert.Tabstops != nil || forItem.SortText != "for" || forItem.FilterText != "for" {
		t.Errorf("for = %+v", forItem)
	}
	// Resolving sends the item back with the default data it lacked.
	s.answer = func(method string, params json.RawMessage) (any, error) {
		return json.RawMessage(`{"label":"foo","kind":3,"documentation":{"kind":"markdown","value":"**foo**"},"additionalTextEdits":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"newText":"import foo\n"}]}`), nil
	}
	res, err := c.ResolveCompletion(context.Background(), foo)
	if err != nil {
		t.Fatal(err)
	}
	if got := decode[map[string]any](t, s.last("completionItem/resolve")); !reflect.DeepEqual(got["data"], map[string]any{"d": 1.0}) {
		t.Errorf("resolve sent %v", got)
	}
	if res.Doc != (Markup{"markdown", "**foo**"}) || len(res.AdditionalEdits) != 1 || res.AdditionalEdits[0].NewText != "import foo\n" || *res.Range != *foo.Range {
		t.Errorf("resolved = %+v", res)
	}
}

// ref: lsp/language/completion.md#textDocument_completion
// quote: "editRange?: Range | {"
func TestCompletionInsertReplace(t *testing.T) {
	c, _, path := withDoc(t, `{"completionProvider":{}}`, "foobar\n", answer(`[{"label":"food","textEdit":{"newText":"food",
		"insert":{"start":{"line":0,"character":0},"end":{"line":0,"character":3}},"replace":{"start":{"line":0,"character":0},"end":{"line":0,"character":6}}}}]`))
	list, err := c.Complete(context.Background(), Loc{Path: path, Pos: pos(0, 3)})
	if err != nil {
		t.Fatal(err)
	}
	it := list.Items[0]
	if *it.Range != rng(0, 0, 0, 3) || *it.ReplaceRange != rng(0, 0, 0, 6) || it.Insert.Text != "food" {
		t.Errorf("item = %+v", it)
	}
}

// ref: lsp/language/signatureHelp.md#textDocument_signatureHelp
// quote: "Either a string or an inclusive start and exclusive end offsets within"
func TestSignatureLabelOffsets(t *testing.T) {
	c, _, path := withDoc(t, `{"signatureHelpProvider":{}}`, "f(\n", answer(`{"signatures":[{"label":"f(é: int, 🎉: str)","parameters":[{"label":[2,8]},{"label":"🎉: str"}],"activeParameter":1}],"activeSignature":0}`))
	h, err := c.SignatureHelp(context.Background(), Loc{Path: path, Pos: pos(0, 2)})
	if err != nil {
		t.Fatal(err)
	}
	sig := h.Signatures[0]
	if sig.ActiveParam != 1 || sig.Params[0].Label != "é: int" || sig.Params[0].Start != 2 || sig.Params[0].End != 9 ||
		sig.Params[1].Label != "🎉: str" || sig.Label[sig.Params[1].Start:sig.Params[1].End] != "🎉: str" {
		t.Errorf("signature = %+v", sig)
	}
}

func TestHoverNull(t *testing.T) {
	c, _, path := withDoc(t, `{"hoverProvider":true}`, "x\n", answer(`null`))
	// ref: lsp/language/hover.md#textDocument_hover
	// quote: "result: `Hover` \\| `null` defined as follows:"
	if h, err := c.Hover(context.Background(), Loc{Path: path}); h != nil || err != nil {
		t.Errorf("hover = %+v, %v", h, err)
	}
}

func TestNotSupported(t *testing.T) {
	c, s, path := withDoc(t, `{}`, "x\n", nil)
	ctx := context.Background()
	at := Loc{Path: path}
	calls := map[string]func() error{
		"hover":      func() error { _, err := c.Hover(ctx, at); return err },
		"complete":   func() error { _, err := c.Complete(ctx, at); return err },
		"definition": func() error { _, err := c.Definition(ctx, at); return err },
		"rename":     func() error { _, err := c.Rename(ctx, at, "y"); return err },
		"format":     func() error { _, err := c.Format(ctx, path, FormatOptions{}); return err },
		"tokens":     func() error { _, err := c.SemanticTokens(ctx, path); return err },
		"pull":       func() error { _, err := c.PullDiagnostics(ctx, path); return err },
		"lenses":     func() error { _, err := c.CodeLenses(ctx, path); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, ErrNotSupported) {
			t.Errorf("%s = %v, want ErrNotSupported", name, err)
		}
	}
	if n := s.count("textDocument/hover"); n != 0 {
		t.Errorf("sent %d hover requests to a server without hover", n)
	}
}

// ref: lsp/language/definition.md#textDocument_definition
// quote: "result: [`Location`](#location) \\| [`Location`](#location)[] \\| [`LocationLink`](#locationLink)[] \\| `null`"
func TestDefinitionLinks(t *testing.T) {
	c, s, path := withDoc(t, `{"definitionProvider":true}`, "é = 1\n", nil)
	s.answer = answer(`[{"targetUri":"` + string(URI(path)) + `","targetRange":{"start":{"line":0,"character":0},"end":{"line":0,"character":5}},` +
		`"targetSelectionRange":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}}}]`)
	ls, err := c.Definition(context.Background(), Loc{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	tr := rng(0, 0, 0, 6)
	if len(ls) != 1 || ls[0].Path != path || ls[0].Range != rng(0, 0, 0, 2) || !reflect.DeepEqual(ls[0].Target, &tr) {
		t.Errorf("definition = %+v", ls)
	}
}

// ref: lsp/language/semanticTokens.md#textDocument_semanticTokens
// quote: "So each token is represented using 5 integers."
func TestSemanticTokens(t *testing.T) {
	// The spec's own example: tokens at 2:5 (3 long), 2:10 (4 long) and 5:2 (7 long).
	content := "\n\n🎉 ab cde fghi\n\n\n  jklmnop\n"
	c, _, path := withDoc(t, `{"semanticTokensProvider":{"legend":{"tokenTypes":["property","type","class"],"tokenModifiers":["private","static"]},"full":true}}`,
		content, answer(`{"data":[2,5,3,0,3,0,5,4,1,0,3,2,7,2,0]}`))
	toks, err := c.SemanticTokens(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	// In UTF-16 the 🎉 is two units, so character 5 is byte 7.
	want := []SemanticToken{
		{Range: rng(2, 7, 2, 10), Type: "property", Modifiers: []string{"private", "static"}},
		{Range: rng(2, 12, 2, 16), Type: "type"},
		{Range: rng(5, 2, 5, 9), Type: "class"},
	}
	if !reflect.DeepEqual(toks, want) {
		t.Errorf("tokens = %+v\nwant %+v", toks, want)
	}
}

// ref: lsp/language/rename.md#textDocument_prepareRename
// quote: "If `null` is returned then it is deemed that a 'textDocument/rename' request is not valid at the given position."
func TestPrepareRename(t *testing.T) {
	c, s, path := withDoc(t, `{"renameProvider":{"prepareProvider":true}}`, "abc\n", answer(`null`))
	if p, err := c.PrepareRename(context.Background(), Loc{Path: path}); p != nil || err != nil {
		t.Errorf("null prepare = %+v, %v", p, err)
	}
	s.answer = answer(`{"defaultBehavior":true}`)
	if p, err := c.PrepareRename(context.Background(), Loc{Path: path}); p == nil || p.Range != nil || err != nil {
		t.Errorf("default behaviour = %+v, %v", p, err)
	}
	s.answer = answer(`{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":3}},"placeholder":"abc"}`)
	if p, err := c.PrepareRename(context.Background(), Loc{Path: path}); p == nil || *p.Range != rng(0, 0, 0, 3) || p.Placeholder != "abc" || err != nil {
		t.Errorf("range = %+v, %v", p, err)
	}
	s.answer = answer(`null`)
	// ref: lsp/language/rename.md#textDocument_rename
	// quote: "`null` should be treated the same was as [`WorkspaceEdit`](#workspaceedit) with no changes (no change was required)."
	if e, err := c.Rename(context.Background(), Loc{Path: path}, "x"); e != nil || err != nil {
		t.Errorf("null rename = %+v, %v", e, err)
	}
}

func TestPrepareRenameUnsupported(t *testing.T) {
	c, s, path := withDoc(t, `{"renameProvider":true}`, "abc\n", nil)
	if p, err := c.PrepareRename(context.Background(), Loc{Path: path}); p == nil || p.Range != nil || err != nil {
		t.Errorf("prepare on a server without it = %+v, %v", p, err)
	}
	if s.count("textDocument/prepareRename") != 0 {
		t.Error("sent prepareRename to a server that doesn't take it")
	}
}

// ref: lsp/language/codeAction.md#textDocument_codeAction
// quote: "The server capability `codeActionProvider.resolveProvider` signals that a server will offer a `codeAction/resolve` route."
func TestCodeActionResolve(t *testing.T) {
	c, s, path := withDoc(t, `{"codeActionProvider":{"resolveProvider":true}}`, "abc\n", answer(`[{"title":"Fix","kind":"quickfix","isPreferred":true,"data":7},{"title":"Run","command":"run"}]`))
	diag := Diagnostic{Range: rng(0, 0, 0, 3), Severity: SevError, Message: "bad", Code: "E1"}
	as, err := c.CodeActions(context.Background(), path, diag.Range, []Diagnostic{diag}, "quickfix")
	if err != nil {
		t.Fatal(err)
	}
	p := decode[map[string]any](t, s.last("textDocument/codeAction"))
	ctx := p["context"].(map[string]any)
	sent := ctx["diagnostics"].([]any)[0].(map[string]any)
	if sent["message"] != "bad" || sent["code"] != "E1" || !reflect.DeepEqual(ctx["only"], []any{"quickfix"}) {
		t.Errorf("context = %v", ctx)
	}
	if len(as) != 2 || as[0].Title != "Fix" || !as[0].Preferred || as[0].Edit != nil || as[1].Command.Name != "run" {
		t.Fatalf("actions = %+v", as)
	}
	s.answer = answer(`{"title":"Fix","kind":"quickfix","edit":{"changes":{"` + string(URI(path)) + `":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":3}},"newText":"xyz"}]}}}`)
	a, err := c.ResolveAction(context.Background(), path, as[0])
	if err != nil {
		t.Fatal(err)
	}
	if got := decode[map[string]any](t, s.last("codeAction/resolve")); got["data"] != 7.0 {
		t.Errorf("resolve sent %v", got)
	}
	if a.Edit == nil || len(a.Edit.Changes) != 1 || a.Edit.Changes[0].Edits[0].NewText != "xyz" {
		t.Errorf("resolved = %+v", a)
	}
}

// ref: lsp/language/documentSymbol.md#textDocument_documentSymbol
// quote: "`SymbolInformation[]` which is a flat list of all symbols found in a given text document."
func TestDocumentSymbolsFlat(t *testing.T) {
	c, s, path := withDoc(t, `{"documentSymbolProvider":true}`, "class A:\n  def f(): pass\n", nil)
	s.answer = answer(`[{"name":"f","kind":6,"containerName":"A","location":{"uri":"` + string(URI(path)) + `","range":{"start":{"line":1,"character":2},"end":{"line":1,"character":15}}}}]`)
	syms, err := c.DocumentSymbols(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(syms) != 1 || syms[0].Container != "A" || syms[0].Kind.String() != "method" || syms[0].Range != rng(1, 2, 1, 15) {
		t.Errorf("symbols = %+v", syms)
	}
}

// ref: lsp/language/foldingRange.md#textDocument_foldingRange
// quote: "startCharacter?: uinteger;"
func TestFoldingRanges(t *testing.T) {
	c, _, path := withDoc(t, `{"foldingRangeProvider":true}`, "🎉 {\n}\n", answer(`[{"startLine":0,"startCharacter":3,"endLine":1,"kind":"region"}]`))
	fs, err := c.FoldingRanges(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if want := (FoldingRange{StartLine: 0, EndLine: 1, StartCol: 5, EndCol: -1, Kind: "region"}); len(fs) != 1 || fs[0] != want {
		t.Errorf("folding = %+v", fs)
	}
}

// ref: lsp/language/selectionRange.md#textDocument_selectionRange
// quote: "`parent.range` must contain `this.range`."
func TestSelectionRanges(t *testing.T) {
	c, _, path := withDoc(t, `{"selectionRangeProvider":true}`, "f(ab)\n", answer(`[{"range":{"start":{"line":0,"character":2},"end":{"line":0,"character":4}},"parent":{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":5}}}}]`))
	rs, err := c.SelectionRanges(context.Background(), path, []text.Pos{pos(0, 3)})
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Range != rng(0, 2, 0, 4) || rs[0].Parent == nil || rs[0].Parent.Range != rng(0, 0, 0, 5) || rs[0].Parent.Parent != nil {
		t.Errorf("selection ranges = %+v", rs)
	}
}

// ref: lsp/language/codeLens.md#codeLens_resolve
// quote: "The code lens resolve request is sent from the client to the server to resolve the command for a given code lens item."
func TestCodeLensResolve(t *testing.T) {
	c, s, path := withDoc(t, `{"codeLensProvider":{"resolveProvider":true}}`, "func TestX() {}\n", answer(`[{"range":{"start":{"line":0,"character":5},"end":{"line":0,"character":10}},"data":"x"}]`))
	ls, err := c.CodeLenses(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ls) != 1 || ls[0].Command != nil || ls[0].Range != rng(0, 5, 0, 10) {
		t.Fatalf("lenses = %+v", ls)
	}
	s.answer = answer(`{"range":{"start":{"line":0,"character":5},"end":{"line":0,"character":10}},"command":{"title":"run test","command":"test.run","arguments":["TestX"]}}`)
	l, err := c.ResolveCodeLens(context.Background(), path, ls[0])
	if err != nil || l.Command == nil || l.Command.Title != "run test" || string(l.Command.Args[0]) != `"TestX"` {
		t.Errorf("resolved = %+v, %v", l, err)
	}
}

// ref: lsp/language/pullDiagnostics.md#textDocument_diagnostic
// quote: "The text document diagnostic request is sent from the client to the server to ask the server to compute the diagnostics for a given document."
func TestPullDiagnostics(t *testing.T) {
	c, _, path := withDoc(t, `{"diagnosticProvider":{"interFileDependencies":false,"workspaceDiagnostics":false}}`, "é x\n",
		answer(`{"kind":"full","resultId":"1","items":[{"range":{"start":{"line":0,"character":2},"end":{"line":0,"character":3}},"severity":2,"message":"m"}]}`))
	ds, err := c.PullDiagnostics(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 1 || ds[0].Range != rng(0, 3, 0, 4) || ds[0].Severity != SevWarning || ds[0].Severity.String() != "warning" {
		t.Errorf("diagnostics = %+v", ds)
	}
}

// ref: lsp/language/inlayHint.md#textDocument_inlayHint
// quote: "label: string | InlayHintLabelPart[];"
func TestInlayHints(t *testing.T) {
	c, s, path := withDoc(t, `{"inlayHintProvider":{}}`, "let é = f(1)\n", answer(`[{"position":{"line":0,"character":5},"label":[{"value":": "},{"value":"number"}],"kind":1,"paddingLeft":true},
		{"position":{"line":0,"character":10},"label":"x:","kind":2,"paddingRight":true}]`))
	hs, err := c.InlayHints(context.Background(), path, rng(0, 0, 1, 0))
	if err != nil {
		t.Fatal(err)
	}
	want := []InlayHint{{Pos: pos(0, 6), Label: ": number", Kind: HintType, PaddingLeft: true, Edits: []TextEdit{}},
		{Pos: pos(0, 11), Label: "x:", Kind: HintParameter, PaddingRight: true, Edits: []TextEdit{}}}
	if !reflect.DeepEqual(hs, want) {
		t.Errorf("hints = %+v", hs)
	}
	p := decode[map[string]any](t, s.last("textDocument/inlayHint"))
	if end := p["range"].(map[string]any)["end"].(map[string]any); end["line"] != 1.0 || end["character"] != 0.0 {
		t.Errorf("hint range = %v", p["range"])
	}
}

func TestCancelledRequest(t *testing.T) {
	block := make(chan struct{})
	c, s, path := withDoc(t, `{"hoverProvider":true}`, "x\n", func(string, json.RawMessage) (any, error) { <-block; return nil, nil })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.Hover(ctx, Loc{Path: path}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("hover = %v", err)
	}
	close(block)
	// ref: lsp/specification.md#cancelRequest
	// quote: "The request id to cancel."
	s.last("$/cancelRequest")
}
