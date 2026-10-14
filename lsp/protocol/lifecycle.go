package protocol

import "encoding/json"

type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type InitializeParams struct {
	ProcessID             *int               `json:"processId"`
	ClientInfo            *ClientInfo        `json:"clientInfo,omitempty"`
	Locale                string             `json:"locale,omitempty"`
	RootPath              *string            `json:"rootPath,omitempty"`
	RootURI               *DocumentURI       `json:"rootUri"`
	InitializationOptions json.RawMessage    `json:"initializationOptions,omitempty"`
	Capabilities          ClientCapabilities `json:"capabilities"`
	Trace                 string             `json:"trace,omitempty"`
	WorkspaceFolders      []WorkspaceFolder  `json:"workspaceFolders"`
}

type ClientCapabilities struct {
	Workspace    *WorkspaceClientCapabilities    `json:"workspace,omitempty"`
	TextDocument *TextDocumentClientCapabilities `json:"textDocument,omitempty"`
	Window       *WindowClientCapabilities       `json:"window,omitempty"`
	General      *GeneralClientCapabilities      `json:"general,omitempty"`
}

type WorkspaceClientCapabilities struct {
	ApplyEdit              bool                       `json:"applyEdit"`
	WorkspaceEdit          *WorkspaceEditCapabilities `json:"workspaceEdit,omitempty"`
	DidChangeConfiguration *Dynamic                   `json:"didChangeConfiguration,omitempty"`
	Symbol                 *SymbolCapabilities        `json:"symbol,omitempty"`
	ExecuteCommand         *Dynamic                   `json:"executeCommand,omitempty"`
	WorkspaceFolders       bool                       `json:"workspaceFolders"`
	Configuration          bool                       `json:"configuration"`
	SemanticTokens         *Refresh                   `json:"semanticTokens,omitempty"`
	CodeLens               *Refresh                   `json:"codeLens,omitempty"`
	InlayHint              *Refresh                   `json:"inlayHint,omitempty"`
	Diagnostics            *Refresh                   `json:"diagnostics,omitempty"`
}

type Dynamic struct {
	DynamicRegistration bool `json:"dynamicRegistration"`
}

type Refresh struct {
	RefreshSupport bool `json:"refreshSupport"`
}

type ValueSet[T any] struct {
	ValueSet []T `json:"valueSet"`
}

type Properties struct {
	Properties []string `json:"properties"`
}

type WorkspaceEditCapabilities struct {
	DocumentChanges         bool     `json:"documentChanges"`
	ResourceOperations      []string `json:"resourceOperations,omitempty"`
	FailureHandling         string   `json:"failureHandling,omitempty"`
	NormalizesLineEndings   bool     `json:"normalizesLineEndings,omitempty"`
	ChangeAnnotationSupport *struct {
		GroupsOnLabel bool `json:"groupsOnLabel"`
	} `json:"changeAnnotationSupport,omitempty"`
}

type SymbolCapabilities struct {
	SymbolKind *ValueSet[int] `json:"symbolKind,omitempty"`
	TagSupport *ValueSet[int] `json:"tagSupport,omitempty"`
}

type TextDocumentClientCapabilities struct {
	Synchronization    *SyncCapabilities           `json:"synchronization,omitempty"`
	Completion         *CompletionCapabilities     `json:"completion,omitempty"`
	Hover              *HoverCapabilities          `json:"hover,omitempty"`
	SignatureHelp      *SignatureHelpCapabilities  `json:"signatureHelp,omitempty"`
	Declaration        *LinkCapabilities           `json:"declaration,omitempty"`
	Definition         *LinkCapabilities           `json:"definition,omitempty"`
	TypeDefinition     *LinkCapabilities           `json:"typeDefinition,omitempty"`
	Implementation     *LinkCapabilities           `json:"implementation,omitempty"`
	References         *Dynamic                    `json:"references,omitempty"`
	DocumentSymbol     *DocumentSymbolCapabilities `json:"documentSymbol,omitempty"`
	CodeAction         *CodeActionCapabilities     `json:"codeAction,omitempty"`
	CodeLens           *Dynamic                    `json:"codeLens,omitempty"`
	Formatting         *Dynamic                    `json:"formatting,omitempty"`
	RangeFormatting    *Dynamic                    `json:"rangeFormatting,omitempty"`
	Rename             *RenameCapabilities         `json:"rename,omitempty"`
	PublishDiagnostics *PublishDiagnosticsCaps     `json:"publishDiagnostics,omitempty"`
	FoldingRange       *FoldingRangeCapabilities   `json:"foldingRange,omitempty"`
	SelectionRange     *Dynamic                    `json:"selectionRange,omitempty"`
	CallHierarchy      *Dynamic                    `json:"callHierarchy,omitempty"`
	SemanticTokens     *SemanticTokensCapabilities `json:"semanticTokens,omitempty"`
	InlayHint          *InlayHintCapabilities      `json:"inlayHint,omitempty"`
	Diagnostic         *DiagnosticCapabilities     `json:"diagnostic,omitempty"`
}

