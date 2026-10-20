// Package nvim runs vimtest cases in `nvim --headless --clean --embed` over msgpack-RPC: the oracle.
package nvim

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/vim/vimtest"
)

// Version is the nvim the oracle is pinned to.
const Version = "v0.11.7"

// Path finds nvim: $ORIEL_NVIM, then PATH.
func Path() (string, error) {
	if p := os.Getenv("ORIEL_NVIM"); p != "" {
		return p, nil
	}
	return exec.LookPath("nvim")
}

var (
	versionOnce sync.Once
	versionErr  error
)

// checkVersion fails unless the nvim at path is the pinned Version.
func checkVersion(path string) error {
	versionOnce.Do(func() {
		out, err := exec.Command(path, "--version").Output()
		if err != nil {
			versionErr = fmt.Errorf("nvim --version: %w", err)
			return
		}
		first, _, _ := strings.Cut(string(out), "\n")
		if first != "NVIM "+Version {
			versionErr = fmt.Errorf("the oracle is pinned to nvim %s, but %s is %q", Version, path, first)
		}
	})
	return versionErr
}

// New returns a vimtest.NewEngine that starts a fresh nvim for every run.
func New() vimtest.NewEngine {
	return func() (vimtest.Engine, error) { return start() }
}

type engine struct {
	cmd *exec.Cmd
	c   *client
}

func start() (*engine, error) { return startIn("") }

// startIn starts nvim with dir as its working directory ("" for ours).
func startIn(dir string) (*engine, error) {
	path, err := Path()
	if err != nil {
		return nil, fmt.Errorf("oracle: no nvim (set ORIEL_NVIM or put nvim %s on PATH): %w", Version, err)
	}
	if err := checkVersion(path); err != nil {
		return nil, err
	}
	cmd := exec.Command(path, "--headless", "--clean", "--embed", "-n", "-i", "NONE")
	cmd.Env = append(os.Environ(), "NVIM_APPNAME=oriel-vimtest")
	cmd.Dir = dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	e := &engine{cmd: cmd, c: newClient(stdout, stdin, 5*time.Second)}
	// nvim's default mappings (Y, &, gc…) aren't vim's.
	if _, err := e.c.call("nvim_exec2", "mapclear | mapclear! | tmapclear", map[string]any{}); err != nil {
		e.Close()
		return nil, err
	}
	return e, nil
}

func (e *engine) Close() error {
	e.cmd.Process.Kill()
	e.cmd.Wait()
	return nil
}

// Load sets the buffer as if it had been read from a file: not an undoable change, and with no jumps.
func (e *engine) Load(b vimtest.Buffer) error {
	if _, err := e.c.call("nvim_exec2", "set undolevels=-1", map[string]any{}); err != nil {
		return err
	}
	if _, err := e.c.call("nvim_buf_set_lines", 0, 0, -1, true, b.Lines); err != nil {
		return err
	}
	if _, err := e.c.call("nvim_exec2", "set undolevels& | clearjumps", map[string]any{}); err != nil {
		return err
	}
	if b.Visual == nil {
		return e.setCursor(b.Cursor)
	}
	if err := e.setCursor(b.Visual.Anchor); err != nil {
		return err
	}
	if err := e.feed("v"); err != nil {
		return err
	}
	return e.setCursor(b.Cursor)
}

func (e *engine) setCursor(p vimtest.Pos) error {
	_, err := e.c.call("nvim_win_set_cursor", 0, []any{p.Line + 1, p.Col})
	return err
}

func (e *engine) Feed(keys []vimtest.Key) error {
	return e.feed(vimtest.Notation(keys))
}

// feed queues keys as if typed, then waits for nvim to process them. Keys that leave nvim
// waiting for more (a count, a register name) can't be read back and fail.
func (e *engine) feed(notation string) error {
	if _, err := e.c.call("nvim_input", notation); err != nil {
		return err
	}
	if _, err := e.c.callTimeout(time.Second, "nvim_eval", "1"); err != nil {
		if m, merr := e.c.call("nvim_get_mode"); merr == nil {
			if mm, _ := m.(map[string]any); mm["blocking"] == true {
				return errors.New("nvim is waiting for more keys; end the case on a complete command or in a mode")
			}
		}
		return err
	}
	return nil
}

// mode maps nvim_get_mode().mode to vimtest modes.
func mode(m string) string {
	switch {
	case strings.HasPrefix(m, "no"):
		return vimtest.ModeOpPending
	case strings.HasPrefix(m, "n"):
		return vimtest.ModeNormal
	case strings.HasPrefix(m, "i"):
		return vimtest.ModeInsert
	case strings.HasPrefix(m, "R"):
		return vimtest.ModeReplace
	case m == "v":
		return vimtest.ModeVisual
	case m == "V":
		return vimtest.ModeVisualLine
	case m == "\x16":
		return vimtest.ModeVisualBlock
	case strings.HasPrefix(m, "s") || strings.HasPrefix(m, "S") || m == "\x13":
		return vimtest.ModeSelect
	case strings.HasPrefix(m, "c"):
		return vimtest.ModeCmdline
	}
	return m
}

