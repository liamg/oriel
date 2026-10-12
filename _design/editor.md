# Editor: the vim engine

You want full vim, not "vim keys". The question is whether we embed neovim
or write our own engine.

## Options

| | Embed neovim (`nvim --embed`, msgpack-RPC) | Fork vim/neovim, render ourselves | **Own engine in Go** |
|---|---|---|---|
| Vim fidelity | 100%, including your config and plugins | 100% | starts ~90%, converges |
| Inline threads, inlays, diff gutters | only through extmarks and virt_lines, on a char grid | patch C | **native: any widget between any two lines** |
| Proportional or mixed fonts, image blocks | no (grid) | heavy patching | yes |
| Agent edits arriving mid-insert | RPC races with the user | same | **one buffer model, one undo tree, with authorship** |
| Process count | +1 nvim per session | +1 | 0 |
| Keystroke path | UI → RPC → nvim → redraw events → UI | same | in-process |
| Who maintains it | upstream | **us, forever, in C** | us, in Go |

The current crit neovim integration is the first column, and the jank comes
from exactly that: two models of the buffer, two cursors, and a grid that
can't host the UI we want.

## Decision: our own engine all the way, with neovim as the test oracle

- **`vim` module**: a Go implementation of vim's normal, insert, visual,
  visual-block, replace, operator-pending and cmdline modes.
- **Conformance by differential testing.** A harness drives the same keystroke
  script into `nvim --headless --clean` and into our engine, then compares
  buffer, cursor, registers, marks and mode after every key. Vim's own test
  suite (`test_normal.vim`, `test_textobjects.vim`, …) gets mined into these
  scripts.
- That oracle loop is what makes this a sound job for agents: every gap fails
  a test that already holds the right answer.
- A **compatibility table** in the repo lists every command and its status,
  generated from the conformance run.
- Every case cites its `:help` tag with a verbatim quote that CI checks, and is written before the code.
  The case format, oracle mode and fuzzing are in `testing.md`.

### Scope, in order

1. Motions, operators, counts, registers, dot-repeat, text objects, marks, jumplist, undo tree.
2. Visual, visual-block, `:s` with vim regex, `:g`, ranges.
3. Macros, `:normal`, abbreviations, `ctrl-a/x`, folds, splits, buffers, quickfix and location lists.
4. Text objects from tree-sitter (`af`, `if`, `ac`): better than vim's own.
5. Config is Lua (`init.lua`, see `scripting.md`). Ex commands like `:set`, `:map` and `:hi` work live.
   neovim Lua plugins won't run unchanged; the `vim` shim in `scripting.md` covers the simple ones.

- No embedded neovim, not even as an escape hatch: one buffer model, one engine,
  and nothing we design around. Gaps get fixed in the engine, with an oracle test.

### Options and defaults

- **Not vim exactly.** Oriel has its own options, with meaningful names and sensible defaults. We don't copy vim's option list.
- **nvim's behaviour is the starting point**: where vim and nvim differ (e.g. `'startofline'`), the engine follows nvim's
  defaults, so the oracle runs unmodified. We can change a default later; the case then says `deviation:` with the reason.
- **Options live at the layer they affect.** Only what changes how keys edit text belongs to the input engine
  (`shiftwidth`, `wrapscan`, `startofline`…). Display and app concerns (line numbers, wrap, cursorline, list chars)
  are app-level options in the registry, the same for every input engine.
- `:set` accepts vim's names as aliases where an Oriel option has the same meaning.

### Windows, buffers, folds and lists (ED-4)

- The engine keeps vim's state per window (cursor, jumplist, alternate buffer, folds, location list) and per
  buffer (text, marks, undo cursors, name, modified). Window commands keep a layout tree of rows and columns
  as vim's, so `CTRL-W h/j/k/l`, `:close` and `:only` mean what they mean in vim; the UI's layout tree (FW-9)
  shows it.
- Buffers have no files yet: `:file` names one, and `:e` reaches only buffers already open. `'hidden'` is on, as nvim.
- Folds follow vim's model: each fold is open, closed, or follows `'foldlevel'`, and the methods are manual,
  indent and marker. Tree-sitter folds come through the same structure later.