type SyncCapabilities struct {
	DynamicRegistration bool `json:"dynamicRegistration"`
	WillSave            bool `json:"willSave"`
	WillSaveWaitUntil   bool `json:"willSaveWaitUntil"`
	DidSave             bool `json:"didSave"`
}

type CompletionCapabilities struct {
	CompletionItem     *CompletionItemCapabilities `json:"completionItem,omitempty"`
	CompletionItemKind *ValueSet[int]              `json:"completionItemKind,omitempty"`
	ContextSupport     bool                        `json:"contextSupport"`
	InsertTextMode     int                         `json:"insertTextMode,omitempty"`
	CompletionList     *CompletionListCapabilities `json:"completionList,omitempty"`
}

type CompletionListCapabilities struct {
	ItemDefaults []string `json:"itemDefaults"`
}

type CompletionItemCapabilities struct {
	SnippetSupport          bool           `json:"snippetSupport"`
	CommitCharactersSupport bool           `json:"commitCharactersSupport"`
	DocumentationFormat     []string       `json:"documentationFormat,omitempty"`
	DeprecatedSupport       bool           `json:"deprecatedSupport"`
	PreselectSupport        bool           `json:"preselectSupport"`
	TagSupport              *ValueSet[int] `json:"tagSupport,omitempty"`
	InsertReplaceSupport    bool           `json:"insertReplaceSupport"`
	ResolveSupport          *Properties    `json:"resolveSupport,omitempty"`
	LabelDetailsSupport     bool           `json:"labelDetailsSupport"`
}

type HoverCapabilities struct {
	ContentFormat []string `json:"contentFormat,omitempty"`
}

type SignatureHelpCapabilities struct {
	SignatureInformation *SignatureInformationCapabilities `json:"signatureInformation,omitempty"`
	ContextSupport       bool                              `json:"contextSupport"`
}

type SignatureInformationCapabilities struct {
	DocumentationFormat    []string                          `json:"documentationFormat,omitempty"`
	ParameterInformation   *ParameterInformationCapabilities `json:"parameterInformation,omitempty"`
	ActiveParameterSupport bool                              `json:"activeParameterSupport"`
}

type ParameterInformationCapabilities struct {
	LabelOffsetSupport bool `json:"labelOffsetSupport"`
}

type LinkCapabilities struct {
	LinkSupport bool `json:"linkSupport"`
}

type DocumentSymbolCapabilities struct {
	SymbolKind                        *ValueSet[int] `json:"symbolKind,omitempty"`
	HierarchicalDocumentSymbolSupport bool           `json:"hierarchicalDocumentSymbolSupport"`
	LabelSupport                      bool           `json:"labelSupport"`
}

type CodeActionCapabilities struct {
	CodeActionLiteralSupport *CodeActionLiteralSupport `json:"codeActionLiteralSupport,omitempty"`
	IsPreferredSupport       bool                      `json:"isPreferredSupport"`
	DisabledSupport          bool                      `json:"disabledSupport"`
	DataSupport              bool                      `json:"dataSupport"`
	ResolveSupport           *Properties               `json:"resolveSupport,omitempty"`
}

type CodeActionLiteralSupport struct {
	CodeActionKind ValueSet[string] `json:"codeActionKind"`
}

type RenameCapabilities struct {
	PrepareSupport bool `json:"prepareSupport"`
}

type PublishDiagnosticsCaps struct {
	RelatedInformation     bool           `json:"relatedInformation"`
	TagSupport             *ValueSet[int] `json:"tagSupport,omitempty"`
	VersionSupport         bool           `json:"versionSupport"`
	CodeDescriptionSupport bool           `json:"codeDescriptionSupport"`
	DataSupport            bool           `json:"dataSupport"`
}

type FoldingRangeCapabilities struct {
	LineFoldingOnly bool `json:"lineFoldingOnly"`
}

type SemanticTokensCapabilities struct {
	Requests       SemanticTokensRequests `json:"requests"`
	TokenTypes     []string               `json:"tokenTypes"`
	TokenModifiers []string               `json:"tokenModifiers"`
	Formats        []string               `json:"formats"`
}

type SemanticTokensRequests struct {
	Full bool `json:"full"`
}

type InlayHintCapabilities struct {
	ResolveSupport *Properties `json:"resolveSupport,omitempty"`
}

type DiagnosticCapabilities struct {
	RelatedDocumentSupport bool `json:"relatedDocumentSupport"`
}

type WindowClientCapabilities struct {
	WorkDoneProgress bool `json:"workDoneProgress"`
	ShowDocument     *struct {
		Support bool `json:"support"`
	} `json:"showDocument,omitempty"`
}

type GeneralClientCapabilities struct {
	PositionEncodings []string                    `json:"positionEncodings,omitempty"`
	Markdown          *MarkdownClientCapabilities `json:"markdown,omitempty"`
}

type MarkdownClientCapabilities struct {
	Parser  string `json:"parser"`
	Version string `json:"version,omitempty"`
}

