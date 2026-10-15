package voice

import (
	"context"
	"math"
	"sync"
	"time"
)

// Tuning is how speech is found in the audio, crit's voice.js cfg. Times are audio time.
type Tuning struct {
	Tick      time.Duration // the live phrase is transcribed again this often
	Pause     time.Duration // this much quiet commits a phrase
	PhraseCap time.Duration // a phrase this long is committed anyway
	Floor     time.Duration // the noise floor is measured over the first stretch
	Speech    time.Duration // energy above the floor this long is speech
	Preroll   time.Duration // audio kept before speech starts
	WakeGap   time.Duration // quiet before a wake word, so one mid-sentence isn't a call
	WakeCheck time.Duration // how much of an utterance is heard for the wake word
	WakeCap   time.Duration // an utterance after the wake word ends by this
}

// Defaults are crit's.
var Defaults = Tuning{Tick: time.Second, Pause: 600 * time.Millisecond, PhraseCap: 20 * time.Second, Floor: 300 * time.Millisecond,
	Speech: 150 * time.Millisecond, Preroll: 300 * time.Millisecond, WakeGap: 700 * time.Millisecond, WakeCheck: 1500 * time.Millisecond,
	WakeCap: 10 * time.Second}

const minThreshold = 0.012

// ListenOpts configure a dictation.
type ListenOpts struct {
	Transcriber Transcriber
	Prompt      string // the words Whisper should spell our way
	Tuning      Tuning // Defaults when zero
	// EndAfterSilence ends it by itself after this much quiet once speech began; StartTimeout gives up if no
	// speech begins within it; Max stops it after this long regardless. 0 is never.
	EndAfterSilence, StartTimeout, Max time.Duration
	OnState                            func(state string) // "hearing", once speech begins
	// OnText is each pass at the phrase: its text so far, which replaces the last pass's; final commits it, and
	// the next phrase starts after it.
	OnText func(text string, final bool)
	// OnEnd is called once, when it's over: why ("stopped", "cancelled", "silence", "timeout", "cap", "ended",
	// "error") and what was committed.
	OnEnd func(reason, committed string, err error)
}

// Dictation is one listening session over a stream, until stopped. Ported from crit's voice.js listen.
type Dictation struct {
	o      ListenOpts
	s      Stream
	stop   chan struct{}
	cancel chan struct{}
	done   chan struct{}

	// The audio side, on the run goroutine.
	phrase                [][]float32
	phraseMs, quiet, loud float64
	spoke, heard          bool
	floor                 []float64
	thresh                float64
	elapsed, tick         float64

	jobs     chan job
	idle     chan struct{} // closed once the last pass is done
	failed   chan struct{} // a pass failed
	mu       sync.Mutex
	inflight int
	dropped  bool // cancelled or failed: no more text
	failure  error
	commit   string
}

type job struct {
	chunks [][]float32
	final  bool
}

// Dictate starts listening on s. It closes s when it ends.
func Dictate(s Stream, o ListenOpts) *Dictation {
	if o.Tuning == (Tuning{}) {
		o.Tuning = Defaults
	}
	d := &Dictation{o: o, s: s, stop: make(chan struct{}), cancel: make(chan struct{}), done: make(chan struct{}), thresh: minThreshold,
		jobs: make(chan job, 64), idle: make(chan struct{}), failed: make(chan struct{}, 1)}
	go d.transcribe()
	go d.run()
	return d
}

// Stop takes one last pass at the phrase, then ends.
func (d *Dictation) Stop() { d.signal(d.stop) }

// Cancel ends it now: the live phrase's text is dropped.
func (d *Dictation) Cancel() { d.signal(d.cancel) }

func (d *Dictation) signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	case <-d.done:
	}
}

// Done is closed once it has ended.
func (d *Dictation) Done() <-chan struct{} { return d.done }

func ms(dur time.Duration) float64 { return float64(dur) / float64(time.Millisecond) }

func rms(f []float32) float64 {
	var s float64
	for _, v := range f {
		s += float64(v) * float64(v)
	}
	return math.Sqrt(s / float64(max(len(f), 1)))
}

