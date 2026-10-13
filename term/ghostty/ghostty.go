// Package ghostty is term.Emulator over Ghostty's libghostty-vt, compiled to wasm with shim.c (wasm/build.sh) and
// run under wazero: one module instance, with its own memory, per terminal.
package ghostty

import (
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/liamg/oriel/term"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

//go:embed wasm/oriel-vt.wasm.gz
var wasmGz []byte

// Options configure a terminal. The zero value is usable.
type Options struct {
	Scrollback  int              // bytes of scrollback kept; 0 is 10 MB
	Reply       func(p []byte)   // answers to queries (DA, DSR…) for the PTY; called from Write, not to call the Terminal
	HoldTimeout time.Duration    // how long synchronized output may hold a frame; 0 is a second
	MaxCommands int              // shell commands remembered; 0 is 1000
	Now         func() time.Time // the clock for HoldTimeout; nil is time.Now
}

var runtime struct {
	once     sync.Once
	err      error
	rt       wazero.Runtime
	cm       wazero.CompiledModule
	mu       sync.Mutex
	cacheDir string
}

// SetCacheDir keeps the compiled module in dir, so it compiles once per version rather than once per run. It takes
// effect only before the first terminal is made.
func SetCacheDir(dir string) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.cacheDir = dir
}

func compiled() (wazero.Runtime, wazero.CompiledModule, error) {
	runtime.once.Do(func() {
		runtime.mu.Lock()
		dir := runtime.cacheDir
		runtime.mu.Unlock()
		ctx := context.Background()
		cfg := wazero.NewRuntimeConfig()
		if dir != "" {
			cache, err := wazero.NewCompilationCacheWithDir(dir)
			if err != nil {
				runtime.err = err
				return
			}
			cfg = cfg.WithCompilationCache(cache)
		}
		runtime.rt = wazero.NewRuntimeWithConfig(ctx, cfg)
		wasm, err := wasmBytes()
		if err != nil {
			runtime.err = err
			return
		}
		runtime.cm, runtime.err = runtime.rt.CompileModule(ctx, wasm)
	})
	return runtime.rt, runtime.cm, runtime.err
}

func wasmBytes() ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(wasmGz))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(zr)
}

// The shim's Term struct, as the host reads it.
const (
	offOut      = 0
	offIn       = 8
	offInCap    = 12
	offReplyLen = 24
	offEvents   = 32
	offHeld     = 36
)

const (
	evTitle = 1 << iota
	evPwd
	evBell
	evHold
	evUnhold
)

// chunk bounds one write into the module, so a huge write doesn't grow its memory to match.
const chunk = 1 << 20

// Terminal is a term.Emulator.
type Terminal struct {
	mu    sync.Mutex
	opts  Options
	m     api.Module
	mem   api.Memory
	fns   map[string]api.Function
	stack []uint64
	t     uint32 // the shim's Term

	scan     term.MarkScanner
	log      term.CommandLog
	title    string
	cwd      string
	stale    int // evTitle|evPwd: refetch on read
	held     bool
	heldAt   time.Time
	lines    []term.Line
	gridCols int
	closed   bool
}

var _ term.Emulator = (*Terminal)(nil)

