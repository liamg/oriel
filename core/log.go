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

// record is one line of an event log.
type record struct {
	Seq   uint64          `json:"seq"`
	Type  string          `json:"type"`
	Event json.RawMessage `json:"event"`
}

// Recorder writes every event it is given as a line of JSON. Subscribe its Record to a new store with the
// zero Filter, so the log holds every event from the start and Replay can rebuild the state.
type Recorder struct {
	mu  sync.Mutex
	w   io.Writer
	seq uint64
	err error
}

func NewRecorder(w io.Writer) *Recorder { return &Recorder{w: w} }

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
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	var seq uint64
	for sc.Scan() {
		var rec record
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			return fmt.Errorf("event %d: %w", seq+1, err)
		}
		if rec.Seq != seq+1 {
			return fmt.Errorf("event %d: seq is %d", seq+1, rec.Seq)
		}
		seq = rec.Seq
		eventsMu.RLock()
		t, ok := eventTypes[rec.Type]
		eventsMu.RUnlock()
		if !ok {
			return fmt.Errorf("event %d: unknown type %q", seq, rec.Type)
		}
		p := reflect.New(t)
		if err := json.Unmarshal(rec.Event, p.Interface()); err != nil {
			return fmt.Errorf("event %d (%s): %w", seq, rec.Type, err)
		}
		if err := s.Apply(p.Elem().Interface().(Event)); err != nil {
			return fmt.Errorf("event %d (%s): %w", seq, rec.Type, err)
		}
	}
	return sc.Err()
}
