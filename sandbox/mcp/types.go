// Package mcp is a minimal Model Context Protocol implementation: a stdio
// server, stdio and Streamable HTTP clients, and OAuth for remote servers.
package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// ProtocolVersion is the MCP revision we advertise.
const ProtocolVersion = "2025-06-18"

var knownVersions = []string{ProtocolVersion, "2025-03-26", "2024-11-05"}

// JSON-RPC error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// Tool describes one tool a server offers.
type Tool struct {
	Name        string           `json:"name"`
	Title       string           `json:"title,omitempty"`
	Description string           `json:"description,omitempty"`
	InputSchema json.RawMessage  `json:"inputSchema"`
	Annotations *ToolAnnotations `json:"annotations,omitempty"`
}

// ToolAnnotations are hints about a tool's behaviour.
type ToolAnnotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

// Content is one item of a tool result. Results keep content as raw JSON so
// unknown content types pass through; Content is for building and reading the
// common ones.
type Content struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// CallResult is the result of tools/call.
type CallResult struct {
	Content           []json.RawMessage `json:"content"`
	IsError           bool              `json:"isError,omitempty"`
	StructuredContent json.RawMessage   `json:"structuredContent,omitempty"`
}

// TextResult is a result holding a single text item.
func TextResult(text string, isErr bool) CallResult {
	b, _ := json.Marshal(Content{Type: "text", Text: text})
	return CallResult{Content: []json.RawMessage{b}, IsError: isErr}
}

// Text joins the result's text content.
func (r CallResult) Text() string {
	var buf bytes.Buffer
	for _, raw := range r.Content {
		var c Content
		if json.Unmarshal(raw, &c) == nil && c.Type == "text" {
			if buf.Len() > 0 {
				buf.WriteByte('\n')
			}
			buf.WriteString(c.Text)
		}
	}
	return buf.String()
}

// ServerInfo identifies a server, as reported on initialize.
type ServerInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

// Error is a JSON-RPC error.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("mcp: %s (code %d)", e.Message, e.Code)
}

// message is any JSON-RPC 2.0 message. A request has Method and ID, a
// notification Method only, a response ID and Result or Error.
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

func (m *message) isResponse() bool { return m.Method == "" && m.ID != nil }

// idKey normalises an id for map lookup.
func idKey(id json.RawMessage) string {
	var buf bytes.Buffer
	if json.Compact(&buf, id) != nil {
		return string(id)
	}
	return buf.String()
}

type initializeResult struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    json.RawMessage `json:"capabilities"`
	ServerInfo      ServerInfo      `json:"serverInfo"`
	Instructions    string          `json:"instructions,omitempty"`
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type listToolsResult struct {
	Tools      []Tool `json:"tools"`
	NextCursor string `json:"nextCursor,omitempty"`
}
