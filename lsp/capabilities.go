package lsp

import "github.com/liamg/oriel/lsp/protocol"

func upTo(n int) *protocol.ValueSet[int] {
	v := &protocol.ValueSet[int]{}
	for i := 1; i <= n; i++ {
		v.ValueSet = append(v.ValueSet, i)
	}
	return v
}

var (
	markupFormats  = []string{protocol.Markdown, protocol.PlainText}
	tokenTypes     = []string{"namespace", "type", "class", "enum", "interface", "struct", "typeParameter", "parameter", "variable", "property", "enumMember", "event", "function", "method", "macro", "keyword", "modifier", "comment", "string", "number", "regexp", "operator", "decorator"}
	tokenModifiers = []string{"declaration", "definition", "readonly", "static", "deprecated", "abstract", "async", "modification", "documentation", "defaultLibrary"}
	actionKinds    = []string{"", "quickfix", "refactor", "refactor.extract", "refactor.inline", "refactor.rewrite", "source", "source.organizeImports", "source.fixAll"}
)

// clientCapabilities is what Oriel tells servers it can do. utf-8 comes first among the encodings, since
// it needs no conversion.
func clientCapabilities() protocol.ClientCapabilities {
	link := &protocol.LinkCapabilities{LinkSupport: true}
	dyn := &protocol.Dynamic{}
	refresh := &protocol.Refresh{RefreshSupport: true}
	sig := &protocol.SignatureHelpCapabilities{ContextSupport: true, SignatureInformation: &protocol.SignatureInformationCapabilities{
		DocumentationFormat: markupFormats, ActiveParameterSupport: true,
		ParameterInformation: &protocol.ParameterInformationCapabilities{LabelOffsetSupport: true}}}
	ca := &protocol.CodeActionCapabilities{IsPreferredSupport: true, DisabledSupport: true, DataSupport: true,
		ResolveSupport:           &protocol.Properties{Properties: []string{"edit"}},
		CodeActionLiteralSupport: &protocol.CodeActionLiteralSupport{CodeActionKind: protocol.ValueSet[string]{ValueSet: actionKinds}}}
	comp := &protocol.CompletionCapabilities{
		CompletionItem: &protocol.CompletionItemCapabilities{SnippetSupport: true, CommitCharactersSupport: true,
			DocumentationFormat: markupFormats, DeprecatedSupport: true, PreselectSupport: true, TagSupport: &protocol.ValueSet[int]{ValueSet: []int{1}},
			InsertReplaceSupport: true, LabelDetailsSupport: true,
			ResolveSupport: &protocol.Properties{Properties: []string{"documentation", "detail", "additionalTextEdits"}}},
		CompletionItemKind: upTo(25),
		ContextSupport:     true,
	}
	comp.CompletionList = &protocol.CompletionListCapabilities{ItemDefaults: []string{"commitCharacters", "editRange", "insertTextFormat", "insertTextMode", "data"}}
	gen := &protocol.GeneralClientCapabilities{PositionEncodings: []string{protocol.UTF8, protocol.UTF16},
		Markdown: &protocol.MarkdownClientCapabilities{Parser: "oriel"}}
	return protocol.ClientCapabilities{
		Workspace: &protocol.WorkspaceClientCapabilities{
			ApplyEdit:              true,
			WorkspaceEdit:          &protocol.WorkspaceEditCapabilities{DocumentChanges: true, ResourceOperations: []string{"create", "rename", "delete"}},
			DidChangeConfiguration: dyn,
			Symbol:                 &protocol.SymbolCapabilities{SymbolKind: upTo(26)},
			ExecuteCommand:         dyn,
			WorkspaceFolders:       true,
			Configuration:          true,
			SemanticTokens:         refresh,
			CodeLens:               refresh,
			InlayHint:              refresh,
			Diagnostics:            refresh,
		},
		TextDocument: &protocol.TextDocumentClientCapabilities{
			Synchronization:    &protocol.SyncCapabilities{DidSave: true},
			Completion:         comp,
			Hover:              &protocol.HoverCapabilities{ContentFormat: markupFormats},
			SignatureHelp:      sig,
			Declaration:        link,
			Definition:         link,
			TypeDefinition:     link,
			Implementation:     link,
			References:         dyn,
			DocumentSymbol:     &protocol.DocumentSymbolCapabilities{SymbolKind: upTo(26), HierarchicalDocumentSymbolSupport: true},
			CodeAction:         ca,
			CodeLens:           dyn,
			Formatting:         dyn,
			RangeFormatting:    dyn,
			Rename:             &protocol.RenameCapabilities{PrepareSupport: true},
			PublishDiagnostics: &protocol.PublishDiagnosticsCaps{RelatedInformation: true, TagSupport: &protocol.ValueSet[int]{ValueSet: []int{1, 2}}, VersionSupport: true, CodeDescriptionSupport: true, DataSupport: true},
			FoldingRange:       &protocol.FoldingRangeCapabilities{},
			SelectionRange:     dyn,
			CallHierarchy:      dyn,
			SemanticTokens:     &protocol.SemanticTokensCapabilities{Requests: protocol.SemanticTokensRequests{Full: true}, TokenTypes: tokenTypes, TokenModifiers: tokenModifiers, Formats: []string{"relative"}},
			InlayHint:          &protocol.InlayHintCapabilities{},
			Diagnostic:         &protocol.DiagnosticCapabilities{},
		},
		Window:  &protocol.WindowClientCapabilities{WorkDoneProgress: true},
		General: gen,
	}
}
