package testrun

import (
	"fmt"
	"time"
)

// EventKind is what happened.
type EventKind int

const (
	Started     EventKind = iota // a test, or a package (Test empty), began
	Output                       // a raw line of output, for a test or a package
	Passed                       // a test or a package passed
	Failed                       // a test or a package failed; Failure says how
	Skipped                      // a test or a package was skipped
	BuildFailed                  // a package didn't build; Failure.Message has the compiler's output
)

var eventNames = [...]string{"started", "output", "passed", "failed", "skipped", "build-failed"}

func (k EventKind) String() string {
	if int(k) < len(eventNames) {
		return eventNames[k]
	}
	return fmt.Sprintf("EventKind(%d)", k)
}

// MarshalText writes the kind's name, for history and goldens.
func (k EventKind) MarshalText() ([]byte, error) { return []byte(k.String()), nil }

// UnmarshalText reads a kind's name.
func (k *EventKind) UnmarshalText(b []byte) error {
	for i, n := range eventNames {
		if n == string(b) {
			*k = EventKind(i)
			return nil
		}
	}
	return fmt.Errorf("testrun: unknown event kind %q", b)
}

// Event is one thing a runner reported. Test is empty for a package's own events.
type Event struct {
	Kind    EventKind     `json:"kind"`
	Package string        `json:"package,omitempty"`
	Test    string        `json:"test,omitempty"`
	Time    time.Time     `json:"time,omitzero"`
	Elapsed time.Duration `json:"elapsed,omitempty"` // for Passed, Failed and Skipped, when the runner says
	Line    string        `json:"line,omitempty"`    // for Output: the raw line, without its newline
	Failure *Failure      `json:"failure,omitempty"` // for Failed and BuildFailed
	Message string        `json:"message,omitempty"` // for Skipped: the reason, when given
}

// Key is the event's test's key.
func (e Event) Key() string { return Key(e.Package, e.Test) }

// Reason is why a test failed.
type Reason string

const (
	ReasonAssert  Reason = "assert"  // an assertion or a reported error
	ReasonPanic   Reason = "panic"   // a panic, an uncaught exception
	ReasonTimeout Reason = "timeout" // it ran out of time
	ReasonBuild   Reason = "build"   // its package didn't build, or its file didn't import
	ReasonError   Reason = "error"   // its setup or teardown failed
)

// Failure is what's known about a failure: the first assertion that failed, best effort.
type Failure struct {
	Reason  Reason `json:"reason"`
	Message string `json:"message,omitempty"`
	Want    string `json:"want,omitempty"`
	Got     string `json:"got,omitempty"`
	Loc     *Loc   `json:"loc,omitempty"` // where it failed, when the output says
}

// Loc is a place in a file. File is as the runner printed it: a relative one is relative to the run's directory,
// or for Go to the test's package directory (Exec resolves it when it can).
type Loc struct {
	File string `json:"file"`
	Line int    `json:"line"` // 0-based
}

func (l Loc) String() string { return fmt.Sprintf("%s:%d", l.File, l.Line+1) }
