package lang

import (
	"log/slog"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/syntax"
	"github.com/liamg/oriel/text"
)

// window is the editor's current window; its Text is nil before there's one.
func window(r registry.Registry) registry.EditorWindow {
	if ed, ok := registry.EditorsOf(r); ok {
		if w, ok := ed.Window(""); ok {
			return w
		}
	}
	return registry.EditorWindow{}
}

// editorMode is the current editor window's mode: "normal", "insert"…
func editorMode(r registry.Registry) string {
	if ed, ok := registry.EditorsOf(r); ok {
		return ed.Mode()
	}
	return ""
}

// editorDir is the directory the editor's relative file names are under.
func editorDir(r registry.Registry) string {
	if ed, ok := registry.EditorsOf(r); ok {
		return ed.Dir()
	}
	return ""
}

// openBuffer is the listed buffer holding the file at path, if one does.
func openBuffer(r registry.Registry, path string) (registry.EditorBuffer, bool) {
	if ed, ok := registry.EditorsOf(r); ok {
		return ed.Buffer(path)
	}
	return registry.EditorBuffer{}, false
}

// syntaxOf is buf's parse, once the editor has made it.
func syntaxOf(r registry.Registry, buf *text.Buffer) (*syntax.Document, bool) {
	if ed, ok := registry.EditorsOf(r); ok {
		return ed.Syntax(buf)
	}
	return nil, false
}

// logTo is the log a source's lines go to: logger's for it, else the plugin's own.
func logTo(h plugin.Host, logger func(source string) *slog.Logger, source string) *slog.Logger {
	if logger == nil {
		return h.Log()
	}
	return logger(source)
}
