package lsp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/liamg/oriel/lsp/jsonrpc"
	"github.com/liamg/oriel/lsp/protocol"
	"github.com/liamg/oriel/lsp/snippet"
	"github.com/liamg/oriel/text"
)

func docID(path string) protocol.TextDocumentIdentifier {
	return protocol.TextDocumentIdentifier{URI: URI(path)}
}

// at is a position in the server's encoding.
func (c *Client) at(l Loc) protocol.TextDocumentPositionParams {
	p := protocol.Position{Line: uint32(l.Pos.Line), Character: uint32(l.Pos.Col)}
	if s := c.snapshot(l.Path); s != nil && l.Pos.Line < s.Lines() {
		p = toProto(*s, c.enc, l.Pos)
	}
	return protocol.TextDocumentPositionParams{TextDocument: docID(l.Path), Position: p}
}

func (c *Client) protoRange(path string, r Range) protocol.Range {
	if s := c.snapshot(path); s != nil {
		return toProtoRange(*s, c.enc, r)
	}
	return protocol.Range{Start: protocol.Position{Line: uint32(r.Start.Line), Character: uint32(r.Start.Col)},
		End: protocol.Position{Line: uint32(r.End.Line), Character: uint32(r.End.Col)}}
}

func markup(m *protocol.StringOrMarkup) Markup {
	if m == nil {
		return Markup{}
	}
	return Markup{Kind: m.Kind, Text: m.Value}
}

func command(c *protocol.Command) *Command {
	if c == nil {
		return nil
	}
	return &Command{Title: c.Title, Name: c.Command, Args: c.Arguments}
}

// Complete asks for completions at a place.
func (c *Client) Complete(ctx context.Context, at Loc) (*CompletionList, error) {
	if c.caps.CompletionProvider == nil {
		return nil, ErrNotSupported
	}
	p := protocol.CompletionParams{TextDocumentPositionParams: c.at(at), Context: &protocol.CompletionContext{TriggerKind: protocol.TriggerInvoked}}
	raw, err := c.conn.CallRaw(ctx, "textDocument/completion", p)
	if err != nil {
		return nil, err
	}
	var list protocol.CompletionList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	var items struct {
		Items []json.RawMessage `json:"items"`
	}
	if strings.HasPrefix(strings.TrimSpace(string(raw)), "[") {
		json.Unmarshal(raw, &items.Items)
	} else {
		json.Unmarshal(raw, &items)
	}
	out := &CompletionList{Incomplete: list.IsIncomplete}
	v := c.conv()
	for i, it := range list.Items {
		out.Items = append(out.Items, c.completion(v, at, it, items.Items[i], &list))
	}
	return out, nil
}

// ResolveCompletion fills in what the server left out of a completion until it was chosen: usually its
// documentation, and sometimes its edits.
func (c *Client) ResolveCompletion(ctx context.Context, comp Completion) (Completion, error) {
	if c.caps.CompletionProvider == nil || !c.caps.CompletionProvider.ResolveProvider || comp.raw == nil {
		return comp, nil
	}
	raw, err := c.conn.CallRaw(ctx, "completionItem/resolve", comp.raw)
	if err != nil {
		return comp, err
	}
	var it protocol.CompletionItem
	if err := json.Unmarshal(raw, &it); err != nil {
		return comp, err
	}
	out := c.completion(c.conv(), comp.at, it, raw, comp.list)
	if out.Range == nil {
		out.Range, out.ReplaceRange = comp.Range, comp.ReplaceRange
	}
	return out, nil
}

