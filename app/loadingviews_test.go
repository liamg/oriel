package app

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/vim"
)

// ui.md, "Loading": the diff says it's reading the work tree while its status hasn't been read, and a read that
// fails shows why with Retry, rather than "Reading…" for good.
func TestDiffWhileTheStatusLoads(t *testing.T) {
	r := diffRepo(t)
	s := newScene(t, "", r.Dir, mockupSize)
	s.sh.git.wait()
	s.sh.git.status = gitx.NewStatus(gitx.Repo{Dir: r.Dir})
	s.run("diff.open", registry.Args{"path": r.Dir})
	s.frame()
	s.now = s.now.Add(time.Second)
	s.frame()
	s.wantFrame(t, "Reading "+filepath.Base(r.Dir)+"'s changes…")

	s.sh.git.status = gitx.NewStatus(gitx.Repo{Dir: filepath.Join(r.Dir, "gone")})
	s.sh.git.reload()
	s.sh.git.wait()
	s.now = s.now.Add(time.Second)
	s.frame()
	s.wantFrame(t, "Retry")
	if strings.Contains(s.frameText(), "'s changes…") {
		t.Error("still reading after the read failed")
	}
}

// A diff of a directory outside any repo says so once its root has been looked for.
func TestDiffOutsideARepo(t *testing.T) {
	s := newScene(t, "", t.TempDir(), mockupSize)
	dir := t.TempDir()
	s.run("view.open", registry.Args{"id": DiffView, "instance": dir, "place": "tab"})
	s.until(t, "the root looked for", func() bool { s.frame(); return s.sh.git.settled() })
	s.now = s.now.Add(time.Second)
	s.frame()
	s.wantFrame(t, filepath.Base(dir)+" isn't in a git repository")
}

// Changed units outside a repo says so, not that it's reading.
func TestChangedUnitsOutsideARepo(t *testing.T) {
	s := newScene(t, "", t.TempDir(), mockupSize)
	s.run("explain.units", nil)
	s.now = s.now.Add(time.Second)
	s.frame()
	s.frame()
	s.wantFrame(t, "Not in a git repository")
}

// A picker source whose items are still coming shows a busy row, and "No matches" only once it's done.
func TestPickerSourceLoading(t *testing.T) {
	s := omniScene(t, "")
	s.omni().open = false
	loading := true
	s.reg.Picker(registry.PickerSource{ID: "slow", Title: "Slow", PickerOnly: true,
		Items:   func(registry.Ctx, string) []registry.PickerItem { return nil },
		Loading: func() bool { return loading }})
	s.run("picker.open", registry.Args{"source": "slow", "query": "x"})
	s.frame()
	if strings.Contains(s.frameText(), "No matches") {
		t.Error("no matches while the source is still loading")
	}
	s.now = s.now.Add(time.Second)
	s.frame()
	s.wantFrame(t, "Slow still loading…")
	loading = false
	s.now = s.now.Add(time.Second)
	s.frame()
	s.frame()
	s.wantFrame(t, "No matches")
}

// oriel.picker.source's loading = fn() is the same signal for a Lua source.
func TestLuaPickerSourceLoading(t *testing.T) {
	s := luaScene(t, `busy = true
oriel.picker.source{ id = "wiki", title = "Wiki", picker_only = true, items = function(q) return {} end, loading = function() return busy end }`)
	s.run("picker.open", registry.Args{"source": "wiki", "query": "x"})
	s.frame()
	s.now = s.now.Add(time.Second)
	s.frame()
	s.wantFrame(t, "Wiki still loading…")
	s.eval(t, `(function() busy = false end)()`)
	s.now = s.now.Add(time.Second)
	s.frame()
	s.frame()
	s.wantFrame(t, "No matches")
}

// The files picker walks the tree off the loop: it says it's loading until the walk is done, then has the files.
func TestFilesPickerWalksOffTheLoop(t *testing.T) {
	s := omniScene(t, "")
	src := mustSource(t, s, "files")
	if sourceLoading(src) {
		t.Fatal("still loading after the walk")
	}
	g, ok := rankSource(s.h(), src, "round", pickerMax)
	if !ok || g.hits[0].item.Label != "internal/pricing/round.go" {
		t.Errorf("hits: %+v", g.hits)
	}
}

// References, rename, calls and code actions asked for while the file's server starts wait for it, saying so, as
// go to definition does.
func TestLSPRequestsWaitForTheServer(t *testing.T) {
	for _, keys := range []string{"grr", "grn", "gra"} {
		s, _ := lspScene(t, "go", "gopls", "main.go")
		x := s.lsp()
		x.mu.Lock()
		clear(x.docs)
		x.mu.Unlock()
		s.editor().e.SetCursor(vim.Pos{Line: 8, Col: 11})
		s.keyCommand(t, keys)
		ns := s.store.State().Notifications
		if !slices.ContainsFunc(ns, func(n core.Notification) bool { return strings.Contains(n.Text, "Starting the go language server") }) {
			t.Errorf("%s: no word that the server is starting: %+v", keys, ns)
		}
		s.frame()
		s.wantFrame(t, "◌", "starting")
		s.lspSettle()
	}
}
