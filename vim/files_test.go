package vim_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/registry/keymap"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/vim"
	"github.com/liamg/oriel/vim/vimtest"
	"github.com/liamg/oriel/vim/vimtest/nvim"
)

// host is the disk under dir, recording quits and the commands the IDE was asked to show.
type host struct {
	vim.Disk
	quits    int
	commands []string
}

func (h *host) Quit() { h.quits++ }

func (h *host) Command(name, args string, bang bool) bool {
	if name == "ls" || name == "copen" || name == "messages" {
		h.commands = append(h.commands, name)
		return true
	}
	return false
}

type session struct {
	t  *testing.T
	ed *edit.Editor
	e  *vim.Engine
	h  *host
}

func newSession(t *testing.T, dir string) *session {
	ed := edit.New(text.New(nil, text.Author{}), text.Author{Kind: text.User})
	e := vim.New(ed)
	h := &host{Disk: vim.Disk{Dir: dir}}
	e.SetHost(h)
	return &session{t: t, ed: ed, e: e, h: h}
}

func (s *session) keys(k string) {
	for _, key := range keymap.Parse(k) {
		s.e.Key(key)
	}
}

func (s *session) notice() string { return s.e.LastNotice().ID }

func writeFile(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// editing.txt *:w*: "Write the whole buffer to the current file."
func TestWriteAndModified(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "one\ntwo\n")
	s := newSession(t, dir)
	s.keys(":e a.txt<CR>")
	if s.e.Modified() || s.e.Name() != "a.txt" {
		t.Fatalf("after :e: modified %v, name %q", s.e.Modified(), s.e.Name())
	}
	s.keys("ddu")
	if s.e.Modified() {
		t.Error("undoing back to the file's text left the buffer modified")
	}
	s.keys("dd")
	if !s.e.Modified() {
		t.Error("dd didn't modify the buffer")
	}
	s.keys(":q<CR>")
	if s.notice() != "vim.unsaved" || s.h.quits != 0 {
		t.Errorf(":q on changes: notice %q, quits %d", s.notice(), s.h.quits)
	}
	s.keys(":w<CR>")
	if s.notice() != "vim.written" || s.e.Modified() || readFile(t, filepath.Join(dir, "a.txt")) != "two\n" {
		t.Errorf(":w: notice %q, modified %v, file %q", s.notice(), s.e.Modified(), readFile(t, filepath.Join(dir, "a.txt")))
	}
	s.keys("u")
	if !s.e.Modified() {
		t.Error("undoing past the written text should modify the buffer")
	}
	s.keys("<C-r>")
	if s.e.Modified() {
		t.Error("redoing back to the written text should clear modified")
	}
	s.keys(":q<CR>")
	if s.h.quits != 1 {
		t.Errorf(":q on a written buffer didn't quit")
	}
}