func (c *Client) completion(v *converter, at Loc, it protocol.CompletionItem, raw json.RawMessage, list *protocol.CompletionList) Completion {
	out := Completion{Label: it.Label, Kind: CompletionKind(it.Kind), Detail: it.Detail, Doc: markup(it.Documentation),
		Deprecated: it.Deprecated, Preselect: it.Preselect, SortText: it.SortText, FilterText: it.FilterText,
		CommitChars: it.CommitCharacters, Command: command(it.Command), at: at, list: list}
	if it.LabelDetails != nil {
		out.LabelDetail, out.LabelDesc = it.LabelDetails.Detail, it.LabelDetails.Description
	}
	for _, t := range it.Tags {
		if t == 1 {
			out.Deprecated = true
		}
	}
	if out.SortText == "" {
		out.SortText = it.Label
	}
	if out.FilterText == "" {
		out.FilterText = it.Label
	}
	format := it.InsertTextFormat
	newText := it.InsertText
	if newText == "" {
		newText = it.Label
	}
	var defaults json.RawMessage
	if d := list.ItemDefaults; d != nil {
		if format == 0 {
			format = d.InsertTextFormat
		}
		if out.CommitChars == nil {
			out.CommitChars = d.CommitCharacters
		}
		defaults = d.EditRange
		if d.Data != nil && it.Data == nil {
			var m map[string]json.RawMessage
			if json.Unmarshal(raw, &m) == nil {
				m["data"] = d.Data
				raw, _ = json.Marshal(m)
			}
		}
	}
	out.raw = raw
	switch {
	case it.TextEdit != nil:
		newText = it.TextEdit.NewText
		c.editRanges(v, &out, at.Path, it.TextEdit.Range, it.TextEdit.Insert, it.TextEdit.Replace)
	case defaults != nil:
		if it.TextEditText != "" {
			newText = it.TextEditText
		}
		var r struct {
			protocol.Range
			Insert  *protocol.Range `json:"insert"`
			Replace *protocol.Range `json:"replace"`
		}
		json.Unmarshal(defaults, &r)
		if r.Insert != nil {
			c.editRanges(v, &out, at.Path, nil, r.Insert, r.Replace)
		} else {
			c.editRanges(v, &out, at.Path, &r.Range, nil, nil)
		}
	}
	if format == protocol.InsertSnippet {
		out.Insert = snippet.Parse(newText, c.vars(at))
	} else {
		out.Insert = snippet.Plain(newText)
	}
	out.AdditionalEdits = v.edits(at.Path, it.AdditionalTextEdits)
	return out
}

func (c *Client) editRanges(v *converter, out *Completion, path string, r, insert, replace *protocol.Range) {
	if r != nil {
		x := v.rng(path, *r)
		out.Range = &x
		return
	}
	if insert != nil {
		x := v.rng(path, *insert)
		out.Range = &x
	}
	if replace != nil && (insert == nil || *replace != *insert) {
		x := v.rng(path, *replace)
		out.ReplaceRange = &x
	}
}

// vars are the snippet variables known at a place.
func (c *Client) vars(at Loc) snippet.Vars {
	return func(name string) (string, bool) {
		base := filepath.Base(at.Path)
		switch name {
		case "TM_FILENAME":
			return base, true
		case "TM_FILENAME_BASE":
			return strings.TrimSuffix(base, filepath.Ext(base)), true
		case "TM_DIRECTORY":
			return filepath.Dir(at.Path), true
		case "TM_FILEPATH":
			return at.Path, true
		case "TM_LINE_INDEX":
			return strconv.Itoa(at.Pos.Line), true
		case "TM_LINE_NUMBER":
			return strconv.Itoa(at.Pos.Line + 1), true
		case "TM_CURRENT_LINE":
			if s := c.snapshot(at.Path); s != nil && at.Pos.Line < s.Lines() {
				return string(s.Line(at.Pos.Line)), true
			}
			return "", true
		case "TM_SELECTED_TEXT", "TM_CURRENT_WORD":
			return "", true
		}
		return "", false
	}
}

// Hover asks what's at a place; nil when there's nothing to say.
func (c *Client) Hover(ctx context.Context, at Loc) (*Hover, error) {
	if !protocol.Enabled(c.caps.HoverProvider) {
		return nil, ErrNotSupported
	}
	var h *protocol.Hover
	if err := c.conn.Call(ctx, "textDocument/hover", c.at(at), &h); err != nil {
		return nil, err
	}
	if h == nil {
		return nil, nil
	}
	m := protocol.HoverContents(h.Contents)
	out := &Hover{Contents: Markup{Kind: m.Kind, Text: m.Value}}
	if h.Range != nil {
		r := c.conv().rng(at.Path, *h.Range)
		out.Range = &r
	}
	return out, nil
}

