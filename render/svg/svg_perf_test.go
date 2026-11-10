//go:build perf

package svg

import (
	"testing"

	"github.com/liamg/oriel/render"
)

// Drawing again at the same rect allocates nothing: the paths of the last draw are reused.
func TestDrawAgainAllocBudget(t *testing.T) {
	d, err := Parse(manyPaths(200))
	if err != nil {
		t.Fatal(err)
	}
	r := render.Rect{Max: render.Point{X: 200, Y: 200}}
	f := &pathFrame{paths: make([]render.Path, 0, 400)}
	d.Draw(f, r, nil)
	if n := testing.AllocsPerRun(20, func() { f.paths = f.paths[:0]; d.Draw(f, r, nil) }); n > 0 {
		t.Fatalf("drawing again at the same rect allocates %v times", n)
	}
}
