package dap

import (
	"bufio"
	"encoding/json"
	"io"

	"github.com/liamg/oriel/lsp/jsonrpc"
)

// Message is any DAP message as it arrives: a request, a response or an event.
type Message struct {
	Seq        int             `json:"seq"`
	Type       string          `json:"type"` // request, response, event
	Command    string          `json:"command,omitempty"`
	Arguments  json.RawMessage `json:"arguments,omitempty"`
	Event      string          `json:"event,omitempty"`
	Body       json.RawMessage `json:"body,omitempty"`
	RequestSeq int             `json:"request_seq,omitempty"`
	Success    bool            `json:"success,omitempty"`
	Message    string          `json:"message,omitempty"`
}

type request struct {
	Seq       int    `json:"seq"`
	Type      string `json:"type"`
	Command   string `json:"command"`
	Arguments any    `json:"arguments,omitempty"`
}

type response struct {
	Seq        int    `json:"seq"`
	Type       string `json:"type"`
	RequestSeq int    `json:"request_seq"`
	Success    bool   `json:"success"`
	Command    string `json:"command"`
	Message    string `json:"message,omitempty"`
	Body       any    `json:"body,omitempty"`
}

// The base protocol's framing is LSP's: a Content-Length header, a blank line, then the JSON.

// ReadFrame reads one message's content.
func ReadFrame(r *bufio.Reader) ([]byte, error) { return jsonrpc.ReadFrame(r) }

// WriteFrame writes one message's content with its header.
func WriteFrame(w io.Writer, body []byte) error { return jsonrpc.WriteFrame(w, body) }

// Capabilities are what an adapter said it supports in its initialize response.
type Capabilities struct {
	SupportsConfigurationDoneRequest  bool                        `json:"supportsConfigurationDoneRequest,omitempty"`
	SupportsFunctionBreakpoints       bool                        `json:"supportsFunctionBreakpoints,omitempty"`
	SupportsConditionalBreakpoints    bool                        `json:"supportsConditionalBreakpoints,omitempty"`
	SupportsHitConditionalBreakpoints bool                        `json:"supportsHitConditionalBreakpoints,omitempty"`
	SupportsEvaluateForHovers         bool                        `json:"supportsEvaluateForHovers,omitempty"`
	ExceptionBreakpointFilters        []ExceptionBreakpointFilter `json:"exceptionBreakpointFilters,omitempty"`
	SupportsSetVariable               bool                        `json:"supportsSetVariable,omitempty"`
	SupportsRestartRequest            bool                        `json:"supportsRestartRequest,omitempty"`
	SupportsTerminateRequest          bool                        `json:"supportsTerminateRequest,omitempty"`
	SupportsLogPoints                 bool                        `json:"supportsLogPoints,omitempty"`
	SupportsDelayedStackTraceLoading  bool                        `json:"supportsDelayedStackTraceLoading,omitempty"`
	SupportTerminateDebuggee          bool                        `json:"supportTerminateDebuggee,omitempty"`
	SupportsExceptionInfoRequest      bool                        `json:"supportsExceptionInfoRequest,omitempty"`
	SupportsSteppingGranularity       bool                        `json:"supportsSteppingGranularity,omitempty"`
}

// ExceptionBreakpointFilter is a kind of exception the adapter can stop on: "raised", "uncaught".
type ExceptionBreakpointFilter struct {
	Filter      string `json:"filter"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Default     bool   `json:"default,omitempty"`
}

// The protocol's own shapes, with its 1-based lines. The client's API converts them to Oriel's 0-based ones.

type wireSource struct {
	Name string `json:"name,omitempty"`
	Path string `json:"path,omitempty"`
}

type wireSourceBreakpoint struct {
	Line         int    `json:"line"`
	Condition    string `json:"condition,omitempty"`
	HitCondition string `json:"hitCondition,omitempty"`
	LogMessage   string `json:"logMessage,omitempty"`
}

type wireBreakpoint struct {
	ID       int         `json:"id,omitempty"`
	Verified bool        `json:"verified"`
	Message  string      `json:"message,omitempty"`
	Source   *wireSource `json:"source,omitempty"`
	Line     int         `json:"line,omitempty"`
	Reason   string      `json:"reason,omitempty"`
}

type wireStackFrame struct {
	ID               int         `json:"id"`
	Name             string      `json:"name"`
	Source           *wireSource `json:"source,omitempty"`
	Line             int         `json:"line"`
	Column           int         `json:"column"`
	PresentationHint string      `json:"presentationHint,omitempty"`
}

type wireVariable struct {
	Name               string `json:"name"`
	Value              string `json:"value"`
	Type               string `json:"type,omitempty"`
	VariablesReference int    `json:"variablesReference"`
	NamedVariables     int    `json:"namedVariables,omitempty"`
	IndexedVariables   int    `json:"indexedVariables,omitempty"`
	EvaluateName       string `json:"evaluateName,omitempty"`
}
