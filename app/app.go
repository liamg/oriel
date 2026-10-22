package app

import (
	"context"
	"net/http"
	"os"
	"path/filepath"

	"github.com/liamg/oriel/app/agentui"
	"github.com/liamg/oriel/app/settings"
	"github.com/liamg/oriel/app/shell"
	"github.com/liamg/oriel/catalog"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/fswatch"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/host/sysload"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/watch"
)

type Config struct {
	Store    core.Store      // the materialised state, shared with the session host; Host's, else in-memory, if nil
	Registry uireg.Registry  // a fresh one if nil
	Plugins  []plugin.Plugin // activated in order, after the built-ins
	// Agents are the agent providers' plugins (app/agents), activated before every other feature; none if nil.
	Agents     []plugin.Plugin
	Logs       *Logs  // where log records go; a ring in memory if nil
	ConfigDir  string // ~/.config/oriel unless set; user themes live in its themes/
	CrashDir   string // crash reports are written to and offered from here; none if ""
	Version    string
	Desktop    Desktop  // the clipboard and the browser; the window's, or the OS's, if nil
	Debug      bool     // oriel --debug: the frame-time overlay
	CPUProfile string   // oriel --cpuprofile: a CPU profile written here while the window is open
	Trace      string   // oriel --trace: the execution trace of the seconds before a stall, written here
	Files      []string // open in the editor at start
	WorkDir    string   // the directory open in the window, where relative paths are; none if "" (the start view shows)
	StateDir   string   // state that isn't config, such as the recent directories; logs.StateDir() if ""
	CacheDir   string   // compiled grammars and the like; the user's cache directory's oriel/ if ""
	DataDir    string   // installed plugins, in its plugins/; DefaultDataDir() if ""
	// Host is the session host, in this process; no sessions if nil.
	Host *host.Host
	// Load is the machine's load, read while a view that shows it is on screen; nil reads none.
	Load *sysload.Sampler
	// Updates is Oriel updating itself; nil in a build that doesn't.
	Updates *Updates
	// Started is called once the window is open and every plugin active: what the updater's health check waits for.
	Started func()
	// Activated is called with each plugin, built-ins first, once it's active: how a harness finds them.
	Activated func(plugin.Plugin)
	Voice     VoiceConfig // dictation's microphone, transcriber and speaker; the real ones, from the voice.* options, if nil
	// Net is what plugins' HTTP goes through (http.request); a client of its own if nil.
	Net *http.Client
	// ImageNet is what markdown's remote images are fetched with; one that dials public addresses only if nil.
	ImageNet *http.Client
	// FSWatch starts the OS's file watchers; fswatch.New if nil.
	FSWatch func() (*fswatch.Watcher, error)
	// WatchTiming is how the freshness managers (package watch) settle changes and let go; the defaults when zero.
	WatchTiming watch.Timing
	// SandboxBackends are the sandbox backends this machine can run, the default first; sandbox.AvailableBackends
	// if nil.
	SandboxBackends func() []string
	// GitHub is how plugins and the plugin registries are read; GitHub's API with your gh login's token if nil.
	GitHub *catalog.GitHub
	// Secrets are the named secrets plugins' requests carry; secrets.json in StateDir if nil.
	Secrets plugin.Secrets
	// Bundled are the plugins shipped in the binary, loaded by the plugin manager unless turned off or installed.
	Bundled []plugin.Bundled
	// Self is the command that runs oriel itself, for bundled plugins' processes; os.Executable() if nil.
	Self []string
}

// The types Config's fields name, and the shell's that the app's callers use.
type (
	Logs         = shell.Logs
	LogOpts      = shell.LogOpts
	Desktop      = shell.Desktop
	Headless     = shell.Headless
	HeadlessOpts = shell.HeadlessOpts
	PluginOpts   = shell.PluginOpts
	Crash        = shell.Crash
	// Updates is Oriel updating itself.
	Updates = settings.Updates
	// VoiceConfig swaps dictation's parts; nil ones are the real ones, from the voice.* options.
	VoiceConfig = agentui.VoiceConfig
)

// OverviewView is the project overview, which a test hides: its temporary paths would change every run.
const OverviewView = shell.OverviewView

// Run opens a window and runs the app until the window closes or ctx is done.
func Run(ctx context.Context, be render.Backend, cfg Config) error {
	return shell.Run(ctx, be, cfg.shell(), cfg.builtins)
}

// NewHeadless starts the app as Run does, with no window: the caller draws each frame (shell.Headless).
func NewHeadless(cfg Config, o HeadlessOpts) (*Headless, error) {
	return shell.NewHeadless(cfg.shell(), cfg.builtins, o)
}

// shell is the window's part of cfg.
func (cfg Config) shell() shell.Config {
	return shell.Config{Store: cfg.Store, Registry: cfg.Registry, Plugins: cfg.Plugins, Logs: cfg.Logs, ConfigDir: cfg.ConfigDir,
		CrashDir: cfg.CrashDir, Version: cfg.Version, Desktop: cfg.Desktop, Debug: cfg.Debug, Trace: cfg.Trace, Files: cfg.Files,
		WorkDir: cfg.WorkDir, StateDir: cfg.StateDir, CacheDir: cfg.CacheDir, Host: cfg.Host, Started: cfg.Started, Activated: cfg.Activated,
		Net: cfg.Net, ImageNet: cfg.ImageNet, FSWatch: cfg.FSWatch, Secrets: cfg.Secrets}
}

func NewLogs(o LogOpts) *Logs { return shell.NewLogs(o) }

// DefaultConfigDir is $XDG_CONFIG_HOME/oriel, or ~/.config/oriel, on every platform.
func DefaultConfigDir() string { return shell.DefaultConfigDir() }

// DefaultDataDir is $XDG_DATA_HOME/oriel, or ~/.local/share/oriel, on every platform.
func DefaultDataDir() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "oriel")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "oriel")
}

// Icon draws the app icon into the square r (shell.Icon).
func Icon(f render.Frame, r render.Rect, th *theme.Resolved) { shell.Icon(f, r, th) }

// DirPlugin is the Lua plugin in dir (shell.DirPlugin).
func DirPlugin(dir string, o PluginOpts) plugin.Plugin { return shell.DirPlugin(dir, o) }

// PendingCrashes are the crash reports in dir not yet deleted, newest first.
func PendingCrashes(dir string) []Crash { return shell.PendingCrashes(dir) }
