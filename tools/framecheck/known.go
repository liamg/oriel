package framecheck

// Known is the I/O still reachable from a frame, by Hit.Key. It may only shrink.
var Known = []string{
	// A code action or rename the user picked: the workspace edit is applied, and previewed, before anything that
	// follows it reads the files.
	"app/lang.(*LSP).actionsMenu.func1 → app/lang.(*LSP).applyEdit: os.Stat",
	"app/lang.(*LSP).actionsMenu.func1 → app/lang.(*LSP).applyEdit: os.WriteFile",
	"app/lang.(*LSP).actionsMenu.func1 → app/lang.(*LSP).applyEdit: os.Rename",
	"app/lang.(*LSP).actionsMenu.func1 → app/lang.(*LSP).applyEdit: os.RemoveAll",
	"app/lang.(*LSP).actionsMenu.func1 → app/lang.(*LSP).applyEdit: os.ReadFile",
	"app/lang.(*LSP).renameInput.func1.func1 → app/lang.(*LSP).previewEdit: os.ReadFile",

	// A setting changed on the Settings page: its TOML file is read, rewritten and read back into the option layer
	// in the same click, so the page and the options agree at the next frame.
	"app/settings.(*Plugin).build.func1.func1.func2.func1 → app/settings.(*Plugin).edit: os.ReadFile",
	"app/settings.(*Plugin).build.func1.func1.func2.func1 → app/settings.(*Plugin).edit: os.MkdirAll",
	"app/settings.(*Plugin).build.func1.func1.func2.func1 → app/settings.(*Plugin).edit: os.WriteFile",
	"app/settings.(*Plugin).build.func1.func1.func2.func1 → app/settings.(*Plugin).trustFile: os.ReadFile",
	"app/settings.(*Plugin).footer.func1 → app/settings.(*Plugin).undoLast: os.WriteFile",
	"app/settings.(*Plugin).footer.func1 → app/settings.(*Plugin).undoLast: os.Remove",

	// The theme editor's edits: the theme resolves from the file it writes, so it's written before the next frame.
	"app/settings.(*ThemeEditor).contrast.func1.func2 → app/settings.(*ThemeEditor).edit: os.ReadFile",
	"app/settings.(*ThemeEditor).contrast.func1.func2 → app/settings.(*ThemeEditor).edit: os.WriteFile",
	"app/settings.(*ThemeEditor).contrast.func1.func2 → app/settings.(*ThemeEditor).target: os.Stat",
	"app/settings.(*ThemeEditor).contrast.func1.func2 → app/settings.(*ThemeEditor).target: os.MkdirAll",
	"app/settings.(*ThemeEditor).contrast.func1.func2 → app/settings.(*ThemeEditor).target: os.WriteFile",
	"app/settings.(*ThemeEditor).strip.func1.func3 → app/settings.(*ThemeEditor).undoLast: os.WriteFile",
}
