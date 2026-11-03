package registry

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// "Every call into a plugin, hook, view, provider or command runs under recover. A panic becomes an
// error notice with the Go stack as its detail, the offending contribution is turned off as above, and the app
// keeps running."
func TestPanickingCommandIsTurnedOff(t *testing.T) {
	r := New()
	var faults []Fault
	r.OnFault(func(f Fault) { faults = append(faults, f) })
	r.Command(Command{ID: "boom", Source: "plugin:x", Run: func(Ctx, Args) error {
		var m map[string]int
		m["x"] = 1
		return nil
	}})

	err := r.Run(context.Background(), "boom", nil)
	var p *Panic
	if !errors.As(err, &p) || !strings.Contains(string(p.Stack), "guard_test.go") {
		t.Fatalf("err = %v, want a *Panic with this file in its stack", err)
	}
	if len(faults) != 1 || faults[0].Kind != KindCommand || faults[0].ID != "boom" || faults[0].Source != "plugin:x" {
		t.Errorf("faults = %+v", faults)
	}
	if err := r.Run(context.Background(), "boom", nil); err == nil || !strings.Contains(err.Error(), "turned off") {
		t.Errorf("second run: %v", err)
	}
	if off := r.TurnedOff(); len(off) != 1 || off[0].ID != "boom" {
		t.Errorf("turned off = %+v", off)
	}
	r.TurnOn(KindCommand, "boom")
	if err := r.Run(context.Background(), "boom", nil); !errors.As(err, &p) {
		t.Errorf("after turning back on: %v", err)
	}
}

func TestCommandErrorsDontTurnItOff(t *testing.T) {
	r := New()
	r.Command(Command{ID: "fail", Run: func(Ctx, Args) error { return errors.New("no") }})
	for range 5 {
		r.Run(context.Background(), "fail", nil)
	}
	if off := r.TurnedOff(); len(off) != 0 {
		t.Errorf("turned off = %+v", off)
	}
}
