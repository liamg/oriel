package widget

import (
	"fmt"
	"time"
	"unicode"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var (
	tProseMic       = theme.Declare("prose.mic", "fg.faint")
	tProseRecording = theme.Declare("prose.recording", "recording")
	tProseRingTrack = theme.Declare("prose.ring.track", "alpha(recording, 25%)")
)

// Dictator is speech to text for a Prose to dictate into, which the app provides (ui.Provide[widget.Dictator]).
// With none, no Prose shows a mic.
type Dictator interface {
	// Start dictates into target, stopping any other dictation first: there's one at a time. Once it returns nil,
	// State(target) is listening. Its error says why it can't (the model isn't ready), and the Dictator tells the user.
	Start(target ui.ID) error
	// Stop ends the dictation. The field keeps what it typed as it stands; nothing more comes from it.
	Stop()
	// State is target's latest dictation: listening while it runs; once it stops by itself (silence), with the text
	// it ended with until target starts again. Zero for a target it isn't dictating into.
	State(target ui.ID) DictationState
}

// DictationState is a dictation as its field shows it.
type DictationState struct {
	Listening bool
	Level     float64 // the mic's loudness, 0 to 1: the border breathes with it
	// Text is what it has typed so far, phrases joined by spaces: voice.Transcript's edits, applied in turn.
	Text string
	Live int // the runes at Text's end still being heard: drawn muted, and replaced as they're corrected
	// NotReady says why a start would fail now ("Dictation is getting ready: …"), as the mic's tooltip.
	NotReady string
}

type ProseOpts struct {
	Label       string // its accessible name
	Placeholder string
	Help        string
	Width       ui.Size // grows to fill by default
	Lines       int     // the lines it shows at most before it scrolls; 6 when under 2
	MinLines    int
	// EnterSubmits makes ⏎ submit and ⇧⏎ a new line (a comment, a reply); otherwise ⌘⏎ (Ctrl⏎ elsewhere) submits.
	EnterSubmits bool
	Disabled     bool
	Invalid      bool
	DefaultFocus bool
	KeyMode      string // its keymap mode; "input" by default
	TabMoves     bool   // Tab and ⇧Tab move focus on (InputOpts.TabMoves)
}

// micReturn is how long after the last key the mic comes back.
const micReturn = 1500 * time.Millisecond

const micSide = 20

type proseState struct {
	on       bool   // dictating into this field
	anchor   int    // where the dictated text starts, in runes
	sep      bool   // a space goes before it, after text that doesn't end in one
	typed    string // the dictated text as the field holds it, with its space
	live     int    // runes at its end still being heard
	join     string // its edits undo together
	sessions int
	status   string // "Listening", "Stopped": read out as a status
	lastKey  time.Time
	micDown  bool // the mic is pressed
	atEnd    bool // and the field hadn't focus then, so dictation goes at the end
	focused  bool // last frame
}

type proseKey struct{}

// Prose is the field for writing: prompts, comments, replies, commit messages. It edits as a multi-line Input does,
// and with a Dictator it has a mic inlaid at its foot (hidden while you type, back 1.5 s after the last key):
// click it, or press ⌘⇧Space (Ctrl⇧Space elsewhere), to dictate at the cursor. Dictated words land through the
// field's own edits, so they undo and move the caret as typing does; the phrase still being heard is muted and
// corrected in place. Any key (which still types), Escape (which drops that phrase), the mic or the shortcut again,
// or focus leaving stops it.
func Prose(c *ui.Ctx, key any, text *string, o ProseOpts) InputResult {
	id := c.Next(key)
	lines := o.Lines
	if lines < 2 {
		lines = 6
	}
	io := InputOpts{Label: o.Label, Placeholder: o.Placeholder, Help: o.Help, Width: o.Width, Lines: lines, MinLines: o.MinLines,
		EnterSubmits: o.EnterSubmits, Disabled: o.Disabled, Invalid: o.Invalid, DefaultFocus: o.DefaultFocus, KeyMode: o.KeyMode, TabMoves: o.TabMoves}
	st := ui.State[proseState](c, ui.Child(id, proseKey{}))
	d, ok := ui.Service[Dictator](c)
	if !ok || o.Disabled {
		if st.on && ok && d.State(id).Listening {
			d.Stop()
		}
		st.on = false
		return field(c, key, text, io, nil)
	}
	in := c.Interaction(id)
	mic := c.Interaction(ui.Child(id, "mic"))
	ds := d.State(id)
	runes := []rune(*text)
	x := &fieldExtra{keys: []render.KeyEvent{}, padR: micSide}

	want := st.typed // what the dictated span should hold by the end of the frame
	stop := func(drop bool) {
		if drop && st.live > 0 {
			w := []rune(want)
			want = string(w[:max(0, len(w)-st.live)])
			if st.sep && want == " " {
				want = ""
			}
		}
		if ds.Listening {
			d.Stop()
		}
		st.on, st.live, st.status = false, 0, "Stopped"
	}
	start := func(atEnd bool) {
		if d.Start(id) != nil {
			return
		}
		fs := ui.State[inputState](c, id)
		lo, hi := clampInt(min(fs.cursor, fs.anchor), 0, len(runes)), clampInt(max(fs.cursor, fs.anchor), 0, len(runes))
		if atEnd {
			lo, hi = len(runes), len(runes)
		}
		st.sessions++
		st.on, st.anchor, st.typed, st.live = true, lo, string(runes[lo:hi]), 0
		st.sep = lo > 0 && !unicode.IsSpace(runes[lo-1])
		st.join, st.status = fmt.Sprintf("dictation %d", st.sessions), "Listening"
		want, ds = st.typed, d.State(id)
	}

	if st.on {
		end := st.anchor + len([]rune(st.typed))
		switch {
		case end > len(runes) || string(runes[st.anchor:end]) != st.typed:
			stop(false) // the text changed under it
		case ds.Listening || ds.Text != "":
			want = ds.Text
			if st.sep && want != "" {
				want = " " + want
			}
			st.live = 0
			if ds.Listening {
				st.live = min(ds.Live, len([]rune(ds.Text)))
			}
		}
		if st.on && !ds.Listening {
			stop(false)
		}
	}
	for _, k := range in.Keys {
		switch {
		case k.Key == " " && k.Mods == platform.primary|render.ModShift:
			if st.on {
				stop(false)
			} else {
				start(false)
			}
		case st.on && k.Key == "Esc" && k.Mods == 0:
			stop(true)
		default:
			if st.on {
				stop(false)
			}
			x.keys = append(x.keys, k)
			st.lastKey = c.Now()
		}
	}
	if st.on && !in.Focused {
		stop(false)
	}
	if mic.Pressed && !st.micDown {
		st.atEnd = !st.focused
	}
	st.micDown = mic.Pressed
	if mic.Clicked {
		if st.on {
			stop(false)
		} else {
			start(st.atEnd)
		}
	}
	st.focused = in.Focused

	if want != st.typed {
		p := commonRunes([]rune(st.typed), []rune(want))
		x.edit = &fieldEdit{From: st.anchor + p, To: st.anchor + len([]rune(st.typed)), Insert: string([]rune(want)[p:]), Join: st.join}
		st.typed = want
	}
	if st.on {
		end := st.anchor + len([]rune(st.typed))
		x.muted = [2]int{end - st.live, end}
		x.stroke = alpha(col(c, tProseRecording), .4+.6*min(max(ds.Level, 0), 1))
	}
	shown := st.on || st.lastKey.IsZero() || c.Now().Sub(st.lastKey) >= micReturn
	if !shown {
		c.WakeAt(st.lastKey.Add(micReturn))
	}
	level, on := ds.Level, st.on
	name := "Dictate"
	if on {
		name = "Stop dictating"
	}
	tip := name
	if !on && ds.NotReady != "" {
		tip = ds.NotReady
	}
	x.inlay = func(c *ui.Ctx) {
		if shown {
			corner := &ui.Float{At: render.Point{X: 1, Y: 1}, Self: render.Point{X: 1, Y: 1}, Offset: render.Point{X: -4, Y: -4}}
			Clickable(c, "mic", ui.Style{W: ui.Px(micSide), H: ui.Px(micSide), Radius: micSide / 2, Main: ui.Center, Cross: ui.Center,
				Float: corner, Z: ui.LayerContent, Label: name, Tooltip: tip, Help: o.Help, Selected: on},
				func(c *ui.Ctx) {
					c.Paint("glyph", ui.Px(micSide), ui.Px(micSide), func(f render.Frame, r render.Rect) {
						drawMic(f, r, on, level, col(c, tProseMic), col(c, tProseRecording), col(c, tProseRingTrack))
					})
				})
		}
		if st.status != "" {
			c.Box("status", ui.Style{Float: &ui.Float{}, Z: ui.LayerContent, Role: ui.RoleStatus, Label: st.status}, nil)
		}
	}
	return field(c, key, text, io, x)
}

// drawMic draws a mic in r: an outline, or while on, filled with a ring round it lit through level.
func drawMic(f render.Frame, r render.Rect, on bool, level float64, idle, rec, track render.Color) {
	k := r.Dx() / 24
	at := func(x, y float32) render.Point { return render.Point{X: r.Min.X + 4*k + x*k, Y: r.Min.Y + 4*k + y*k} }
	ink := idle
	if on {
		ink = rec
		ring := render.Rect{Min: render.Point{X: r.Min.X + 1, Y: r.Min.Y + 1}, Max: render.Point{X: r.Max.X - 1, Y: r.Max.Y - 1}}
		f.Path(render.Ellipse(ring), render.Style{Stroke: track, Width: 1.5})
		if level > 0 {
			f.Path(arc(ring, min(level, 1)), render.Style{Stroke: rec, Width: 1.5})
		}
	}
	body := render.Rect{Min: at(6, 2), Max: at(10, 9.5)}
	st := render.Style{Stroke: ink, Width: 1.25, Radius: 2 * k}
	if on {
		st = render.Style{Fill: ink, Radius: 2 * k}
	}
	f.Rect(body, st)
	cradle := new(render.Path).MoveTo(at(3.5, 7.5)).CubeTo(at(3.5, 11), at(6, 12.5), at(8, 12.5)).
		CubeTo(at(10, 12.5), at(12.5, 11), at(12.5, 7.5)).MoveTo(at(8, 12.5)).LineTo(at(8, 15))
	f.Path(cradle, render.Style{Stroke: ink, Width: 1.25})
}

// commonRunes is how many runes a and b start with alike.
func commonRunes(a, b []rune) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}
