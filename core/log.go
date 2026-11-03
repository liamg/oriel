package core

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sync"
)

var (
	eventsMu   sync.RWMutex
	eventTypes = map[string]reflect.Type{}
	eventNames = map[reflect.Type]string{}
)

// RegisterEvent names an event type in logs, so a log holding it can be replayed.
// Plugins register their own events the same way; registering a name twice panics.
func RegisterEvent[E Event](name string) {
	eventsMu.Lock()
	defer eventsMu.Unlock()
	t := reflect.TypeFor[E]()
	if prev, ok := eventTypes[name]; ok && prev != t {
		panic(fmt.Sprintf("core: event name %q is already %s", name, prev))
	}
	eventTypes[name] = t
	eventNames[t] = name
}

// EventName is ev's registered name, or its Go type when it has none.
func EventName(ev Event) string {
	eventsMu.RLock()
	defer eventsMu.RUnlock()
	if n, ok := eventNames[reflect.TypeOf(ev)]; ok {
		return n
	}
	return fmt.Sprintf("%T", ev)
}

// record is one line of an event log.
type record struct {
	Seq   uint64          `json:"seq"`
	Type  string          `json:"type"`
	Event json.RawMessage `json:"event"`
}

// Recorder writes every event it is given as a line of JSON. Subscribe its Record to a new store with the
// zero Filter, so the log holds every event from the start and Replay can rebuild the state.
type Recorder struct {
	mu    sync.Mutex
	w     io.Writer
	seq   uint64
	err   error
	vault *Vault
}

func NewRecorder(w io.Writer) *Recorder { return &Recorder{w: w} }

// ResumeRecorder carries on a log that already holds seq events, so it still replays.
func ResumeRecorder(w io.Writer, seq uint64) *Recorder { return &Recorder{w: w, seq: seq} }

// Keep puts the text sealed events take out of the log in v; without one it's dropped.
func (r *Recorder) Keep(v *Vault) *Recorder {
	r.vault = v
	return r
}

func (r *Recorder) Record(ev Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return
	}
	r.err = r.write(ev)
}

func (r *Recorder) write(ev Event) error {
	eventsMu.RLock()
	name, ok := eventNames[reflect.TypeOf(ev)]
	eventsMu.RUnlock()
	if !ok {
		return fmt.Errorf("core: event type %T is not registered", ev)
	}
	if s, ok := ev.(Sealed); ok {
		var texts map[string]string
		ev, texts = s.Seal()
		if r.vault != nil && len(texts) > 0 {
			_ = r.vault.Put(texts)
		}
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	r.seq++
	line, err := json.Marshal(record{Seq: r.seq, Type: name, Event: b})
	if err != nil {
		return err
	}
	_, err = r.w.Write(append(line, '\n'))
	return err
}

// Err is the first error recording hit; nothing is written after it.
func (r *Recorder) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// Replay applies every event in a recorded log to s, in order. It stops at the first event that
// can't be read, is out of sequence or doesn't apply.
func Replay(r io.Reader, s Store) error {
	_, err := ReplayCount(r, s)
	return err
}

// ReplayCount is Replay, and how many events it applied.
func ReplayCount(r io.Reader, s Store) (uint64, error) { return ReplayVault(r, s, nil) }

// ReplayVault is ReplayCount, with the text sealed events left out of the log put back from v.
func ReplayVault(r io.Reader, s Store, v *Vault) (uint64, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	var seq uint64
	for sc.Scan() {
		var rec record
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			return seq, fmt.Errorf("event %d: %w", seq+1, err)
		}
		if rec.Seq != seq+1 {
			return seq, fmt.Errorf("event %d: seq is %d", seq+1, rec.Seq)
		}
		seq = rec.Seq
		eventsMu.RLock()
		t, ok := eventTypes[rec.Type]
		eventsMu.RUnlock()
		if !ok {
			return seq, fmt.Errorf("event %d: unknown type %q", seq, rec.Type)
		}
		p := reflect.New(t)
		if err := json.Unmarshal(rec.Event, p.Interface()); err != nil {
			return seq, fmt.Errorf("event %d (%s): %w", seq, rec.Type, err)
		}
		ev := p.Elem().Interface().(Event)
		if sealed, ok := ev.(Sealed); ok && v != nil {
			ev = sealed.Unseal(v.Get)
		}
		if err := s.Apply(ev); err != nil {
			return seq, fmt.Errorf("event %d (%s): %w", seq, rec.Type, err)
		}
	}
	return seq, sc.Err()
}

// Envelope is an event with its registered name, as it goes over the wire.
type Envelope struct {
	Type  string          `json:"type"`
	Event json.RawMessage `json:"event"`
}

// EncodeEvent wraps ev for the wire. Its type must be registered.
func EncodeEvent(ev Event) (Envelope, error) {
	eventsMu.RLock()
	name, ok := eventNames[reflect.TypeOf(ev)]
	eventsMu.RUnlock()
	if !ok {
		return Envelope{}, fmt.Errorf("core: event type %T is not registered", ev)
	}
	b, err := json.Marshal(ev)
	return Envelope{Type: name, Event: b}, err
}

// DecodeEvent unwraps an event from the wire.
func DecodeEvent(e Envelope) (Event, error) {
	eventsMu.RLock()
	t, ok := eventTypes[e.Type]
	eventsMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("core: unknown event type %q", e.Type)
	}
	p := reflect.New(t)
	if err := json.Unmarshal(e.Event, p.Interface()); err != nil {
		return nil, fmt.Errorf("core: %s: %w", e.Type, err)
	}
	return p.Elem().Interface().(Event), nil
}
