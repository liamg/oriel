package app

import (
	"context"
	"os"
	"path/filepath"

	"github.com/liamg/oriel/app/agentui"
	"github.com/liamg/oriel/app/shell"
	"github.com/liamg/oriel/app/updates"
	"github.com/liamg/oriel/catalog"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/lsp"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/sysload"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/watch"
)

type Config struct {
	// Window is the window's part: the state, the directories, the plugins after the built-ins, the host and the hooks.
	Window
	// Agents are the agent providers' plugins (app/agents), activated before every other feature; none if nil.
	Agents     []plugin.Plugin
	CPUProfile string // oriel --cpuprofile: a CPU profile written here while the window is open
	DataDir    string // installed plugins, in its plugins/; DefaultDataDir() if ""
	// Load is the machine's load, read while a view that shows it is on screen; nil reads none.
	Load *sysload.Sampler
	// Updates is Oriel updating itself; nil in a build that doesn't.
	Updates *Updates
	Voice   VoiceConfig // dictation's microphone, transcriber and speaker; the real ones, from the voice.* options, if nil
	// WatchTiming is how the freshness managers (package watch) settle changes and let go; the defaults when zero.
	WatchTiming watch.Timing
	// LanguageServers says which language servers may start, of those installed; every one if nil. Tests keep the
	// machine's own (gopls…) out, and run the recorded ones they register.
	LanguageServers func(lsp.ServerSpec) bool
	// SandboxBackends are the sandbox backends the settings offer, the default first; docker alone if nil.
	SandboxBackends func() []string
	// GitHub is how plugins and the plugin registries are read; GitHub's API with your gh login's token if nil.
	GitHub *catalog.GitHub
	// GitHubClient is the client GitHub's code host asks GitHub through, as host.Options.GitHub is the session host's;
	// GitHub's API with the user's token if nil.
	GitHubClient func(dir string, env []string) gh.Client
	// Bundled are the plugins shipped in the binary, loaded by the plugin manager unless turned off or installed.
	Bundled []plugin.Bundled
	// Self is the command that runs oriel itself, for bundled plugins' processes; os.Executable() if nil.
	Self []string
}

// The types Config's fields name, and the shell's that the app's callers use.
type (
	// Window is the window's part of Config (shell.Config).
	Window       = shell.Config
	Logs         = shell.Logs
	LogOpts      = shell.LogOpts
	Desktop      = shell.Desktop
	Headless     = shell.Headless
	HeadlessOpts = shell.HeadlessOpts
	PluginOpts   = shell.PluginOpts
	Crash        = shell.Crash
	// Updates is Oriel updating itself.
	Updates = updates.Updates
	// VoiceConfig swaps dictation's parts; nil ones are the real ones, from the voice.* options.
	VoiceConfig = agentui.VoiceConfig
)

// OverviewView is the project overview, which a test hides: its temporary paths would change every run.
const OverviewView = shell.OverviewView

// Run opens a window and runs the app until the window closes or ctx is done.
func Run(ctx context.Context, be render.Backend, cfg Config) error {
	return shell.Run(ctx, be, cfg.Window, cfg.builtins)
}

// NewHeadless starts the app as Run does, with no window: the caller draws each frame (shell.Headless).
func NewHeadless(cfg Config, o HeadlessOpts) (*Headless, error) {
	return shell.NewHeadless(cfg.Window, cfg.builtins, o)
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
