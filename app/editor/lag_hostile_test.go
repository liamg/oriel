//go:build !race

package editor_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// Typing in a 50,000-line file holds the hostile-data budget.
func TestTypingInAHugeFileBudget(t *testing.T) {
	apptest.FrameTimed(t)
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("package big\n\n")
	for i := range 50_000 / 5 {
		fmt.Fprintf(&b, "// f%d returns its number.\nfunc f%d() int {\n\treturn %d\n}\n\n", i, i, i)
	}
	os.WriteFile(filepath.Join(dir, "big.go"), []byte(b.String()), 0o644)
	s := apptest.New(t, apptest.Options{WorkDir: dir, Size: render.Point{X: 1600, Y: 1000}})
	s.Run("editor.open", registry.Args{"path": "big.go"})
	s.Frame()
	keys(s, "25000Go")
	waitHighlights(s)
	s.HoldsFrameBudget("typing in 50k lines", apptest.HostileP99, 100, nil, func(i int) { engine(s).Key(string(rune('a' + i%26))) })
	waitHighlights(s)
}
