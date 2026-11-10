//go:build showcase

package editor_test

import (
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/editor"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

func TestShowcaseGutter(t *testing.T) {
	s, _ := apptest.SignsScene(t)
	s.CursorTo(1)
	s.Shot("04-gutter-blame")
	s.CursorTo(3)
	s.Run("git.hunk.preview", nil)
	s.SettleSigns()
	s.Shot("05-hunk-preview")
}

func TestShowcaseSearch(t *testing.T) {
	s, dir := apptest.SearchScene(t)
	s.Run("editor.open", registry.Args{"path": filepath.Join(dir, "internal/pricing/tiers.go")})
	s.Frame()
	s.Click(render.Point{X: 22, Y: kit.TitleH + 30 + 17 + 34}, 0)
	s.Frame()
	s.TypeInto("Find", `Round(\w*)\(`)
	s.WaitSearch("in 2 files")
	s.Shot("06-search")
	s.TypeInto("Replace", `Rounded$1(`)
	s.Frame()
	s.Click(apptest.Centre(s.Node("Replace all…").Bounds), 0)
	s.Until("the replace's diff", func() bool { return s.Showing(editor.ReplaceView) })
	s.Shot("07-replace-preview")
}

func TestShowcaseMultiCursor(t *testing.T) {
	s, root := apptest.SearchScene(t)
	s.Run("editor.open", registry.Args{"path": root + "/internal/pricing/round.go"})
	s.Frame()
	s.EditorKeys("3G6|")
	s.Press("<C-n>")
	s.Press("<C-n>")
	s.Shot("14-multi-cursor")
	s.EditorKeys("cRoundCents")
	s.Shot("15-multi-cursor-typed")
}
