package protocol

import (
	"encoding/json"
)

type Diagnostic struct {
	Range           Range        `json:"range"`
	Severity        int          `json:"severity,omitempty"`
	Code            *IntOrString `json:"code,omitempty"`
	CodeDescription *struct {
		Href string `json:"href"`
	} `json:"codeDescription,omitempty"`
	Source             string                         `json:"source,omitempty"`
	Message            string                         `json:"message"`
	Tags               []int                          `json:"tags,omitempty"`
	RelatedInformation []DiagnosticRelatedInformation `json:"relatedInformation,omitempty"`
	Data               json.RawMessage                `json:"data,omitempty"`
}

type DiagnosticRelatedInformation struct {
	Location Location `json:"location"`
	Message  string   `json:"message"`
}

type PublishDiagnosticsParams struct {
	URI         DocumentURI  `json:"uri"`
	Version     *int32       `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

type DocumentDiagnosticParams struct {
	TextDocument     TextDocumentIdentifier `json:"textDocument"`
	Identifier       string                 `json:"identifier,omitempty"`
	PreviousResultID string                 `json:"previousResultId,omitempty"`
}

// DocumentDiagnosticReport is a full or unchanged report.
type DocumentDiagnosticReport struct {
	Kind     string       `json:"kind"` // "full" or "unchanged"
	ResultID string       `json:"resultId,omitempty"`
	Items    []Diagnostic `json:"items,omitempty"`
}

// Completion trigger kinds.
const (
	TriggerInvoked   = 1
	TriggerCharacter = 2
)

type CompletionParams struct {
	TextDocumentPositionParams
	Context *CompletionContext `json:"context,omitempty"`
}

type CompletionContext struct {
	TriggerKind      int    `json:"triggerKind"`
	TriggerCharacter string `json:"triggerCharacter,omitempty"`
}

// Insert text formats.
const (
	InsertPlainText = 1
	InsertSnippet   = 2
)

type CompletionItem struct {
	Label        string `json:"label"`
	LabelDetails *struct {
		Detail      string `json:"detail,omitempty"`
		Description string `json:"description,omitempty"`
	} `json:"labelDetails,omitempty"`
	Kind                int             `json:"kind,omitempty"`
	Tags                []int           `json:"tags,omitempty"`
	Detail              string          `json:"detail,omitempty"`
	Documentation       *StringOrMarkup `json:"documentation,omitempty"`
	Deprecated          bool            `json:"deprecated,omitempty"`
	Preselect           bool            `json:"preselect,omitempty"`
	SortText            string          `json:"sortText,omitempty"`
	FilterText          string          `json:"filterText,omitempty"`
	InsertText          string          `json:"insertText,omitempty"`
	InsertTextFormat    int             `json:"insertTextFormat,omitempty"`
	InsertTextMode      int             `json:"insertTextMode,omitempty"`
	TextEdit            *CompletionEdit `json:"textEdit,omitempty"`
	TextEditText        string          `json:"textEditText,omitempty"`
	AdditionalTextEdits []TextEdit      `json:"additionalTextEdits,omitempty"`
	CommitCharacters    []string        `json:"commitCharacters,omitempty"`
	Command             *Command        `json:"command,omitempty"`
	Data                json.RawMessage `json:"data,omitempty"`
}

// CompletionEdit is `TextEdit | InsertReplaceEdit`: one has Range, the other Insert and Replace.
type CompletionEdit struct {
	NewText string `json:"newText"`
	Range   *Range `json:"range,omitempty"`
	Insert  *Range `json:"insert,omitempty"`
	Replace *Range `json:"replace,omitempty"`
}

// CompletionList is the completion result, also when the server answers a bare array.
type CompletionList struct {
	IsIncomplete bool `json:"isIncomplete"`
	ItemDefaults *struct {
		CommitCharacters []string        `json:"commitCharacters,omitempty"`
		EditRange        json.RawMessage `json:"editRange,omitempty"` // Range | {insert, replace}
		InsertTextFormat int             `json:"insertTextFormat,omitempty"`
		InsertTextMode   int             `json:"insertTextMode,omitempty"`
		Data             json.RawMessage `json:"data,omitempty"`
	} `json:"itemDefaults,omitempty"`
	Items []CompletionItem `json:"items"`
}

func (l *CompletionList) UnmarshalJSON(b []byte) error {
	*l = CompletionList{}
	if isNull(b) {
		return nil
	}
	if isArray(b) {
		return json.Unmarshal(b, &l.Items)
	}
	type plain CompletionList
	return json.Unmarshal(b, (*plain)(l))
}

type HoverParams = TextDocumentPositionParams

// Hover's Contents is `MarkedString | MarkedString[] | MarkupContent`; HoverMarkdown reads it.
type Hover struct {
	Contents json.RawMessage `json:"contents"`
	Range    *Range          `json:"range,omitempty"`
}

// HoverContents reads hover contents as one markup: a MarkedString with a language is a fenced code block,
// and several are joined with blank lines.
func HoverContents(raw json.RawMessage) MarkupContent {
	if isNull(raw) {
		return MarkupContent{Kind: PlainText}
	}
	if isArray(raw) {
		var parts []json.RawMessage
		json.Unmarshal(raw, &parts)
		var out MarkupContent
		out.Kind = Markdown
		for i, p := range parts {
			m := HoverContents(p)
			if i > 0 {
				out.Value += "\n\n"
			}
			out.Value += m.Value
		}
		return out
	}
	if isString(raw) {
		var s string
		json.Unmarshal(raw, &s)
		return MarkupContent{Kind: Markdown, Value: s}
	}
	var m struct {
		Kind     string `json:"kind"`
		Language string `json:"language"`
		Value    string `json:"value"`
	}
	json.Unmarshal(raw, &m)
	if m.Kind != "" {
		return MarkupContent{Kind: m.Kind, Value: m.Value}
	}
	return MarkupContent{Kind: Markdown, Value: "```" + m.Language + "\n" + m.Value + "\n```"}
}

type SignatureHelpParams struct {
	TextDocumentPositionParams
	Context *SignatureHelpContext `json:"context,omitempty"`
}

type SignatureHelpContext struct {
	TriggerKind      int    `json:"triggerKind"`
	TriggerCharacter string `json:"triggerCharacter,omitempty"`
	IsRetrigger      bool   `json:"isRetrigger"`
}

type SignatureHelp struct {
	Signatures      []SignatureInformation `json:"signatures"`
	ActiveSignature *uint32                `json:"activeSignature,omitempty"`
	ActiveParameter *uint32                `json:"activeParameter,omitempty"`
}

type SignatureInformation struct {
	Label           string                 `json:"label"`
	Documentation   *StringOrMarkup        `json:"documentation,omitempty"`
	Parameters      []ParameterInformation `json:"parameters,omitempty"`
	ActiveParameter *uint32                `json:"activeParameter,omitempty"`
}

// ParameterInformation's Label is `string | [uinteger, uinteger]`, kept raw.
type ParameterInformation struct {
	Label         json.RawMessage `json:"label"`
	Documentation *StringOrMarkup `json:"documentation,omitempty"`
}

type ReferenceParams struct {
	TextDocumentPositionParams
	Context struct {
		IncludeDeclaration bool `json:"includeDeclaration"`
	} `json:"context"`
}

type CodeActionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
	Context      CodeActionContext      `json:"context"`
}

type CodeActionContext struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
	Only        []string     `json:"only,omitempty"`
	TriggerKind int          `json:"triggerKind,omitempty"`
}

// Code action trigger kinds.
const (
	CodeActionInvoked   = 1
	CodeActionAutomatic = 2
)

type CodeAction struct {
	Title       string       `json:"title"`
	Kind        string       `json:"kind,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
	IsPreferred bool         `json:"isPreferred,omitempty"`
	Disabled    *struct {
		Reason string `json:"reason"`
	} `json:"disabled,omitempty"`
	Edit    *WorkspaceEdit  `json:"edit,omitempty"`
	Command *Command        `json:"command,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// CommandOrCodeAction is one element of the code action result: a Command has a string command field, a
// CodeAction doesn't.
type CommandOrCodeAction struct {
	Command    *Command
	CodeAction *CodeAction
}

func (c *CommandOrCodeAction) UnmarshalJSON(b []byte) error {
	var probe struct {
		Command json.RawMessage `json:"command"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return err
	}
	if isString(probe.Command) {
		c.Command = new(Command)
		return json.Unmarshal(b, c.Command)
	}
	c.CodeAction = new(CodeAction)
	return json.Unmarshal(b, c.CodeAction)
}

