// Package assistant is the conversation behind Oriel's assistant (ui.md 11, crit-reference.md "Assistant"): one
// conversation at a time with a utility agent, fed typed or spoken turns, whose replies, progress, confirmations and
// actions are kept as the log the drawer draws. The agent itself is behind Agent: a utility session on a worker.
package assistant

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Agent is the conversation's agent: it takes turns and says what it does.
type Agent interface {
	Send(ctx context.Context, text string) error
	Interrupt(ctx context.Context) error
	Signals() <-chan Signal // closed when the agent is gone
	Close() error
}

// Starter starts the agent for a new conversation, with its first turn.
type Starter func(ctx context.Context, first string) (Agent, error)

// Signal is something the agent did.
type Signal struct {
	Kind SignalKind
	Text string // a message's, or a failure's
	Tool string // a tool call's tool
}

type SignalKind string

const (
	TurnStarted SignalKind = "turn-start"
	Said        SignalKind = "message"
	UsedTool    SignalKind = "tool"
	TurnEnded   SignalKind = "turn-end"
	Failed      SignalKind = "failure"
	Exited      SignalKind = "exited"
)

// Kind is what a log entry is.
type Kind string

const (
	KindUser     Kind = "user"
	KindReply    Kind = "reply"
	KindTool     Kind = "tool"     // a tool the agent used this turn
	KindProgress Kind = "progress" // what it's doing, while it's the latest word
	KindConfirm  Kind = "confirm"  // an act waiting for the user's yes
	KindAction   Kind = "action"   // something its tools did
	KindError    Kind = "error"
	KindDivider  Kind = "divider" // a new conversation starts
)

// Answer is how a confirmation was answered.
type Answer string

const (
	Yes      Answer = "yes"
	No       Answer = "no"
	NoAnswer Answer = "timeout"
	Dropped  Answer = "dropped" // the conversation or the call ended first
)

// Entry is one line of the log.
type Entry struct {
	Seq   int
	At    time.Time
	Kind  Kind
	Text  string
	Voice bool // the turn it's part of was spoken
	// A reply's: it asked the user something, and the rows it offers (number keys pick one).
	ExpectsReply bool
	Items        []Item
	// A tool's, a confirmation's and an action's.
	Tool string
	ID   string
	Card any // what a confirmation shows, as its asker gave it
	// Answer is a confirmation's answer, "" while it waits.
	Answer Answer
	// Until is when a confirmation stops waiting, or an action can no longer be undone.
	Until time.Time
	// An action's: the line under it, the command that opens what it made, and whether it was undone.
	Detail   string
	Open     string
	OpenArgs map[string]any
	Undone   bool
	UndoErr  string
}

// Item is a row a reply offers: a session to open, a link, or something to ask about.
type Item struct {
	Title   string `json:"title"`
	Text    string `json:"text,omitempty"`
	Meta    string `json:"meta,omitempty"`    // "22m · devbox"
	Session string `json:"session,omitempty"` // a session's id: picking it opens it
	URL     string `json:"url,omitempty"`     // picking it opens the link
	Status  string `json:"status,omitempty"`  // a session's status, for its dot
}

const keepEntries = 200

// Conversation is the assistant's conversation and its log.
type Conversation struct {
	// Start starts the agent for a conversation.
	Start Starter
	// Idle ends a conversation nobody has spoken in for this long.
	Idle time.Duration
	// ConfirmWait is how long a confirmation waits for an answer; UndoWait how long an action can be undone.
	ConfirmWait, UndoWait time.Duration
	// Progress is the line shown while a tool runs; "" shows none.
	Progress func(tool string) string
	// Now is the clock: it stamps turns, so the agent can resolve "ten minutes ago".
	Now func() time.Time
	// Changed is told whenever the log changes, from any goroutine.
	Changed func()

	mu      sync.Mutex
	agent   Agent
	gen     int // which agent the conversation is on; a signal from an older one is dropped
	entries []Entry
	seq     int
	busy    bool
	queue   []turn
	cur     turn
	replied bool
	said    string // the agent's last words this turn
	last    time.Time
	asks    map[string]chan bool
	undo    map[string]func(context.Context) error
	stop    chan struct{}
}

type turn struct {
	text  string
	voice bool
}

// New is a conversation that starts its agent with start, with its idle reaper running.
func New(start Starter) *Conversation {
	c := &Conversation{Start: start, Idle: 15 * time.Minute, ConfirmWait: 2 * time.Minute, UndoWait: 10 * time.Second,
		Now: time.Now, asks: map[string]chan bool{}, undo: map[string]func(context.Context) error{}, stop: make(chan struct{})}
	go c.reap()
	return c
}

// Log is the conversation so far, and whether a turn is running.
func (c *Conversation) Log() ([]Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Entry(nil), c.entries...), c.busy
}

// Busy says whether a turn is running or waiting.
func (c *Conversation) Busy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.busy || len(c.queue) > 0
}

// Voice says whether the turn running was spoken.
func (c *Conversation) Voice() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cur.voice
}

