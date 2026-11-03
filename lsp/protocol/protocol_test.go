package protocol

import (
	"encoding/json"
	"reflect"
	"testing"
)

func decode[T any](t *testing.T, s string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("decode %s: %v", s, err)
	}
	return v
}

func rng(l1, c1, l2, c2 uint32) Range {
	return Range{Position{l1, c1}, Position{l2, c2}}
}

// ref: lsp/language/definition.md#textDocument_definition
// quote: "result: [`Location`](#location) \\| [`Location`](#location)[] \\| [`LocationLink`](#locationLink)[] \\| `null`"
func TestLocations(t *testing.T) {
	loc := `{"uri":"file:///a.go","range":{"start":{"line":1,"character":2},"end":{"line":1,"character":5}}}`
	link := `{"targetUri":"file:///b.go","targetRange":{"start":{"line":0,"character":0},"end":{"line":9,"character":1}},` +
		`"targetSelectionRange":{"start":{"line":3,"character":5},"end":{"line":3,"character":8}}}`
	want := Location{URI: "file:///a.go", Range: rng(1, 2, 1, 5)}
	if got := decode[Locations](t, loc); len(got) != 1 || got[0].Location != want {
		t.Errorf("single = %+v", got)
	}
	if got := decode[Locations](t, "["+loc+"]"); len(got) != 1 || got[0].Location != want {
		t.Errorf("array = %+v", got)
	}
	if got := decode[Locations](t, "null"); got != nil {
		t.Errorf("null = %+v", got)
	}
	got := decode[Locations](t, "["+link+"]")
	tr := rng(0, 0, 9, 1)
	if len(got) != 1 || got[0].Location != (Location{URI: "file:///b.go", Range: rng(3, 5, 3, 8)}) || !reflect.DeepEqual(got[0].TargetRange, &tr) {
		t.Errorf("links = %+v", got)
	}
}