type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
	ServerInfo   *struct {
		Name    string `json:"name"`
		Version string `json:"version,omitempty"`
	} `json:"serverInfo,omitempty"`
}

// ServerCapabilities keeps `boolean | Options` fields raw; Enabled reads them.
type ServerCapabilities struct {
	PositionEncoding   string          `json:"positionEncoding,omitempty"`
	TextDocumentSync   json.RawMessage `json:"textDocumentSync,omitempty"`
	CompletionProvider *struct {
		TriggerCharacters []string `json:"triggerCharacters,omitempty"`
		ResolveProvider   bool     `json:"resolveProvider,omitempty"`
	} `json:"completionProvider,omitempty"`
	HoverProvider         json.RawMessage `json:"hoverProvider,omitempty"`
	SignatureHelpProvider *struct {
		TriggerCharacters   []string `json:"triggerCharacters,omitempty"`
		RetriggerCharacters []string `json:"retriggerCharacters,omitempty"`
	} `json:"signatureHelpProvider,omitempty"`
	DeclarationProvider    json.RawMessage `json:"declarationProvider,omitempty"`
	DefinitionProvider     json.RawMessage `json:"definitionProvider,omitempty"`
	TypeDefinitionProvider json.RawMessage `json:"typeDefinitionProvider,omitempty"`
	ImplementationProvider json.RawMessage `json:"implementationProvider,omitempty"`
	ReferencesProvider     json.RawMessage `json:"referencesProvider,omitempty"`
	DocumentSymbolProvider json.RawMessage `json:"documentSymbolProvider,omitempty"`
	CodeActionProvider     json.RawMessage `json:"codeActionProvider,omitempty"`
	CodeLensProvider       *struct {
		ResolveProvider bool `json:"resolveProvider,omitempty"`
	} `json:"codeLensProvider,omitempty"`
	DocumentFormattingProvider json.RawMessage `json:"documentFormattingProvider,omitempty"`
	RangeFormattingProvider    json.RawMessage `json:"documentRangeFormattingProvider,omitempty"`
	RenameProvider             json.RawMessage `json:"renameProvider,omitempty"`
	FoldingRangeProvider       json.RawMessage `json:"foldingRangeProvider,omitempty"`
	SelectionRangeProvider     json.RawMessage `json:"selectionRangeProvider,omitempty"`
	CallHierarchyProvider      json.RawMessage `json:"callHierarchyProvider,omitempty"`
	SemanticTokensProvider     *struct {
		Legend SemanticTokensLegend `json:"legend"`
		Full   json.RawMessage      `json:"full,omitempty"`
	} `json:"semanticTokensProvider,omitempty"`
	WorkspaceSymbolProvider json.RawMessage `json:"workspaceSymbolProvider,omitempty"`
	InlayHintProvider       json.RawMessage `json:"inlayHintProvider,omitempty"`
	DiagnosticProvider      json.RawMessage `json:"diagnosticProvider,omitempty"`
	ExecuteCommandProvider  *struct {
		Commands []string `json:"commands"`
	} `json:"executeCommandProvider,omitempty"`
}

// Enabled reads a `boolean | Options` capability: true, or any options object.
func Enabled(raw json.RawMessage) bool {
	if isNull(raw) {
		return false
	}
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return b
	}
	return true
}

// Option reads a field of a `boolean | Options` capability's options: rename's prepareProvider, code
// action's resolveProvider.
func Option(raw json.RawMessage, field string) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	var b bool
	json.Unmarshal(m[field], &b)
	return b
}

// Text document sync kinds.
const (
	SyncNone        = 0
	SyncFull        = 1
	SyncIncremental = 2
)

// TextDocumentSync is textDocumentSync read: a bare kind, or TextDocumentSyncOptions.
type TextDocumentSync struct {
	OpenClose   bool
	Change      int
	Save        bool
	IncludeText bool
}

// DecodeSync reads textDocumentSync. A bare kind implies open and close notifications.
func DecodeSync(raw json.RawMessage) TextDocumentSync {
	if isNull(raw) {
		return TextDocumentSync{}
	}
	var kind int
	if json.Unmarshal(raw, &kind) == nil {
		return TextDocumentSync{OpenClose: kind != SyncNone, Change: kind, Save: kind != SyncNone}
	}
	var o struct {
		OpenClose bool            `json:"openClose"`
		Change    int             `json:"change"`
		Save      json.RawMessage `json:"save"`
	}
	json.Unmarshal(raw, &o)
	s := TextDocumentSync{OpenClose: o.OpenClose, Change: o.Change, Save: Enabled(o.Save)}
	s.IncludeText = Option(o.Save, "includeText")
	return s
}

type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

type DidChangeTextDocumentParams struct {
	TextDocument   VersionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

// TextDocumentContentChangeEvent with no Range replaces the whole document.
type TextDocumentContentChangeEvent struct {
	Range *Range `json:"range,omitempty"`
	Text  string `json:"text"`
}

type DidSaveTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Text         *string                `json:"text,omitempty"`
}

type DidCloseTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type DidChangeConfigurationParams struct {
	Settings json.RawMessage `json:"settings"`
}