func (e *engine) Snapshot(q vimtest.Query) (vimtest.Result, error) {
	r := vimtest.Result{Source: "nvim " + Version}
	m, err := e.c.call("nvim_get_mode")
	if err != nil {
		return r, err
	}
	mm, _ := m.(map[string]any)
	modeStr, _ := mm["mode"].(string)
	r.Mode = mode(modeStr)

	lines, err := e.c.call("nvim_buf_get_lines", 0, 0, -1, true)
	if err != nil {
		return r, err
	}
	for _, l := range lines.([]any) {
		s, _ := l.(string)
		r.Lines = append(r.Lines, s)
	}
	if r.Cursor, err = e.cursor(); err != nil {
		return r, err
	}
	if r.Mode == vimtest.ModeVisual {
		anchor, err := e.getpos("v")
		if err != nil || anchor == nil {
			return r, errors.Join(err, errors.New("no visual start"))
		}
		v := &vimtest.Visual{Start: *anchor, End: r.Cursor, Anchor: *anchor}
		if v.End.Line < v.Start.Line || v.End.Line == v.Start.Line && v.End.Col < v.Start.Col {
			v.Start, v.End = v.End, v.Start
		}
		r.Visual = v
	}

	if len(q.Regs) > 0 {
		r.Regs = map[rune]*string{}
	}
	for _, name := range q.Regs {
		typ, err := e.c.call("nvim_call_function", "getregtype", []any{string(name)})
		if err != nil {
			return r, err
		}
		if typ == "" {
			r.Regs[name] = nil
			continue
		}
		v, err := e.c.call("nvim_call_function", "getreg", []any{string(name)})
		if err != nil {
			return r, err
		}
		s, _ := v.(string)
		r.Regs[name] = &s
	}

	if len(q.Marks) > 0 {
		r.Marks = map[rune]*vimtest.Pos{}
	}
	for _, name := range q.Marks {
		if r.Marks[name], err = e.getpos("'" + string(name)); err != nil {
			return r, err
		}
	}

	if q.Jumps {
		v, err := e.c.call("nvim_call_function", "getjumplist", []any{})
		if err != nil {
			return r, err
		}
		v2, err := e.c.call("nvim_eval", "bufnr('%')")
		if err != nil {
			return r, err
		}
		cur, _ := v2.(int64)
		r.Jumps = []vimtest.Jump{}
		if l, ok := v.([]any); ok && len(l) > 0 {
			entries, _ := l[0].([]any)
			for _, en := range entries {
				m, _ := en.(map[string]any)
				line, _ := m["lnum"].(int64)
				col, _ := m["col"].(int64)
				buf, _ := m["bufnr"].(int64)
				j := vimtest.Jump{Pos: vimtest.Pos{Line: int(line) - 1, Col: int(col)}}
				if buf != cur {
					j.Buf = int(buf)
				}
				r.Jumps = append(r.Jumps, j)
			}
		}
	}

	if q.Win {
		v, err := e.c.call("nvim_eval", "[winnr(), winnr('$')]")
		if err != nil {
			return r, err
		}
		w, _ := v.([]any)
		cur, _ := w[0].(int64)
		count, _ := w[1].(int64)
		r.Win = vimtest.Win{Cur: int(cur), Count: int(count)}
	}

	if q.Top {
		v, err := e.c.call("nvim_eval", "line('w0')")
		if err != nil {
			return r, err
		}
		top, _ := v.(int64)
		r.Top = int(top)
	}

	if q.Left {
		v, err := e.c.call("nvim_eval", "winsaveview().leftcol")
		if err != nil {
			return r, err
		}
		left, _ := v.(int64)
		r.Left = int(left)
	}

	if q.Bufs {
		v, err := e.c.call("nvim_eval", `join(map(getbufinfo({'buflisted': 1}), {_, b -> b.bufnr .. (b.bufnr == bufnr('%') ? '%' : b.bufnr == bufnr('#') ? '#' : '')}))`)
		if err != nil {
			return r, err
		}
		r.Bufs, _ = v.(string)
	}

	if q.Msg {
		v, err := e.c.call("nvim_exec2", "messages", map[string]any{"output": true})
		if err != nil {
			return r, err
		}
		out, _ := v.(map[string]any)["output"].(string)
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		r.Msg = lines[len(lines)-1]
	}
	return r, nil
}

func (e *engine) cursor() (vimtest.Pos, error) {
	v, err := e.c.call("nvim_win_get_cursor", 0)
	if err != nil {
		return vimtest.Pos{}, err
	}
	rc, _ := v.([]any)
	if len(rc) != 2 {
		return vimtest.Pos{}, fmt.Errorf("nvim_win_get_cursor: %v", v)
	}
	line, _ := rc[0].(int64)
	col, _ := rc[1].(int64)
	return vimtest.Pos{Line: int(line) - 1, Col: int(col)}, nil
}

// getpos reads a position with getpos(); nil when it's unset.
func (e *engine) getpos(expr string) (*vimtest.Pos, error) {
	v, err := e.c.call("nvim_call_function", "getpos", []any{expr})
	if err != nil {
		return nil, err
	}
	p, _ := v.([]any)
	if len(p) < 3 {
		return nil, fmt.Errorf("getpos(%q): %v", expr, v)
	}
	line, _ := p[1].(int64)
	col, _ := p[2].(int64)
	if line == 0 {
		return nil, nil
	}
	return &vimtest.Pos{Line: int(line) - 1, Col: int(col) - 1}, nil
}
