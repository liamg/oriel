package lsp_test

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/lsp"
	"github.com/liamg/oriel/text"
)

// want is what the UI should end up showing for a fixture's session. Places are found in the fixture's text,
// so columns are bytes whatever encoding the server spoke.
type want struct {
	diag      lsp.Diagnostic // Range is filled in from the seeded `greting`
	kind      lsp.CompletionKind
	insert    string
	tabstops  int
	doc       string   // in the resolved completion
	hover     []string // in the hover markdown
	sigLabel  string
	param     string
	fix       string   // the quick fix's title, "" for none
	fixText   string   // what the quick fix inserts
	hints     []string // parameter hints, label@regexp of the argument
	refs      []string // file:regexp of each reference, nth match after a #
	rename    []string // file:regexp=new text
	symbols   []string
	callers   []string
	formatted string // a line after formatting; "" when the server doesn't format
	tokenType string // the semantic token on the greeting call; "" when the server has none
}

var wants = map[string]want{
	// ref: oracle gopls v0.20.0
	"go": {
		diag: lsp.Diagnostic{Severity: lsp.SevError, Source: "compiler", Code: "UndeclaredName", Message: "undefined: greting"},
		kind: 3, insert: "greeting()", tabstops: 1, doc: "greeting says hello to name.",
		hover:    []string{"```go\nfunc greeting(name string) string\n```", "greeting says hello to name."},
		sigLabel: "greeting(name string) string", param: "name string",
		fix: "Create function greting", fixText: "func greting(s string) string {",
		hints:     []string{`name:@greeting\((n)\)`, `name:@shout\(("é")\)`},
		refs:      []string{`lib:func (greeting)`, `lib:return (greeting)`, `main:\+= (greeting)`},
		rename:    []string{`lib:// (greeting) says → salute`, `lib:func (greeting) → salute`, `lib:return (greeting) → salute`, `main:\+= (greeting) → salute`},
		symbols:   []string{"greeting function", "shout function"},
		callers:   []string{"shout", "Welcome"},
		formatted: "\tsize := len(out)",
		tokenType: "function",
	},
	// ref: oracle typescript-language-server 5.3.0, typescript 6.0.3
	"ts": {
		diag: lsp.Diagnostic{Severity: lsp.SevError, Source: "typescript", Code: "2552", Message: "Cannot find name 'greting'. Did you mean 'greeting'?"},
		kind: 6, insert: "greeting", doc: "Say hello to name.",
		hover:    []string{"```typescript\n(alias) greeting(name: string): string\nimport greeting\n```", "Say hello to name."},
		sigLabel: "greeting(name: string): string", param: "name: string",
		fix: "Change spelling to 'greeting'", fixText: "greeting",
		hints:     []string{`name:@greeting\((n)\)`, `name:@shout\(("é")\)`},
		refs:      []string{`main:\{ (greeting),`, `main:\+= (greeting)`, `lib:function (greeting)`, `lib:return (greeting)`},
		rename:    []string{`main:\{ (greeting), → greeting as salute`, `main:\+= (greeting) → salute`},
		symbols:   []string{"greeting function", "shout function"},
		callers:   []string{"shout", "welcome"},
		formatted: "  let size = out.length;",
		tokenType: "function",
	},
	// ref: oracle pyright 1.1.413
	"py": {
		diag: lsp.Diagnostic{Severity: lsp.SevError, Source: "Pyright", Code: "reportUndefinedVariable", Message: `"greting" is not defined`},
		kind: 3, insert: "greeting", doc: "Say hello to name.",
		hover:    []string{"```python\n(function) def greeting(name: str) -> str\n```", "Say hello to name."},
		sigLabel: "(name: str) -> str", param: "name: str",
		refs:    []string{`main:import (greeting),`, `main:\+= (greeting)`, `lib:def (greeting)`, `lib:return (greeting)`},
		rename:  []string{`main:import (greeting), → salute`, `main:\+= (greeting) → salute`, `lib:def (greeting) → salute`, `lib:return (greeting) → salute`},
		symbols: []string{"greeting function", "shout function"},
		callers: []string{"welcome", "shout"},
	},
}

// TestSession replays each server's recorded session through the Manager and checks what the UI would show.
// With ORIEL_LSP_LIVE=1 it runs against the installed servers instead, and -record rewrites the transcripts.
func TestSession(t *testing.T) {
	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			r := run(t, fx)
			checkSession(t, r, wants[fx.name])
		})
	}
}

func (r *result) file(name string) (path, src string) {
	if name == "lib" {
		return r.lib, r.libText
	}
	return r.main, r.mainText
}