func (d *Dictation) run() {
	t := d.o.Tuning
	reason := ""
	frames := d.s.Frames()
loop:
	for {
		select {
		case <-d.stop:
			reason = "stopped"
			break loop
		case <-d.failed:
			reason = "error"
			break loop
		case <-d.cancel:
			d.mu.Lock()
			d.dropped = true
			d.mu.Unlock()
			reason = "cancelled"
			break loop
		case f, ok := <-frames:
			if !ok {
				reason = "ended"
				break loop
			}
			if r := d.frame(f, t); r != "" {
				reason = r
				break loop
			}
		}
	}
	_ = d.s.Close()
	streamErr := d.s.Err()
	if reason != "cancelled" {
		d.send(true)
	}
	close(d.jobs)
	<-d.idle
	d.mu.Lock()
	err, committed := d.failure, d.commit
	d.mu.Unlock()
	if err == nil && reason == "ended" {
		err = streamErr
	}
	if err != nil {
		reason = "error"
	}
	if d.o.OnEnd != nil {
		d.o.OnEnd(reason, committed, err)
	}
	close(d.done)
}

// frame takes in 20 ms of audio; it says why to stop, if it should.
func (d *Dictation) frame(f []float32, t Tuning) string {
	d.mu.Lock()
	failed := d.failure != nil
	d.mu.Unlock()
	if failed {
		return "error"
	}
	fms := float64(len(f)) / Rate * 1000
	e := rms(f)
	d.elapsed += fms
	if d.elapsed < ms(t.Floor) {
		d.floor = append(d.floor, e)
	} else if len(d.floor) > 0 {
		var sum float64
		for _, v := range d.floor {
			sum += v
		}
		d.thresh = max(minThreshold, sum/float64(len(d.floor))*3)
		d.floor = nil
	}
	d.phrase = append(d.phrase, f)
	d.phraseMs += fms
	if e > d.thresh {
		d.loud += fms
		d.quiet = 0
		if d.loud >= ms(t.Speech) {
			d.spoke = true
			if !d.heard {
				d.heard = true
				if d.o.OnState != nil {
					d.o.OnState("hearing")
				}
			}
		}
	} else {
		d.loud = 0
		d.quiet += fms
	}
	if !d.spoke {
		d.phrase, d.phraseMs = preroll(d.phrase, ms(t.Preroll))
		if d.o.StartTimeout > 0 && !d.heard && d.elapsed > ms(d.o.StartTimeout) {
			return "timeout"
		}
	} else if d.quiet >= ms(t.Pause) || d.phraseMs >= ms(t.PhraseCap) {
		d.send(true)
	}
	d.tick += fms
	if d.tick >= ms(t.Tick) {
		d.tick = 0
		d.send(false)
	}
	if d.o.EndAfterSilence > 0 && d.heard && d.quiet >= ms(d.o.EndAfterSilence) {
		return "silence"
	}
	if d.o.Max > 0 && d.elapsed >= ms(d.o.Max) {
		return "cap"
	}
	return ""
}

// preroll keeps only the run-up to speech: the last keep ms of chunks.
func preroll(chunks [][]float32, keep float64) ([][]float32, float64) {
	got, i := 0.0, len(chunks)
	for i > 0 && got < keep {
		i--
		got += float64(len(chunks[i])) / Rate * 1000
	}
	if i == 0 {
		total := 0.0
		for _, c := range chunks {
			total += float64(len(c)) / Rate * 1000
		}
		return chunks, total
	}
	return append([][]float32(nil), chunks[i:]...), got
}

// send queues a pass at the phrase; a live one is skipped while another is in flight.
func (d *Dictation) send(final bool) {
	if !d.spoke || len(d.phrase) == 0 {
		if final {
			d.phrase, d.phraseMs = nil, 0
		}
		return
	}
	d.mu.Lock()
	if !final && d.inflight > 0 {
		d.mu.Unlock()
		return
	}
	d.inflight++
	d.mu.Unlock()
	chunks := append([][]float32(nil), d.phrase...)
	if final {
		d.phrase, d.phraseMs, d.spoke = nil, 0, false
	}
	d.jobs <- job{chunks: chunks, final: final}
}

func (d *Dictation) transcribe() {
	defer close(d.idle)
	for j := range d.jobs {
		d.mu.Lock()
		skip := d.dropped
		d.mu.Unlock()
		var text string
		var err error
		if !skip {
			text, err = d.o.Transcriber.Transcribe(context.Background(), EncodeWAV(j.chunks), d.o.Prompt)
		}
		d.mu.Lock()
		switch {
		case skip || d.dropped:
		case err != nil:
			d.failure, d.dropped = err, true
			select {
			case d.failed <- struct{}{}:
			default:
			}
		default:
			if j.final && text != "" {
				if d.commit != "" {
					d.commit += " "
				}
				d.commit += text
			}
			d.mu.Unlock()
			if d.o.OnText != nil {
				d.o.OnText(text, j.final)
			}
			d.mu.Lock()
		}
		d.inflight--
		d.mu.Unlock()
	}
}