// SignatureHelp asks for the signatures of the call around a place; nil outside one.
func (c *Client) SignatureHelp(ctx context.Context, at Loc) (*SignatureHelp, error) {
	if c.caps.SignatureHelpProvider == nil {
		return nil, ErrNotSupported
	}
	p := protocol.SignatureHelpParams{TextDocumentPositionParams: c.at(at), Context: &protocol.SignatureHelpContext{TriggerKind: protocol.TriggerInvoked}}
	var h *protocol.SignatureHelp
	if err := c.conn.Call(ctx, "textDocument/signatureHelp", p, &h); err != nil {
		return nil, err
	}
	if h == nil || len(h.Signatures) == 0 {
		return nil, nil
	}
	out := &SignatureHelp{}
	if h.ActiveSignature != nil && int(*h.ActiveSignature) < len(h.Signatures) {
		out.Active = int(*h.ActiveSignature)
	}
	for _, s := range h.Signatures {
		sig := Signature{Label: s.Label, Doc: markup(s.Documentation), ActiveParam: -1}
		active := h.ActiveParameter
		if s.ActiveParameter != nil {
			active = s.ActiveParameter
		}
		if active != nil && int(*active) < len(s.Parameters) {
			sig.ActiveParam = int(*active)
		}
		for _, p := range s.Parameters {
			sig.Params = append(sig.Params, c.param(s.Label, p))
		}
		out.Signatures = append(out.Signatures, sig)
	}
	return out, nil
}

// param finds a parameter in its signature's label: by its text, or by offsets in the server's encoding.
func (c *Client) param(label string, p protocol.ParameterInformation) Param {
	out := Param{Start: -1, End: -1, Doc: markup(p.Documentation)}
	var s string
	if json.Unmarshal(p.Label, &s) == nil {
		out.Label = s
		if i := strings.Index(label, s); i >= 0 && s != "" {
			out.Start, out.End = i, i+len(s)
		}
		return out
	}
	var off [2]int
	if json.Unmarshal(p.Label, &off) != nil {
		return out
	}
	out.Start, out.End = byteOffset(label, c.enc, off[0]), byteOffset(label, c.enc, off[1])
	out.Label = label[out.Start:out.End]
	return out
}

// byteOffset converts an offset into s in the encoding to bytes.
func byteOffset(s, enc string, n int) int {
	if enc == protocol.UTF8 {
		return min(n, len(s))
	}
	units := 0
	for i, r := range s {
		if units >= n {
			return i
		}
		if enc == protocol.UTF32 {
			units++
		} else {
			units += utf16.RuneLen(r)
		}
	}
	return len(s)
}

func (c *Client) locations(ctx context.Context, cap json.RawMessage, method string, params any) ([]Location, error) {
	if !protocol.Enabled(cap) {
		return nil, ErrNotSupported
	}
	var ls protocol.Locations
	if err := c.conn.Call(ctx, method, params, &ls); err != nil {
		return nil, err
	}
	v := c.conv()
	out := make([]Location, 0, len(ls))
	for _, l := range ls {
		loc := v.location(l.Location)
		if l.TargetRange != nil {
			r := v.rng(loc.Path, *l.TargetRange)
			loc.Target = &r
		}
		out = append(out, loc)
	}
	return out, nil
}

func (c *Client) Definition(ctx context.Context, at Loc) ([]Location, error) {
	return c.locations(ctx, c.caps.DefinitionProvider, "textDocument/definition", c.at(at))
}

func (c *Client) Declaration(ctx context.Context, at Loc) ([]Location, error) {
	return c.locations(ctx, c.caps.DeclarationProvider, "textDocument/declaration", c.at(at))
}

