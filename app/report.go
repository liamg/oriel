package app

import (
	"encoding/json"
	"fmt"
	"github.com/liamg/oriel/theme"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/ext"
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
	layout  *layoutExt
	poke    func() // asks the loop for a frame
	keys    *registry.Resolver
	ui      *ui.UI
	modes   func() []string
	// enginePending is the focused editor's pending keys, when the editor is in front.
	enginePending func() ext.Pending
	theme         func() *theme.Resolved
	todoMu        sync.Mutex
	todo          []func()                         // Host.Do's, for the loop
	editorAt      func(at registry.At) registry.At // the focused editor's buffer and window, added to at
	shot          func(th *theme.Resolved) *image.RGBA
	reopen        chan string // Open Directory's directory, for the loop to start again on
	recents       *recents
	git           *gitExt // the work tree's git status
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
		b.WriteString(r.Text())
		b.WriteByte('\n')
	}
	b.WriteString("\n-- events --\n")
	for _, ev := range sh.events.list() {
		fmt.Fprintf(&b, "%d %s %s\n", ev.Seq, ev.Type, ev.JSON)
	}
	return sh.cfg.Logs.Redact.Redact(b.String())
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
