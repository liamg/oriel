package lang

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/liamg/oriel/app/kit/work"

	"github.com/liamg/oriel/registry/services"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/kit/fuzzy"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/lsp"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	reglang "github.com/liamg/oriel/registry/lang"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/text"
)

// The key layers of the LSP's popups: while one is up, its keys come first (registry.KeyLayer).
const (
	CompletionKeyMode = "completion"
	HoverKeyMode      = "lsp.hover"
)

// LSP is the editor's language servers: one per language and project root, started as files
// open, fed their edits, with their diagnostics in Problems and the editor, completion as you type, signature help,
// hover, code actions and inlay hints. Servers are registry contributions (registry/lang.LSPServer).
type LSP struct {
	kit.Disposer
	Version string                           // Oriel's, as servers are told it
	Logger  func(source string) *slog.Logger // a server's stderr goes to the log as source
	Servers func(lsp.ServerSpec) bool        // which servers may start, of those installed; every one if nil
	h       plugin.Host
	m       *lsp.Manager
	work    work.Work

	mu       sync.Mutex
	docs     map[string]*lspDoc // by path
	statuses map[string]lsp.Status
	gen      int // moves with each request the UI starts, so a late answer to an old one is dropped

	comp      completion
	sig       *signature
	hover     *hoverCard
	actions   *actionMenu
	peek      *refPeek
	rename    *renamePrompt
	callItems []uireg.PickerItem
	preview   kit.FilePreview
	typing    work.Still[*lspDoc] // a document's inlay hints wait for it to be still (hintsQuiet) once it has some
}

// hintsQuiet is how long a document being typed in waits, still, before its inlay hints are asked for again.
const hintsQuiet = 200 * time.Millisecond

// lspDoc is a buffer open on a server.
type lspDoc struct {
	path, lang string
	buf        *text.Buffer
	client     *lsp.Client
	opening    bool
	failed     bool
	queue      []text.Change // edits made while the server was starting
	ready      []func()      // requests made while it was starting, run once it has
	cancel     func()

	hints        []lsp.InlayHint
	hintsVersion uint64
	hintsLoading bool

	tokens        []lsp.SemanticToken
	tokensVersion uint64 // the snapshot's version + 1 they're for
	tokensLoading bool
}

func (x *LSP) Manifest() plugin.Manifest { return plugin.Manifest{ID: "lsp"} }