// New is a terminal of cols × rows cells.
func New(cols, rows int, opts Options) (*Terminal, error) {
	if cols <= 0 || rows <= 0 {
		return nil, fmt.Errorf("ghostty: bad size %dx%d", cols, rows)
	}
	if opts.Scrollback <= 0 {
		opts.Scrollback = 10 << 20
	}
	if opts.HoldTimeout <= 0 {
		opts.HoldTimeout = time.Second
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	rt, cm, err := compiled()
	if err != nil {
		return nil, fmt.Errorf("ghostty: %w", err)
	}
	m, err := rt.InstantiateModule(context.Background(), cm, wazero.NewModuleConfig().WithName(""))
	if err != nil {
		return nil, fmt.Errorf("ghostty: %w", err)
	}
	t := &Terminal{opts: opts, m: m, mem: m.Memory(), fns: map[string]api.Function{}, stack: make([]uint64, 8)}
	t.log.Max = opts.MaxCommands
	t.t = t.call("oriel_new", uint64(cols), uint64(rows), uint64(opts.Scrollback))
	if t.t == 0 {
		m.Close(context.Background())
		return nil, errors.New("ghostty: out of memory")
	}
	return t, nil
}

func (t *Terminal) call(name string, args ...uint64) uint32 {
	f, ok := t.fns[name]
	if !ok {
		f = t.m.ExportedFunction(name)
		if f == nil {
			panic("ghostty: no " + name)
		}
		t.fns[name] = f
	}
	copy(t.stack, args)
	if err := f.CallWithStack(context.Background(), t.stack); err != nil {
		panic(fmt.Sprintf("ghostty: %s: %v", name, err))
	}
	return uint32(t.stack[0])
}

func (t *Terminal) u32(p uint32) uint32 {
	v, ok := t.mem.ReadUint32Le(p)
	if !ok {
		panic("ghostty: read out of range")
	}
	return v
}

// out is the result of the shim's last call, n bytes long, valid until the next call.
func (t *Terminal) out(n uint32) []byte {
	b, ok := t.mem.Read(t.u32(t.t+offOut), n)
	if !ok {
		panic("ghostty: read out of range")
	}
	return b
}

// in copies p to the shim's input buffer.
func (t *Terminal) in(p []byte) {
	if uint32(len(p)) > t.u32(t.t+offInCap) && t.call("oriel_input", uint64(t.t), uint64(len(p))) == 0 {
		panic("ghostty: out of memory")
	}
	if !t.mem.Write(t.u32(t.t+offIn), p) {
		panic("ghostty: write out of range")
	}
}

// Write feeds the program's output to the terminal.
func (t *Terminal) Write(p []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	for len(p) > 0 {
		n, mark, ok := t.scan.Next(p)
		t.write(p[:n])
		if ok {
			t.mark(mark)
		}
		p = p[n:]
	}
	t.drain()
}

func (t *Terminal) write(p []byte) {
	for len(p) > 0 {
		n := min(len(p), chunk)
		t.in(p[:n])
		t.call("oriel_write", uint64(t.t), uint64(n))
		p = p[n:]
	}
}

// drain hands replies to Options.Reply and notes what changed.
func (t *Terminal) drain() {
	if t.u32(t.t+offReplyLen) == 0 && t.u32(t.t+offEvents) == 0 {
		return
	}
	b := t.out(t.call("oriel_take", uint64(t.t)))
	ev := binary.LittleEndian.Uint32(b)
	if reply := b[4:]; len(reply) > 0 && t.opts.Reply != nil {
		t.opts.Reply(append([]byte(nil), reply...))
	}
	t.stale |= int(ev) & (evTitle | evPwd)
	if ev&evHold != 0 {
		t.heldAt = t.opts.Now()
	}
	held, _ := t.mem.ReadByte(t.t + offHeld)
	t.held = held != 0
}

func (t *Terminal) mark(m term.Mark) {
	if t.altScreen() {
		return
	}
	if a := t.anchor(term.Point{Y: -1}); a != nil {
		t.log.Mark(m, a)
	}
}

// anchor tracks p, or the cursor when p.Y < 0; nil if it can't.
func (t *Terminal) anchor(p term.Point) term.Anchor {
	y := uint64(uint32(int32(p.Y)))
	ref := t.call("oriel_mark", uint64(t.t), uint64(p.X), y)
	if ref == 0 {
		return nil
	}
	return &anchor{t: t, ref: ref}
}

type anchor struct {
	t   *Terminal
	ref uint32
}

func (a *anchor) Point() (term.Point, bool) {
	if a.t.closed {
		return term.Point{}, false
	}
	n := a.t.call("oriel_mark_point", uint64(a.t.t), uint64(a.ref))
	if n == 0 {
		return term.Point{}, false
	}
	b := a.t.out(n)
	return term.Point{X: int(binary.LittleEndian.Uint32(b)), Y: int(binary.LittleEndian.Uint32(b[4:]))}, true
}

func (a *anchor) Free() {
	if !a.t.closed {
		a.t.call("oriel_mark_free", uint64(a.ref))
	}
}

type state struct {
	cols, rows, cx, cy, pendingWrap, screen, total, offset, len, mouse, held uint32
}

func (t *Terminal) state() state {
	b := t.out(t.call("oriel_state", uint64(t.t)))
	v := func(i int) uint32 { return binary.LittleEndian.Uint32(b[i*4:]) }
	return state{v(0), v(1), v(2), v(3), v(4), v(5), v(6), v(7), v(8), v(9), v(10)}
}

func (t *Terminal) Resize(cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.call("oriel_resize", uint64(t.t), uint64(cols), uint64(rows))
	t.lines = nil
	t.drain()
}

func (t *Terminal) Size() (cols, rows int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return 0, 0
	}
	s := t.state()
	return int(s.cols), int(s.rows)
}

