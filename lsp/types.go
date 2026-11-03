package lsp

import (
	"encoding/json"
	"fmt"

	"github.com/liamg/oriel/lsp/protocol"
	"github.com/liamg/oriel/lsp/snippet"
	"github.com/liamg/oriel/text"
)

// Positions here are Oriel's: 0-based lines and byte columns. The client converts to and from the encoding it
// negotiated with the server.

// Loc is a place in a file.
type Loc struct {
	Path string
	Pos  text.Pos
}

// Range is a span between two positions in one file, end exclusive.
type Range struct {
	Start, End text.Pos
}

func (r Range) String() string { return r.Start.String() + "-" + r.End.String() }

// Location is a range in a file. Target, from a server that answers with links, is the whole of what the
// range names (a function's body, where Range is its name).
type Location struct {
	Path   string
	Range  Range
	Target *Range
}

// Severity is a diagnostic's.
type Severity int

const (
	SevError Severity = iota + 1
	SevWarning
	SevInfo
	SevHint
)

// String is the severity as registry.Diagnostic spells it.
func (s Severity) String() string {
	switch s {
	case SevError:
		return "error"
	case SevWarning:
		return "warning"
	case SevInfo:
		return "info"
	case SevHint:
		return "hint"
	}
	return fmt.Sprintf("Severity(%d)", int(s))
}

// Diagnostic is a problem a server reports.
type Diagnostic struct {
	Range       Range
	Severity    Severity // 0 when the server gave none
	Source      string
	Code        string
	CodeURL     string
	Message     string
	Unnecessary bool // faded: unused code
	Deprecated  bool // struck through
	Related     []Related

	raw json.RawMessage // as the server sent it, for code actions
}

// Related is another place a diagnostic points to.
type Related struct {
	Location Location
	Message  string
}

// DiagnosticSet is all of a server's diagnostics for one file; an empty set clears the file.
type DiagnosticSet struct {
	Server      string
	Path        string
	Version     int // the document version they're for, or -1
	Diagnostics []Diagnostic
}

// Markup is documentation: markdown or plain text.
type Markup struct {
	Kind string // "markdown" or "plaintext"
	Text string
}

// CompletionKind is what a completion is.
type CompletionKind int

var completionKinds = [...]string{"", "text", "method", "function", "constructor", "field", "variable", "class",
	"interface", "module", "property", "unit", "value", "enum", "keyword", "snippet", "color", "file", "reference",
	"folder", "enumMember", "constant", "struct", "event", "operator", "typeParameter"}

func (k CompletionKind) String() string {
	if k >= 0 && int(k) < len(completionKinds) {
		return completionKinds[k]
	}
	return fmt.Sprintf("CompletionKind(%d)", int(k))
}

// Completion is one completion item.
type Completion struct {
	Label       string
	LabelDetail string // shown after the label: a signature
	LabelDesc   string // shown faded at the end: a package
	Kind        CompletionKind
	Detail      string
	Doc         Markup
	Deprecated  bool
	Preselect   bool
	SortText    string // the label when the server gave none
	FilterText  string // the label when the server gave none
	// Insert is what accepting it puts in: plain text, with the snippet's tab stops if it was one.
	Insert snippet.Snippet
	// Range is the text Insert replaces; nil when the server left it to the editor (the word before the cursor).
	Range           *Range
	ReplaceRange    *Range // the range to replace when accepting with replace rather than insert, if it differs
	AdditionalEdits []TextEdit
	CommitChars     []string
	Command         *Command

	raw  json.RawMessage
	at   Loc
	list *protocol.CompletionList
}

// CompletionList is a completion answer. Incomplete means typing more should ask again.
type CompletionList struct {
	Incomplete bool
	Items      []Completion
}

// Hover is what a hover shows, and the range it's about.
type Hover struct {
	Contents Markup
	Range    *Range
}

// SignatureHelp is the signatures of the call around the cursor.
type SignatureHelp struct {
	Signatures []Signature
	Active     int
}

type Signature struct {
	Label       string
	Doc         Markup
	Params      []Param
	ActiveParam int // -1 for none
}

