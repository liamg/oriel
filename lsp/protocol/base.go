// Package protocol is the LSP 3.17 messages Oriel uses, as JSON-tagged Go types. Positions in it are the
// protocol's: lines and characters in the negotiated encoding. Unions are decoded by types with their own
// UnmarshalJSON, or kept as json.RawMessage for the client to tell apart.
package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type DocumentURI string

// Position encodings.
const (
	UTF8  = "utf-8"
	UTF16 = "utf-16"
	UTF32 = "utf-32"
)

type Position struct {
	Line      uint32 `json:"line"`
	Character uint32 `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Location struct {
	URI   DocumentURI `json:"uri"`
	Range Range       `json:"range"`
}

type LocationLink struct {
	OriginSelectionRange *Range      `json:"originSelectionRange,omitempty"`
	TargetURI            DocumentURI `json:"targetUri"`
	TargetRange          Range       `json:"targetRange"`
	TargetSelectionRange Range       `json:"targetSelectionRange"`
}

type TextDocumentIdentifier struct {
	URI DocumentURI `json:"uri"`
}

type VersionedTextDocumentIdentifier struct {
	URI     DocumentURI `json:"uri"`
	Version int32       `json:"version"`
}

// OptionalVersionedTextDocumentIdentifier has a null version for a file on disk.
type OptionalVersionedTextDocumentIdentifier struct {
	URI     DocumentURI `json:"uri"`
	Version *int32      `json:"version"`
}

type TextDocumentItem struct {
	URI        DocumentURI `json:"uri"`
	LanguageID string      `json:"languageId"`
	Version    int32       `json:"version"`
	Text       string      `json:"text"`
}

type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

type TextEdit struct {
	Range        Range  `json:"range"`
	NewText      string `json:"newText"`
	AnnotationID string `json:"annotationId,omitempty"`
}

type Command struct {
	Title     string            `json:"title"`
	Command   string            `json:"command"`
	Arguments []json.RawMessage `json:"arguments,omitempty"`
}

// Markup kinds.
const (
	PlainText = "plaintext"
	Markdown  = "markdown"
)

type MarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// StringOrMarkup is `string | MarkupContent`, as documentation is; a plain string is plaintext.
type StringOrMarkup MarkupContent

func (m *StringOrMarkup) UnmarshalJSON(b []byte) error {
	if isString(b) {
		m.Kind = PlainText
		return json.Unmarshal(b, &m.Value)
	}
	return json.Unmarshal(b, (*MarkupContent)(m))
}

func (m StringOrMarkup) MarshalJSON() ([]byte, error) { return json.Marshal(MarkupContent(m)) }

// IntOrString is `integer | string`, as diagnostic codes and progress tokens are. It keeps the JSON as sent.
type IntOrString struct{ Raw json.RawMessage }

func (v *IntOrString) UnmarshalJSON(b []byte) error {
	v.Raw = append(json.RawMessage(nil), b...)
	return nil
}

func (v IntOrString) MarshalJSON() ([]byte, error) {
	if len(v.Raw) == 0 {
		return []byte("null"), nil
	}
	return v.Raw, nil
}

func (v IntOrString) String() string {
	var s string
	if json.Unmarshal(v.Raw, &s) == nil {
		return s
	}
	return string(bytes.TrimSpace(v.Raw))
}

func isString(b []byte) bool {
	b = bytes.TrimSpace(b)
	return len(b) > 0 && b[0] == '"'
}

func isArray(b []byte) bool {
	b = bytes.TrimSpace(b)
	return len(b) > 0 && b[0] == '['
}

func isNull(b []byte) bool {
	b = bytes.TrimSpace(b)
	return len(b) == 0 || string(b) == "null"
}

// Locations decodes `Location | Location[] | LocationLink[] | null`, as definition and friends answer.
// A LocationLink becomes a Location at its target selection range, with its whole target range kept.
type Locations []LocationOrLink

type LocationOrLink struct {
	Location
	TargetRange *Range // a LocationLink's whole target
}

func (ls *Locations) UnmarshalJSON(b []byte) error {
	*ls = nil
	if isNull(b) {
		return nil
	}
	if !isArray(b) {
		var l Location
		if err := json.Unmarshal(b, &l); err != nil {
			return err
		}
		*ls = Locations{{Location: l}}
		return nil
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(b, &raws); err != nil {
		return err
	}
	for _, r := range raws {
		var probe struct {
			TargetURI *DocumentURI `json:"targetUri"`
		}
		if err := json.Unmarshal(r, &probe); err != nil {
			return err
		}
		if probe.TargetURI != nil {
			var l LocationLink
			if err := json.Unmarshal(r, &l); err != nil {
				return err
			}
			tr := l.TargetRange
			*ls = append(*ls, LocationOrLink{Location: Location{URI: l.TargetURI, Range: l.TargetSelectionRange}, TargetRange: &tr})
			continue
		}
		var l Location
		if err := json.Unmarshal(r, &l); err != nil {
			return err
		}
		*ls = append(*ls, LocationOrLink{Location: l})
	}
	return nil
}

// ProgressParams is $/progress's.
type ProgressParams struct {
	Token IntOrString     `json:"token"`
	Value json.RawMessage `json:"value"`
}

// WorkDoneProgress is a begin, report or end value.
type WorkDoneProgress struct {
	Kind        string  `json:"kind"` // "begin", "report", "end"
	Title       string  `json:"title,omitempty"`
	Cancellable bool    `json:"cancellable,omitempty"`
	Message     string  `json:"message,omitempty"`
	Percentage  *uint32 `json:"percentage,omitempty"`
}

type WorkDoneProgressCreateParams struct {
	Token IntOrString `json:"token"`
}

type LogMessageParams struct {
	Type    int    `json:"type"`
	Message string `json:"message"`
}

// Message types, for window/showMessage and window/logMessage.
const (
	MessageError   = 1
	MessageWarning = 2
	MessageInfo    = 3
	MessageLog     = 4
	MessageDebug   = 5
)

type ConfigurationParams struct {
	Items []ConfigurationItem `json:"items"`
}

type ConfigurationItem struct {
	ScopeURI DocumentURI `json:"scopeUri,omitempty"`
	Section  string      `json:"section,omitempty"`
}

type ApplyWorkspaceEditParams struct {
	Label string        `json:"label,omitempty"`
	Edit  WorkspaceEdit `json:"edit"`
}

type ApplyWorkspaceEditResult struct {
	Applied       bool   `json:"applied"`
	FailureReason string `json:"failureReason,omitempty"`
}

type ShowDocumentResult struct {
	Success bool `json:"success"`
}

type WorkspaceFolder struct {
	URI  DocumentURI `json:"uri"`
	Name string      `json:"name"`
}

// WorkspaceEdit's DocumentChanges are kept raw: each is a TextDocumentEdit or a create, rename or delete
// (DecodeDocumentChange).
type WorkspaceEdit struct {
	Changes           map[DocumentURI][]TextEdit `json:"changes,omitempty"`
	DocumentChanges   []json.RawMessage          `json:"documentChanges,omitempty"`
	ChangeAnnotations json.RawMessage            `json:"changeAnnotations,omitempty"`
}

type TextDocumentEdit struct {
	TextDocument OptionalVersionedTextDocumentIdentifier `json:"textDocument"`
	Edits        []TextEdit                              `json:"edits"`
}

// FileOperation is a CreateFile, RenameFile or DeleteFile.
type FileOperation struct {
	Kind    string      `json:"kind"`
	URI     DocumentURI `json:"uri,omitempty"`
	OldURI  DocumentURI `json:"oldUri,omitempty"`
	NewURI  DocumentURI `json:"newUri,omitempty"`
	Options *struct {
		Overwrite         bool `json:"overwrite,omitempty"`
		IgnoreIfExists    bool `json:"ignoreIfExists,omitempty"`
		Recursive         bool `json:"recursive,omitempty"`
		IgnoreIfNotExists bool `json:"ignoreIfNotExists,omitempty"`
	} `json:"options,omitempty"`
}

// DecodeDocumentChange tells a TextDocumentEdit from a file operation by its kind field.
func DecodeDocumentChange(raw json.RawMessage) (*TextDocumentEdit, *FileOperation, error) {
	var probe struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, nil, err
	}
	switch probe.Kind {
	case "":
		var e TextDocumentEdit
		err := json.Unmarshal(raw, &e)
		return &e, nil, err
	case "create", "rename", "delete":
		var op FileOperation
		err := json.Unmarshal(raw, &op)
		return nil, &op, err
	}
	return nil, nil, fmt.Errorf("protocol: unknown document change kind %q", probe.Kind)
}
