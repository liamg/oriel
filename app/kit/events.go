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

// DirChanged is the DirChanged hook's event: the window has moved onto another directory, as a session in another one
// comes to the front. Plugins live on across it; one that keeps something per directory swaps it here, before the
// new directory's layout comes back.
type DirChanged struct {
	Dir     string `json:"dir"`               // the directory the window is on now; "" for the start view
	Was     string `json:"was,omitempty"`     // the one it left
	Session string `json:"session,omitempty"` // the session it moved for; "" for none
}

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