// Param is a parameter: its label and where it is in the signature's label, in bytes (-1 if it isn't).
type Param struct {
	Label      string
	Start, End int
	Doc        Markup
}

// TextEdit replaces Range with NewText.
type TextEdit struct {
	Range   Range
	NewText string
}

// DocumentChange is one step of a workspace edit: edits to a file, or (Op set) a file created, renamed or
// deleted. Steps apply in order.
type DocumentChange struct {
	Path    string
	Version *int // the document version the edits are for; nil for any
	Edits   []TextEdit

	Op                string // "", "create", "rename" or "delete"
	NewPath           string // for a rename
	Overwrite         bool
	IgnoreIfExists    bool
	Recursive         bool
	IgnoreIfNotExists bool
}

// WorkspaceEdit is changes to many files.
type WorkspaceEdit struct {
	Changes []DocumentChange
}

// Command is a server command to run (ExecuteCommand).
type Command struct {
	Title string
	Name  string
	Args  []json.RawMessage
}

// Action is a code action.
type Action struct {
	Title       string
	Kind        string // "quickfix", "refactor.extract"…
	Preferred   bool
	Disabled    string // why it can't run now
	Diagnostics []Diagnostic
	Edit        *WorkspaceEdit
	Command     *Command

	raw json.RawMessage // nil for a bare command
}

// InlayHintKind is what an inlay hint shows.
type InlayHintKind int

const (
	HintType      InlayHintKind = 1
	HintParameter InlayHintKind = 2
)

type InlayHint struct {
	Pos          text.Pos
	Label        string
	Kind         InlayHintKind // 0 when the server gave none
	PaddingLeft  bool
	PaddingRight bool
	Tooltip      Markup
	Edits        []TextEdit // what accepting it inserts
}

// SymbolKind is what a symbol is.
type SymbolKind int

var symbolKinds = [...]string{"", "file", "module", "namespace", "package", "class", "method", "property", "field",
	"constructor", "enum", "interface", "function", "variable", "constant", "string", "number", "boolean", "array",
	"object", "key", "null", "enumMember", "struct", "event", "operator", "typeParameter"}

func (k SymbolKind) String() string {
	if k >= 0 && int(k) < len(symbolKinds) {
		return symbolKinds[k]
	}
	return fmt.Sprintf("SymbolKind(%d)", int(k))
}

// Symbol is a document or workspace symbol. A server that answers flat gives no Children but a Container;
// a workspace symbol may come without a Range.
type Symbol struct {
	Name       string
	Detail     string
	Kind       SymbolKind
	Deprecated bool
	Path       string
	Range      Range
	Selection  Range // the name
	Container  string
	Children   []Symbol
}

// CallItem is a function in a call hierarchy.
type CallItem struct {
	Name      string
	Kind      SymbolKind
	Detail    string
	Path      string
	Range     Range
	Selection Range

	raw json.RawMessage
}

// Call is one edge of a call hierarchy: the other function, and the call sites (in the caller's file).
type Call struct {
	Item  CallItem
	Sites []Location
}

// SemanticToken is a classified span.
type SemanticToken struct {
	Range     Range
	Type      string
	Modifiers []string
}

// FoldingRange is a foldable span of lines. StartCol and EndCol are byte columns, or -1 to fold whole lines.
type FoldingRange struct {
	StartLine, EndLine int
	StartCol, EndCol   int
	Kind               string // "comment", "imports", "region" or ""
	Collapsed          string // what to show when folded
}

// SelectionRange is a range to select and the larger ranges around it.
type SelectionRange struct {
	Range  Range
	Parent *SelectionRange
}

// CodeLens is a command shown over a line.
type CodeLens struct {
	Range   Range
	Command *Command // nil until resolved

	raw json.RawMessage
}

// FormatOptions are what formatting asks for.
type FormatOptions struct {
	TabSize                int
	InsertSpaces           bool
	TrimTrailingWhitespace bool
	InsertFinalNewline     bool
	TrimFinalNewlines      bool
}

// PrepareRename says whether a rename can start at a place, and what it would rename.
type PrepareRename struct {
	Range       *Range // nil: the editor's own idea (the word)
	Placeholder string
}
