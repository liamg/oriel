package lang

import (
	"cmp"
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

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/kit/vimreg"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/term"
	"github.com/liamg/oriel/term/ghostty"
	"github.com/liamg/oriel/term/pty"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/ui/layout"
	"github.com/liamg/oriel/ui/layout/panes"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/vim"
)

// TerminalView is a terminal: one instance per terminal, placed anywhere a view can be.
const TerminalView = kit.TerminalView

// The terminal's key modes: keys go to the program, or in terminal-normal mode to vim over the scrollback.
const (
	TerminalKeyMode       = "terminal"
	TerminalNormalKeyMode = "terminal-normal"
)

// Terminals are the window's terminals: a PTY each, Ghostty's VT parsing what it prints, drawn by the terminal view.
type Terminals struct {
	kit.Disposer
	CacheDir, WorkDir string
	SavedAt           string         // where the screens are kept for the work dir
	Clipboard         edit.Clipboard // the system clipboard, for copying from the scrollback
	h                 plugin.Host

	mu    sync.Mutex
	terms map[string]*terminal
	next  int
	saved map[string][]byte // screens kept when the app stopped, by instance
	busy  sync.WaitGroup
	paced kit.PacedRedraw // output's frames
}

// TermSource is what a terminal shows and types into: a program in a PTY, or a stream from elsewhere, such as an
// agent's terminal. term.open takes one as {source}.
type TermSource interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Resize(cols, rows int) error
	Close() error
}

// terminal is one: its program, its VT, and the vim engine terminal-normal mode reads the scrollback with.
type terminal struct {
	id     string
	cmd    []string
	name   string        // the user's name for it, in place of the program's
	rename widget.Rename // its name being edited, over it
	own    bool          // a program it started, rather than a source it was given: it can start again
	// sandboxed says it runs in the session's sandbox, beside its agent: a shell of the backend's, not cmd's program.
	sandboxed bool
	emu       term.Emulator
	src       TermSource
	cols      int
	rows      int
	done      bool
	err       error

	sel *termSel // what the mouse selected

	normal  bool
	ed      *edit.Editor
	vim     *vim.Engine
	cells   [][]term.Cell // each terminal-normal line's cells, for its colours
	bracket string        // [ or ] typed in terminal-normal mode, waiting for its twin
}

func (x *Terminals) Manifest() plugin.Manifest { return plugin.Manifest{ID: "terminal"} }

func (x *Terminals) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	x.paced.Redraw = h.Redraw
	x.mu.Lock()
	if x.terms == nil {
		x.terms = map[string]*terminal{}
	}
	if x.saved == nil {
		x.saved = map[string][]byte{}
		x.loadSaved()
	}
	x.mu.Unlock()
	if x.CacheDir != "" {
		ghostty.SetCacheDir(filepath.Join(x.CacheDir, "wasm"))
	}
	x.Add(h.Option(registry.OptionSpec{Name: "terminal.shell", Type: registry.String, Default: "$SHELL", Scopes: registry.ScopeUser | registry.ScopeProject,
		Help: "The program a new terminal runs, with its arguments; $VARS expand, so $SHELL is your login shell."}))
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
			place := cmp.Or(a.String("place"), "dock")
			return open(place, a.String("side"))(ctx, a)
		}},
		{ID: "term", Title: "Open a terminal; term! in the main area", Run: open("dock", "")},
		{ID: "term.host", Title: "Open a terminal on this machine, outside the session's sandbox, in {dir} (the window's directory by default)",
			Run: func(ctx registry.Ctx, a registry.Args) error {
				b := registry.Args{"host": true}
				for k, v := range a {
					b[k] = v
				}
				return open(cmp.Or(a.String("place"), "dock"), a.String("side"))(ctx, b)
			}},
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
			kit.CopyText(x.h, out)
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
			line := commandLine(t.emu, c)
			if line == "" {
				return fmt.Errorf("the last command is empty")
			}
			return x.open(ctx, registry.Args{"cmd": []string{"sh", "-c", line + "; exec " + x.shell()[0]}}, "split", "down")
		}},
	} {
		x.Add(h.Command(c))
	}
	x.Add(h.Keymap(registry.Keymap{Mode: "global", Keys: "<C-'>", Command: "term.open", Desc: "Terminal"}))
	x.Add(h.Keymap(registry.Keymap{Mode: TerminalNormalKeyMode, Keys: "q", Command: "view.close", Desc: "Close"}))
	x.Add(h.Keymap(registry.Keymap{Mode: TerminalKeyMode, Keys: "<C-Esc>", Command: "term.normal", Desc: "Terminal-normal mode"}))
	x.Add(h.Keymap(registry.Keymap{Mode: TerminalKeyMode, Keys: `<C-\><C-n>`, Command: "term.normal", Desc: "Terminal-normal mode"}))
	x.Add(h.Contribute(registry.RawKeys{Mode: TerminalKeyMode}))
	x.renaming(h)
	x.Add(h.Contribute(registry.MenuItem{Name: "term.fix", Label: "Ask the agent to fix the failed command", Group: "agent", Order: 10,
		When: registry.MenuWhen{View: TerminalView}, Command: registry.FixCommand, Trouble: x.failedCommand, Source: "builtin:terminal"}))
	x.Add(h.View(TerminalView, terminalView{x}))
	return nil
}