// span is the byte range of re's first group (or of re) in a file, at the match after # if given.
func (r *result) span(t *testing.T, spec string) lsp.Location {
	t.Helper()
	name, re, _ := strings.Cut(spec, ":")
	path, src := r.file(name)
	n := 0
	if i := strings.LastIndex(re, "#"); i > 0 {
		fmt.Sscan(re[i+1:], &n)
		re = re[:i]
	}
	ms := regexp.MustCompile(re).FindAllStringSubmatchIndex(src, -1)
	if n >= len(ms) {
		t.Fatalf("no match %d of %q in %s", n, re, name)
	}
	m := ms[n]
	if len(m) > 2 {
		m = m[2:]
	}
	snap := text.New([]byte(src), text.Author{}).Snapshot()
	return lsp.Location{Path: path, Range: lsp.Range{Start: snap.PosAt(m[0]), End: snap.PosAt(m[1])}}
}

func checkSession(t *testing.T, r *result, w want) {
	// ref: lsp/types/textDocuments.md#textDocuments
	// quote: "So in a string of the form `a𐐀b` the character offset of the character `a` is 0, the character offset of `𐐀` is 1 and the character offset of b is 3 since `𐐀` is represented using two code units in UTF-16."
	seeded := r.span(t, `main:(greting)`)
	if len(r.diags) != 1 {
		t.Fatalf("diagnostics = %+v, want the seeded one", r.diags)
	}
	d := r.diags[0]
	if d.Range != seeded.Range || d.Severity != w.diag.Severity || d.Source != w.diag.Source || d.Code != w.diag.Code || d.Message != w.diag.Message {
		t.Errorf("diagnostic = %v %v %q %q %q, want %v %v %q %q %q", d.Range, d.Severity, d.Source, d.Code, d.Message,
			seeded.Range, w.diag.Severity, w.diag.Source, w.diag.Code, w.diag.Message)
	}

	if !slices.Contains(r.states, lsp.Ready) || r.states[0] != lsp.Starting || r.states[len(r.states)-1] != lsp.Stopped {
		t.Errorf("states = %v, want starting … ready … stopped", r.states)
	}
	if !r.sawWork || !slices.Contains(r.states, lsp.Busy) {
		t.Errorf("no work in progress reported: states %v", r.states)
	}

	if r.resolved == nil {
		t.Fatalf("no completion labelled greeting among %d", len(r.completion.Items))
	}
	c := *r.resolved
	if c.Kind != w.kind || c.Insert.Text != w.insert || len(c.Insert.Tabstops) != w.tabstops || !strings.Contains(c.Doc.Text, w.doc) {
		t.Errorf("completion = kind %v, insert %q, %d tab stops, doc %q", c.Kind, c.Insert.Text, len(c.Insert.Tabstops), c.Doc.Text)
	}
	if c.Range != nil {
		typed := r.completionRange(t)
		if *c.Range != typed {
			t.Errorf("completion replaces %v, want the typed prefix %v", *c.Range, typed)
		}
	}

	use := r.span(t, `main:\+= (greeting)`)
	if r.hover == nil || r.hover.Contents.Kind != "markdown" {
		t.Fatalf("hover = %+v", r.hover)
	}
	for _, s := range w.hover {
		if !strings.Contains(r.hover.Contents.Text, s) {
			t.Errorf("hover %q lacks %q", r.hover.Contents.Text, s)
		}
	}
	if r.hover.Range != nil && *r.hover.Range != use.Range {
		t.Errorf("hover range = %v, want %v", *r.hover.Range, use.Range)
	}

	if r.sig == nil || len(r.sig.Signatures) == 0 {
		t.Fatalf("signature help = %+v", r.sig)
	}
	sig := r.sig.Signatures[r.sig.Active]
	if sig.Label != w.sigLabel || len(sig.Params) != 1 || sig.Params[0].Label != w.param || sig.ActiveParam != 0 ||
		sig.Label[sig.Params[0].Start:sig.Params[0].End] != w.param {
		t.Errorf("signature = %+v", sig)
	}

	checkFix(t, r, w, seeded)

	if len(w.hints) == 0 {
		if !errors.Is(r.hintsErr, lsp.ErrNotSupported) {
			t.Errorf("inlay hints = %+v, %v; want unsupported", r.hints, r.hintsErr)
		}
	}
	for _, h := range w.hints {
		label, re, _ := strings.Cut(h, "@")
		pos := r.span(t, "main:"+re).Range.Start
		found := false
		for _, x := range r.hints {
			found = found || x.Label == label && x.Pos == pos && x.Kind == lsp.HintParameter
		}
		if !found {
			t.Errorf("no %q parameter hint at %v in %+v", label, pos, r.hints)
		}
	}

	decl := r.span(t, `lib:(?:func|def|function) (greeting)`)
	if len(r.def) != 1 || r.def[0].Path != decl.Path || r.def[0].Range != decl.Range {
		t.Errorf("definition = %+v, want %+v", r.def, decl)
	}
	// ref: lsp/language/references.md#textDocument_references
	// quote: "The references request is sent from the client to the server to resolve project-wide references for the symbol denoted by the given text document position."
	var want []lsp.Location
	for _, s := range w.refs {
		want = append(want, r.span(t, s))
	}
	if !sameLocations(r.refs, want) {
		t.Errorf("references = %+v\nwant %+v", r.refs, want)
	}

	if r.prepare == nil || (r.prepare.Range != nil && *r.prepare.Range != use.Range) {
		t.Errorf("prepare rename = %+v, want the name at %v", r.prepare, use.Range)
	}
	checkRename(t, r, w)

	var syms []string
	for _, s := range r.symbols {
		syms = append(syms, s.Name+" "+s.Kind.String())
		if want := r.span(t, `lib:(?:func|def|function) (`+s.Name+`)`); s.Selection != want.Range || s.Path != r.lib {
			t.Errorf("symbol %s selects %v, want %v", s.Name, s.Selection, want.Range)
		}
	}
	if !slices.Equal(syms, w.symbols) {
		t.Errorf("document symbols = %q, want %q", syms, w.symbols)
	}
	// ref: lsp/workspace/symbol.md#workspace_symbol
	// quote: "The workspace symbol request is sent from the client to the server to list project-wide symbols matching the query string."
	found := false
	for _, s := range r.wsSymbols {
		found = found || s.Name == "greeting" && s.Path == r.lib && s.Kind.String() == "function"
	}
	if !found {
		t.Errorf("workspace symbols %+v lack greeting", r.wsSymbols)
	}

	checkCalls(t, r, w, decl)
	checkFormat(t, r, w)

	if w.tokenType == "" {
		if !errors.Is(r.tokensErr, lsp.ErrNotSupported) {
			t.Errorf("semantic tokens = %d, %v; want unsupported", len(r.tokens), r.tokensErr)
		}
	} else {
		found := false
		for _, tok := range r.tokens {
			found = found || tok.Range == use.Range && tok.Type == w.tokenType
		}
		if !found {
			t.Errorf("no %s token at %v among %+v", w.tokenType, use.Range, r.tokens)
		}
	}
}

