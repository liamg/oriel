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

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
)

// IssueURL is where a report goes, by hand: Oriel never sends one itself.
const IssueURL = "https://github.com/liamg/oriel/issues/new"

// shell is what the built-ins share: the config, the event tail, the desktop.
type shell struct {
	cfg     Config
	events  *eventTail
	desktop Desktop
	sites   *codeSites // where remotes and pull requests are hosted
	layout  *layoutPlugin
	poke    func() // asks the loop for a frame
	keys    *registry.Resolver
	ui      *ui.UI
	modes   func() []string
	// enginePending is the focused editor's pending keys, when the editor is in front.
	enginePending func() plugin.Pending
	// engineHints are the focused editor engine's own commands after keys a mapping is waiting on.
	engineHints func(keys []string) []plugin.Hint
	theme       func() *theme.Resolved
	todoMu      sync.Mutex
	todo        []func()                         // Host.Do's, for the loop
	editorAt    func(at registry.At) registry.At // the focused editor's buffer and window, added to at
	shot        func(th *theme.Resolved) *image.RGBA
	reopen      chan reopenTo // the directory for the loop to start again on: Open Directory's, or a session's
	recents     *recents
	secretsOnce sync.Once
	secrets     plugin.Secrets   // the store plugins' requests take secrets from, each one handed out redacted from the logs
	git         *gitPlugin       // the work tree's git status
	active      *[]plugin.Plugin // the built-ins and the config's plugins, as they started
	loaded      []plugin.Plugin  // the plugins started since by Host.Load, still running
	editor      *editorPlugin
	search      *projectSearch
	tests       *testsPlugin
	explain     *explainPlugin
	sessions    *sessions                    // the sessions; nil while it isn't active
	ship        *shipPlugin                  // the Ship page; nil while it isn't active
	stats       *statsPlugin                 // what the sessions cost
	hud         *hudPlugin                   // the HUD, and what every slot's indicators last built
	inbox       *inboxPlugin                 // every card waiting on you, and those answered
	present     *presentFocus                // the presentation with focus, for its keys
	afterOpen   *afterOpen                   // what session.open runs once the window has opened again on the session
	draining    atomic.Pointer[quitDrain]    // the session host handing over as Oriel quits
	closeWin    func()                       // closes the window, which quits
	relaunch    atomic.Pointer[func() error] // starts Oriel again once the window has closed: an update's restart
	launchDone  bool                         // the window has started once: mission control opens at launch only
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
	for _, e := range sh.cfg.Plugins {
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