type RenameParams struct {
	TextDocumentPositionParams
	NewName string `json:"newName"`
}

type PrepareRenameParams = TextDocumentPositionParams

// PrepareRenameResult is `Range | { range, placeholder } | { defaultBehavior } | null`.
type PrepareRenameResult struct {
	Range           *Range
	Placeholder     string
	DefaultBehavior bool
}

func (p *PrepareRenameResult) UnmarshalJSON(b []byte) error {
	*p = PrepareRenameResult{}
	if isNull(b) {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	if _, ok := m["start"]; ok {
		p.Range = new(Range)
		return json.Unmarshal(b, p.Range)
	}
	if r, ok := m["range"]; ok {
		p.Range = new(Range)
		if err := json.Unmarshal(r, p.Range); err != nil {
			return err
		}
		return json.Unmarshal(m["placeholder"], &p.Placeholder)
	}
	return json.Unmarshal(m["defaultBehavior"], &p.DefaultBehavior)
}

type DocumentSymbolParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           int              `json:"kind"`
	Tags           []int            `json:"tags,omitempty"`
	Deprecated     bool             `json:"deprecated,omitempty"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

// SymbolInformation is also WorkspaceSymbol, whose location can lack a range.
type SymbolInformation struct {
	Name     string `json:"name"`
	Kind     int    `json:"kind"`
	Tags     []int  `json:"tags,omitempty"`
	Location struct {
		URI   DocumentURI `json:"uri"`
		Range *Range      `json:"range,omitempty"`
	} `json:"location"`
	ContainerName string          `json:"containerName,omitempty"`
	Data          json.RawMessage `json:"data,omitempty"`
}

// DocumentSymbols is `DocumentSymbol[] | SymbolInformation[] | null`: one of the two is set.
type DocumentSymbols struct {
	Hierarchy []DocumentSymbol
	Flat      []SymbolInformation
}

func (d *DocumentSymbols) UnmarshalJSON(b []byte) error {
	*d = DocumentSymbols{}
	if isNull(b) {
		return nil
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(b, &raws); err != nil {
		return err
	}
	if len(raws) == 0 {
		return nil
	}
	var probe struct {
		Location json.RawMessage `json:"location"`
	}
	json.Unmarshal(raws[0], &probe)
	if probe.Location != nil {
		return json.Unmarshal(b, &d.Flat)
	}
	return json.Unmarshal(b, &d.Hierarchy)
}

type WorkspaceSymbolParams struct {
	Query string `json:"query"`
}

type CallHierarchyItem struct {
	Name           string          `json:"name"`
	Kind           int             `json:"kind"`
	Tags           []int           `json:"tags,omitempty"`
	Detail         string          `json:"detail,omitempty"`
	URI            DocumentURI     `json:"uri"`
	Range          Range           `json:"range"`
	SelectionRange Range           `json:"selectionRange"`
	Data           json.RawMessage `json:"data,omitempty"`
}

type CallHierarchyCallsParams struct {
	Item CallHierarchyItem `json:"item"`
}

type CallHierarchyIncomingCall struct {
	From       CallHierarchyItem `json:"from"`
	FromRanges []Range           `json:"fromRanges"`
}

type CallHierarchyOutgoingCall struct {
	To         CallHierarchyItem `json:"to"`
	FromRanges []Range           `json:"fromRanges"`
}

type FormattingOptions struct {
	TabSize                uint32 `json:"tabSize"`
	InsertSpaces           bool   `json:"insertSpaces"`
	TrimTrailingWhitespace bool   `json:"trimTrailingWhitespace,omitempty"`
	InsertFinalNewline     bool   `json:"insertFinalNewline,omitempty"`
	TrimFinalNewlines      bool   `json:"trimFinalNewlines,omitempty"`
}

type DocumentFormattingParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Options      FormattingOptions      `json:"options"`
}

type DocumentRangeFormattingParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
	Options      FormattingOptions      `json:"options"`
}

type SemanticTokensLegend struct {
	TokenTypes     []string `json:"tokenTypes"`
	TokenModifiers []string `json:"tokenModifiers"`
}

type SemanticTokensParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type SemanticTokens struct {
	ResultID string   `json:"resultId,omitempty"`
	Data     []uint32 `json:"data"`
}

type FoldingRangeParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type FoldingRange struct {
	StartLine      uint32  `json:"startLine"`
	StartCharacter *uint32 `json:"startCharacter,omitempty"`
	EndLine        uint32  `json:"endLine"`
	EndCharacter   *uint32 `json:"endCharacter,omitempty"`
	Kind           string  `json:"kind,omitempty"`
	CollapsedText  string  `json:"collapsedText,omitempty"`
}

type SelectionRangeParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Positions    []Position             `json:"positions"`
}

type SelectionRange struct {
	Range  Range           `json:"range"`
	Parent *SelectionRange `json:"parent,omitempty"`
}

type CodeLensParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type CodeLens struct {
	Range   Range           `json:"range"`
	Command *Command        `json:"command,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type InlayHintParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
}

