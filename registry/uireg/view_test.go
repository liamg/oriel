package uireg_test

import (
	"errors"
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/ui"
)

func TestDisposeUnderneathKeepsOverride(t *testing.T) {
	r := uireg.New()
	base := r.View("v", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return ui.TextNode("base") }))
	r.View("v", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return ui.TextNode("override") }))
	base.Dispose()
	v, ok := r.ViewByID("v")
	if tx, isText := v.Build(uireg.ViewCtx{}).(*ui.Text); !ok || !isText || tx.Text != "override" {
		t.Error("disposing the base view removed the override")
	}
}

// Report counts any contribution's errors the same way: a view's, a decoration provider's.
func TestReport(t *testing.T) {
	r := uireg.New()
	r.View("status", uireg.SourcedView{Source: "init.lua:31", View: uireg.ViewFunc(nil)})
	var faults []registry.Fault
	r.OnFault(func(f registry.Fault) { faults = append(faults, f) })
	for range 2 {
		r.Report(registry.KindView, "status", errors.New("x"))
	}
	r.Report(registry.KindView, "status", nil)
	for range 3 {
		r.Report(registry.KindView, "status", errors.New("y"))
	}
	if len(faults) != 1 || faults[0].Source != "init.lua:31" || faults[0].Errors != 3 {
		t.Errorf("faults %+v", faults)
	}
}

// A slot view stands in for a view on sessions of its kinds, and only there.
func TestSlotView(t *testing.T) {
	front := &core.SessionState{Kind: "work"}
	r := kindsReg(front)
	built := ""
	r.View("pr", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { built = "raise"; return nil }))
	r.Contribute(uireg.SlotView{Name: "review.pr", Slot: "pr", Kinds: []core.Kind{"review"},
		View: uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { built = "review"; return nil })})
	build := func() string {
		v, ok := r.ViewByID("pr")
		if !ok {
			t.Fatal("no pr view")
		}
		v.Build(uireg.ViewCtx{})
		return built
	}
	if got := build(); got != "raise" {
		t.Errorf("a work session's pr view = %s", got)
	}
	front.Kind = "review"
	if got := build(); got != "review" {
		t.Errorf("a review's pr view = %s", got)
	}
}