func (c *Client) TypeDefinition(ctx context.Context, at Loc) ([]Location, error) {
	return c.locations(ctx, c.caps.TypeDefinitionProvider, "textDocument/typeDefinition", c.at(at))
}

func (c *Client) Implementation(ctx context.Context, at Loc) ([]Location, error) {
	return c.locations(ctx, c.caps.ImplementationProvider, "textDocument/implementation", c.at(at))
}

// References finds the uses of what's at a place, with its declaration if includeDecl.
func (c *Client) References(ctx context.Context, at Loc, includeDecl bool) ([]Location, error) {
	p := protocol.ReferenceParams{TextDocumentPositionParams: c.at(at)}
	p.Context.IncludeDeclaration = includeDecl
	return c.locations(ctx, c.caps.ReferencesProvider, "textDocument/references", p)
}

// PullDiagnostics asks for a file's diagnostics (textDocument/diagnostic), for servers that don't push them.
func (c *Client) PullDiagnostics(ctx context.Context, path string) ([]Diagnostic, error) {
	if !protocol.Enabled(c.caps.DiagnosticProvider) {
		return nil, ErrNotSupported
	}
	var rep struct {
		Kind  string            `json:"kind"`
		Items []json.RawMessage `json:"items"`
	}
	if err := c.conn.Call(ctx, "textDocument/diagnostic", protocol.DocumentDiagnosticParams{TextDocument: docID(path)}, &rep); err != nil {
		return nil, err
	}
	if rep.Kind == "unchanged" {
		return c.Diagnostics(path), nil
	}
	v := c.conv()
	var out []Diagnostic
	for _, raw := range rep.Items {
		if d, ok := v.diagnostic(path, raw); ok {
			out = append(out, d)
		}
	}
	return out, nil
}

// CodeActions asks for the actions over a range, given the diagnostics there; only, if given, filters by kind
// ("quickfix").
func (c *Client) CodeActions(ctx context.Context, path string, r Range, diags []Diagnostic, only ...string) ([]Action, error) {
	if !protocol.Enabled(c.caps.CodeActionProvider) {
		return nil, ErrNotSupported
	}
	var params struct {
		TextDocument protocol.TextDocumentIdentifier `json:"textDocument"`
		Range        protocol.Range                  `json:"range"`
		Context      struct {
			Diagnostics []json.RawMessage `json:"diagnostics"`
			Only        []string          `json:"only,omitempty"`
			TriggerKind int               `json:"triggerKind"`
		} `json:"context"`
	}
	params.TextDocument, params.Range = docID(path), c.protoRange(path, r)
	params.Context.Diagnostics = []json.RawMessage{}
	for _, d := range diags {
		params.Context.Diagnostics = append(params.Context.Diagnostics, c.protoDiagnostic(path, d))
	}
	params.Context.Only, params.Context.TriggerKind = only, protocol.CodeActionInvoked
	var raws []json.RawMessage
	if err := c.conn.Call(ctx, "textDocument/codeAction", params, &raws); err != nil {
		return nil, err
	}
	v := c.conv()
	var out []Action
	for _, raw := range raws {
		var a protocol.CommandOrCodeAction
		if json.Unmarshal(raw, &a) != nil {
			continue
		}
		if a.Command != nil {
			out = append(out, Action{Title: a.Command.Title, Command: command(a.Command)})
			continue
		}
		out = append(out, c.action(v, path, a.CodeAction, raw))
	}
	return out, nil
}

func (c *Client) action(v *converter, path string, a *protocol.CodeAction, raw json.RawMessage) Action {
	out := Action{Title: a.Title, Kind: a.Kind, Preferred: a.IsPreferred, Command: command(a.Command), raw: raw}
	if a.Disabled != nil {
		out.Disabled = a.Disabled.Reason
	}
	var ds struct {
		Diagnostics []json.RawMessage `json:"diagnostics"`
	}
	json.Unmarshal(raw, &ds)
	for _, d := range ds.Diagnostics {
		if d, ok := v.diagnostic(path, d); ok {
			out.Diagnostics = append(out.Diagnostics, d)
		}
	}
	if a.Edit != nil {
		e := v.workspaceEdit(*a.Edit)
		out.Edit = &e
	}
	return out
}