type InlayHint struct {
	Position     Position        `json:"position"`
	Label        json.RawMessage `json:"label"` // string | InlayHintLabelPart[]
	Kind         int             `json:"kind,omitempty"`
	TextEdits    []TextEdit      `json:"textEdits,omitempty"`
	Tooltip      *StringOrMarkup `json:"tooltip,omitempty"`
	PaddingLeft  bool            `json:"paddingLeft,omitempty"`
	PaddingRight bool            `json:"paddingRight,omitempty"`
	Data         json.RawMessage `json:"data,omitempty"`
}

type InlayHintLabelPart struct {
	Value    string          `json:"value"`
	Tooltip  *StringOrMarkup `json:"tooltip,omitempty"`
	Location *Location       `json:"location,omitempty"`
	Command  *Command        `json:"command,omitempty"`
}

// InlayHintLabel reads a hint's label: the string, or its parts' values joined.
func InlayHintLabel(raw json.RawMessage) string {
	if isString(raw) {
		var s string
		json.Unmarshal(raw, &s)
		return s
	}
	var parts []InlayHintLabelPart
	json.Unmarshal(raw, &parts)
	var s string
	for _, p := range parts {
		s += p.Value
	}
	return s
}

type ExecuteCommandParams struct {
	Command   string            `json:"command"`
	Arguments []json.RawMessage `json:"arguments,omitempty"`
}