// completionRange is the prefix the session typed on the blank line.
func (r *result) completionRange(t *testing.T) lsp.Range {
	blank := at(t, r.mainText, `greeting\(n\);?\n(?:\s*\}\n)?(\n)`, 0, 0)
	indent := map[string]int{".go": 1, ".ts": 2, ".py": 4}[r.main[strings.LastIndex(r.main, "."):]]
	return lsp.Range{Start: text.Pos{Line: blank.Line, Col: indent}, End: text.Pos{Line: blank.Line, Col: indent + 3}}
}

func sameLocations(got, want []lsp.Location) bool {
	key := func(l lsp.Location) string { return l.Path + " " + l.Range.String() }
	var a, b []string
	for _, l := range got {
		a = append(a, key(l))
	}
	for _, l := range want {
		b = append(b, key(l))
	}
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// ref: lsp/language/codeAction.md#textDocument_codeAction
// quote: "Marks this as a preferred action. Preferred actions are used by the"
func checkFix(t *testing.T, r *result, w want, seeded lsp.Location) {
	var fix *lsp.Action
	for i, a := range r.actions {
		if a.Kind == "quickfix" {
			fix = &r.actions[i]
		}
	}
	if w.fix == "" {
		if fix != nil {
			t.Errorf("unexpected quick fix %+v", fix)
		}
		return
	}
	if fix == nil || fix.Title != w.fix || fix.Edit == nil {
		t.Fatalf("quick fix = %+v, want %q with an edit", fix, w.fix)
	}
	var edits []lsp.TextEdit
	for _, ch := range fix.Edit.Changes {
		if ch.Path == r.main {
			edits = append(edits, ch.Edits...)
		}
	}
	if len(edits) != 1 || !strings.Contains(edits[0].NewText, w.fixText) {
		t.Fatalf("quick fix edits = %+v", fix.Edit.Changes)
	}
	// A spelling fix replaces the seeded name; a new function goes after the last one.
	if edits[0].NewText == w.fixText && edits[0].Range != seeded.Range {
		t.Errorf("quick fix replaces %v, want %v", edits[0].Range, seeded.Range)
	}
	if edits[0].NewText != w.fixText {
		snap := text.New([]byte(r.mainText), text.Author{}).Snapshot()
		end := snap.PosAt(strings.LastIndex(r.mainText, "}") + 1)
		if edits[0].Range != (lsp.Range{Start: end, End: end}) {
			t.Errorf("new function goes at %v, want %v", edits[0].Range, end)
		}
	}
	if len(fix.Diagnostics) > 0 && fix.Diagnostics[0].Range != seeded.Range {
		t.Errorf("quick fix is for %v, want %v", fix.Diagnostics[0].Range, seeded.Range)
	}
	if w.fix == "Change spelling to 'greeting'" && !fix.Preferred {
		t.Error("the spelling fix isn't preferred")
	}
}

// ref: lsp/types/workspaceEdit.md#workspaceEdit
// quote: "Holds changes to existing resources."
func checkRename(t *testing.T, r *result, w want) {
	if r.rename == nil {
		t.Fatal("rename gave no edit")
	}
	type edit struct {
		path string
		rng  lsp.Range
		text string
	}
	var got, want []edit
	for _, ch := range r.rename.Changes {
		for _, e := range ch.Edits {
			got = append(got, edit{ch.Path, e.Range, e.NewText})
		}
	}
	for _, s := range w.rename {
		where, to, _ := strings.Cut(s, " → ")
		l := r.span(t, where)
		want = append(want, edit{l.Path, l.Range, to})
	}
	cmp := func(a, b edit) int { return strings.Compare(fmt.Sprint(a), fmt.Sprint(b)) }
	slices.SortFunc(got, cmp)
	slices.SortFunc(want, cmp)
	if !slices.Equal(got, want) {
		t.Errorf("rename edits = %+v\nwant %+v", got, want)
	}
}

// ref: lsp/language/callHierarchy.md#callHierarchy_incomingCalls
// quote: "The ranges at which the calls appear. This is relative to the caller"
func checkCalls(t *testing.T, r *result, w want, decl lsp.Location) {
	if len(r.callItems) != 1 || r.callItems[0].Name != "greeting" || r.callItems[0].Selection != decl.Range {
		t.Fatalf("call hierarchy item = %+v", r.callItems)
	}
	var callers []string
	for _, c := range r.incoming {
		callers = append(callers, c.Item.Name)
		var site lsp.Location
		if c.Item.Name == "shout" {
			site = r.span(t, `lib:return (greeting)`)
		} else {
			site = r.span(t, `main:\+= (greeting)`)
		}
		if len(c.Sites) != 1 || c.Sites[0] != site {
			t.Errorf("%s calls greeting at %+v, want %+v", c.Item.Name, c.Sites, site)
		}
	}
	slices.Sort(callers)
	want := slices.Sorted(slices.Values(w.callers))
	if !slices.Equal(callers, want) {
		t.Errorf("callers = %q, want %q", callers, want)
	}
	// ref: lsp/language/callHierarchy.md#callHierarchy_outgoingCalls
	// quote: "the caller, e.g the item passed to `callHierarchy/outgoingCalls` request."
	site := r.span(t, `lib:return (greeting)`)
	if len(r.outgoing) != 1 || r.outgoing[0].Item.Name != "greeting" || len(r.outgoing[0].Sites) != 1 || r.outgoing[0].Sites[0] != site {
		t.Errorf("shout's outgoing calls = %+v, want greeting at %+v", r.outgoing, site)
	}
}

// ref: lsp/types/textEditArray.md#textEditArray
// quote: "All text edits ranges refer to positions in the document they are computed on."
func checkFormat(t *testing.T, r *result, w want) {
	// ref: lsp/language/formatting.md#textDocument_formatting
	// quote: "The document formatting request is sent from the client to the server to format a whole document."
	if w.formatted == "" {
		if !errors.Is(r.formatErr, lsp.ErrNotSupported) {
			t.Errorf("format = %+v, %v; want unsupported", r.format, r.formatErr)
		}
		return
	}
	if r.formatErr != nil {
		t.Fatal(r.formatErr)
	}
	buf := text.New([]byte(r.mainText), text.Author{})
	e, err := lsp.EditFor(buf.Snapshot(), r.format, text.Author{Kind: text.Format})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := buf.Apply(e); err != nil {
		t.Fatal(err)
	}
	if out := buf.Snapshot().String(); !slices.Contains(strings.Split(out, "\n"), w.formatted) {
		t.Errorf("formatted:\n%s\nlacks the line %q", out, w.formatted)
	}
}