func (x *LSP) Activate(_ context.Context, h plugin.Host) error {
	x.Add(h.Contribute(registry.Busy{Name: "lsp", Busy: x.work.Busy, Source: "builtin:lsp"}))
	x.h = h
	x.docs, x.statuses = map[string]*lspDoc{}, map[string]lsp.Status{}
	x.typing = work.Still[*lspDoc]{Wait: hintsQuiet, Wake: func() { h.DecorationsChanged("lsp") }, Clock: h.Clock()}
	for _, s := range lsp.Builtins() {
		s.Source = "builtin:lsp"
		x.Add(h.Contribute(reglang.LSPServer(s)))
	}
	installed := lsp.Installed
	if allow := x.Servers; allow != nil {
		installed = func(s lsp.ServerSpec) bool { return allow(s) && lsp.Installed(s) }
	}
	x.m = lsp.NewManager(lsp.ManagerOpts{
		Installed: installed,
		Servers: func() []lsp.ServerSpec {
			var out []lsp.ServerSpec
			for _, s := range reglang.LSPServers(h) {
				out = append(out, lsp.ServerSpec(s))
			}
			return out
		},
		Stderr: func(spec lsp.ServerSpec, root string) io.Writer {
			return &logs.Lines{Log: logTo(x.h, x.Logger, "lsp:"+spec.Name)}
		},
		Client: lsp.Options{
			Client:  "oriel",
			Version: x.Version,
			OnDiagnostics: func(s lsp.DiagnosticSet) {
				h.SetDiagnostics(s.Server, s.Path, registryDiagnostics(s.Diagnostics))
				h.Redraw()
			},
			OnStatus: func(s lsp.Status) {
				x.mu.Lock()
				x.statuses[s.Server+"\x00"+s.Root] = s
				x.mu.Unlock()
				if s.State == lsp.Crashed && s.Err != nil {
					h.Notify(core.Notice{ID: "lsp.crashed." + s.Server, Severity: core.SevWarning, Source: "lsp", Title: s.Server + " stopped",
						Text: s.Err.Error(), Actions: []core.Action{{Title: "Restart", Command: "lsp.restart"}}})
				}
				h.Redraw()
			},
			OnApplyEdit: func(label string, e lsp.WorkspaceEdit) (bool, string) {
				done := make(chan error, 1)
				h.Do(func() { done <- x.applyEdit(e) })
				if err := <-done; err != nil {
					return false, err.Error()
				}
				return true, ""
			},
			OnMessage: func(server string, level lsp.MessageLevel, msg string) {
				sev := core.SevInfo
				switch level {
				case lsp.MessageError:
					sev = core.SevError
				case lsp.MessageWarning:
					sev = core.SevWarning
				}
				h.Notify(core.Notice{ID: "lsp.message." + server, Severity: sev, Source: server, Text: msg})
			},
			OnRefresh: func(server, what string) {
				if what == "inlayHint" {
					x.mu.Lock()
					for _, d := range x.docs {
						d.hintsVersion = 0
					}
					x.mu.Unlock()
					h.Redraw()
				}
			},
		},
	})
	x.Add(h.Decorations("lsp", uireg.KeyedDecorations{Key: x.decorationKey, Make: x.decorations}))
	x.Add(registry.On(h, registry.Hook{Label: "tell the language server"}, func(_ registry.Ctx, w *kit.BufWritePost) (registry.Verdict, error) {
		x.mu.Lock()
		d := x.docs[w.File]
		x.mu.Unlock()
		if d != nil && d.client != nil {
			x.m.Save(d.path, d.buf.Snapshot())
		}
		return registry.Verdict{}, nil
	}))
	x.Add(h.Contribute(registry.KeyLayer{Name: "completion", Mode: CompletionKeyMode, Active: func() bool { return x.comp.open }}))
	x.Add(h.Contribute(registry.KeyLayer{Name: "lsp.hover", Mode: HoverKeyMode, Active: func() bool { return x.hover != nil }}))
	x.lspCommands(h)
	x.navigation(h)
	x.agentTools(h)
	x.Add(h.Option(registry.OptionSpec{Name: "lsp.start", Type: registry.Enum, Values: []string{"project", "file"}, Default: "project",
		Scopes: registry.ScopeUser | registry.ScopeProject,
		Help:   "When language servers start: project, as soon as a project with their root files (go.mod, Cargo.toml…) opens, so the first request doesn't wait; or file, when one of their files first opens."}))
	h.Do(x.warm) // once every plugin has contributed its servers
	// The servers started for the directory the window left keep running, for its buffers still open; finding the
	// new one's reads the disk, so it's off the loop.
	x.Add(kit.OnDirChanged(h, "language servers for the directory", func(kit.DirChanged) {
		x.work.Go(func(context.Context) { x.warm() })
	}))
	x.Add(h.Contribute(registry.InspectSection{Name: "LANGUAGE SERVER", Order: 30, Rows: x.inspectRows, Source: "builtin:lsp"}))
	x.Add(h.Picker(x.callsSource()))
	x.Add(h.View(kit.OverlayPrefix+"lsp", lspOverlay{x}))
	x.Add(h.View("statusline.left.60-lsp", lspStatus{x}))
	return nil
}

func (x *LSP) Deactivate(ctx context.Context) error {
	x.mu.Lock()
	for _, d := range x.docs {
		if d.cancel != nil {
			d.cancel()
		}
	}
	x.mu.Unlock()
	plugin.Teardown(ctx, func(ctx context.Context) {
		sctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		x.m.Shutdown(sctx)
		x.work.Stop()
	})
	return x.Disposer.Deactivate(ctx)
}

// dir is the project the window is on now.
func (x *LSP) dir() string { return kit.WindowDir(x.h) }

// Wait waits for the work it has running off the loop: the servers' answers.
func (x *LSP) Wait() { x.work.Wait() }