// Deactivate keeps every terminal's screen, so they come back when the app starts again on this directory, and stops
// their programs.
func (x *Terminals) Deactivate(ctx context.Context) error {
	x.mu.Lock()
	terms := x.terms
	x.terms = map[string]*terminal{}
	x.mu.Unlock()
	for id, t := range terms {
		if snap, err := t.emu.Snapshot(); err == nil {
			x.saved[id] = snap
		}
		t.stop()
	}
	x.storeSaved()
	x.busy.Wait()
	x.paced.Stop()
	return x.Disposer.Deactivate(ctx)
}

func (x *Terminals) savedPath() string { return x.SavedAt }

func (x *Terminals) loadSaved() {
	if p := x.savedPath(); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			json.Unmarshal(b, &x.saved)
		}
	}
}

func (x *Terminals) storeSaved() {
	if p := x.savedPath(); p != "" && len(x.saved) > 0 {
		os.MkdirAll(filepath.Dir(p), 0o755)
		if b, err := json.Marshal(x.saved); err == nil {
			os.WriteFile(p, b, 0o600)
		}
	}
}

func (x *Terminals) get(id string) *terminal {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.terms[id]
}

func (x *Terminals) focused() *terminal {
	if f := x.h.Layout().Focus; f != nil && f.ID == TerminalView {
		return x.get(f.Instance)
	}
	return nil
}