// ResolveAction fills in a code action's edit, for servers that compute it only once it's chosen.
func (c *Client) ResolveAction(ctx context.Context, path string, a Action) (Action, error) {
	if a.raw == nil || a.Edit != nil || !protocol.Option(c.caps.CodeActionProvider, "resolveProvider") {
		return a, nil
	}
	raw, err := c.conn.CallRaw(ctx, "codeAction/resolve", a.raw)
	if err != nil {
		return a, err
	}
	var ca protocol.CodeAction
	if err := json.Unmarshal(raw, &ca); err != nil {
		return a, err
	}
	return c.action(c.conv(), path, &ca, raw), nil
}

// ExecuteCommand runs a server command (from a code action or a lens).
func (c *Client) ExecuteCommand(ctx context.Context, cmd Command) (json.RawMessage, error) {
	return c.conn.CallRaw(ctx, "workspace/executeCommand", protocol.ExecuteCommandParams{Command: cmd.Name, Arguments: cmd.Args})
}

// InlayHints asks for the hints in a range of a file.
func (c *Client) InlayHints(ctx context.Context, path string, r Range) ([]InlayHint, error) {
	if !protocol.Enabled(c.caps.InlayHintProvider) {
		return nil, ErrNotSupported
	}
	var hs []protocol.InlayHint
	if err := c.conn.Call(ctx, "textDocument/inlayHint", protocol.InlayHintParams{TextDocument: docID(path), Range: c.protoRange(path, r)}, &hs); err != nil {
		return nil, err
	}
	v := c.conv()
	out := make([]InlayHint, 0, len(hs))
	for _, h := range hs {
		out = append(out, InlayHint{Pos: v.pos(path, h.Position), Label: protocol.InlayHintLabel(h.Label), Kind: InlayHintKind(h.Kind),
			PaddingLeft: h.PaddingLeft, PaddingRight: h.PaddingRight, Tooltip: markup(h.Tooltip), Edits: v.edits(path, h.TextEdits)})
	}
	return out, nil
}

// PrepareRename asks whether what's at a place can be renamed; nil, nil when it can't.
func (c *Client) PrepareRename(ctx context.Context, at Loc) (*PrepareRename, error) {
	if !protocol.Enabled(c.caps.RenameProvider) {
		return nil, ErrNotSupported
	}
	if !protocol.Option(c.caps.RenameProvider, "prepareProvider") {
		return &PrepareRename{}, nil
	}
	var res *protocol.PrepareRenameResult
	if err := c.conn.Call(ctx, "textDocument/prepareRename", c.at(at), &res); err != nil {
		return nil, err
	}
	if res == nil || (res.Range == nil && !res.DefaultBehavior) {
		return nil, nil
	}
	out := &PrepareRename{Placeholder: res.Placeholder}
	if res.Range != nil {
		r := c.conv().rng(at.Path, *res.Range)
		out.Range = &r
	}
	return out, nil
}

// Rename renames what's at a place everywhere; nil when nothing needs to change.
func (c *Client) Rename(ctx context.Context, at Loc, to string) (*WorkspaceEdit, error) {
	if !protocol.Enabled(c.caps.RenameProvider) {
		return nil, ErrNotSupported
	}
	var e *protocol.WorkspaceEdit
	if err := c.conn.Call(ctx, "textDocument/rename", protocol.RenameParams{TextDocumentPositionParams: c.at(at), NewName: to}, &e); err != nil {
		return nil, err
	}
	if e == nil {
		return nil, nil
	}
	out := c.conv().workspaceEdit(*e)
	return &out, nil
}

