package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/term"
	"github.com/liamg/oriel/term/ghostty"
	"github.com/liamg/oriel/term/pty"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/ui/layout"
	"github.com/liamg/oriel/vim"
	"github.com/liamg/oriel/vim/vimreg"
)

// TerminalView is a terminal (ide.md, "Terminals"): one instance per terminal, placed anywhere a view can be.
const TerminalView = "terminal"

// The terminal's key modes: keys go to the program, or in terminal-normal mode to vim over the scrollback.
const (
	TerminalKeyMode       = "terminal"
	TerminalNormalKeyMode = "terminal-normal"
)

// terminals are the window's terminals: a PTY each, Ghostty's VT parsing what it prints, drawn by the terminal view.
type terminals struct {
	disposer
	sh *shell
	h  ext.Host

	mu    sync.Mutex
	terms map[string]*terminal
	next  int
	saved map[string][]byte // screens kept when the app stopped, by instance
	busy  sync.WaitGroup
}

// TermSource is what a terminal shows and types into: a program in a PTY, or a stream from elsewhere, such as an
// agent's terminal on a worker. term.open takes one as {source}.
type TermSource interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Resize(cols, rows int) error
	Close() error
}

// terminal is one: its program, its VT, and the vim engine terminal-normal mode reads the scrollback with.
type terminal struct {
	id   string
	cmd  []string
	own  bool // a program it started, rather than a source it was given: it can start again
	emu  term.Emulator
	src  TermSource
	cols int
	rows int
	done bool
	err  error

	normal  bool
	ed      *edit.Editor
	vim     *vim.Engine
	cells   [][]term.Cell // each terminal-normal line's cells, for its colours
	bracket string        // [ or ] typed in terminal-normal mode, waiting for its twin
}

func (x *terminals) Manifest() ext.Manifest { return ext.Manifest{ID: "terminal"} }

func (x *terminals) Activate(_ context.Context, h ext.Host) error {
	x.h = h
	x.mu.Lock()
	if x.terms == nil {
		x.terms = map[string]*terminal{}
	}
	if x.saved == nil {
		x.saved = map[string][]byte{}
		x.loadSaved()
	}
	x.mu.Unlock()
	if x.sh.cfg.CacheDir != "" {
		ghostty.SetCacheDir(filepath.Join(x.sh.cfg.CacheDir, "wasm"))
	}
	x.add(h.Option(registry.OptionSpec{Name: "terminal.shell", Type: registry.String, Default: "", Scopes: registry.ScopeUser | registry.ScopeProject,
		Help: "The program a new terminal runs; your $SHELL if empty."}))
	open := func(place, side string) func(registry.Ctx, registry.Args) error {
		return func(ctx registry.Ctx, a registry.Args) error {
			if a == nil {
				a = registry.Args{}
			}
			if a.Bool("bang") && place == "dock" {
				return x.open(ctx, a, "tab", "")
			}
			return x.open(ctx, a, place, side)
		}
	}
	for _, c := range []registry.Command{
		{ID: "term.open", Title: "Open a terminal: {cmd}, {env}, {place}, {title}, or a {source} to show; with {bang}, in the main area", Run: func(ctx registry.Ctx, a registry.Args) error {
			place := cmpOr(a.String("place"), "dock")
			return open(place, a.String("side"))(ctx, a)
		}},
		{ID: "term", Title: "Open a terminal; term! in the main area", Run: open("dock", "")},
		{ID: "vterm", Title: "Open a terminal in a split to the right", Run: open("split", "right")},
		{ID: "sterm", Title: "Open a terminal in a split below", Run: open("split", "down")},
		{ID: "term.normal", Title: "Terminal-normal mode: vim's motions over the scrollback", Run: func(registry.Ctx, registry.Args) error {
			if t := x.focused(); t != nil {
				x.enterNormal(t)
			}
			return nil
		}},
		{ID: "term.copy_last_output", Title: "Copy the last command's output", Run: func(registry.Ctx, registry.Args) error {
			t := x.focusedOrLast()
			if t == nil {
				return fmt.Errorf("no terminal")
			}
			out := term.LastOutput(t.emu)
			if out == "" {
				return fmt.Errorf("no command output yet (the shell needs OSC 133 marks)")
			}
			x.sh.desktop.SetClipboard(out)
			return nil
		}},
		{ID: "term.send_last_output", Title: "Send the last command's output to a request",
			Available: func(registry.Args) bool { return h.Available("request.attach", nil) },
			Run: func(ctx registry.Ctx, _ registry.Args) error {
				t := x.focusedOrLast()
				if t == nil {
					return fmt.Errorf("no terminal")
				}
				return h.Run(ctx, "request.attach", registry.Args{"text": term.LastOutput(t.emu), "title": "terminal output"})
			}},
		{ID: "term.rerun_last", Title: "Run the last command again, in a new split", Run: func(ctx registry.Ctx, _ registry.Args) error {
			t := x.focusedOrLast()
			if t == nil {
				return fmt.Errorf("no terminal")
			}
			cmds := t.emu.Commands()
			if len(cmds) == 0 {
				return fmt.Errorf("no command yet")
			}
			c := cmds[len(cmds)-1]
			line := strings.TrimSpace(t.emu.Text(c.Input, term.Point{X: 1 << 16, Y: c.Input.Y}))
			if line == "" {
				return fmt.Errorf("the last command is empty")
			}
			return x.open(ctx, registry.Args{"cmd": []string{"sh", "-c", line + "; exec " + x.shell()[0]}}, "split", "down")
		}},
	} {
		x.add(h.Command(c))
	}
	x.add(h.Keymap(registry.Keymap{Mode: "global", Keys: "<C-'>", Command: "term.open", Desc: "Terminal"}))
	x.add(h.Keymap(registry.Keymap{Mode: TerminalNormalKeyMode, Keys: "q", Command: "view.close", Desc: "Close"}))
	x.add(h.Keymap(registry.Keymap{Mode: TerminalKeyMode, Keys: "<C-Esc>", Command: "term.normal", Desc: "Terminal-normal mode"}))
	x.add(h.Keymap(registry.Keymap{Mode: TerminalKeyMode, Keys: `<C-\><C-n>`, Command: "term.normal", Desc: "Terminal-normal mode"}))
	x.add(h.Contribute(registry.RawKeys{Mode: TerminalKeyMode}))
	x.add(h.View(TerminalView, terminalView{x}))
	return nil
}

