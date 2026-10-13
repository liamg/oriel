package edit

// Severity is how the IDE shows a notice.
type Severity int

const (
	Info Severity = iota
	Warning
	Error
	// Question asks the user something; the engine takes the answer as the next key.
	Question
)

// Notice is a message from an engine for the IDE's own notification handling.
// ID is stable ("vim.unsaved") and Args fill its blanks, so tests and plugins don't depend on Text.
type Notice struct {
	ID       string
	Severity Severity
	Args     []string
	Text     string
}

// Error makes a notice usable as an error.
func (n Notice) Error() string { return n.Text }