// DocumentSymbols is a file's outline: a tree, or a flat list with containers from servers that answer so.
func (c *Client) DocumentSymbols(ctx context.Context, path string) ([]Symbol, error) {
	if !protocol.Enabled(c.caps.DocumentSymbolProvider) {
		return nil, ErrNotSupported
	}
	var ds protocol.DocumentSymbols
	if err := c.conn.Call(ctx, "textDocument/documentSymbol", protocol.DocumentSymbolParams{TextDocument: docID(path)}, &ds); err != nil {
		return nil, err
	}
	v := c.conv()
	if ds.Flat != nil {
		return c.flatSymbols(v, ds.Flat), nil
	}
	var tree func([]protocol.DocumentSymbol) []Symbol
	tree = func(in []protocol.DocumentSymbol) []Symbol {
		var out []Symbol
		for _, s := range in {
			out = append(out, Symbol{Name: s.Name, Detail: s.Detail, Kind: SymbolKind(s.Kind), Deprecated: s.Deprecated || hasTag(s.Tags, 1),
				Path: path, Range: v.rng(path, s.Range), Selection: v.rng(path, s.SelectionRange), Children: tree(s.Children)})
		}
		return out
	}
	return tree(ds.Hierarchy), nil
}

func hasTag(tags []int, t int) bool {
	for _, x := range tags {
		if x == t {
			return true
		}
	}
	return false
}

func (c *Client) flatSymbols(v *converter, in []protocol.SymbolInformation) []Symbol {
	out := make([]Symbol, 0, len(in))
	for _, s := range in {
		path := Path(s.Location.URI)
		sym := Symbol{Name: s.Name, Kind: SymbolKind(s.Kind), Deprecated: hasTag(s.Tags, 1), Path: path, Container: s.ContainerName}
		if s.Location.Range != nil {
			sym.Range = v.rng(path, *s.Location.Range)
			sym.Selection = sym.Range
		}
		out = append(out, sym)
	}
	return out
}

// WorkspaceSymbols searches the project's symbols.
func (c *Client) WorkspaceSymbols(ctx context.Context, query string) ([]Symbol, error) {
	if !protocol.Enabled(c.caps.WorkspaceSymbolProvider) {
		return nil, ErrNotSupported
	}
	var ss []protocol.SymbolInformation
	if err := c.conn.Call(ctx, "workspace/symbol", protocol.WorkspaceSymbolParams{Query: query}, &ss); err != nil {
		return nil, err
	}
	return c.flatSymbols(c.conv(), ss), nil
}

// PrepareCallHierarchy names the function at a place, to ask for its calls.
func (c *Client) PrepareCallHierarchy(ctx context.Context, at Loc) ([]CallItem, error) {
	if !protocol.Enabled(c.caps.CallHierarchyProvider) {
		return nil, ErrNotSupported
	}
	var raws []json.RawMessage
	if err := c.conn.Call(ctx, "textDocument/prepareCallHierarchy", c.at(at), &raws); err != nil {
		return nil, err
	}
	v := c.conv()
	var out []CallItem
	for _, raw := range raws {
		if it, ok := c.callItem(v, raw); ok {
			out = append(out, it)
		}
	}
	return out, nil
}

func (c *Client) callItem(v *converter, raw json.RawMessage) (CallItem, bool) {
	var it protocol.CallHierarchyItem
	if json.Unmarshal(raw, &it) != nil {
		return CallItem{}, false
	}
	path := Path(it.URI)
	return CallItem{Name: it.Name, Kind: SymbolKind(it.Kind), Detail: it.Detail, Path: path, Range: v.rng(path, it.Range),
		Selection: v.rng(path, it.SelectionRange), raw: raw}, true
}

// IncomingCalls are the functions that call item, with where they do.
func (c *Client) IncomingCalls(ctx context.Context, item CallItem) ([]Call, error) {
	return c.calls(ctx, "callHierarchy/incomingCalls", "from", item)
}

// OutgoingCalls are the functions item calls, with where (in item's file).
func (c *Client) OutgoingCalls(ctx context.Context, item CallItem) ([]Call, error) {
	return c.calls(ctx, "callHierarchy/outgoingCalls", "to", item)
}

