package shell

import (
	"encoding/json"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/host/search"
	"github.com/liamg/oriel/theme"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
)

// IssueURL is where a report goes, by hand: Oriel never sends one itself.
const IssueURL = "https://github.com/liamg/oriel/issues/new"

// shell is what the built-ins share: the config, the event tail, the desktop.
type shell struct {
	cfg         Config
	builtins    Builtins
	reads       *kit.Reads    // the window's files, folders and images, read off the loop
	polls       *kit.PollSet  // its periodic work
	tools       *search.Tools // the machine's search tools
	store       core.Store    // cfg.Store: the state the loop and the plugins' hosts read and change
	events      *eventTail
	desktop     Desktop
	layout      *layoutPlugin
	poke        func() // asks the loop for a frame
	keys        *registry.Resolver
	ui          atomic.Pointer[ui.UI] // set as the window opens; read by plugins' goroutines too
	widgets     widgetServices        // plugins' services for the UI's widgets (Host.ProvideUI)
	modes       func() []string
	theme       func() *theme.Resolved
	todoMu      sync.Mutex
	todo        []func() // Host.Do's, for the loop
	shot        func(th *theme.Resolved) *image.RGBA
	reopen      chan reopenTo            // the directory for the loop to start again on: Open Directory's, or a session's
	heading     atomic.Pointer[reopenTo] // what reopen holds until the loop takes it, for the opening view
	recents     *recents
	secretsOnce sync.Once
	secrets     plugin.Secrets               // the store plugins' requests take secrets from, each one handed out redacted from the logs
	active      *[]plugin.Plugin             // the built-ins and the config's plugins, as they started
	loaded      []plugin.Plugin              // the plugins started since by Host.Load, still running
	afterOpen   *afterOpen                   // what session.open runs once the window has opened again on the session
	nav         navHistory                   // the window's sessions and pages, back and forward
	draining    atomic.Pointer[quitDrain]    // the session host handing over as Oriel quits
	closeWin    func()                       // closes the window, which quits
	relaunch    atomic.Pointer[func() error] // starts Oriel again once the window has closed: an update's restart
	launchDone  bool                         // the window has started once: mission control opens at launch only
	focus       *windowFocus                 // whether the window has the OS's focus
	framed      kit.Signals                  // after each frame is drawn, on the loop
	claims      sync.Map                     // Host.Claim's keys held
	teardowns   plugin.Teardowns             // stopped plugins' slow cleanup, off the loop
}

type afterOpen struct {
	session core.SessionID
	calls   []kit.Call
}

// report is "Copy report": version, OS, settings that differ from their defaults, plugins, the
// last 500 log records and the event-log tail by kind, ID and time, redacted and with the home folder as ~, as one
// block of text. head goes first: a panic and its stack, for a crash report.
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
	var last time.Time
	for _, ev := range sh.events.list() {
		since := time.Duration(0)
		if !last.IsZero() {
			since = ev.At.Sub(last)
		}
		last = ev.At
		fmt.Fprintf(&b, "%d %s +%s %s %s\n", ev.Seq, ev.At.UTC().Format("15:04:05.000"), since.Round(time.Millisecond), ev.Type, orNone(string(ev.Scope)))
	}
	return tildeHome(sh.cfg.Logs.Redact.Redact(b.String()))
}

// tildeHome is s with the home folder as ~.
func tildeHome(s string) string {
	home, err := os.UserHomeDir()
	if err != nil || len(home) < 2 {
		return s
	}
	return strings.ReplaceAll(s, strings.TrimRight(home, string(filepath.Separator)), "~")
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
	c := Crash{Time: time.Now(), Version: sh.cfg.Version, Panic: tildeHome(sh.cfg.Logs.Redact.Redact(fmt.Sprint(v)))}
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
