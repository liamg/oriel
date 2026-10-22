package kit

// The editor's events, which hooks (and Lua's oriel.on) can be on.
type (
	// BufRead is a file read into a buffer. A rewrite that sets ReadOnly, saying why, opens it read-only.
	BufRead struct{ Buf, File, ReadOnly string }
	// BufWritePre is a buffer about to be written: a hook can veto it.
	BufWritePre struct{ Buf, File string }
	// BufWritePost is a buffer just written.
	BufWritePost struct{ Buf, File string }
)

// AgentTurnEnd is the AgentTurnEnd hook's event.
type AgentTurnEnd struct {
	Session string `json:"session"`
}

// CommentSend is the CommentSend hook's event: a comment about to go to an agent. A rewrite changes it, a veto
// stops it.
type CommentSend struct {
	Session string `json:"session"`
	Thread  string `json:"thread,omitempty"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	Body    string `json:"body"`
	Urgent  bool   `json:"urgent,omitempty"`
	Btw     bool   `json:"btw,omitempty"`
}