func (c *Client) calls(ctx context.Context, method, field string, item CallItem) ([]Call, error) {
	if !protocol.Enabled(c.caps.CallHierarchyProvider) {
		return nil, ErrNotSupported
	}
	var raws []map[string]json.RawMessage
	if err := c.conn.Call(ctx, method, map[string]json.RawMessage{"item": item.raw}, &raws); err != nil {
		return nil, err
	}
	v := c.conv()
	var out []Call
	for _, r := range raws {
		it, ok := c.callItem(v, r[field])
		if !ok {
			continue
		}
		var ranges []protocol.Range
		json.Unmarshal(r["fromRanges"], &ranges)
		sites := it.Path
		if field == "to" {
			sites = item.Path
		}
		call := Call{Item: it}
		for _, rg := range ranges {
			call.Sites = append(call.Sites, Location{Path: sites, Range: v.rng(sites, rg)})
		}
		out = append(out, call)
	}
	return out, nil
}

func formattingOptions(o FormatOptions) protocol.FormattingOptions {
	return protocol.FormattingOptions{TabSize: uint32(o.TabSize), InsertSpaces: o.InsertSpaces, TrimTrailingWhitespace: o.TrimTrailingWhitespace,
		InsertFinalNewline: o.InsertFinalNewline, TrimFinalNewlines: o.TrimFinalNewlines}
}

// Format asks for the edits that format a file.
func (c *Client) Format(ctx context.Context, path string, o FormatOptions) ([]TextEdit, error) {
	if !protocol.Enabled(c.caps.DocumentFormattingProvider) {
		return nil, ErrNotSupported
	}
	var es []protocol.TextEdit
	if err := c.conn.Call(ctx, "textDocument/formatting", protocol.DocumentFormattingParams{TextDocument: docID(path), Options: formattingOptions(o)}, &es); err != nil {
		return nil, err
	}
	return c.conv().edits(path, es), nil
}

// FormatRange asks for the edits that format part of a file.
func (c *Client) FormatRange(ctx context.Context, path string, r Range, o FormatOptions) ([]TextEdit, error) {
	if !protocol.Enabled(c.caps.RangeFormattingProvider) {
		return nil, ErrNotSupported
	}
	var es []protocol.TextEdit
	p := protocol.DocumentRangeFormattingParams{TextDocument: docID(path), Range: c.protoRange(path, r), Options: formattingOptions(o)}
	if err := c.conn.Call(ctx, "textDocument/rangeFormatting", p, &es); err != nil {
		return nil, err
	}
	return c.conv().edits(path, es), nil
}

// SemanticTokens classifies a whole file, decoded from the server's relative encoding with its legend.
func (c *Client) SemanticTokens(ctx context.Context, path string) ([]SemanticToken, error) {
	st := c.caps.SemanticTokensProvider
	if st == nil || !protocol.Enabled(st.Full) {
		return nil, ErrNotSupported
	}
	var toks *protocol.SemanticTokens
	if err := c.conn.Call(ctx, "textDocument/semanticTokens/full", protocol.SemanticTokensParams{TextDocument: docID(path)}, &toks); err != nil {
		return nil, err
	}
	if toks == nil {
		return nil, nil
	}
	return decodeTokens(c.conv(), path, st.Legend, toks.Data), nil
}

func decodeTokens(v *converter, path string, legend protocol.SemanticTokensLegend, data []uint32) []SemanticToken {
	var out []SemanticToken
	line, char := uint32(0), uint32(0)
	for i := 0; i+5 <= len(data); i += 5 {
		if data[i] != 0 {
			char = 0
		}
		line += data[i]
		char += data[i+1]
		start := protocol.Position{Line: line, Character: char}
		end := protocol.Position{Line: line, Character: char + data[i+2]}
		t := SemanticToken{Range: Range{Start: v.pos(path, start), End: v.pos(path, end)}}
		if n := int(data[i+3]); n < len(legend.TokenTypes) {
			t.Type = legend.TokenTypes[n]
		}
		for b := 0; b < len(legend.TokenModifiers) && b < 32; b++ {
			if data[i+4]&(1<<b) != 0 {
				t.Modifiers = append(t.Modifiers, legend.TokenModifiers[b])
			}
		}
		out = append(out, t)
	}
	return out
}

