//go:build showcase

package lang_test

import (
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/lang"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

func TestShowcaseLSP(t *testing.T) {
	s, _ := apptest.LSPScene(t, "go", "gopls", "main.go")
	s.Shot("08-lsp-diagnostics")
	s.CursorAt(8, 11)
	s.Press("K")
	s.LSPSettle()
	s.Shot("09-lsp-hover")
	s.Press("<Esc>")
	s.Press("grr")
	s.LSPSettle()
	s.Shot("10-lsp-references")
	s.Press("<Esc>")
	s.LSPSettle()

	c, _ := apptest.LSPScene(t, "go", "gopls", "main.go")
	c.EditorKeys("11GS<Tab>gre")
	c.LSPSettle()
	c.Shot("11-lsp-completion")

	r, _ := apptest.LSPScene(t, "go", "gopls", "main.go")
	r.CursorAt(8, 11)
	r.Press("grn")
	r.LSPSettle()
	for _, k := range "salute" {
		r.UI().Key(render.KeyEvent{Key: string(k)})
		r.Frame()
	}
	r.UI().Key(render.KeyEvent{Key: "CR"})
	r.LSPSettle()
	r.Shot("12-lsp-rename")

	m, _ := apptest.LSPScene(t, "go", "gopls", "main.go")
	m.EditorKeys(`:%s/\<greeting\>/salute/g`)
	m.LSPSettle()
	m.Shot("13-substitute-offers-rename")
}

func TestShowcaseTests(t *testing.T) {
	s, dir := apptest.TestsScene(t)
	s.Shot("16-test-gutter")
	s.Run("test.run.file", nil)
	s.RunDone()
	s.EditorKeys("20Gzt")
	s.Shot("17-test-failure-block")
	s.Run("view.open", registry.Args{"id": lang.TestsView, "place": "dock", "edge": "bottom", "size": 320})
	s.Frame()
	s.ClickText("-3.5")
	s.Shot("18-tests-panel")
	s.Run("test.coverage", nil)
	s.RunDone()
	s.Run("editor.open", registry.Args{"path": filepath.Join(dir, "round.go")})
	s.Shot("19-coverage")
}

const showcaseColours = `printf '\033]7;file://host/src/pricing\a'
printf '\033]133;A\a\033[1;32m~/src/pricing\033[0m $ \033]133;B\agit log --oneline -3\n\033]133;C\a'
printf '\033[33m9f2c1ab\033[0m (\033[1;36mHEAD -> \033[1;32mrethink/rounding\033[0m) round half to even\n'
printf '\033[33m41d0e77\033[0m currency type\n\033[33m0be3f19\033[0m base\n\033]133;D;0\a'
printf '\033]133;A\a\033[1;32m~/src/pricing\033[0m $ \033]133;B\ago test ./...\n\033]133;C\a'
printf '\033[32mok  \033[0m  example.com/pricing/billing  0.012s\n'
printf '\033[31m--- FAIL: TestRoundHalfEven/-3.5\033[0m\n    round_test.go:22: got -3, want -4\n\033[31mFAIL\033[0m\n\033]133;D;1\a'
printf '\033]133;A\a\033[1;32m~/src/pricing\033[0m $ \033]133;B\aprintf "%%s\\n" box\n\033]133;C\a'
printf '┌──────────┬──────────┐\n│ \033[1mpackage\033[0m  │ \033[1mcoverage\033[0m │\n├──────────┼──────────┤\n│ pricing  │ \033[38;2;255;155;69m 82.4%%\033[0m   │\n│ billing  │ \033[38;5;42m 97.1%%\033[0m   │\n└──────────┴──────────┘\n'
printf '\033]133;D;0\a\033]133;A\a\033[1;32m~/src/pricing\033[0m $ \033]133;B\a'
exec cat`

func TestShowcaseTerminal(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	s.Frame()
	s.Run("term.open", registry.Args{"cmd": []string{"sh", "-c", showcaseColours}})
	s.Frame()
	s.WaitTerm("97.1%")
	s.Shot("20-terminal")
	s.Press("<C-Esc>")
	s.TypeTerm("G[[[[")
	s.TypeTerm("V")
	s.TypeTerm("jj")
	s.Shot("21-terminal-normal")

	b := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	b.Frame()
	b.Run("term.open", registry.Args{"cmd": []string{"sh", "-c", showcaseColours}, "bang": true})
	b.Frame()
	b.WaitTerm("97.1%")
	b.Run("term.open", registry.Args{"cmd": []string{"sh", "-c", "printf 'second terminal\\n'; exec cat"}, "place": "float"})
	b.Frame()
	b.WaitTerm("second terminal")
	b.Shot("22-terminal-placements")
}