func TestWriteCommands(t *testing.T) {
	dir := t.TempDir()
	p := func(n string) string { return filepath.Join(dir, n) }
	writeFile(t, p("a.txt"), "a\n")
	writeFile(t, p("taken.txt"), "keep\n")
	s := newSession(t, dir)

	s.keys(":w<CR>")
	if s.notice() != "vim.write-no-name" {
		t.Errorf(":w with no name: %q", s.notice())
	}
	s.keys("inew<Esc>:w new.txt<CR>")
	if readFile(t, p("new.txt")) != "new\n" || s.e.Name() != "new.txt" || s.e.Modified() {
		t.Errorf(":w name on an unnamed buffer: file %q, name %q", readFile(t, p("new.txt")), s.e.Name())
	}
	s.keys(":e a.txt<CR>Ab<Esc>:w taken.txt<CR>")
	if s.notice() != "vim.file-exists" || readFile(t, p("taken.txt")) != "keep\n" {
		t.Errorf(":w over another file: %q", s.notice())
	}
	s.keys(":w! taken.txt<CR>")
	if readFile(t, p("taken.txt")) != "ab\n" || s.e.Name() != "a.txt" || !s.e.Modified() {
		t.Errorf(":w! other: file %q, name %q, modified %v", readFile(t, p("taken.txt")), s.e.Name(), s.e.Modified())
	}
	s.keys(":sav copy.txt<CR>")
	if readFile(t, p("copy.txt")) != "ab\n" || s.e.Name() != "copy.txt" || s.e.Modified() {
		t.Errorf(":sav: name %q", s.e.Name())
	}
	s.keys(":e a.txt<CR>Ac<Esc>:e new.txt<CR>Ax<Esc>:wa<CR>")
	if readFile(t, p("a.txt")) != "ac\n" || readFile(t, p("new.txt")) != "newx\n" {
		t.Errorf(":wa: %q %q", readFile(t, p("a.txt")), readFile(t, p("new.txt")))
	}
	s.keys("Ay<Esc>:x<CR>")
	if readFile(t, p("new.txt")) != "newxy\n" || s.h.quits != 1 {
		t.Errorf(":x: %q, quits %d", readFile(t, p("new.txt")), s.h.quits)
	}
	s.keys("Az<Esc>ZQ")
	if s.h.quits != 2 || readFile(t, p("new.txt")) != "newxy\n" {
		t.Errorf("ZQ: quits %d", s.h.quits)
	}
	s.keys("ZZ")
	if readFile(t, p("new.txt")) != "newxyz\n" || s.h.quits != 3 {
		t.Errorf("ZZ: %q, quits %d", readFile(t, p("new.txt")), s.h.quits)
	}
}

func TestQuitAllWithUnsaved(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "a\n")
	writeFile(t, filepath.Join(dir, "b.txt"), "b\n")
	s := newSession(t, dir)
	s.keys(":e a.txt<CR>x:e b.txt<CR>:qa<CR>")
	if s.notice() != "vim.unsaved-file" || s.h.quits != 0 {
		t.Errorf(":qa: %q, quits %d", s.notice(), s.h.quits)
	}
	s.keys(":q<CR>")
	if s.notice() != "vim.unsaved-file" || s.h.quits != 0 {
		t.Errorf(":q with another buffer changed: %q", s.notice())
	}
	s.keys(":qa!<CR>")
	if s.h.quits != 1 {
		t.Error(":qa! didn't quit")
	}
}

func TestEditReloadAndReadOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "a\n")
	writeFile(t, filepath.Join(dir, "bin"), "x\x00y")
	s := newSession(t, dir)
	s.keys(":e a.txt<CR>dd")
	writeFile(t, filepath.Join(dir, "a.txt"), "changed\n")
	s.keys(":e!<CR>")
	if got := s.e.Buf().String(); got != "changed" || s.e.Modified() {
		t.Errorf(":e! = %q, modified %v", got, s.e.Modified())
	}
	s.keys(":e bin<CR>")
	if s.notice() != "vim.opened-read-only" || s.e.ReadOnly() != "binary" {
		t.Errorf("binary: %q %q", s.notice(), s.e.ReadOnly())
	}
	s.keys("x:w<CR>")
	if s.notice() != "vim.read-only-file" || readFile(t, filepath.Join(dir, "bin")) != "x\x00y" {
		t.Errorf("writing a read-only file: %q", s.notice())
	}
}

// A file changed on disk since it was read asks before :w overwrites it.
func TestWriteAsksWhenChangedOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	writeFile(t, path, "a\n")
	s := newSession(t, dir)
	s.keys(":e a.txt<CR>x")
	writeFile(t, path, "theirs\n")
	s.keys(":w<CR>")
	if s.e.LastNotice().Severity != edit.Question || readFile(t, path) != "theirs\n" {
		t.Fatalf("notice %+v, file %q", s.e.LastNotice(), readFile(t, path))
	}
	s.keys("y")
	if readFile(t, path) != "\n" {
		t.Errorf("after y: %q", readFile(t, path))
	}
}

