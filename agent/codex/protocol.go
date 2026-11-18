package codex

// The messages Oriel sends codex's app server, typed as the responses it reads back are. Its config overrides stay
// maps: they're dotted keys, open-ended by nature.

// decision is the answer to an approval request (item/commandExecution/requestApproval, item/fileChange/...).
type decision string

const (
	decline          decision = "decline"
	accept           decision = "accept"
	acceptForSession decision = "acceptForSession"
)

// approvalReply answers an approval request.
type approvalReply struct {
	Decision decision `json:"decision"`
}

// elicitationReply answers an MCP server's elicitation: "accept" with what was filled in, or "decline".
type elicitationReply struct {
	Action  string         `json:"action"`
	Content map[string]any `json:"content,omitempty"`
}

// inputItem is an item of a turn's input: text, or an image file.
type inputItem struct {
	Type string `json:"type"` // "text" or "localImage"
	Text string `json:"text,omitempty"`
	Path string `json:"path,omitempty"`
}

func textInput(t string) inputItem { return inputItem{Type: "text", Text: t} }

// turnParams are turn/start's and turn/steer's: steering names the turn it adds to.
type turnParams struct {
	ThreadID       string      `json:"threadId"`
	Input          []inputItem `json:"input"`
	ExpectedTurnID string      `json:"expectedTurnId,omitempty"`
	Effort         string      `json:"effort,omitempty"`
	Environments   *[]any      `json:"environments,omitempty"` // none, as an empty list, for a side question
}