// FoldingRanges asks for a file's foldable spans.
func (c *Client) FoldingRanges(ctx context.Context, path string) ([]FoldingRange, error) {
	if !protocol.Enabled(c.caps.FoldingRangeProvider) {
		return nil, ErrNotSupported
	}
	var fs []protocol.FoldingRange
	if err := c.conn.Call(ctx, "textDocument/foldingRange", protocol.FoldingRangeParams{TextDocument: docID(path)}, &fs); err != nil {
		return nil, err
	}
	v := c.conv()
	out := make([]FoldingRange, 0, len(fs))
	col := func(line uint32, ch *uint32) int {
		if ch == nil {
			return -1
		}
		return v.pos(path, protocol.Position{Line: line, Character: *ch}).Col
	}
	for _, f := range fs {
		out = append(out, FoldingRange{StartLine: int(f.StartLine), EndLine: int(f.EndLine), StartCol: col(f.StartLine, f.StartCharacter),
			EndCol: col(f.EndLine, f.EndCharacter), Kind: f.Kind, Collapsed: f.CollapsedText})
	}
	return out, nil
}

// SelectionRanges asks for the ranges to grow a selection through, one chain per position.
func (c *Client) SelectionRanges(ctx context.Context, path string, at []text.Pos) ([]*SelectionRange, error) {
	if !protocol.Enabled(c.caps.SelectionRangeProvider) {
		return nil, ErrNotSupported
	}
	p := protocol.SelectionRangeParams{TextDocument: docID(path)}
	for _, pos := range at {
		p.Positions = append(p.Positions, c.at(Loc{Path: path, Pos: pos}).Position)
	}
	var rs []*protocol.SelectionRange
	if err := c.conn.Call(ctx, "textDocument/selectionRange", p, &rs); err != nil {
		return nil, err
	}
	v := c.conv()
	var conv func(*protocol.SelectionRange) *SelectionRange
	conv = func(r *protocol.SelectionRange) *SelectionRange {
		if r == nil {
			return nil
		}
		return &SelectionRange{Range: v.rng(path, r.Range), Parent: conv(r.Parent)}
	}
	out := make([]*SelectionRange, 0, len(rs))
	for _, r := range rs {
		out = append(out, conv(r))
	}
	return out, nil
}

// CodeLenses asks for a file's lenses.
func (c *Client) CodeLenses(ctx context.Context, path string) ([]CodeLens, error) {
	if c.caps.CodeLensProvider == nil {
		return nil, ErrNotSupported
	}
	var raws []json.RawMessage
	if err := c.conn.Call(ctx, "textDocument/codeLens", protocol.CodeLensParams{TextDocument: docID(path)}, &raws); err != nil {
		return nil, err
	}
	v := c.conv()
	var out []CodeLens
	for _, raw := range raws {
		if l, ok := c.lens(v, path, raw); ok {
			out = append(out, l)
		}
	}
	return out, nil
}

func (c *Client) lens(v *converter, path string, raw json.RawMessage) (CodeLens, bool) {
	var l protocol.CodeLens
	if json.Unmarshal(raw, &l) != nil {
		return CodeLens{}, false
	}
	return CodeLens{Range: v.rng(path, l.Range), Command: command(l.Command), raw: raw}, true
}

// ResolveCodeLens fills in a lens's command.
func (c *Client) ResolveCodeLens(ctx context.Context, path string, l CodeLens) (CodeLens, error) {
	if l.Command != nil || l.raw == nil || c.caps.CodeLensProvider == nil || !c.caps.CodeLensProvider.ResolveProvider {
		return l, nil
	}
	raw, err := c.conn.CallRaw(ctx, "codeLens/resolve", l.raw)
	if err != nil {
		return l, err
	}
	out, ok := c.lens(c.conv(), path, raw)
	if !ok {
		return l, &jsonrpc.Error{Code: jsonrpc.ParseError, Message: "bad code lens"}
	}
	return out, nil
}