var ascii [128]string

func init() {
	for i := range ascii {
		ascii[i] = string(rune(i))
	}
}

// Grid is the viewport. While synchronized output holds the frame it stays as it was, until HoldTimeout passes.
func (t *Terminal) Grid() term.Grid {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return term.Grid{}
	}
	s := t.state()
	cols, rows := int(s.cols), int(s.rows)
	all := len(t.lines) != rows || t.gridCols != cols
	if all {
		t.lines = make([]term.Line, rows)
		t.gridCols = cols
	}
	force := t.held && t.opts.Now().Sub(t.heldAt) >= t.opts.HoldTimeout
	b := t.out(t.call("oriel_grid", uint64(t.t), b2u(all), b2u(force)))
	for i := range t.lines {
		t.lines[i].Dirty = false
	}
	b = b[4:]
	var buf [4 * 17]byte
	for len(b) >= 8 {
		y, flags, n := int(binary.LittleEndian.Uint16(b)), binary.LittleEndian.Uint16(b[2:]), int(binary.LittleEndian.Uint16(b[4:]))
		b = b[8:]
		if y >= len(t.lines) {
			break
		}
		l := &t.lines[y]
		l.Dirty = flags&1 != 0
		l.Wrapped = flags&2 != 0
		l.WrapContinuation = flags&4 != 0
		l.Prompt = term.Prompt(flags >> 3 & 3)
		l.Cells = l.Cells[:0]
		for range n {
			cp := binary.LittleEndian.Uint32(b)
			extra := int(b[7])
			c := term.Cell{
				Width:          term.Width(b[4]),
				Attrs:          term.Attrs(b[5]),
				Underline:      term.Underline(b[6]),
				FG:             term.Color(binary.LittleEndian.Uint32(b[8:])),
				BG:             term.Color(binary.LittleEndian.Uint32(b[12:])),
				UnderlineColor: term.Color(binary.LittleEndian.Uint32(b[16:])),
			}
			b = b[20:]
			switch {
			case cp == 0:
			case extra == 0 && cp < 128:
				c.Text = ascii[cp]
			default:
				s := utf8.AppendRune(buf[:0], rune(cp))
				for i := range extra {
					s = utf8.AppendRune(s, rune(binary.LittleEndian.Uint32(b[i*4:])))
				}
				c.Text = string(s)
			}
			b = b[extra*4:]
			l.Cells = append(l.Cells, c)
		}
	}
	return term.Grid{Cols: cols, Rows: rows, Lines: t.lines}
}

func b2u(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}

func (t *Terminal) Cursor() (x, y int, shape term.Shape) {
	c := t.CursorState()
	return c.X, c.Y, c.Shape
}

func (t *Terminal) CursorState() term.CursorState {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return term.CursorState{}
	}
	s := t.state()
	b := t.out(t.call("oriel_cursor", uint64(t.t)))
	v := func(i int) uint32 { return binary.LittleEndian.Uint32(b[i*4:]) }
	return term.CursorState{
		X: int(s.cx), Y: int(s.cy),
		InViewport: v(0) != 0, ViewX: int(v(1)), ViewY: int(v(2)),
		Visible: v(3) != 0, Blinking: v(4) != 0, Shape: term.Shape(v(5)), WideTail: v(6) != 0,
	}
}

func (t *Terminal) Title() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ""
	}
	if t.stale&evTitle != 0 {
		t.title = string(t.out(t.call("oriel_title", uint64(t.t))))
		t.stale &^= evTitle
	}
	return t.title
}

func (t *Terminal) Cwd() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ""
	}
	if t.stale&evPwd != 0 {
		t.cwd = term.CwdFromURL(string(t.out(t.call("oriel_pwd", uint64(t.t)))))
		t.stale &^= evPwd
	}
	return t.cwd
}