// Deactivate keeps every terminal's screen, so they come back when the app starts again on this directory, and stops
// their programs.
func (x *terminals) Deactivate(ctx context.Context) error {
	x.mu.Lock()
	terms := x.terms
	x.terms = map[string]*terminal{}
	x.mu.Unlock()
	for id, t := range terms {
		if snap, err := t.emu.Snapshot(); err == nil {
			x.saved[id] = snap
		}
		t.src.Close()
		if p, ok := t.src.(*pty.PTY); ok && p.Cmd().Process != nil {
			p.Cmd().Process.Kill()
		}
		t.emu.Close()
	}
	x.storeSaved()
	x.busy.Wait()
	return x.disposer.Deactivate(ctx)
}

func (x *terminals) savedPath() string { return x.sh.dirState("terminals") }

func (x *terminals) loadSaved() {
	if p := x.savedPath(); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			json.Unmarshal(b, &x.saved)
		}
	}
}

func (x *terminals) storeSaved() {
	if p := x.savedPath(); p != "" && len(x.saved) > 0 {
		os.MkdirAll(filepath.Dir(p), 0o755)
		if b, err := json.Marshal(x.saved); err == nil {
			os.WriteFile(p, b, 0o600)
		}
	}
}

func (x *terminals) get(id string) *terminal {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.terms[id]
}

func (x *terminals) focused() *terminal {
	if f := x.h.Layout().Focus; f != nil && f.ID == TerminalView {
		return x.get(f.Instance)
	}
	return nil
}

