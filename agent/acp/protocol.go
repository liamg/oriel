package acp

// The messages Oriel sends an ACP agent, typed as the responses it reads back are.

// contentBlock is a block of a prompt: text, or an image with its bytes.
type contentBlock struct {
	Type     string `json:"type"` // "text" or "image"
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"` // an image's bytes, base64
	URI      string `json:"uri,omitempty"`
}

func text(t string) contentBlock { return contentBlock{Type: "text", Text: t} }

// promptParams are session/prompt's.
type promptParams struct {
	SessionID string         `json:"sessionId"`
	Prompt    []contentBlock `json:"prompt"`
}

// initializeParams are initialize's: what Oriel speaks and offers the agent.
type initializeParams struct {
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientCapabilities clientCapabilities `json:"clientCapabilities"`
	ClientInfo         clientInfo         `json:"clientInfo"`
}

type clientCapabilities struct {
	FS          fsCapabilities `json:"fs"`
	Terminal    bool           `json:"terminal"`
	Elicitation elicitation    `json:"elicitation"`
}

type fsCapabilities struct {
	ReadTextFile  bool `json:"readTextFile"`
	WriteTextFile bool `json:"writeTextFile"`
}

// elicitation offers form elicitation: its form is an empty object.
type elicitation struct {
	Form struct{} `json:"form"`
}

type clientInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Version string `json:"version"`
}