func (t *Terminal) Mode(m term.Mode) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	return int32(t.call("oriel_mode", uint64(t.t), uint64(m.N), b2u(m.ANSI))) == 1
}

func (t *Terminal) AltScreen() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	return t.altScreen()
}

// altScreen is AltScreen for a caller holding the lock.
func (t *Terminal) altScreen() bool { return t.state().screen == 1 }

func (t *Terminal) Held() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	return t.held
}

func (t *Terminal) Scroll(s term.Scroll) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	v := s.Delta
	if s.To == term.ScrollRow {
		v = s.Row
	}
	t.call("oriel_scroll", uint64(t.t), uint64(s.To), uint64(uint32(int32(v))))
}

func (t *Terminal) Scrollbar() term.Scrollbar {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return term.Scrollbar{}
	}
	s := t.state()
	return term.Scrollbar{Total: int(s.total), Offset: int(s.offset), Len: int(s.len)}
}

func (t *Terminal) Text(from, to term.Point) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ""
	}
	if from.X < 0 || from.Y < 0 || to.X < 0 || to.Y < 0 {
		return ""
	}
	n := t.call("oriel_text", uint64(t.t), uint64(from.X), uint64(from.Y), uint64(to.X), uint64(to.Y))
	return string(t.out(n))
}

func (t *Terminal) Commands() []term.Command {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	return t.log.Commands()
}

func (t *Terminal) EncodeKey(ev term.KeyEvent) []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.in([]byte(ev.Text))
	n := t.call("oriel_key", uint64(t.t), uint64(ev.Key), uint64(ev.Mods), uint64(ev.Action), uint64(len(ev.Text)), uint64(ev.Unshifted))
	return append([]byte(nil), t.out(n)...)
}

func (t *Terminal) Paste(text string) []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.in([]byte(text))
	n := t.call("oriel_paste", uint64(t.t), uint64(len(text)))
	return append([]byte(nil), t.out(n)...)
}

const snapMagic = "ORIELVT\x01"

var errClosed = errors.New("ghostty: closed")

type snapState struct {
	Commands []term.SavedCommand `json:"commands,omitempty"`
	Pending  []byte              `json:"pending,omitempty"`
}

// Snapshot is the terminal's state: libghostty-vt's snapshot (screens, scrollback, modes, an unfinished sequence)
// after our own (shell-integration marks).
func (t *Terminal) Snapshot() ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, errClosed
	}
	own, err := json.Marshal(snapState{Commands: t.log.Save(), Pending: t.scan.Pending()})
	if err != nil {
		return nil, err
	}
	n := t.call("oriel_snapshot", uint64(t.t))
	if n == 0 {
		return nil, errors.New("ghostty: snapshot failed")
	}
	out := make([]byte, 0, len(snapMagic)+4+len(own)+int(n))
	out = append(out, snapMagic...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(own)))
	out = append(out, own...)
	return append(out, t.out(n)...), nil
}

// Restore replaces the terminal's state with a snapshot's.
func (t *Terminal) Restore(snap []byte) error {
	if len(snap) < len(snapMagic)+4 || string(snap[:len(snapMagic)]) != snapMagic {
		return errors.New("ghostty: not a snapshot")
	}
	snap = snap[len(snapMagic):]
	n := binary.LittleEndian.Uint32(snap)
	if uint64(n) > uint64(len(snap)-4) {
		return errors.New("ghostty: truncated snapshot")
	}
	var own snapState
	if err := json.Unmarshal(snap[4:4+n], &own); err != nil {
		return fmt.Errorf("ghostty: snapshot: %w", err)
	}
	vt := snap[4+n:]
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return errClosed
	}
	t.in(vt)
	if t.call("oriel_restore", uint64(t.t), uint64(len(vt)), uint64(t.opts.Scrollback)) == 0 {
		return errors.New("ghostty: bad snapshot")
	}
	t.scan = term.MarkScanner{}
	t.scan.Next(own.Pending)
	t.log.Load(own.Commands, t.anchor)
	t.lines = nil
	t.stale = evTitle | evPwd
	t.held = false
	return nil
}

func (t *Terminal) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.log.Reset()
	t.call("oriel_free", uint64(t.t))
	t.closed = true
	return t.m.Close(context.Background())
}
