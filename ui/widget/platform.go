package widget

import (
	"runtime"

	"github.com/liamg/oriel/render"
)

// editMods are the modifiers a platform edits text with: the clipboard and select all, moving by word, and by line
// (none, past Home and End).
type editMods struct{ primary, word, line render.Mods }

var platform = platformMods(runtime.GOOS)

func platformMods(goos string) editMods {
	if goos == "darwin" {
		return editMods{primary: render.ModSuper, word: render.ModAlt, line: render.ModSuper}
	}
	return editMods{primary: render.ModCtrl, word: render.ModCtrl}
}