func (c *Conversation) emitLocked(e Entry) Entry {
	c.seq++
	e.Seq = c.seq
	if e.At.IsZero() {
		e.At = c.Now()
	}
	c.entries = append(c.entries, e)
	if len(c.entries) > keepEntries {
		c.entries = c.entries[len(c.entries)-keepEntries:]
	}
	c.changedLocked()
	return e
}

func (c *Conversation) changedLocked() {
	if c.Changed != nil {
		go c.Changed()
	}
}

func (c *Conversation) setLocked(seq int, fn func(*Entry)) {
	for i := range c.entries {
		if c.entries[i].Seq == seq {
			fn(&c.entries[i])
			c.changedLocked()
			return
		}
	}
}

// Say queues a turn: typed, or spoken.
func (c *Conversation) Say(text string, voice bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.emitLocked(Entry{Kind: KindUser, Text: text, Voice: voice})
	c.queue = append(c.queue, turn{text: text, voice: voice})
	c.nextLocked()
}

// Reply records the turn's answer, from the agent's reply tool: its text, whether it asked the user something, and
// the rows it offers. Only the first reply of a turn counts.
func (c *Conversation) Reply(text string, expects bool, items []Item) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.replied || !c.busy {
		return
	}
	c.replied = true
	c.emitLocked(Entry{Kind: KindReply, Text: strings.TrimSpace(text), ExpectsReply: expects, Items: items, Voice: c.cur.voice})
}

func newID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Confirm asks the user to confirm an act, shown as card, and waits for their answer, ConfirmWait, or ctx. line is
// the act in a few words.
func (c *Conversation) Confirm(ctx context.Context, tool, line string, card any) Answer {
	id := newID()
	ch := make(chan bool, 1)
	c.mu.Lock()
	c.asks[id] = ch
	e := c.emitLocked(Entry{Kind: KindConfirm, ID: id, Tool: tool, Text: line, Card: card, Voice: c.cur.voice, Until: c.Now().Add(c.ConfirmWait)})
	c.mu.Unlock()

	t := time.NewTimer(c.ConfirmWait)
	defer t.Stop()
	ans := Dropped
	select {
	case yes, ok := <-ch:
		if ok {
			ans = map[bool]Answer{true: Yes, false: No}[yes]
		}
	case <-t.C:
		ans = NoAnswer
	case <-ctx.Done():
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, waiting := c.asks[id]; waiting {
		delete(c.asks, id)
	} else if ans == NoAnswer || ans == Dropped {
		// Answered as it timed out: the answer stands.
		if yes, ok := <-ch; ok {
			ans = map[bool]Answer{true: Yes, false: No}[yes]
		}
	}
	c.setLocked(e.Seq, func(e *Entry) { e.Answer = ans })
	return ans
}

// Answered gives the user's answer to a confirmation; false when it's no longer waiting.
func (c *Conversation) Answered(id string, yes bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch, ok := c.asks[id]
	if !ok {
		return false
	}
	delete(c.asks, id)
	ch <- yes
	return true
}

// Waiting are the confirmations still waiting for an answer, oldest first.
func (c *Conversation) Waiting() []Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Entry
	for _, e := range c.entries {
		if e.Kind == KindConfirm && e.Answer == "" {
			if _, ok := c.asks[e.ID]; ok {
				out = append(out, e)
			}
		}
	}
	return out
}

// Act is something the agent's tools did.
type Act struct {
	Tool, Line, Detail string
	Open               string // the command that opens what it made
	OpenArgs           map[string]any
	Undo               func(ctx context.Context) error // nil when it can't be undone
}

// Acted records what a tool did; one that can be undone can be for UndoWait.
func (c *Conversation) Acted(a Act) Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := Entry{Kind: KindAction, ID: newID(), Tool: a.Tool, Text: a.Line, Detail: a.Detail, Open: a.Open, OpenArgs: a.OpenArgs, Voice: c.cur.voice}
	if a.Undo != nil {
		e.Until = c.Now().Add(c.UndoWait)
		c.undo[e.ID] = a.Undo
	}
	return c.emitLocked(e)
}

// Undo takes back an action while it can be.
func (c *Conversation) Undo(ctx context.Context, id string) error {
	c.mu.Lock()
	fn, ok := c.undo[id]
	var until time.Time
	for _, e := range c.entries {
		if e.ID == id && e.Kind == KindAction {
			until = e.Until
		}
	}
	if ok {
		delete(c.undo, id)
	}
	c.mu.Unlock()
	if !ok {
		return errors.New("that can't be undone")
	}
	if c.Now().After(until) {
		return errors.New("it's too late to undo that")
	}
	err := fn(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.entries {
		if e.ID == id && e.Kind == KindAction {
			c.setLocked(e.Seq, func(e *Entry) {
				e.Undone = err == nil
				if err != nil {
					e.UndoErr = err.Error()
				}
			})
		}
	}
	return err
}

// Interrupt stops the turn running.
func (c *Conversation) Interrupt(ctx context.Context) error {
	c.mu.Lock()
	a, busy := c.agent, c.busy
	c.queue = nil
	c.mu.Unlock()
	if a == nil || !busy {
		return nil
	}
	return a.Interrupt(ctx)
}

// Clear ends the conversation; the next turn starts a fresh one.
func (c *Conversation) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queue = nil
	c.endLocked()
	c.emitLocked(Entry{Kind: KindDivider, Text: "new conversation"})
}

