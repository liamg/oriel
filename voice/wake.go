package voice

import (
	"context"
	"regexp"
	"strings"
	"sync"
)

// DefaultWakeWord is crit's.
const DefaultWakeWord = "computer"

// spellings are the ways Whisper writes a short, unfamiliar wake word.
var spellings = map[string]string{
	"oriel": "oriel|oriol|oreal|orial|orielle|oriel's",
}

// WakeMatch is what followed the wake word when text opens with it ("hey" or "ok" before it allowed); ok is false
// when it doesn't.
func WakeMatch(text, word string) (rest string, ok bool) {
	forms, known := spellings[strings.ToLower(word)]
	if !known {
		forms = regexp.QuoteMeta(word)
	}
	re := regexp.MustCompile(`(?i)^\W*(?:(?:hey|ok|okay)\W+)?(?:` + forms + `)(?:[^\w']|$)[\s,.!?:;-]*`)
	m := re.FindStringIndex(text)
	if m == nil {
		return "", false
	}
	return strings.TrimSpace(text[m[1]:]), true
}

// WakeOpts configure listening for the wake word.
type WakeOpts struct {
	Transcriber Transcriber
	Word        string
	Prompt      string            // the vocabulary; the word is added to it
	Tuning      Tuning            // Defaults when zero
	OnWake      func(rest string) // the word was said after a pause: rest is what followed it in the same breath
	OnError     func(err error)   // a transcription failed: it stops listening
}

// Waker listens for the wake word said after a pause, transcribing only the start of each utterance and one at a
// time. Ported from crit's voice.js wake.
type Waker struct {
	o    WakeOpts
	s    Stream
	stop chan struct{}
	done chan struct{}

	mu   sync.Mutex
	busy bool
	off  bool

	t, loud, quiet, lastSpeech float64
	floor                      []float64
	thresh                     float64
	pre                        [][]float32
	utt                        *utterance
}

type utterance struct {
	chunks [][]float32
	ms     float64
	state  string // check, pending, wake, skip
	ended  bool
}

// Wake starts listening on s for the wake word. It closes s when stopped.
func Wake(s Stream, o WakeOpts) *Waker {
	if o.Tuning == (Tuning{}) {
		o.Tuning = Defaults
	}
	if o.Word == "" {
		o.Word = DefaultWakeWord
	}
	w := &Waker{o: o, s: s, stop: make(chan struct{}), done: make(chan struct{}), thresh: minThreshold, lastSpeech: -1e9}
	go w.run()
	return w
}

// Stop stops listening.
func (w *Waker) Stop() {
	select {
	case w.stop <- struct{}{}:
	case <-w.done:
	}
	<-w.done
}

// Done is closed once it has stopped: stopped, its stream ended or a transcription failed.
func (w *Waker) Done() <-chan struct{} { return w.done }

func (w *Waker) run() {
	defer close(w.done)
	defer w.s.Close()
	frames := w.s.Frames()
	for {
		select {
		case <-w.stop:
			w.mu.Lock()
			w.off = true
			w.mu.Unlock()
			return
		case f, ok := <-frames:
			w.mu.Lock()
			off := w.off
			w.mu.Unlock()
			if !ok || off {
				return
			}
			w.frame(f)
		}
	}
}

func (w *Waker) prompt() string {
	if w.o.Prompt == "" {
		return w.o.Word + "."
	}
	return strings.TrimSuffix(w.o.Prompt, ".") + ", " + w.o.Word + "."
}

// hear transcribes chunks and hands then what followed the wake word, or false.
func (w *Waker) hear(chunks [][]float32, then func(rest string, ok bool)) {
	w.mu.Lock()
	w.busy = true
	w.mu.Unlock()
	go func() {
		text, err := w.o.Transcriber.Transcribe(context.Background(), EncodeWAV(chunks), w.prompt())
		w.mu.Lock()
		w.busy = false
		off := w.off
		if err != nil {
			w.off = true
		}
		w.mu.Unlock()
		if off {
			return
		}
		if err != nil {
			if w.o.OnError != nil {
				w.o.OnError(err)
			}
			return
		}
		then(WakeMatch(text, w.o.Word))
	}()
}

func (w *Waker) endUtt(u *utterance) {
	w.mu.Lock()
	state, busy, chunks := u.state, w.busy, u.chunks
	w.mu.Unlock()
	switch {
	case state == "check" && !busy:
		w.hear(chunks, func(rest string, ok bool) {
			if ok {
				w.o.OnWake(rest)
			}
		})
	case state == "wake":
		w.hear(chunks, func(rest string, _ bool) { w.o.OnWake(rest) })
	}
}

func (w *Waker) frame(f []float32) {
	t := w.o.Tuning
	fms := float64(len(f)) / Rate * 1000
	e := rms(f)
	w.t += fms
	if w.t < ms(t.Floor) {
		w.floor = append(w.floor, e)
		return
	}
	if len(w.floor) > 0 {
		var sum float64
		for _, v := range w.floor {
			sum += v
		}
		w.thresh = max(minThreshold, sum/float64(len(w.floor))*3)
		w.floor = nil
	}
	if e > w.thresh {
		w.loud += fms
		w.quiet = 0
	} else {
		w.loud = 0
		w.quiet += fms
	}
	speaking := w.loud >= ms(t.Speech)
	if w.utt == nil {
		w.pre = append(w.pre, f)
		var keep float64
		w.pre, keep = preroll(w.pre, ms(t.Preroll))
		if speaking {
			gap := w.t - w.loud - w.lastSpeech
			w.mu.Lock()
			busy := w.busy
			w.mu.Unlock()
			state := "skip"
			if gap >= ms(t.WakeGap) && !busy {
				state = "check"
			}
			w.utt = &utterance{chunks: w.pre, ms: keep, state: state}
			w.pre = nil
		}
	} else {
		u := w.utt
		u.ms += fms
		w.mu.Lock()
		if u.state != "skip" {
			u.chunks = append(u.chunks, f)
		}
		check := u.state == "check" && u.ms >= ms(t.WakeCheck)
		var heard [][]float32
		if check {
			u.state, heard = "pending", append([][]float32(nil), u.chunks...)
		}
		w.mu.Unlock()
		if check {
			w.hear(heard, func(_ string, ok bool) {
				w.mu.Lock()
				if ok {
					u.state = "wake"
				} else {
					u.state, u.chunks = "skip", nil
				}
				ended := u.ended
				w.mu.Unlock()
				if ended {
					// The utterance ended while the check was out: its answer decides the whole of it.
					w.endUtt(u)
				}
			})
		}
		if w.quiet >= ms(t.Pause) || u.ms >= ms(t.WakeCap) {
			w.utt = nil
			w.mu.Lock()
			pending := u.state == "pending"
			u.ended = true
			w.mu.Unlock()
			if !pending {
				w.endUtt(u)
			}
		}
	}
	if speaking {
		w.lastSpeech = w.t
	}
}
