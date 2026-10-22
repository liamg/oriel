package app

import (
	"cmp"
	"path/filepath"

	"github.com/liamg/oriel/app/agentui"
	"github.com/liamg/oriel/app/editor"
	"github.com/liamg/oriel/app/explain"
	"github.com/liamg/oriel/app/gitui"
	"github.com/liamg/oriel/app/lang"
	"github.com/liamg/oriel/app/review"
	appsessions "github.com/liamg/oriel/app/sessions"
	"github.com/liamg/oriel/app/settings"
	"github.com/liamg/oriel/app/shell"
	"github.com/liamg/oriel/app/ship"
	"github.com/liamg/oriel/app/stats"
	"github.com/liamg/oriel/plugin"
)

// builtins are the window's built-in plugins, in the order they start: the shell's own (p) and the features'.
func (cfg Config) builtins(p shell.Parts) []plugin.Plugin {
	c := p.Config // the window's, as it is now
	ed, gits := &editor.Editor{WorkDir: c.WorkDir, Files: c.Files, Clipboard: p.Clipboard, Tools: p.Tools}, &gitui.Git{}
	st, voice := &stats.Plugin{}, &agentui.Voice{Config: cfg.Voice}
	return []plugin.Plugin{
		p.UI, p.Frames, &editor.Languages{Dir: filepath.Join(c.ConfigDir, "languages"), CacheDir: c.CacheDir}, p.Themes, p.Layout, gits,
		p.Rail, &gitui.Explorer{Git: gits}, p.Start, p.Overview, p.Introspect, p.StatusLine, p.SessionTabs, &settings.About{Version: c.Version},
		&stats.HUD{}, &agentui.Inbox{}, &appsessions.Mission{}, p.Omnibox, p.Picker, p.WhichKey, &settings.Consent{}, p.Help, p.Nav, p.Menus,
		p.Notices, &lang.Problems{ConfigDir: c.ConfigDir, WorkDir: c.WorkDir}, &settings.ThemeEditor{Dir: filepath.Join(c.ConfigDir, "themes")},
		&explain.Presenter{}, ed, &editor.Markdown{Pictures: p.Pictures}, &editor.Images{}, p.Desktop, &editor.GitSigns{Editor: ed},
		&editor.Search{Editor: ed, Tools: p.Tools}, &lang.LSP{WorkDir: c.WorkDir, Version: c.Version, Logger: c.Logs.Logger},
		lang.TestsEntries(), &lang.Tests{HistoryFile: c.DirState("tests")}, lang.DebugEntries(),
		&lang.Debug{WorkDir: c.WorkDir, StateFile: c.DirState("debug"), Logger: c.Logs.Logger},
		&explain.Plugin{}, &explain.Walkthrough{WorkDir: c.WorkDir, ConfigDir: c.ConfigDir},
		&lang.Terminals{CacheDir: c.CacheDir, WorkDir: c.WorkDir, SavedAt: c.DirState("terminals"), Clipboard: p.Clipboard},
		&settings.Plugin{ConfigDir: c.ConfigDir, WorkDir: c.WorkDir}, &ship.CodeSites{}, &appsessions.Sessions{Host: c.Host, ConfigDir: c.ConfigDir},
		p.Quit, &settings.SandboxSettings{WorkDir: c.WorkDir, Backends: cfg.SandboxBackends}, &agentui.Console{}, &agentui.Permissions{},
		&appsessions.Workspace{}, st, &stats.Page{Stats: st}, &appsessions.Jobs{}, &appsessions.Repos{}, &stats.Machine{Load: cfg.Load}, &stats.Perf{Load: cfg.Load},
		&settings.Update{Updates: cfg.Updates, StateDir: c.StateDir}, &settings.Prompts{ConfigDir: c.ConfigDir}, agentui.NewVoiceEntries(voice), voice,
		&agentui.Assistant{ConfigDir: c.ConfigDir, StateDir: c.StateDir}, &appsessions.Tickets{}, &review.Reviews{}, &ship.GitHubHost{},
		&appsessions.Launcher{}, &agentui.Requests{}, &appsessions.Plan{}, &gitui.Diff{}, &ship.Ship{ConfigDir: c.ConfigDir}, &review.PRReview{},
		&appsessions.End{}, &ship.AIReview{ConfigDir: c.ConfigDir}, &ship.Verify{ConfigDir: c.ConfigDir}, &ship.GitHub{}, &settings.Secrets{},
		&settings.Lua{Dir: c.ConfigDir}, &settings.Catalog{GitHub: cfg.GitHub, ConfigDir: c.ConfigDir, WorkDir: c.WorkDir,
			DataDir: cmp.Or(cfg.DataDir, DefaultDataDir()), StateDir: c.StateDir, CacheDir: c.CacheDir, Bundled: cfg.Bundled, Self: cfg.Self},
	}
}