func (x *Terminals) focusedOrLast() *terminal {
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

// sandboxed says a terminal opened with a runs a shell in the sandbox of the session in front, beside its agent:
// one with no program or source of its own while that session is sandboxed. Others run here.
func (x *Terminals) sandboxed(a registry.Args) (host.Access, core.SessionID, bool) {
	if src, _ := a["source"].(TermSource); src != nil || len(strList(a["cmd"])) > 0 || len(strList(a["args"])) > 0 || a.Bool("host") {
		return nil, "", false
	}
	w, _ := kit.SessionHost(x.h)
	if w == nil {
		return nil, "", false
	}
	se, ok := x.h.State().Session(x.h.State().Focus)
	if !ok || !se.Snapshot().Agent.Sandboxed {
		return nil, "", false
	}
	return w, se.Snapshot().ID, true
}

// shell is what a new terminal runs: terminal.shell, else $SHELL, else sh.
func (x *Terminals) shell() []string {
	if v, _, err := x.h.Options().Get("terminal.shell", x.h.Here()); err == nil {
		if s, _ := v.(string); strings.TrimSpace(os.ExpandEnv(s)) != "" {
			return strings.Fields(os.ExpandEnv(s))
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
func (x *Terminals) open(ctx registry.Ctx, a registry.Args, place, side string) error {
	if w, se, ok := x.sandboxed(a); ok {
		dir := x.WorkDir
		return plugin.RunSlow(ctx, x.h, plugin.Slow{ID: "term.open.sandbox", Doing: "Opening a terminal in the sandbox"},
			func(ctx context.Context) (sandbox.Command, error) {
				c, err := w.SandboxShell(ctx, se, dir)
				if err != nil {
					return c, fmt.Errorf("%w; term.host opens a terminal on this machine instead", err)
				}
				return c, nil
			},
			func(ctx registry.Ctx, c sandbox.Command) error { return x.show(ctx, a, &c, place, side) })
	}
	return x.show(ctx, a, nil, place, side)
}

// show starts a terminal, in the sandbox's shell sb if not nil, and opens its view.
func (x *Terminals) show(ctx registry.Ctx, a registry.Args, sb *sandbox.Command, place, side string) error {
	id, err := x.start(a, sb)
	if err != nil {
		return err
	}
	args := registry.Args{"id": TerminalView, "instance": id, "place": place}
	switch place {
	case "dock":
		args["edge"], args["size"] = "bottom", 260
	case "split":
		args["side"] = cmp.Or(side, "right")
	}
	if err := x.h.Run(ctx, "view.open", args); err != nil {
		return err
	}
	x.h.Layout().Show(layout.ViewRef{ID: TerminalView, Instance: id})
	return nil
}

// start starts a terminal, with its screen from before the app last stopped when a["instance"] had one, running sb
// when it's not nil, and returns its instance.
func (x *Terminals) start(a registry.Args, sb *sandbox.Command) (string, error) {
	src, _ := a["source"].(TermSource)
	cmdline := strList(a["cmd"])
	if len(cmdline) == 0 {
		cmdline = strList(a["args"])
	}
	if title := a.String("title"); src != nil && title != "" {
		cmdline = []string{title}
	}
	var env []string
	sandboxed := false
	if sb != nil {
		cmdline, env, sandboxed = sb.Argv, sb.Env, true
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
	t := &terminal{id: id, cmd: cmdline, cols: 100, rows: 24, own: src == nil, sandboxed: sandboxed}
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
		cmd.Dir = cmp.Or(a.String("dir"), x.WorkDir)
		if env == nil {
			env = os.Environ()
		}
		cmd.Env = append(env, "TERM=xterm-256color", "COLORTERM=truecolor", "TERM_PROGRAM=oriel")
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
	before := x.terms[id]
	x.terms[id] = t
	x.mu.Unlock()
	if before != nil {
		before.stop()
	}
	x.busy.Add(1)
	go x.read(t)
	return id, nil
}

// stop ends t's source, and its program if it started one, so its reader returns.
func (t *terminal) stop() {
	t.src.Close()
	if p, ok := t.src.(*pty.PTY); ok && p.Cmd().Process != nil {
		p.Cmd().Process.Kill()
	}
	t.emu.Close()
}

// read feeds the program's output to the VT until it exits.
func (x *Terminals) read(t *terminal) {
	defer x.busy.Done()
	buf := make([]byte, 64<<10)
	for {
		n, err := t.src.Read(buf)
		if n > 0 {
			t.emu.Write(buf[:n])
			x.paced.Ask()
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
func (x *Terminals) enterNormal(t *terminal) {
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
	if x.Clipboard != nil {
		t.ed.SetClipboard(x.Clipboard)
	}
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
func (x *Terminals) normalLine(t *terminal) int {
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

// commandLine is what was typed at a command's prompt.
func commandLine(emu term.Emulator, c term.Command) string {
	cols, _ := emu.Size()
	return strings.TrimSpace(emu.Text(c.Input, term.Point{X: cols - 1, Y: c.Input.Y}))
}

// failedCommand is the terminal's last command, if it failed: what was typed, what it printed, and how it exited.
func (x *Terminals) failedCommand(at registry.MenuCtx) (registry.Trouble, bool) {
	t := x.get(at.Instance)
	if t == nil {
		return registry.Trouble{}, false
	}
	cmds := t.emu.Commands()
	for i := len(cmds) - 1; i >= 0; i-- {
		c := cmds[i]
		if !c.Finished {
			continue
		}
		if c.ExitCode == 0 || c.Input.Y < 0 {
			return registry.Trouble{}, false
		}
		line := commandLine(t.emu, c)
		var b strings.Builder
		if cwd := t.emu.Cwd(); cwd != "" {
			fmt.Fprintf(&b, "In %s:\n", cwd)
		}
		fmt.Fprintf(&b, "$ %s\n", line)
		if c.Output.Y >= 0 && c.End.Y >= 0 {
			b.WriteString(strings.TrimRight(term.OutputText(t.emu, c), "\n") + "\n")
		}
		fmt.Fprintf(&b, "(exit status %d)", c.ExitCode)
		return registry.Trouble{Kind: "command", Subject: fmt.Sprintf("`%s` exited %d", line, c.ExitCode), Context: b.String(),
			Fix: fmt.Sprintf("Fix the failing command `%s`: it exited %d.", line, c.ExitCode)}, true
	}
	return registry.Trouble{}, false
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
	if t.name != "" {
		if t.done {
			return t.name + " (exited)"
		}
		return t.name
	}
	name := filepath.Base(t.cmd[0])
	if t.sandboxed {
		name = "sandbox"
	}
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

// renaming is Rename… on a terminal's tab, and term.rename behind it.
func (x *Terminals) renaming(h plugin.Host) {
	x.Add(h.Command(registry.Command{ID: "term.rename", Title: "Rename terminal {instance}, the one in front by default, to {name}; with no {name}, edit it over the terminal; an empty one puts back the program's",
		Run: func(_ registry.Ctx, a registry.Args) error {
			t := x.focusedOrLast()
			if i := a.String("instance"); i != "" {
				t = x.get(i)
			}
			if t == nil {
				return errors.New("no terminal is open")
			}
			if name, ok := a["name"].(string); ok {
				t.name = strings.TrimSpace(name)
				return nil
			}
			t.rename.Start(t.title())
			return nil
		}}))
	x.Add(h.Contribute(registry.MenuItem{Name: "term.rename", Label: "Rename…", Group: "pane", Order: 80,
		When: registry.MenuWhen{View: TerminalView, Help: panes.HelpTab}, Source: "builtin:terminal",
		Run: func(ctx registry.Ctx, at registry.MenuCtx) error {
			return h.Run(ctx, "term.rename", registry.Args{"instance": at.Instance})
		}}))
}