// :ls, :copen and :messages open the IDE's switcher, results panel and history when it has them.
func TestHostCommands(t *testing.T) {
	s := newSession(t, t.TempDir())
	s.keys(":ls<CR>:copen<CR>:messages<CR>:nonsense<CR>")
	if strings.Join(s.h.commands, ",") != "ls,copen,messages" || s.notice() != "vim.unknown-command" {
		t.Errorf("commands %v, last notice %q", s.h.commands, s.notice())
	}
}

// The bytes :w writes are exactly nvim's, for the same file and keys.
func TestWriteMatchesNvim(t *testing.T) {
	if !vimtest.Oracle() {
		t.Skip("needs the nvim oracle")
	}
	for _, tt := range []struct{ name, file, keys string }{
		{"lf", "one\ntwo\nthree\n", "ddGonew<Esc>"},
		{"crlf", "one\r\ntwo\r\n", "Aend<Esc>jOmid<Esc>"},
		{"bom", "\xef\xbb\xbfone\ntwo\n", "x"},
		{"bom crlf", "\xef\xbb\xbfone\r\ntwo\r\n", "ddp"},
		{"no newline at the end", "one\ntwo", "x"},
		{"crlf without the last newline", "one\r\ntwo", "Gx"},
		{"mixed endings", "one\r\ntwo\n", "x"},
		{"empty", "", "ihello<Esc>"},
		{"one empty line", "\n", "ihi<Esc>"},
		{"deleted to nothing", "a\nb\n", "dddd"},
		{"unicode", "héllo wörld\n", "wx"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ours, theirs := t.TempDir(), t.TempDir()
			writeFile(t, filepath.Join(ours, "f.txt"), tt.file)
			writeFile(t, filepath.Join(theirs, "f.txt"), tt.file)

			s := newSession(t, ours)
			s.keys(":e f.txt<CR>" + tt.keys + ":w<CR>")

			n, err := nvim.Start(theirs)
			if err != nil {
				t.Fatal(err)
			}
			defer n.Close()
			if err := n.Exec("edit f.txt"); err != nil {
				t.Fatal(err)
			}
			if err := n.Input(tt.keys + ":w<CR>"); err != nil {
				t.Fatal(err)
			}
			want, got := readFile(t, filepath.Join(theirs, "f.txt")), readFile(t, filepath.Join(ours, "f.txt"))
			if got != want {
				t.Errorf("wrote %q, nvim wrote %q", got, want)
			}
		})
	}
}

type fakeClipboard struct{ text, primary string }

func (c *fakeClipboard) Read(primary bool) (string, bool) {
	if primary {
		return c.primary, c.primary != ""
	}
	return c.text, c.text != ""
}

func (c *fakeClipboard) Write(primary bool, s string) {
	if primary {
		c.primary = s
	} else {
		c.text = s
	}
}

// editor.md: "y, d, c, x and p use the system clipboard through the IDE's clipboard service, as nvim with
// clipboard=unnamedplus"; named registers stay the engine's.
func TestClipboard(t *testing.T) {
	s := newSession(t, t.TempDir())
	clip := &fakeClipboard{}
	s.ed.SetClipboard(clip)
	s.keys("ione<CR>two<Esc>ggyy")
	if clip.text != "one\n" {
		t.Errorf("yy put %q on the clipboard", clip.text)
	}
	clip.text = "from another app"
	s.keys("jp")
	if got := s.e.Buf().String(); got != "one\ntwofrom another app" {
		t.Errorf("p pasted: %q", got)
	}
	clip.text = "a line\n"
	s.keys("P")
	if got := s.e.Buf().String(); got != "one\na line\ntwofrom another app" {
		t.Errorf("a clipboard ending in a newline pastes as lines: %q", got)
	}
	s.keys(`"ayy`)
	if clip.text != "a line\n" {
		t.Errorf("a named register wrote the clipboard: %q", clip.text)
	}
	s.keys("ggdw")
	if clip.text != "one" {
		t.Errorf("dw put %q on the clipboard", clip.text)
	}
	s.keys(`"*yy`)
	if clip.primary != "\n" || clip.text != "one" {
		t.Errorf(`"* wrote %q to the primary selection`, clip.primary)
	}
}