- Quickfix and location lists parse the `file:line:col:message` and `file:line:message` parts of the
  default `'errorformat'`. `:cexpr` takes string literals only, since there is no expression language;
  `:vimgrep` searches buffers already open.
- `:map`, `:noremap` and `:abbreviate` and their mode forms are engine-local tables for now, so vim
  configs and tests work. They move onto the registry keymap (`plugins.md`) when Lua config lands.

## Buffer model (`text` module)

- Written in this repo, not taken from `liamg/rope`. That library is fine as a small rope, but it stores `[]rune` and
  is binary-tree based, and it lacks the byte, UTF-16 and snapshot support listed below. Rewriting is cheaper than adapting it.
- Rope of UTF-8 chunks (B-tree, 1–4 KB leaves) with cached line and UTF-16 counts.
  LSP needs UTF-16 offsets, and the rope answers them in O(log n). Leaves never split a rune, so each leaf's counts
  are exact on its own; an edit that would join pieces of a UTF-8 sequence across its ends is widened to the whole
  rune (`Change.Replaces` reports what was applied).
- **No grapheme counts in the rope.** Grapheme clusters aren't additive across chunk boundaries, never cross a line,
  and vim moves by character (with composing characters), not by grapheme. Rendering segments a line into graphemes
  when it lays it out (go-text's UAX #29 segmenter), so the cache would buy nothing.
- Immutable snapshots: the renderer, LSP sync, tree-sitter and the agent diff
  each read a snapshot without locks.
- Every edit carries an **author** (`user`, `agent:<session>`, `lsp`, `format`, `plugin:<id>`; text as loaded is `file`),
  and the rope keeps it per byte as runs. The undo tree and gutter blame use it: you can undo just the agent's last
  edit (`UndoAuthor`: transformed through every later edit, refused if one overlaps it), and see which lines it wrote
  that you haven't read yet.
- The undo tree is vim's: `Undo` goes to the parent, `Redo` to the child last undone from, `UndoTo(seq)` to any step
  on any branch. Edits sharing an `UndoGroup` coalesce into one step, as typing in insert mode does.
- Anchors (stable positions) for threads, breakpoints, diagnostics and
  annotations: they survive edits from any author. An anchor at either end of replaced text keeps its side; one
  inside it, or at a pure insertion, goes to the side its `Bias` says.

## Regex

- **Two dialects, one matcher.** The vim engine (`/`, `?`, `:s`, `:g`, `*`) speaks **vim's dialect** (`pattern.txt`), as
  nvim does. Everything else (project search, the `plain` engine, plugins and Lua) speaks **PCRE2** (ED-10).
- Both parse into one **backtracking matcher** of our own, `vimregex` (Go's `regexp` is RE2: no look-around, no
  backreferences, no editor-state atoms like `\%V` and `\%#`). It runs over the buffer line by line, with a step budget
  and a recursion limit in place of RE2's linear-time guarantee.

## Syntax

- **Tree-sitter** for highlighting, folds, text objects and outline.
- Run the grammars as **WASM under wazero**: pure Go, no cgo, grammars
  sandboxed, and a plugin can ship a grammar as a `.wasm`.
- **One WASM module per grammar**: the tree-sitter runtime, the grammar and `syntax/grammars/shim.c` (a flat ABI:
  parse, edit, run a query) built together with wasi-sdk by `syntax/grammars/build.sh`, from sources pinned by
  revision and sha256 in `manifest.tsv`. The build is reproducible; CI rebuilds and checks the committed `.wasm`.
- Grammar revisions follow nvim-treesitter's `parsers.lua` at the commit the queries come from, so they agree.
- Highlight, fold and injection queries come from nvim-treesitter (Apache-2.0), vendored in `syntax/queries/`.
  The host runs their predicates and directives: `#eq?`, `#any-of?`, `#lua-match?` (our own Lua-pattern matcher),
  `#match?` (Go regexp), `#contains?`, `#has-parent?`, `#set!` and `#offset!`. Injections (code in Markdown, inline
  Markdown) parse the injected ranges with the other language's grammar. `units.scm` is ours.
- Overlapping captures resolve as nvim's highlighter does: higher `priority` wins, then deeper injections, then the
  later pattern.
- Incremental: an edit is applied to the old tree and the text reparsed, reusing everything unchanged: under 1 ms
  for a 600-line file, 2.7 ms at 2,000 lines (WASM costs about 3.6× native). So parsing is **off the keystroke
  path**: a `syntax.Document` reparses on its own goroutine, and readers get the last highlights moved through the
  edits made since. Plugins add a
  language with `syntax.Register(Language{Name, Wasm, Extensions, Queries})`, the same call the built-ins use.

## Code units

- A **unit** is a syntax node worth talking about: function, method, type, block, `if`/`switch`/`case`, loop, closure.
- Unit kinds are defined per language by a tree-sitter query (`units.scm`), next to the highlight queries.
  LSP `documentSymbol` adds names and kinds where available.
- `text.Unit{Kind, Name, Range, Parent}` is a shared model. It's used by explain (`ui.md` 07b), the
  outline, the `af`/`if`/`ic` text objects, the review checklist, sticky scroll headers and agent anchors.

## What the editor surface can show between lines

- Comment threads (Claude, Codex, GitHub reviewers), collapsed or open.
- Explain-walkthrough notes pinned to ranges.
- Diff hunks, inline (deleted lines as ghost blocks) or as a split view.
- Diagnostics as virtual text, or as a full block for long messages.
- Inlay hints, code lenses, and test results next to the test function.
- Debugger values inline while stopped.
- Images and mermaid diagrams in markdown, drawn in place.

![09-lsp](mockups/09-lsp.png)

Each is a `Decoration` provider (see `plugins.md`): plugins add their own the
same way.

## Go interfaces

The editor is three layers, each of them swappable:

- `text`: buffers.
- `edit`: the editing primitives, shared by every input engine.
- `input`: the engine that turns keys into those primitives. Vim is one engine, emacs could be another.

```go
// package text — the buffer of record. Pure data, no UI. Offsets are bytes; Pos is 0-based line and byte column.
type Buffer struct{ … }                          // concrete: there is one implementation
func New(content []byte, a Author) *Buffer
func (b *Buffer) Snapshot() Snapshot             // immutable, cheap (structural sharing)
func (b *Buffer) Apply(e Edit) (Change, error)   // the only way to mutate; Change says what was applied
func (b *Buffer) Undo() (Change, bool)           // also Redo, UndoTo(seq), UndoAuthor(a, by)
func (b *Buffer) Anchor(off int, bias Bias) AnchorID // stable across edits by anyone
func (b *Buffer) Resolve(AnchorID) (int, bool)
func (b *Buffer) Subscribe(fn func(Change)) (cancel func())

type Edit struct {
    Ranges []Replace                             // multi-cursor = several ranges, one undo step
    Author Author                                // {User} | {Agent, session} | {LSP} | {Format} | {Plugin, id}
    Group  UndoGroup                             // coalesce typing into one undo step
}

type Snapshot struct{ … }                        // a value: copy it freely, read it from any goroutine
func (s Snapshot) Len() int
func (s Snapshot) Lines() int
func (s Snapshot) Slice(r Range) []byte
func (s Snapshot) Line(n int) []byte
func (s Snapshot) PosAt(offset int) Pos          // and Offset(Pos) int
func (s Snapshot) UTF16(pos Pos) int             // for LSP; and PosFromUTF16(line, col)
func (s Snapshot) Runes(from int, dir Dir) iter.Seq2[int, rune] // motions walk this
func (s Snapshot) Authors(r Range) iter.Seq[AuthorSpan]        // blame
```

```go
// package edit — what an engine is allowed to do. Engines never touch text.Buffer directly.
type Editor struct{ … }                          // one per window; concrete, like text.Buffer
func (e *Editor) Buf() text.Snapshot
func (e *Editor) Cursors() []Cursor               // primary first; SetCursors(cs)
func (e *Editor) Do(rs []text.Replace, g text.UndoGroup) (text.Change, error) // undo, authorship, LSP sync
func (e *Editor) Undo() (text.Change, bool)       // also Redo, UndoTo, UndoSeq, UndoParent, NewGroup
func (e *Editor) Anchor(off int, b text.Bias) text.AnchorID
func (e *Editor) Register(r rune) (Register, bool) // and SetRegister: {Text, Charwise | Linewise | Blockwise}
func (e *Editor) Echo(msg string)                 // and Messages()
// Still to come, when a second engine needs them (ED-5): Motion, Object and Search by name, Exec,
// Scroll and Prompt. Until then motions and text objects are the vim engine's own tables, which
// plugins extend with vim.RegisterMotion and vim.RegisterObject.
```

```go
// package input — a pluggable input engine.
type Engine interface {
    ID() string                                   // "vim", "emacs", "helix", "plain"
    Attach(ed edit.Editor) State                  // per-window state
}

type State interface {
    Key(k Key) Result                             // the hot path, called once per key
    Mode() ModeInfo                               // name, cursor shape, statusline label, colour token
    Pending() string                              // "d2" / "C-x" for the which-key popup
    Keymaps() keymap.Table                        // so :Keys and which-key can list everything
}

type Result struct {
    Handled bool
    Repaint bool
    Recenter bool
}
```

```go
// input/vim — the built-in engine (sketch)
type vimState struct {
    ed   edit.Editor
    mode mode                                     // normal, insert, visual, op-pending, …
    pend pending                                  // count, register, operator, motion
    dot  []Key                                    // for "."
    km   *keymap.Trie
}

func (s *vimState) Key(k input.Key) input.Result {
    if act, ok := s.km.Step(s.mode, k); ok {      // user/plugin maps first
        return s.run(act)
    }
    return s.builtin(k)                           // operators, motions, counts…
}
```

```go
// input/emacs — what an emacs plugin implements. Same interface, about 2k lines
type emacsState struct {
    ed   edit.Editor
    mark *text.AnchorID
    kill *killRing
    km   *keymap.Trie                             // C-x C-f, M-w, C-k…
}

func (s *emacsState) Key(k input.Key) input.Result {
    act, more := s.km.Step("global", k)
    if more {
        return input.Result{Handled: true}        // mid-chord: C-x …
    }
    switch act {
    case "kill-line":
        r, _ := s.ed.Motion("line-end", 1)
        s.kill.Push(s.ed.Buf().Slice(r))
        s.ed.Do(text.Delete(r))
    case "set-mark":
        m := s.ed.Anchor(s.ed.Cursors()[0].Pos)
        s.mark = &m
    case "":
        if k.Printable() { s.ed.Do(text.InsertAtCursors(k.Text())) }
    }
    return input.Result{Handled: true, Repaint: true}
}
```

```go
// registering it
func init() { input.Register(emacs.Engine{}) }    // built-in or `oriel build --with`
// config: L.o.input = "emacs"     (global, or per buffer: L.bo.input = "plain")
```

- **What ships**: two built-in native engines, `vim` (the default) and `plain` (CUA keys:
  ⌘C, ⌘V, shift-select, for people who don't use vim). Anything else is a plugin that
  registers an engine, emacs or helix for example, in Lua or native via `oriel build --with`.
  The option `input` picks one, globally or per buffer.
- **Swapping the engine is the big lever, and rarely needed.** Most customisation extends vim
  instead: keymaps, custom operators, motions and text objects, and modes registered in Lua
  (`plugins.md`). The vim engine looks these up in its keymap trie before its built-ins.
  Plugins like surround or a leap/flash-style jump work this way, without replacing the engine.
- An engine can also **wrap** another: `input.Wrap(vim, fn)` sees each key first, then hands it on.
  That suits a "god mode" or a small layer over vim without forking it.
- **Does it work?** Yes. The engine only translates keys into `edit` primitives. Everything
  expensive (motions over the rope, regex, tree-sitter objects) is Go underneath, whichever engine called it.
- **Is it fast enough?** A key costs one `Key` call plus a handful of primitive calls.
  - In Go that's a few µs.
  - In Lua (gopher-lua) it's 10–40 µs. The budget is 8 ms, so an emacs mode written entirely in Lua is fine.
  - See "Performance and native plugins" in `plugins.md` for where Lua does stop being fine.
- The vim conformance suite runs against `input.Engine`. An emacs engine can get its own
  oracle, `emacs --batch` with the same keystroke-script harness.
