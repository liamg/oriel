package uireg

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
	"github.com/liamg/oriel/ui"
)

func TestPanickingViewIsTurnedOff(t *testing.T) {
	r := New()
	var faults []registry.Fault
	r.OnFault(func(f registry.Fault) { faults = append(faults, f) })
	r.View("bad", ViewFunc(func(ViewCtx) ui.Node {
		return &ui.Func{Fn: func(c *ui.Ctx) {
			c.Text("half", "built before the panic", ui.TextStyle{})
			panic("view bug")
		}}
	}))
	r.View("good", ViewFunc(func(ViewCtx) ui.Node { return ui.TextNode("still here") }))

	u := ui.New(headless.Shaper{}, ui.TextStyle{})
	frame := func() string {
		var l render.List
		u.Frame(l.Record(1), render.Point{X: 800, Y: 600}, func(c *ui.Ctx) {
			for _, id := range []string{"bad", "good"} {
				v, _ := r.ViewByID(id)
				c.Node(v.Build(ViewCtx{}))
			}
		})
		return headless.Frame{List: l}.Text()
	}
	got := frame()
	if strings.Contains(got, "built before") || !strings.Contains(got, "view bad is turned off: panic: view bug") || !strings.Contains(got, "still here") {
		t.Errorf("frame text:\n%s", got)
	}
	if len(faults) != 1 || faults[0].Kind != registry.KindView || faults[0].ID != "bad" {
		t.Fatalf("faults = %+v", faults)
	}
	if !strings.Contains(string(faults[0].Err.(*registry.Panic).Stack), "guard_test.go") {
		t.Error("the fault's stack doesn't reach the view")
	}
	if got := frame(); !strings.Contains(got, "turned off") || len(faults) != 1 {
		t.Errorf("second frame: %q, %d faults", got, len(faults))
	}
}