// warm starts the servers for the project the window is on, and for the projects directly in it (a session's
// worktrees), when lsp.start says to: each server whose own root files are there, its generic ones (.git) aside.
func (x *LSP) warm() {
	dir := x.dir()
	if v, _, err := x.h.Options().Get("lsp.start", registry.At{}); err != nil || v != "project" || dir == "" {
		return
	}
	roots := []string{dir}
	if ents, err := os.ReadDir(dir); err == nil {
		for _, e := range ents {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && len(roots) < 32 {
				roots = append(roots, filepath.Join(dir, e.Name()))
			}
		}
	}
	seen := map[string]bool{}
	for _, s := range reglang.LSPServers(x.h) {
		for _, root := range roots {
			if !hasOwnRoot(s.Roots, root) {
				continue
			}
			for _, lang := range s.Language {
				spec, ok := x.m.SpecFor(lang)
				if !ok || seen[spec.Name+"\x00"+root] {
					continue
				}
				seen[spec.Name+"\x00"+root] = true
				x.work.Go(func(ctx context.Context) {
					ctx, cancel := context.WithTimeout(ctx, time.Minute)
					defer cancel()
					if _, err := x.m.Warm(ctx, lang, root); err != nil && !errors.Is(err, lsp.ErrNoServer) {
						x.h.Log().Info("lsp warm", "server", spec.Name, "root", root, "err", err)
					}
				})
			}
		}
	}
}

// hasOwnRoot reports whether dir has one of a server's root files other than .git, which every repo has.
func hasOwnRoot(markers []string, dir string) bool {
	for _, m := range markers {
		if m == ".git" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
			return true
		}
	}
	return false
}

// async runs fn off the loop, and done with its result back on it.
func async[T any](x *LSP, fn func(ctx context.Context) (T, error), done func(T, error)) {
	x.work.Go(func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		v, err := fn(ctx)
		x.h.Do(func() { done(v, err) })
	})
}

// registryDiagnostics are a server's diagnostics as the registry keeps them: on their first line, in bytes.
func registryDiagnostics(ds []lsp.Diagnostic) []registry.Diagnostic {
	out := make([]registry.Diagnostic, 0, len(ds))
	for _, d := range ds {
		r := registry.Diagnostic{Line: d.Range.Start.Line, From: d.Range.Start.Col, Message: d.Message}
		if d.Range.End.Line == d.Range.Start.Line {
			r.To = d.Range.End.Col
		}
		r.Severity = d.Severity.String()
		if d.Severity == 0 {
			r.Severity = registry.SevError // LSP leaves one without a severity to the client
		}
		out = append(out, r)
	}
	return out
}

// doc is the server document for a buffer showing path, opening it the first time; nil when no server serves it.
func (x *LSP) doc(path string, buf *text.Buffer) *lspDoc {
	x.mu.Lock()
	d, ok := x.docs[path]
	x.mu.Unlock()
	if ok && d.buf == buf {
		if d.lang == "" {
			return nil
		}
		return d
	}
	lang, ok := kit.LanguageFor(x.h, path, buf.Snapshot().Bytes())
	if !ok {
		x.mu.Lock()
		x.docs[path] = &lspDoc{path: path, buf: buf, failed: true}
		x.mu.Unlock()
		return nil
	}
	if _, ok := x.m.SpecFor(lang.Name); !ok {
		x.mu.Lock()
		x.docs[path] = &lspDoc{path: path, buf: buf, failed: true}
		x.mu.Unlock()
		return nil
	}
	d = &lspDoc{path: path, lang: lang.Name, buf: buf, opening: true}
	x.mu.Lock()
	x.docs[path] = d
	x.mu.Unlock()
	d.cancel = buf.Subscribe(func(ch text.Change) { x.changed(d, ch) })
	snap := buf.Snapshot()
	x.work.Go(func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		c, err := x.m.Open(ctx, path, d.lang, snap)
		x.h.Do(func() {
			d.opening = false
			ready := d.ready
			d.ready = nil
			defer func() {
				for _, fn := range ready {
					fn()
				}
			}()
			if err != nil {
				d.failed = true
				if !errors.Is(err, lsp.ErrNoServer) {
					x.h.Log().Warn("lsp open", "path", path, "err", err)
				}
				return
			}
			d.client = c
			for _, ch := range d.queue {
				x.m.Change(path, ch)
			}
			d.queue = nil
			x.h.Redraw()
		})
	})
	return d
}

