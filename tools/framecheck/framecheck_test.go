package framecheck

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/tools/layout"
	"github.com/liamg/oriel/tools/ratchet"
)

// scanned are the packages whose code can run in a frame.
var scanned = []string{"app", "ui", "registry", "plugin", "render", "theme", "syntax", "gitx", "host", "core", "text", "vim",
	"edit", "lsp", "dap", "term", "testrun", "agent", "sandbox", "script", "fswatch", "voice", "catalog", "assistant", "gh", "update"}

func files(t *testing.T) []ratchet.File {
	wd, _ := os.Getwd()
	root, err := layout.Root(wd)
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, d := range scanned {
		if _, err := os.Stat(root + "/" + d); err == nil {
			dirs = append(dirs, d)
		}
	}
	fs, err := ratchet.Parse(root, dirs...)
	if err != nil {
		t.Fatal(err)
	}
	return slices.DeleteFunc(fs, func(f ratchet.File) bool { return testHelper(f.Path) })
}

// testHelper is a package of test harnesses: it never runs in the app.
func testHelper(p string) bool {
	for _, h := range []string{"apptest/", "plugintest/", "hosttest/", "gittest/", "agenttest/", "vimtest/", "voicetest/", "ghtest/"} {
		if strings.HasPrefix(p, h) || strings.Contains(p, "/"+h) {
			return true
		}
	}
	return false
}

// No file, process or network I/O is reachable from code that builds a frame, but for Known,
// which may only shrink.
func TestNoIOInFrames(t *testing.T) {
	seen := map[string]bool{}
	for _, h := range Check(files(t), layout.Module) {
		seen[h.Key()] = true
		if !slices.Contains(Known, h.Key()) {
			t.Error(h)
		}
	}
	for _, k := range Known {
		if !seen[k] {
			t.Errorf("%s is no longer reached: take it off Known", k)
		}
	}
}
