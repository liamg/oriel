package dap

import "context"

// Lines here are 0-based, as everywhere in Oriel; the client converts to and from the protocol's 1-based ones.

// Breakpoint is a breakpoint as the user set it on a line of a file.
type Breakpoint struct {
	Line         int
	Condition    string // an expression: stop only when it's true
	HitCondition string // stop only when the hit count meets it: "3", ">= 3", "% 2", as the adapter reads it
	LogMessage   string // a logpoint: log this, with {expressions} interpolated, rather than stop
}

// BreakpointState is what the adapter made of a breakpoint.
type BreakpointState struct {
	ID       int
	Verified bool
	Line     int // where it actually is; -1 when the adapter didn't say
	Message  string
	Path     string
}

// Thread is a thread of the debuggee: a goroutine for delve.
type Thread struct {
	ID   int
	Name string
}

// Frame is one frame of a thread's stack.
type Frame struct {
	ID        int
	Name      string
	Path      string // "" when the frame has no source
	Line, Col int
	Hint      string // normal, label or subtle (a frame the user rarely cares about)
}

// Scope is a group of a frame's variables: Locals, Arguments, Globals.
type Scope struct {
	Name      string
	Ref       int
	Expensive bool
	Hint      string // arguments, locals, registers, returnValue
}

// Variable is a named value. Ref > 0 means it has children, fetched with Variables(Ref).
type Variable struct {
	Name, Value, Type string
	Ref               int
	Named, Indexed    int
	Evaluate          string // an expression that evaluates to it: what a watch of it would be
}

// StepKind is how far a step goes.
type StepKind int

const (
	StepOver StepKind = iota // next
	StepIn
	StepOut
)

func (k StepKind) command() string {
	switch k {
	case StepIn:
		return "stepIn"
	case StepOut:
		return "stepOut"
	}
	return "next"
}

func (k StepKind) String() string {
	return [...]string{"over", "in", "out"}[k]
}

// Evaluation contexts: where an expression comes from, which can change how the adapter answers.
const (
	ContextWatch = "watch"
	ContextREPL  = "repl"
	ContextHover = "hover"
)

// Event is something the adapter told the client: one of the types below.
type Event interface{ dapEvent() }

// Stopped: the debuggee stopped, at a breakpoint, after a step, on an exception or a pause.
type Stopped struct {
	Reason      string // step, breakpoint, exception, pause, entry, goto, function breakpoint…
	Description string
	Text        string
	Thread      int
	AllThreads  bool
	Breakpoints []int // the ids of the breakpoints hit
}

// Continued: the debuggee runs again, without the client having asked.
type Continued struct {
	Thread     int
	AllThreads bool
}

// Output is text the debuggee or the adapter printed.
type Output struct {
	Category string // console, important, stdout, stderr, telemetry
	Text     string
	Path     string
	Line     int // -1 when there's no place
}

// Exited: the debuggee exited.
type Exited struct{ Code int }

// Terminated: the session is over.
type Terminated struct{}

// ThreadChanged: a thread started or exited.
type ThreadChanged struct {
	Reason string // started, exited
	Thread int
}

// BreakpointChanged: the adapter changed a breakpoint by itself (verified it once code loaded, moved it).
type BreakpointChanged struct {
	Reason     string // changed, new, removed
	Breakpoint BreakpointState
}

// ProcessStarted: the debuggee's process started.
type ProcessStarted struct {
	Name string
	PID  int
}

func (Stopped) dapEvent()           {}
func (Continued) dapEvent()         {}
func (Output) dapEvent()            {}
func (Exited) dapEvent()            {}
func (Terminated) dapEvent()        {}
func (ThreadChanged) dapEvent()     {}
func (BreakpointChanged) dapEvent() {}
func (ProcessStarted) dapEvent()    {}

// Session is a running debug session: what the debugger UI and the agent's debugger tools drive.
type Session interface {
	Capabilities() Capabilities
	SetBreakpoints(ctx context.Context, path string, bps []Breakpoint) ([]BreakpointState, error)
	SetExceptionBreakpoints(ctx context.Context, filters []string) error
	Threads(ctx context.Context) ([]Thread, error)
	Stack(ctx context.Context, thread int) ([]Frame, error)
	Scopes(ctx context.Context, frame int) ([]Scope, error)
	Variables(ctx context.Context, ref int) ([]Variable, error)
	Evaluate(ctx context.Context, frame int, expr, context string) (Variable, error)
	Continue(ctx context.Context, thread int) error
	Step(ctx context.Context, thread int, kind StepKind) error
	Pause(ctx context.Context, thread int) error
	Stop(ctx context.Context) error
	Done() <-chan struct{}
}