// changed sends a buffer's edit to its server, and asks for completion or signature help when typing calls for it.
func (x *LSP) changed(d *lspDoc, ch text.Change) {
	if d.opening {
		d.queue = append(d.queue, ch)
		return
	}
	if d.client == nil {
		return
	}
	if err := x.m.Change(d.path, ch); err != nil {
		x.h.Log().Warn("lsp change", "path", d.path, "err", err)
	}
	x.h.Do(func() { x.typed(d) })
}

// current is the current editor window, its file's path, and its server document.
func (x *LSP) current() (registry.EditorWindow, string, *lspDoc) {
	w := window(x.h)
	if w.Path == "" || w.Text == nil {
		return w, "", nil
	}
	return w, w.Path, x.doc(w.Path, w.Text)
}

// decorationKey is what the decorations of ctx are made from: the references peek, and the file's inlay hints.
func (x *LSP) decorationKey(ctx uireg.DecorationCtx) any {
	type made struct {
		peek    *refPeek
		doc     *lspDoc
		version uint64
		loading bool
		served  bool
	}
	k := made{peek: x.peek, doc: x.doc(ctx.Path, ctx.Buffer)}
	if k.doc != nil {
		k.version, k.loading, k.served = k.doc.hintsVersion, k.doc.hintsLoading, k.doc.client != nil
	}
	return k
}

func (x *LSP) decorations(ctx uireg.DecorationCtx) []uireg.Decoration {
	d := x.doc(ctx.Path, ctx.Buffer)
	if d == nil || d.client == nil {
		return nil
	}
	out := x.peekDecoration(ctx)
	snap := ctx.Snap
	if d.hintsVersion != snap.Version()+1 && !d.hintsLoading && (d.hintsVersion == 0 || x.typing.Ready(d, snap.Version())) {
		d.hintsLoading = true
		version := snap.Version()
		whole := lsp.Range{End: snap.PosAt(snap.Len())}
		c := d.client
		async(x, func(ctx context.Context) ([]lsp.InlayHint, error) { return c.InlayHints(ctx, d.path, whole) },
			func(hs []lsp.InlayHint, err error) {
				d.hintsLoading = false
				if err == nil || errors.Is(err, lsp.ErrNotSupported) {
					d.hints, d.hintsVersion = hs, version+1
				}
				x.h.Redraw()
			})
	}
	for _, hint := range d.hints {
		if hint.Pos.Line < ctx.Top || hint.Pos.Line >= ctx.Bottom {
			continue
		}
		label := hint.Label
		if hint.PaddingLeft {
			label = " " + label
		}
		if hint.PaddingRight {
			label += " "
		}
		out = append(out, uireg.Decoration{Kind: uireg.Inlay, Line: hint.Pos.Line, From: hint.Pos.Col, Text: label, Style: "editor.inlay"})
	}
	return out
}