func (x *terminals) focusedOrLast() *terminal {
	if t := x.focused(); t != nil {
		return t
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	var last *terminal
	for _, t := range x.terms {
		if last == nil || t.id > last.id {
			last = t
		}
	}
	return last
}

// shell is what a new terminal runs: terminal.shell, else $SHELL, else sh.
func (x *terminals) shell() []string {
	if v, _, err := x.h.Options().Get("terminal.shell", x.h.Here()); err == nil {
		if s, _ := v.(string); strings.TrimSpace(s) != "" {
			return strings.Fields(s)
		}
	}
	if s := os.Getenv("SHELL"); s != "" {
		return []string{s}
	}
	return []string{"sh"}
}

func strList(v any) []string {
	switch v := v.(type) {
	case []string:
		return v
	case []any:
		var out []string
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return strings.Fields(v)
	}
	return nil
}

// open starts a terminal and shows it.
func (x *terminals) open(ctx registry.Ctx, a registry.Args, place, side string) error {
	id, err := x.start(a)
	if err != nil {
		return err
	}
	args := registry.Args{"id": TerminalView, "instance": id, "place": place}
	switch place {
	case "dock":
		args["edge"], args["size"] = "bottom", 260
	case "split":
		args["side"] = cmpOr(side, "right")
	}
	if err := x.h.Run(ctx, "view.open", args); err != nil {
		return err
	}
	x.h.Layout().Show(layout.ViewRef{ID: TerminalView, Instance: id})
	return nil
}

// start starts a terminal, with its screen from before the app last stopped when a["instance"] had one, and
// returns its instance.
func (x *terminals) start(a registry.Args) (string, error) {
	src, _ := a["source"].(TermSource)
	cmdline := strList(a["cmd"])
	if len(cmdline) == 0 {
		cmdline = strList(a["args"])
	}
	if title := a.String("title"); src != nil && title != "" {
		cmdline = []string{title}
	}
	if len(cmdline) == 0 {
		cmdline = x.shell()
	}
	x.mu.Lock()
	id := a.String("instance")
	if id == "" {
		x.next++
		id = strconv.Itoa(x.next)
		for x.terms[id] != nil {
			x.next++
			id = strconv.Itoa(x.next)
		}
	}
	saved := x.saved[id]
	delete(x.saved, id)
	x.mu.Unlock()
	t := &terminal{id: id, cmd: cmdline, cols: 100, rows: 24, own: src == nil}
	emu, err := ghostty.New(t.cols, t.rows, ghostty.Options{Reply: func(p []byte) {
		if t.src != nil {
			t.src.Write(p)
		}
	}})
	if err != nil {
		return "", err
	}
	if saved != nil {
		if err := emu.Restore(saved); err != nil {
			x.h.Log().Warn("terminal restore", "err", err)
		}
	}
	t.emu = emu
	if src == nil {
		cmd := exec.Command(cmdline[0], cmdline[1:]...)
		cmd.Dir = x.sh.cfg.WorkDir
		cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor", "TERM_PROGRAM=oriel")
		cmd.Env = append(cmd.Env, strList(a["env"])...)
		p, err := pty.Start(cmd, t.cols, t.rows)
		if err != nil {
			emu.Close()
			return "", fmt.Errorf("terminal: %w", err)
		}
		src = p
	} else {
		src.Resize(t.cols, t.rows)
	}
	t.src = src
	x.mu.Lock()
	x.terms[id] = t
	x.mu.Unlock()
	x.busy.Add(1)
	go x.read(t)
	return id, nil
}

// read feeds the program's output to the VT until it exits.
func (x *terminals) read(t *terminal) {
	defer x.busy.Done()
	buf := make([]byte, 64<<10)
	var last time.Time
	for {
		n, err := t.src.Read(buf)
		if n > 0 {
			t.emu.Write(buf[:n])
			if now := time.Now(); now.Sub(last) > 4*time.Millisecond {
				last = now
				x.h.Redraw()
			}
		}
		if err != nil {
			if p, ok := t.src.(*pty.PTY); ok {
				p.Cmd().Wait()
			}
			x.h.Do(func() {
				if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) && !errors.Is(err, io.ErrClosedPipe) {
					t.err = err
				}
				t.done = true
			})
			return
		}
	}
}

