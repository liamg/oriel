package text

import "fmt"

// Pos is a line and a byte column, both 0-based.
type Pos struct {
	Line, Col int
}

func (p Pos) String() string { return fmt.Sprintf("%d:%d", p.Line, p.Col) }

// Range is a half-open span of byte offsets.
type Range struct {
	Start, End int
}

func (r Range) Len() int { return r.End - r.Start }

// Dir is the direction a walk over the text goes.
type Dir int

const (
	Forward Dir = iota
	Backward
)

// Bias says which side of an insertion at its offset an anchor sticks to.
type Bias int

const (
	Left  Bias = iota // stays before text inserted at its offset
	Right             // moves past text inserted at its offset
)

// AuthorKind is who an edit came from.
type AuthorKind uint8

const (
	File   AuthorKind = iota // the text as loaded, or of unknown origin
	User                     // typed or pasted by the person at the keyboard
	Agent                    // an agent session; ID is the session
	LSP                      // a language server's edit (rename, code action)
	Format                   // a formatter
	Plugin                   // a plugin; ID is the plugin
)

var kindNames = [...]string{"file", "user", "agent", "lsp", "format", "plugin"}

func (k AuthorKind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return fmt.Sprintf("AuthorKind(%d)", k)
}

// Author is who wrote some text: every edit carries one, and the rope keeps it per byte.
type Author struct {
	Kind AuthorKind
	ID   string
}

func (a Author) String() string {
	if a.ID == "" {
		return a.Kind.String()
	}
	return a.Kind.String() + ":" + a.ID
}

// AuthorSpan is a run of text written by one author.
type AuthorSpan struct {
	Range  Range
	Author Author
}