// lspCommands are the LSP's commands and keys.
func (x *LSP) lspCommands(h plugin.Host) {
	for _, c := range []registry.Command{
		{ID: "lsp.hover", Title: "Show what the language server knows about the symbol under the cursor", Run: func(ctx registry.Ctx, _ registry.Args) error {
			return x.startHover(ctx)
		}},
		{ID: "lsp.hover.close", Title: "Close the hover card", Run: func(registry.Ctx, registry.Args) error { x.hover = nil; return nil }},
		{ID: "lsp.complete", Title: "Complete the word before the cursor", Run: func(registry.Ctx, registry.Args) error {
			_, _, d := x.current()
			if d == nil || d.client == nil {
				return fmt.Errorf("no language server for this file")
			}
			x.requestCompletion(d, true)
			return nil
		}},
		{ID: "completion.next", Title: "Highlight the next completion", Run: func(registry.Ctx, registry.Args) error { x.comp.move(1); return nil }},
		{ID: "completion.prev", Title: "Highlight the previous completion", Run: func(registry.Ctx, registry.Args) error { x.comp.move(-1); return nil }},
		{ID: "completion.accept", Title: "Take the highlighted completion", Run: func(registry.Ctx, registry.Args) error { return x.accept() }},
		{ID: "completion.cancel", Title: "Close the completion menu", Run: func(registry.Ctx, registry.Args) error { x.comp.close(); return nil }},
		{ID: "lsp.code_actions", Title: "Show the code actions at the cursor", Run: func(ctx registry.Ctx, _ registry.Args) error {
			return x.codeActions(ctx)
		}},
		{ID: "lsp.restart", Title: "Restart the language servers", Run: func(ctx registry.Ctx, _ registry.Args) error {
			for _, c := range x.m.Clients() {
				if _, err := x.m.Restart(ctx, c); err != nil {
					return err
				}
			}
			return nil
		}},
	} {
		x.Add(h.Command(c))
	}
	for _, k := range []registry.Keymap{
		{Mode: "n", Keys: "K", Command: "lsp.hover", Desc: "Hover"},
		{Mode: "n", Keys: "<Leader>ca", Command: "lsp.code_actions", Desc: "Code actions"},
		{Mode: "x", Keys: "<Leader>ca", Command: "lsp.code_actions", Desc: "Code actions"},
		{Mode: "i", Keys: "<C-Space>", Command: "lsp.complete", Desc: "Complete"},
		{Mode: CompletionKeyMode, Keys: "<C-n>", Command: "completion.next", Desc: "Next"},
		{Mode: CompletionKeyMode, Keys: "<Down>", Command: "completion.next", Desc: "Next"},
		{Mode: CompletionKeyMode, Keys: "<C-p>", Command: "completion.prev", Desc: "Previous"},
		{Mode: CompletionKeyMode, Keys: "<Up>", Command: "completion.prev", Desc: "Previous"},
		{Mode: CompletionKeyMode, Keys: "<CR>", Command: "completion.accept", Desc: "Take it"},
		{Mode: CompletionKeyMode, Keys: "<Tab>", Command: "completion.accept", Desc: "Take it"},
		{Mode: CompletionKeyMode, Keys: "<C-y>", Command: "completion.accept", Desc: "Take it"},
		{Mode: CompletionKeyMode, Keys: "<C-e>", Command: "completion.cancel", Desc: "Close"},
		{Mode: HoverKeyMode, Keys: "<Esc>", Command: "lsp.hover.close", Desc: "Close"},
	} {
		x.Add(h.Keymap(k))
	}
}

// completion is the completion menu: the server's items for a word, filtered as it's typed.
type completion struct {
	open     bool
	path     string
	line     int
	start    int // the word's start column
	items    []lsp.Completion
	shown    []int // indexes into items, best first
	sel      int
	resolved map[int]lsp.Completion
	gen      int
}

func (c *completion) close() { *c = completion{} }

func (c *completion) move(d int) {
	if len(c.shown) > 0 {
		c.sel = (c.sel + d + len(c.shown)) % len(c.shown)
	}
}

func isWordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// wordStart is where the word ending at col on line starts.
func wordStart(line []byte, col int) int {
	for col > 0 {
		r, n := utf8.DecodeLastRune(line[:col])
		if !isWordRune(r) {
			break
		}
		col -= n
	}
	return col
}

// typed is after an edit in the current buffer: in insert mode, the menu refilters, closes, or is asked for; and
// signature help opens on a call's ( or , and closes on its ).
func (x *LSP) typed(d *lspDoc) {
	p, path, cur := x.current()
	if cur != d || path != d.path || editorMode(x.h) != "insert" {
		return
	}
	pos := p.Cursor
	line := p.Text.Snapshot().Line(pos.Line)
	col := min(pos.Col, len(line))
	before := rune(0)
	if col > 0 {
		before, _ = utf8.DecodeLastRune(line[:col])
	}
	caps := d.client.Capabilities()
	trigger := false
	if caps.CompletionProvider != nil {
		trigger = slices.Contains(caps.CompletionProvider.TriggerCharacters, string(before))
	}
	start := wordStart(line, col)
	switch {
	case x.comp.open && x.comp.path == d.path && x.comp.line == pos.Line && col >= x.comp.start && !trigger:
		x.filter(line[x.comp.start:col])
	case trigger || isWordRune(before) && col-start >= 1:
		x.requestCompletion(d, false)
	default:
		x.comp.close()
	}
	if caps.SignatureHelpProvider != nil {
		switch {
		case slices.Contains(caps.SignatureHelpProvider.TriggerCharacters, string(before)) || x.sig != nil && before == ',':
			x.requestSignature(d)
		case before == ')':
			x.sig = nil
		}
	}
}