// Close ends the conversation for good.
func (c *Conversation) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.stop:
	default:
		close(c.stop)
	}
	c.endLocked()
}

func (c *Conversation) endLocked() {
	for id, ch := range c.asks {
		delete(c.asks, id)
		close(ch)
	}
	c.gen++
	if c.agent != nil {
		a := c.agent
		c.agent = nil
		go a.Close()
	}
	c.busy = false
	c.changedLocked()
}

// stamp is a turn as the agent reads it: when it was said, and how.
func (c *Conversation) stamp(t turn) string {
	how := "typed"
	if t.voice {
		how = "spoken"
	}
	return fmt.Sprintf("[%s, %s] %s", c.Now().Format("Mon 2 Jan 15:04"), how, t.text)
}

// nextLocked starts the next queued turn when the agent is free, starting the agent first if there's none.
func (c *Conversation) nextLocked() {
	if c.busy || len(c.queue) == 0 {
		return
	}
	select {
	case <-c.stop:
		return
	default:
	}
	t := c.queue[0]
	c.queue = c.queue[1:]
	c.cur, c.replied, c.said, c.last = t, false, "", c.Now()
	c.busy = true
	text := c.stamp(t)
	if c.agent == nil {
		c.gen++
		go c.launch(c.gen, text)
		return
	}
	a := c.agent
	go func() {
		if err := a.Send(context.Background(), text); err != nil {
			c.failed(a, "the assistant stopped: "+err.Error())
		}
	}()
}

// launch starts the agent with a conversation's first turn.
func (c *Conversation) launch(gen int, first string) {
	if c.Start == nil {
		c.startFailed(gen, errors.New("the assistant isn't set up"))
		return
	}
	a, err := c.Start(context.Background(), first)
	if err != nil {
		c.startFailed(gen, err)
		return
	}
	c.mu.Lock()
	if gen != c.gen {
		c.mu.Unlock()
		a.Close()
		return
	}
	c.agent = a
	c.mu.Unlock()
	go c.read(gen, a)
}

func (c *Conversation) startFailed(gen int, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen != c.gen {
		return
	}
	c.emitLocked(Entry{Kind: KindError, Text: "The assistant couldn't start: " + err.Error()})
	c.queue = nil
	c.busy = false
}

// failed is the agent gone: the turn running ends with why, and the next starts a fresh agent.
func (c *Conversation) failed(a Agent, why string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.agent != a {
		return
	}
	c.agent = nil
	c.gen++
	go a.Close()
	if c.busy {
		c.emitLocked(Entry{Kind: KindError, Text: why})
		c.busy = false
		c.nextLocked()
	}
	c.changedLocked()
}

// read follows the agent's signals until it's gone.
func (c *Conversation) read(gen int, a Agent) {
	why := "the assistant stopped"
	for s := range a.Signals() {
		if s.Kind == Exited {
			if s.Text != "" {
				why += ": " + s.Text
			}
			break
		}
		c.signal(gen, s)
	}
	c.failed(a, why)
}

func (c *Conversation) signal(gen int, s Signal) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen != c.gen {
		return
	}
	switch s.Kind {
	case UsedTool:
		if s.Tool == "reply" {
			return
		}
		c.emitLocked(Entry{Kind: KindTool, Tool: s.Tool})
		if c.Progress != nil {
			if line := c.Progress(s.Tool); line != "" {
				c.emitLocked(Entry{Kind: KindProgress, Text: line})
			}
		}
	case Said:
		c.said = s.Text
	case Failed:
		c.emitLocked(Entry{Kind: KindError, Text: s.Text})
	case TurnEnded:
		if !c.busy {
			return
		}
		if !c.replied && strings.TrimSpace(c.said) != "" {
			c.replied = true
			c.emitLocked(Entry{Kind: KindReply, Text: strings.TrimSpace(c.said), Voice: c.cur.voice})
		}
		c.last = c.Now()
		c.busy = false
		c.changedLocked()
		c.nextLocked()
	}
}

// reap ends a conversation left idle.
func (c *Conversation) reap() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-t.C:
		}
		c.ReapIdle()
	}
}

// ReapIdle ends the conversation if it has been idle for Idle: the next turn starts a fresh one.
func (c *Conversation) ReapIdle() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.agent != nil && !c.busy && len(c.asks) == 0 && c.Now().Sub(c.last) >= c.Idle {
		c.endLocked()
		c.emitLocked(Entry{Kind: KindDivider, Text: "new conversation"})
	}
}
