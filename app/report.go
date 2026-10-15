package app

import (
	"encoding/json"
	"fmt"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/theme"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/worker"
)

// IssueURL is where a report goes, by hand: Oriel never sends one itself.
const IssueURL = "https://github.com/liamg/oriel/issues/new"

// shell is what the built-ins share: the config, the event tail, the desktop.
type shell struct {
	cfg     Config
	events  *eventTail
	desktop Desktop
	layout  *layoutExt
	poke    func() // asks the loop for a frame
	keys    *registry.Resolver
	ui      *ui.UI
	modes   func() []string
	// enginePending is the focused editor's pending keys, when the editor is in front.
	enginePending func() ext.Pending
	// engineHints are the focused editor engine's own commands after keys a mapping is waiting on.
	engineHints func(keys []string) []ext.Hint
	theme       func() *theme.Resolved
	todoMu      sync.Mutex
	todo        []func()                         // Host.Do's, for the loop
	editorAt    func(at registry.At) registry.At // the focused editor's buffer and window, added to at
	shot        func(th *theme.Resolved) *image.RGBA
	reopen      chan reopenTo // the directory for the loop to start again on: Open Directory's, or a session's
	recents     *recents
	git         *gitExt // the work tree's git status
	editor      *editorExt
	search      *projectSearch
	tests       *testsExt
	explain     *explainExt
	sessions    *sessions                    // the workers' sessions; nil while it isn't active
	stats       *statsExt                    // what the workers' sessions cost
	hud         *hudExt                      // the HUD, and what every slot's indicators last built
	present     *presentFocus                // the presentation with focus, for its keys
	afterOpen   *afterOpen                   // what session.open runs once the window has opened again on the session
	draining    atomic.Pointer[workerDrain]  // this machine's worker handing over, for a restart or a quit
	closeWin    func()                       // closes the window, which quits
	relaunch    atomic.Pointer[func() error] // starts Oriel again once the window has closed: an update's restart
	joinMu      sync.Mutex
	onJoin      map[*joinHook]struct{} // told of each worker as it connects
}

type joinHook struct {
	fn func(worker.Client, worker.Info)
}

// onWorkerJoined calls fn, off the loop, with each worker that connects from now on, and its info: the updater
// pushes a release to one that's behind. cancel stops it.
func (sh *shell) onWorkerJoined(fn func(worker.Client, worker.Info)) (cancel func()) {
	k := &joinHook{fn}
	sh.joinMu.Lock()
	if sh.onJoin == nil {
		sh.onJoin = map[*joinHook]struct{}{}
	}
	sh.onJoin[k] = struct{}{}
	sh.joinMu.Unlock()
	return func() {
		sh.joinMu.Lock()
		delete(sh.onJoin, k)
		sh.joinMu.Unlock()
	}
}

func (sh *shell) workerJoined(c worker.Client, info worker.Info) {
	sh.joinMu.Lock()
	hooks := make([]*joinHook, 0, len(sh.onJoin))
	for k := range sh.onJoin {
		hooks = append(hooks, k)
	}
	sh.joinMu.Unlock()
	for _, k := range hooks {
		k.fn(c, info)
	}
}

type afterOpen struct {
	session core.SessionID
	calls   []call
}

// report is "Copy report" (errors.md): version, OS, settings that differ from their defaults, plugins, the
// last 500 log records and the event-log tail, redacted, as one block of text. head goes first: a panic and
// its stack, for a crash report.
func (sh *shell) report(head string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "oriel %s · %s/%s\n", sh.cfg.Version, runtime.GOOS, runtime.GOARCH)
	var plugins []string
	for _, e := range sh.cfg.Extensions {
		plugins = append(plugins, e.Manifest().ID)
	}
	fmt.Fprintf(&b, "plugins %s\n", orNone(strings.Join(plugins, ", ")))
	fmt.Fprintf(&b, "settings %s\n", orNone(strings.Join(sh.changedSettings(), ", ")))
	if head != "" {
		fmt.Fprintf(&b, "\n%s\n", strings.TrimRight(head, "\n"))
	}
	b.WriteString("\n-- log --\n")
	for _, r := range sh.cfg.Logs.Ring.Tail(500) {
		writeRecord(&b, r)
	}
	b.WriteString("\n-- events --\n")
	for _, ev := range sh.events.list() {
		fmt.Fprintf(&b, "%d %s %s\n", ev.Seq, ev.Type, withoutDetail(ev.JSON))
	}
	return sh.cfg.Logs.Redact.Redact(b.String())
}

// writeRecord is a log record as a line, with any attribute that has lines of its own (a stack) under it,
// indented, as it reads in a terminal.
func writeRecord(b *strings.Builder, r logs.Record) {
	var blocks []logs.Attr
	one := r
	one.Attrs = nil
	for _, a := range r.Attrs {
		if strings.Contains(a.Value, "\n") {
			blocks = append(blocks, a)
			continue
		}
		one.Attrs = append(one.Attrs, a)
	}
	b.WriteString(one.Text())
	b.WriteByte('\n')
	for _, a := range blocks {
		fmt.Fprintf(b, "  %s:\n", a.Key)
		for line := range strings.SplitSeq(strings.TrimRight(a.Value, "\n"), "\n") {
			b.WriteString("    " + strings.ReplaceAll(line, "\t", "    ") + "\n")
		}
	}
}

// withoutDetail is an event's JSON without the stacks the log above already has as lines.
func withoutDetail(raw string) string {
	var m map[string]any
	if json.Unmarshal([]byte(raw), &m) != nil {
		return raw
	}
	if _, ok := m["detail"]; !ok {
		return raw
	}
	delete(m, "detail")
	b, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return string(b)
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func (sh *shell) changedSettings() []string {
	o := sh.cfg.Registry.Options()
	var out []string
	for _, spec := range o.Specs() {
		v, layer, err := o.Get(spec.Name, registry.At{})
		if err == nil && layer != registry.LayerDefault {
			out = append(out, fmt.Sprintf("%s=%v", spec.Name, v))
		}
	}
	return out
}

// Crash is a crash report on disk.
type Crash struct {
	Path    string
	Time    time.Time `json:"time"`
	Version string    `json:"version"`
	Panic   string    `json:"panic"`
	Report  string    `json:"report"`
}

// crashed writes a crash report for a panic outside every contribution, and is the error Run returns.
func (sh *shell) crashed(v any, stack []byte) error {
	err := fmt.Errorf("oriel crashed: panic: %v", v)
	if sh.cfg.CrashDir == "" {
		return err
	}
	c := Crash{Time: time.Now(), Version: sh.cfg.Version, Panic: sh.cfg.Logs.Redact.Redact(fmt.Sprint(v))}
	c.Report = sh.report(fmt.Sprintf("panic: %s\n\n%s", c.Panic, stack))
	path, werr := WriteCrash(sh.cfg.CrashDir, c)
	if werr != nil {
		return fmt.Errorf("%w (and the crash report: %v)", err, werr)
	}
	return fmt.Errorf("%w; report in %s", err, path)
}

// WriteCrash saves c in dir, named for its time.
func WriteCrash(dir string, c Crash) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "crash-"+c.Time.UTC().Format("20060102T150405.000000000")+".json")
	return path, os.WriteFile(path, b, 0o600)
}

// PendingCrashes are the reports in dir not yet deleted, newest first.
func PendingCrashes(dir string) []Crash {
	files, _ := filepath.Glob(filepath.Join(dir, "crash-*.json"))
	var out []Crash
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var c Crash
		if json.Unmarshal(b, &c) == nil {
			c.Path = f
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b Crash) int { return b.Time.Compare(a.Time) })
	return out
}