// requestCompletion asks the server for the items at the cursor.
func (x *LSP) requestCompletion(d *lspDoc, manual bool) {
	p := window(x.h)
	pos := p.Cursor
	line := p.Text.Snapshot().Line(pos.Line)
	col := min(pos.Col, len(line))
	x.gen++
	gen := x.gen
	c := d.client
	async(x, func(ctx context.Context) (*lsp.CompletionList, error) {
		return c.Complete(ctx, lsp.Loc{Path: d.path, Pos: text.Pos{Line: pos.Line, Col: col}})
	}, func(list *lsp.CompletionList, err error) {
		if gen != x.gen || err != nil || list == nil {
			return
		}
		p := window(x.h)
		now := p.Cursor
		if p.Text == nil || now.Line != pos.Line || editorMode(x.h) != "insert" {
			return
		}
		ln := p.Text.Snapshot().Line(now.Line)
		start := wordStart(ln, min(col, len(ln)))
		x.comp = completion{path: d.path, line: pos.Line, start: start, items: list.Items, resolved: map[int]lsp.Completion{}, gen: gen}
		x.filter(ln[start:min(now.Col, len(ln))])
	})
}

// filter ranks the items against the word typed so far, and opens the menu while any match.
func (x *LSP) filter(word []byte) {
	type hit struct{ i, score int }
	var hits []hit
	for i, it := range x.comp.items {
		ft := it.FilterText
		if ft == "" {
			ft = it.Label
		}
		if len(word) == 0 {
			hits = append(hits, hit{i, 0})
			continue
		}
		if score, _, ok := fuzzy.Match(string(word), ft); ok {
			hits = append(hits, hit{i, score})
		}
	}
	slices.SortStableFunc(hits, func(a, b hit) int {
		if a.score != b.score {
			return b.score - a.score
		}
		return strings.Compare(x.comp.items[a.i].SortText, x.comp.items[b.i].SortText)
	})
	x.comp.shown = x.comp.shown[:0]
	for _, h := range hits {
		x.comp.shown = append(x.comp.shown, h.i)
	}
	x.comp.sel = 0
	x.comp.open = len(x.comp.shown) > 0
}

// selected is the highlighted item, resolved if it has been.
func (x *LSP) selected() (lsp.Completion, int, bool) {
	if !x.comp.open || x.comp.sel >= len(x.comp.shown) {
		return lsp.Completion{}, 0, false
	}
	i := x.comp.shown[x.comp.sel]
	if r, ok := x.comp.resolved[i]; ok {
		return r, i, true
	}
	return x.comp.items[i], i, true
}

// accept puts the highlighted item in, in place of the word typed, with its other edits (an import), and the cursor
// at its first tab stop.
func (x *LSP) accept() error {
	it, _, ok := x.selected()
	if !ok {
		return nil
	}
	ed, ok := services.EditorsOf(x.h)
	if !ok {
		return nil
	}
	p := window(x.h)
	snap := p.Text.Snapshot()
	pos := p.Cursor
	r := lsp.Range{Start: text.Pos{Line: x.comp.line, Col: x.comp.start}, End: text.Pos{Line: pos.Line, Col: pos.Col}}
	if it.Range != nil && it.Range.Start.Line == pos.Line {
		r = lsp.Range{Start: it.Range.Start, End: text.Pos{Line: pos.Line, Col: max(pos.Col, it.Range.End.Col)}}
	}
	edits := append([]lsp.TextEdit{{Range: r, NewText: it.Insert.Text}}, it.AdditionalEdits...)
	e, err := lsp.EditFor(snap, edits, text.Author{Kind: text.User})
	if err != nil {
		return err
	}
	at := snap.Offset(r.Start)
	before := 0
	for _, rp := range e.Ranges {
		if rp.Range.Start < at {
			before += len(rp.Text) - (rp.Range.End - rp.Range.Start)
		}
	}
	if err := ed.Edit(e.Ranges); err != nil {
		return err
	}
	caret := at + before + len(it.Insert.Text)
	if len(it.Insert.Tabstops) > 0 && len(it.Insert.Tabstops[0].Ranges) > 0 {
		caret = at + before + it.Insert.Tabstops[0].Ranges[0].Start
	}
	after := p.Text.Snapshot()
	ed.SetCursor(after.PosAt(min(caret, after.Len())))
	x.comp.close()
	return nil
}

// signature is the signature help showing.
type signature struct {
	help *lsp.SignatureHelp
	line int
}