// enterNormal puts the scrollback and screen into a read-only buffer under a vim engine, the cursor where the
// terminal's is.
func (x *terminals) enterNormal(t *terminal) {
	sb := t.emu.Scrollbar()
	cols, _ := t.emu.Size()
	all := t.emu.Text(term.Point{}, term.Point{X: cols - 1, Y: max(sb.Total-1, 0)})
	lines := strings.Split(all, "\n")
	for len(lines) > 1 && strings.TrimRight(lines[len(lines)-1], " ") == "" {
		lines = lines[:len(lines)-1]
	}
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	t.cells = styledLines(t.emu)
	for i := range min(len(t.cells), len(lines)) {
		if cellsText(t.cells[i]) != lines[i] {
			t.cells[i] = nil // drawn plain rather than out of step with the text
		}
	}
	buf := text.New([]byte(strings.Join(lines, "\n")), text.Author{})
	t.ed = edit.New(buf, text.Author{Kind: text.User})
	t.ed.SetClipboard(desktopClipboard{x.sh})
	t.vim = vim.New(t.ed)
	t.vim.SetSettings(vimreg.Settings{Options: x.h.Options(), At: func(string, string) registry.At { return x.h.Here() }})
	cs := t.emu.CursorState()
	line := sb.Offset + cs.ViewY
	t.vim.SetCursor(vim.Pos{Line: min(line, len(lines)-1), Col: cs.ViewX})
	t.normal = true
}

// styledLines are the cells of every row of the scrollback and screen, soft-wrapped rows joined into one line as
// Emulator.Text joins them. The viewport is moved over the scrollback to read them, and put back.
func styledLines(emu term.Emulator) [][]term.Cell {
	sb := emu.Scrollbar()
	defer emu.Scroll(term.Scroll{To: term.ScrollRow, Row: sb.Offset})
	var out [][]term.Cell
	joining := false
	for row := 0; row < sb.Total; {
		emu.Scroll(term.Scroll{To: term.ScrollRow, Row: row})
		off := emu.Scrollbar().Offset
		g := emu.Grid()
		for i, l := range g.Lines {
			if off+i < row {
				continue
			}
			if joining {
				out[len(out)-1] = append(out[len(out)-1], l.Cells...)
			} else {
				out = append(out, slices.Clone(l.Cells))
			}
			joining = l.Wrapped
		}
		row = off + len(g.Lines)
		if len(g.Lines) == 0 {
			break
		}
	}
	return out
}

func cellsText(cells []term.Cell) string {
	var b strings.Builder
	for _, c := range cells {
		switch {
		case c.Width == term.SpacerTail || c.Width == term.SpacerHead:
		case c.Text == "":
			b.WriteByte(' ')
		default:
			b.WriteString(c.Text)
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// normalLine is the terminal-normal cursor's row, from the top of the scrollback.
func (x *terminals) normalLine(t *terminal) int {
	if t.vim == nil {
		return -1
	}
	return t.vim.Cursor().Line
}

// jumpPrompt moves the terminal-normal cursor to the previous or next command's prompt.
func (t *terminal) jumpPrompt(dir int) {
	line := t.vim.Cursor().Line
	best := -1
	for _, c := range t.emu.Commands() {
		y := c.Prompt.Y
		if y < 0 {
			continue
		}
		if dir < 0 && y < line && y > best || dir > 0 && y > line && (best < 0 || y < best) {
			best = y
		}
	}
	if best >= 0 {
		t.vim.SetCursor(vim.Pos{Line: best})
	}
}

// exitDots are how each command ended, by the row of its prompt: "ok", "failed", or "running".
func (t *terminal) exitDots() map[int]string {
	out := map[int]string{}
	for _, c := range t.emu.Commands() {
		if c.Prompt.Y < 0 || !c.Finished && c.Output.Y < 0 {
			continue
		}
		switch {
		case !c.Finished:
			out[c.Prompt.Y] = "running"
		case c.ExitCode == 0:
			out[c.Prompt.Y] = "ok"
		default:
			out[c.Prompt.Y] = "failed"
		}
	}
	return out
}

// title is the tab's: the program, and the directory the shell says it's in.
func (t *terminal) title() string {
	name := filepath.Base(t.cmd[0])
	if s := t.emu.Title(); s != "" {
		name = s
	}
	if cwd := t.emu.Cwd(); cwd != "" {
		name += " · " + filepath.Base(cwd)
	}
	if t.done {
		name += " (exited)"
	}
	return name
}
