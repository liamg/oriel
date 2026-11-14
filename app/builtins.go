package app

import (
	"cmp"
	"path/filepath"

	"github.com/liamg/oriel/app/agentui"
	"github.com/liamg/oriel/app/editor"
	"github.com/liamg/oriel/app/explain"
	"github.com/liamg/oriel/app/gitui"
	"github.com/liamg/oriel/app/lang"
	"github.com/liamg/oriel/app/luaconfig"
	"github.com/liamg/oriel/app/overview"
	"github.com/liamg/oriel/app/rail"
	"github.com/liamg/oriel/app/review"
	appsessions "github.com/liamg/oriel/app/sessions"
	"github.com/liamg/oriel/app/settings"
	"github.com/liamg/oriel/app/shell"
	"github.com/liamg/oriel/app/ship"
	"github.com/liamg/oriel/app/stats"
	"github.com/liamg/oriel/app/terminal"
	"github.com/liamg/oriel/app/updates"
	"github.com/liamg/oriel/app/watches"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/syntax"
	"github.com/liamg/oriel/term/ghostty"
)

// builtins are the window's built-in plugins, in the order they start: the shell's own (p) and the features'. They're
// made once and run as long as the app: what they keep per directory follows the window's (kit.WindowDir,
// p.DirState, DirChanged).
func (cfg Config) builtins(p shell.Parts) []plugin.Plugin {
	c := p.Config // the window's, as it starts
	wasmCaches(c)
	dirState := func(name string) func() string { return func() string { return p.DirState(name) } }
	ed, gits := &editor.Editor{Files: c.Files, Clipboard: p.Clipboard, Tools: p.Tools}, &gitui.Git{}
	st, voice := &stats.Plugin{}, &agentui.Voice{Config: cfg.Voice}
	out := []plugin.Plugin{p.UI, p.Frames, &watches.Watches{Timing: cfg.WatchTiming}}
	// The agent providers come before the plugins and config that may register their own under the same name.
	out = append(out, cfg.Agents...)
	return append(out, &editor.Languages{Dir: filepath.Join(c.ConfigDir, "languages")}, p.Themes, p.Layout, gits,
		&rail.Rail{DirState: p.DirState}, &gitui.Explorer{Git: gits}, p.Start, &overview.Overview{WorkDir: p.WorkDir}, p.Introspect, p.StatusLine, p.SessionTabs, &settings.About{Version: c.Version},
		&stats.HUD{}, &agentui.Inbox{}, p.Omnibox, p.Picker, p.WhichKey, &settings.Consent{}, p.Help, p.Nav, p.Menus,
		p.Notices, &lang.Problems{ConfigDir: c.ConfigDir}, &settings.ThemeEditor{Dir: filepath.Join(c.ConfigDir, "themes")},
		&explain.Presenter{}, ed, &editor.Markdown{Pictures: p.Pictures}, &editor.Images{}, p.Desktop, &editor.GitSigns{Editor: ed},
		&editor.Search{Editor: ed, Tools: p.Tools}, &lang.LSP{Version: c.Version, Logger: c.Logs.Logger, Servers: cfg.LanguageServers},
		lang.TestsEntries(), &lang.Tests{HistoryFile: dirState("tests")}, lang.DebugEntries(),
		&lang.Debug{StateFile: dirState("debug"), Logger: c.Logs.Logger},
		&explain.Plugin{}, &explain.Walkthrough{ConfigDir: c.ConfigDir},
		&terminal.Terminals{SavedAt: dirState("terminals"), Clipboard: p.Clipboard},
		&settings.Plugin{ConfigDir: c.ConfigDir}, &ship.CodeSites{}, &appsessions.Sessions{Host: c.Host, ConfigDir: c.ConfigDir},
		p.Quit, &settings.SandboxSettings{Backends: cfg.SandboxBackends}, &agentui.Console{}, &agentui.Permissions{},
		&appsessions.Workspace{}, st, &stats.Page{Stats: st}, &appsessions.Jobs{}, &appsessions.Repos{}, &stats.Machine{Load: cfg.Load}, &stats.Perf{Load: cfg.Load},
		&updates.Update{Updates: cfg.Updates, StateDir: c.StateDir}, &settings.Prompts{ConfigDir: c.ConfigDir}, agentui.NewVoiceEntries(voice), voice,
		&agentui.Assistant{ConfigDir: c.ConfigDir, StateDir: c.StateDir}, &appsessions.Tickets{}, &review.Reviews{}, &ship.GitHubHost{Client: cfg.GitHubClient},
		&appsessions.Launcher{}, &agentui.Requests{}, &appsessions.Plan{}, &gitui.Diff{}, &ship.Ship{ConfigDir: c.ConfigDir}, &review.PRReview{},
		&appsessions.End{}, &ship.AIReview{ConfigDir: c.ConfigDir}, &ship.Verify{ConfigDir: c.ConfigDir}, &ship.GitHub{}, &settings.Secrets{},
		&luaconfig.Lua{Dir: c.ConfigDir}, &settings.Catalog{GitHub: cfg.GitHub, ConfigDir: c.ConfigDir,
			DataDir: cmp.Or(cfg.DataDir, DefaultDataDir()), StateDir: c.StateDir, CacheDir: c.CacheDir, Bundled: cfg.Bundled, Self: cfg.Self},
	)
}

// wasmCaches points the grammar and terminal runtimes at the window's cache before any plugin can use them: they're
// the process's, so a later window's own dir (a test's) is logged as not taken, not dropped silently.
func wasmCaches(c shell.Config) {
	if c.CacheDir == "" {
		return
	}
	for _, err := range []error{syntax.SetCacheDir(c.CacheDir), ghostty.SetCacheDir(filepath.Join(c.CacheDir, "wasm"))} {
		if err != nil && c.Logs != nil {
			c.Logs.Logger("app").Debug("wasm cache", "err", err)
		}
	}
}