func (x *LSP) requestSignature(d *lspDoc) {
	pos := window(x.h).Cursor
	c := d.client
	async(x, func(ctx context.Context) (*lsp.SignatureHelp, error) {
		return c.SignatureHelp(ctx, lsp.Loc{Path: d.path, Pos: text.Pos{Line: pos.Line, Col: pos.Col}})
	}, func(s *lsp.SignatureHelp, err error) {
		if err != nil || s == nil || len(s.Signatures) == 0 {
			x.sig = nil
			return
		}
		x.sig = &signature{help: s, line: pos.Line}
	})
}

// hoverCard is the K card.
type hoverCard struct {
	at   text.Pos
	path string
	md   lsp.Markup
}

func (x *LSP) startHover(ctx registry.Ctx) error {
	p, path, d := x.current()
	if d == nil || d.client == nil {
		return x.h.Run(ctx, "editor.keys", registry.Args{"keys": "K", "noremap": true})
	}
	pos := p.Cursor
	c := d.client
	async(x, func(ctx context.Context) (*lsp.Hover, error) {
		return c.Hover(ctx, lsp.Loc{Path: path, Pos: text.Pos{Line: pos.Line, Col: pos.Col}})
	}, func(hv *lsp.Hover, err error) {
		if err != nil {
			x.h.Notify(core.Notice{ID: "lsp.hover", Severity: core.SevWarning, Key: true, Text: err.Error()})
			return
		}
		if hv == nil || strings.TrimSpace(hv.Contents.Text) == "" {
			x.h.Notify(core.Notice{ID: "lsp.hover", Severity: core.SevInfo, Key: true, Text: "Nothing to show here"})
			return
		}
		x.hover = &hoverCard{at: text.Pos{Line: pos.Line, Col: pos.Col}, path: path, md: hv.Contents}
	})
	return nil
}

// actionMenu is the code actions menu: the server's, then the Agent group.
type actionMenu struct {
	path    string
	actions []lsp.Action
	client  *lsp.Client
	agent   []registry.MenuItem
	asked   bool // for focus
}

// agentActions are the Agent group for the diagnostics on a line: ask the agent to fix them, and its explain twin.
func (x *LSP) agentActions(path string, snap text.Snapshot, line int) []registry.MenuItem {
	var ds []registry.Diagnostic
	for _, d := range x.h.DiagnosticsFor(path) {
		if d.Line == line {
			ds = append(ds, d)
		}
	}
	if len(ds) == 0 {
		return nil
	}
	return registry.WithTwins([]registry.MenuItem{{Name: "lsp.fix", Label: "Ask the agent to fix in a thread", Group: "agent",
		Command: registry.FixCommand, Args: diagnosticTrouble(snap, ds).Args(), Source: "builtin:lsp"}})
}

func (x *LSP) codeActions(ctx registry.Ctx) error {
	p, path, d := x.current()
	if path == "" {
		return fmt.Errorf("the editor has no file here")
	}
	if x.whenStarted(d, "code_actions", func() error { return x.codeActions(ctx) }) {
		return nil
	}
	pos := p.Cursor
	agent := x.agentActions(path, p.Text.Snapshot(), pos.Line)
	if d == nil || d.client == nil {
		if len(agent) == 0 {
			return fmt.Errorf("no language server for this file")
		}
		x.actions = &actionMenu{path: path, agent: agent}
		return nil
	}
	var diags []lsp.Diagnostic
	for _, dg := range d.client.Diagnostics(path) {
		if dg.Range.Start.Line <= pos.Line && dg.Range.End.Line >= pos.Line {
			diags = append(diags, dg)
		}
	}
	r := lsp.Range{Start: text.Pos{Line: pos.Line, Col: pos.Col}, End: text.Pos{Line: pos.Line, Col: pos.Col}}
	if len(diags) > 0 {
		r = diags[0].Range
	}
	c := d.client
	answered := x.waiting(c)
	async(x, func(ctx context.Context) ([]lsp.Action, error) { return c.CodeActions(ctx, path, r, diags) },
		func(as []lsp.Action, err error) {
			answered()
			if err != nil {
				x.h.Notify(core.Notice{ID: "lsp.code_actions", Severity: core.SevWarning, Key: true, Text: err.Error()})
				as = nil
			}
			if len(as) == 0 && len(agent) == 0 {
				if err == nil {
					x.h.Notify(core.Notice{ID: "lsp.code_actions", Severity: core.SevInfo, Key: true, Text: "No code actions here"})
				}
				return
			}
			x.actions = &actionMenu{path: path, actions: as, client: c, agent: agent}
		})
	return nil
}