// ref: lsp/language/hover.md#textDocument_hover
// quote: "The pair of a language and a value is an equivalent to markdown:"
func TestHoverContents(t *testing.T) {
	tests := []struct {
		in   string
		want MarkupContent
	}{
		{`{"kind":"markdown","value":"**x**"}`, MarkupContent{Markdown, "**x**"}},
		{`{"kind":"plaintext","value":"x"}`, MarkupContent{PlainText, "x"}},
		{`"*md*"`, MarkupContent{Markdown, "*md*"}},
		{`{"language":"go","value":"func f()"}`, MarkupContent{Markdown, "```go\nfunc f()\n```"}},
		{`[{"language":"python","value":"x: int"},"doc"]`, MarkupContent{Markdown, "```python\nx: int\n```\n\ndoc"}},
	}
	for _, tc := range tests {
		if got := HoverContents(json.RawMessage(tc.in)); got != tc.want {
			t.Errorf("HoverContents(%s) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

// ref: lsp/language/completion.md#textDocument_completion
// quote: "If a `CompletionItem[]` is provided it is interpreted to be complete. So it is the same as `{ isIncomplete: false, items }`"
func TestCompletionList(t *testing.T) {
	l := decode[CompletionList](t, `[{"label":"a"}]`)
	if l.IsIncomplete || len(l.Items) != 1 || l.Items[0].Label != "a" {
		t.Errorf("array = %+v", l)
	}
	l = decode[CompletionList](t, `{"isIncomplete":true,"items":[{"label":"b","textEdit":{"newText":"b","insert":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"replace":{"start":{"line":0,"character":0},"end":{"line":0,"character":2}}}}]}`)
	if !l.IsIncomplete || l.Items[0].TextEdit.Insert == nil || l.Items[0].TextEdit.Replace.End.Character != 2 {
		t.Errorf("list = %+v", l)
	}
	if l := decode[CompletionList](t, `null`); l.Items != nil {
		t.Errorf("null = %+v", l)
	}
}

// ref: lsp/language/codeAction.md#textDocument_codeAction
// quote: "result: `(Command | CodeAction)[]` \\| `null` where `CodeAction` is defined as follows:"
func TestCommandOrCodeAction(t *testing.T) {
	got := decode[[]CommandOrCodeAction](t, `[{"title":"Run","command":"go.run","arguments":[1]},`+
		`{"title":"Fix","kind":"quickfix","isPreferred":true,"command":{"title":"x","command":"y"}}]`)
	if got[0].Command == nil || got[0].Command.Command != "go.run" || got[0].CodeAction != nil {
		t.Errorf("command = %+v", got[0])
	}
	if a := got[1].CodeAction; a == nil || a.Kind != "quickfix" || !a.IsPreferred || a.Command.Command != "y" {
		t.Errorf("code action = %+v", got[1])
	}
}

// ref: lsp/language/documentSymbol.md#textDocument_documentSymbol
// quote: "`DocumentSymbol[]` and `SymbolInformation[]` can not be mixed."
func TestDocumentSymbols(t *testing.T) {
	h := decode[DocumentSymbols](t, `[{"name":"T","kind":23,"range":{"start":{"line":0,"character":0},"end":{"line":3,"character":1}},`+
		`"selectionRange":{"start":{"line":0,"character":5},"end":{"line":0,"character":6}},"children":[{"name":"F","kind":8,`+
		`"range":{"start":{"line":1,"character":1},"end":{"line":1,"character":6}},"selectionRange":{"start":{"line":1,"character":1},"end":{"line":1,"character":2}}}]}]`)
	if len(h.Hierarchy) != 1 || h.Flat != nil || h.Hierarchy[0].Children[0].Name != "F" {
		t.Errorf("hierarchical = %+v", h)
	}
	f := decode[DocumentSymbols](t, `[{"name":"f","kind":12,"location":{"uri":"file:///a","range":{"start":{"line":2,"character":0},"end":{"line":2,"character":1}}},"containerName":"m"}]`)
	if len(f.Flat) != 1 || f.Hierarchy != nil || f.Flat[0].ContainerName != "m" || f.Flat[0].Location.Range.Start.Line != 2 {
		t.Errorf("flat = %+v", f)
	}
}

// ref: lsp/language/rename.md#textDocument_prepareRename
// quote: "result: `Range | { range: Range, placeholder: string } | { defaultBehavior: boolean } | null`"
func TestPrepareRename(t *testing.T) {
	r := rng(1, 2, 1, 4)
	tests := []struct {
		in   string
		want PrepareRenameResult
	}{
		{`{"start":{"line":1,"character":2},"end":{"line":1,"character":4}}`, PrepareRenameResult{Range: &r}},
		{`{"range":{"start":{"line":1,"character":2},"end":{"line":1,"character":4}},"placeholder":"ab"}`, PrepareRenameResult{Range: &r, Placeholder: "ab"}},
		{`{"defaultBehavior":true}`, PrepareRenameResult{DefaultBehavior: true}},
		{`null`, PrepareRenameResult{}},
	}
	for _, tc := range tests {
		if got := decode[PrepareRenameResult](t, tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s = %+v", tc.in, got)
		}
	}
}

// ref: lsp/specification.md#textDocument_synchronization
// quote: "property type: `TextDocumentSyncKind | TextDocumentSyncOptions`."
func TestDecodeSync(t *testing.T) {
	tests := []struct {
		in   string
		want TextDocumentSync
	}{
		{`2`, TextDocumentSync{OpenClose: true, Change: SyncIncremental, Save: true}},
		{`0`, TextDocumentSync{}},
		{``, TextDocumentSync{}},
		{`{"openClose":true,"change":1,"save":{"includeText":true}}`, TextDocumentSync{OpenClose: true, Change: SyncFull, Save: true, IncludeText: true}},
		// ref: lsp/specification.md#textDocument_synchronization
		// quote: "Open and close notifications are sent to the server. If omitted open"
		{`{"change":2,"save":true}`, TextDocumentSync{Change: SyncIncremental, Save: true}},
	}
	for _, tc := range tests {
		if got := DecodeSync(json.RawMessage(tc.in)); got != tc.want {
			t.Errorf("DecodeSync(%s) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

// ref: lsp/types/resourceChanges.md#resourceChanges
// quote: "kind: 'rename';"
func TestDecodeDocumentChange(t *testing.T) {
	e, op, err := DecodeDocumentChange(json.RawMessage(`{"textDocument":{"uri":"file:///a","version":3},"edits":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"newText":"x"}]}`))
	if err != nil || op != nil || e.TextDocument.Version == nil || *e.TextDocument.Version != 3 || e.Edits[0].NewText != "x" {
		t.Errorf("text edit = %+v %+v %v", e, op, err)
	}
	e, op, err = DecodeDocumentChange(json.RawMessage(`{"kind":"rename","oldUri":"file:///a","newUri":"file:///b"}`))
	if err != nil || e != nil || op.Kind != "rename" || op.NewURI != "file:///b" {
		t.Errorf("rename = %+v %+v %v", e, op, err)
	}
}

// ref: lsp/types/diagnostic.md#diagnostic
// quote: "code?: integer | string;"
func TestIntOrString(t *testing.T) {
	d := decode[[]Diagnostic](t, `[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"message":"m","code":"E1"},`+
		`{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"message":"m","code":42}]`)
	if d[0].Code.String() != "E1" || d[1].Code.String() != "42" {
		t.Errorf("codes = %v, %v", d[0].Code, d[1].Code)
	}
	b, _ := json.Marshal(d[1])
	if back := decode[Diagnostic](t, string(b)); back.Code.String() != "42" || string(back.Code.Raw) != "42" {
		t.Errorf("round trip = %s", b)
	}
}

// ref: lsp/language/inlayHint.md#textDocument_inlayHint
// quote: "label: string | InlayHintLabelPart[];"
func TestInlayHintLabel(t *testing.T) {
	if got := InlayHintLabel(json.RawMessage(`"x:"`)); got != "x:" {
		t.Errorf("string label = %q", got)
	}
	if got := InlayHintLabel(json.RawMessage(`[{"value":"a"},{"value":"b"}]`)); got != "ab" {
		t.Errorf("parts label = %q", got)
	}
}

// ref: lsp/general/initialize.md#initialize
// quote: "If omitted it defaults to 'utf-16'."
func TestEnabled(t *testing.T) {
	for in, want := range map[string]bool{`true`: true, `false`: false, `{}`: true, `{"workDoneProgress":true}`: true, ``: false, `null`: false} {
		if got := Enabled(json.RawMessage(in)); got != want {
			t.Errorf("Enabled(%s) = %v", in, got)
		}
	}
	if !Option(json.RawMessage(`{"prepareProvider":true}`), "prepareProvider") || Option(json.RawMessage(`true`), "prepareProvider") {
		t.Error("Option")
	}
}