// Every hint which-key shows after a prefix is a command the engine has.
func TestNextKeysAreCommands(t *testing.T) {
	for _, prefix := range []string{"g", "z", "[", "]", "<C-w>"} {
		s := newSession(t, t.TempDir())
		s.keys("ione<CR>two<Esc>")
		s.keys(prefix)
		hints := s.e.NextKeys()
		if len(hints) == 0 {
			t.Fatalf("%s: no hints", prefix)
		}
		if got := strings.Join(s.e.Pending(), ""); got != strings.Join(keymap.Parse(prefix), "") {
			t.Fatalf("%s: pending %q", prefix, got)
		}
		for _, h := range hints {
			s := newSession(t, t.TempDir())
			s.keys("ione<CR>two<Esc>" + prefix + h.Keys)
			if len(s.e.Pending()) != 0 && s.e.Mode() != "op-pending" {
				s.keys("a") // a command that takes a character, as g' takes a mark
			}
			if p := s.e.Pending(); len(p) != 0 && s.e.Mode() != "op-pending" {
				t.Errorf("%s%s (%s) left %q pending", prefix, h.Keys, h.Desc, p)
			}
		}
	}
}

// A pane's file bytes are what :w writes, the last line's newline fixed as 'fixendofline' does.
func TestPaneFileBytes(t *testing.T) {
	for _, body := range []string{"a\nb\n", "a\r\nb\r\n", "a\nb", "\ufeffa\n", ""} {
		want := body
		if body == "a\nb" {
			want = "a\nb\n"
		}
		dir := t.TempDir()
		p := filepath.Join(dir, "f.txt")
		os.WriteFile(p, []byte(body), 0o644)
		ed := edit.New(text.New(nil, text.Author{}), text.Author{Kind: text.User})
		e := vim.New(ed)
		e.SetHost(vim.Disk{Dir: dir})
		e.Edit("f.txt")
		pane, _ := e.Pane(e.WindowID())
		if got := string(pane.FileBytes()); got != want {
			t.Errorf("%q: file bytes %q", body, got)
		}
	}
}

// motion.txt *jump-motions*: "\"L\", \"M\", \"H\" and the commands that start editing a new file." JumpTo is a tag
// jump: to another file in the same window, or within the file, and CTRL-O comes back.
func TestJumpTo(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "one\ntwo\nthree\n")
	writeFile(t, filepath.Join(dir, "b.txt"), "alpha\nbeta\n")
	s := newSession(t, dir)
	s.keys(":e a.txt<CR>jl")
	at := func(what, name string, line, col int) {
		t.Helper()
		if c := s.e.Cursor(); s.e.Name() != name || c != (vim.Pos{Line: line, Col: col}) {
			t.Errorf("%s: at %s %+v, want %s %d:%d", what, s.e.Name(), c, name, line, col)
		}
	}
	if err := s.e.JumpTo("b.txt", vim.Pos{Line: 1, Col: 2}); err != nil {
		t.Fatal(err)
	}
	at("JumpTo b.txt", "b.txt", 1, 2)
	if s.e.Windows() != 1 {
		t.Errorf("JumpTo opened a window: %d", s.e.Windows())
	}
	s.keys("<C-o>")
	at("CTRL-O", "a.txt", 1, 1)
	s.keys("<C-i>")
	at("CTRL-I", "b.txt", 1, 2)
	if err := s.e.JumpTo("b.txt", vim.Pos{Line: 0, Col: 3}); err != nil {
		t.Fatal(err)
	}
	at("JumpTo in the same file", "b.txt", 0, 3)
	s.keys("<C-o>")
	at("CTRL-O in the same file", "b.txt", 1, 2)
}