// runAction resolves an action if it needs it, applies its edit, and runs its command.
func (x *LSP) runAction(m *actionMenu, a lsp.Action) {
	async(x, func(ctx context.Context) (lsp.Action, error) {
		if a.Edit == nil && a.Command == nil {
			return m.client.ResolveAction(ctx, m.path, a)
		}
		return a, nil
	}, func(a lsp.Action, err error) {
		if err == nil && a.Edit != nil {
			err = x.applyEdit(*a.Edit)
		}
		if err == nil && a.Command != nil {
			cmd := *a.Command
			async(x, func(ctx context.Context) (struct{}, error) {
				_, err := m.client.ExecuteCommand(ctx, cmd)
				return struct{}{}, err
			},
				func(_ struct{}, err error) {
					if err != nil {
						x.h.Notify(core.Notice{ID: "lsp.code_action", Severity: core.SevError, Key: true, Text: err.Error()})
					}
				})
		}
		if err != nil {
			x.h.Notify(core.Notice{ID: "lsp.code_action", Severity: core.SevError, Key: true, Text: err.Error()})
		}
	})
}

// applyEdit makes a server's workspace edit: open buffers change in the buffer (one undoable step each), other files
// on disk; files are created, renamed and deleted as it says.
func (x *LSP) applyEdit(e lsp.WorkspaceEdit) error {
	var errs []error
	for _, ch := range e.Changes {
		switch ch.Op {
		case "create":
			if _, err := os.Stat(ch.Path); err == nil && !ch.Overwrite {
				continue
			}
			errs = append(errs, os.WriteFile(ch.Path, nil, 0o644))
			continue
		case "rename":
			errs = append(errs, os.Rename(ch.Path, ch.NewPath))
			continue
		case "delete":
			errs = append(errs, os.RemoveAll(ch.Path))
			continue
		}
		if f, ok := openBuffer(x.h, ch.Path); ok {
			ed, err := lsp.EditFor(f.Text.Snapshot(), ch.Edits, text.Author{Kind: text.LSP})
			if err == nil {
				_, err = f.Text.Apply(ed)
			}
			errs = append(errs, err)
			continue
		}
		b, err := os.ReadFile(ch.Path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		buf := text.New(b, text.Author{})
		ed, err := lsp.EditFor(buf.Snapshot(), ch.Edits, text.Author{Kind: text.LSP})
		if err == nil {
			if _, err = buf.Apply(ed); err == nil {
				err = os.WriteFile(ch.Path, buf.Snapshot().Bytes(), 0o644)
			}
		}
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// diagnosticTrouble is the diagnostics on one line as something to fix or explain: what they say, and the code
// around them.
func diagnosticTrouble(snap text.Snapshot, ds []registry.Diagnostic) registry.Trouble {
	d := ds[0]
	var msgs []string
	for _, x := range ds {
		msgs = append(msgs, fmt.Sprintf("%s %s: %s", x.Owner, x.Severity, x.Message))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s:%d:\n%s\n", d.File, d.Line+1, strings.Join(msgs, "\n"))
	if snap.Lines() > d.Line {
		from, to := max(0, d.Line-5), min(snap.Lines()-1, d.Line+5)
		b.WriteString("\nThe code around it:\n```\n")
		for l := from; l <= to; l++ {
			b.WriteString(strings.TrimRight(string(snap.Line(l)), "\r\n") + "\n")
		}
		b.WriteString("```\n")
	}
	where := fmt.Sprintf("%s:%d", filepath.Base(d.File), d.Line+1)
	return registry.Trouble{Kind: "diagnostic", Subject: fmt.Sprintf("%s at %s: %s", d.Owner, where, kit.FirstLine(d.Message)), Context: b.String(),
		File: d.File, Line: d.Line + 1, EndLine: d.Line + 1, Fix: fmt.Sprintf("Fix the %s %s at %s: %s", d.Owner, d.Severity, where, kit.FirstLine(d.Message))}
}
