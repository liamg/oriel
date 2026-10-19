//go:build showcase

package app

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/theme"
)

func (s *errScene) shot(t *testing.T, name string) {
	t.Helper()
	s.frame()
	s.frame()
	img := raster.Render(s.list, s.size, 1, s.ui.Theme().C(theme.BgBase))
	f, err := os.Create(filepath.Join(os.Getenv("ORIEL_SHOTS"), name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	png.Encode(f, img)
}

func TestShowcaseExplorer(t *testing.T) {
	s, r := explorerScene(t)
	s.run("editor.open", registry.Args{"path": filepath.Join(r.Dir, "internal/pricing/round.go")})
	s.settle()
	s.keyCommand(t, "<M-3>")
	s.keyCommand(t, "<M-1>")
	s.shot(t, "01-explorer-changed")
	s.keyCommand(t, "<M-2>")
	s.shot(t, "02-explorer-uncommitted")
	s.keyCommand(t, "<M-3>")
	s.shot(t, "03-explorer-all")
}

func TestShowcaseGutter(t *testing.T) {
	s, _ := signsScene(t)
	s.cursorTo(1)
	s.shot(t, "04-gutter-blame")
	s.cursorTo(3)
	s.run("git.hunk.preview", nil)
	s.settle()
	s.shot(t, "05-hunk-preview")
}

func TestShowcaseSearch(t *testing.T) {
	s, dir := searchScene(t)
	s.run("editor.open", registry.Args{"path": filepath.Join(dir, "internal/pricing/tiers.go")})
	s.frame()
	s.click(railAt(1))
	s.frame()
	s.typeInto(t, "Find", `Round(\w*)\(`)
	s.waitSearch(t, "in 2 files")
	s.shot(t, "06-search")
	s.typeInto(t, "Replace", `Rounded$1(`)
	s.frame()
	s.click(centre(s.access("Replace all…").Bounds))
	s.frame()
	s.shot(t, "07-replace-preview")
}

func TestShowcaseLSP(t *testing.T) {
	s, _ := lspScene(t, "go", "gopls", "main.go")
	s.shot(t, "08-lsp-diagnostics")
	s.cursorAt(8, 11)
	s.keyCommand(t, "K")
	s.lspSettle()
	s.shot(t, "09-lsp-hover")
	s.keyCommand(t, "<Esc>")
	s.keyCommand(t, "grr")
	s.lspSettle()
	s.shot(t, "10-lsp-references")
	s.keyCommand(t, "<Esc>")
	s.lspSettle()

	c, _ := lspScene(t, "go", "gopls", "main.go")
	c.keys("11GS<Tab>gre")
	c.lspSettle()
	c.shot(t, "11-lsp-completion")

	r, _ := lspScene(t, "go", "gopls", "main.go")
	r.cursorAt(8, 11)
	r.keyCommand(t, "grn")
	r.lspSettle()
	for _, k := range "salute" {
		r.ui.Key(render.KeyEvent{Key: string(k)})
		r.frame()
	}
	r.ui.Key(render.KeyEvent{Key: "CR"})
	r.lspSettle()
	r.shot(t, "12-lsp-rename")

	m, _ := lspScene(t, "go", "gopls", "main.go")
	m.keys(`:%s/\<greeting\>/salute/g`)
	m.lspSettle()
	m.shot(t, "13-substitute-offers-rename")
}

func TestShowcaseMultiCursor(t *testing.T) {
	s, root := searchScene(t)
	s.run("editor.open", registry.Args{"path": root + "/internal/pricing/round.go"})
	s.frame()
	s.keys("3G6|")
	s.keyCommand(t, "<C-n>")
	s.keyCommand(t, "<C-n>")
	s.shot(t, "14-multi-cursor")
	s.keys("cRoundCents")
	s.shot(t, "15-multi-cursor-typed")
}

func TestShowcaseTests(t *testing.T) {
	s, dir := testsScene(t)
	s.shot(t, "16-test-gutter")
	s.run("test.run.file", nil)
	s.runDone(t)
	s.keys("20Gzt")
	s.shot(t, "17-test-failure-block")
	s.run("view.open", registry.Args{"id": TestsView, "place": "dock", "edge": "bottom", "size": 320})
	s.frame()
	s.clickText(t, "-3.5")
	s.shot(t, "18-tests-panel")
	s.run("test.coverage", nil)
	s.runDone(t)
	s.run("editor.open", registry.Args{"path": filepath.Join(dir, "round.go")})
	s.shot(t, "19-coverage")
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
	s := newScene(t, "", t.TempDir(), mockupSize)
	s.frame()
	s.run("term.open", registry.Args{"cmd": []string{"sh", "-c", showcaseColours}})
	s.frame()
	s.waitTerm(t, "97.1%")
	s.shot(t, "20-terminal")
	s.keyCommand(t, "<C-Esc>")
	s.typeTerm(t, "G[[[[")
	s.typeTerm(t, "V")
	s.typeTerm(t, "jj")
	s.shot(t, "21-terminal-normal")

	b := newScene(t, "", t.TempDir(), mockupSize)
	b.frame()
	b.run("term.open", registry.Args{"cmd": []string{"sh", "-c", showcaseColours}, "bang": true})
	b.frame()
	b.waitTerm(t, "97.1%")
	b.run("term.open", registry.Args{"cmd": []string{"sh", "-c", "printf 'second terminal\\n'; exec cat"}, "place": "float"})
	b.frame()
	b.waitTerm(t, "second terminal")
	b.shot(t, "22-terminal-placements")
}

func TestShowcaseRepos(t *testing.T) {
	s := reposScene(t)
	s.shot(t, "45-repos-page")
	s.clickText(t, "New tag…")
	s.until(t, "the tag form", func() bool { return strings.Contains(s.frameText(), "Create tag v0.4.3") })
	s.shot(t, "45-repos-tag")
	s.run("repos.new", registry.Args{"mode": "clone", "url": "https://github.com/example/my-project"})
	s.until(t, "the clone target", func() bool {
		s.now = s.now.Add(100 * time.Millisecond) // past the fields' rest
		return strings.Contains(s.frameText(), "new folder")
	})
	s.shot(t, "45-repos-add")
}
