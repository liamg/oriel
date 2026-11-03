package registry_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/liamg/oriel/registry"
)

type BufWritePre struct{ Buf string }

// Hooks observe, veto, rewrite or decide, in priority order; a veto or
// a decision stops the rest.
func TestHooks(t *testing.T) {
	r := registry.New()
	var seen []string
	registry.On(r, registry.Hook{Priority: 2, Kind: registry.Observe, Source: "a"}, func(_ registry.Ctx, ev *BufWritePre) (registry.Verdict, error) {
		seen = append(seen, "observe "+ev.Buf)
		return registry.Verdict{}, nil
	})
	registry.On(r, registry.Hook{Priority: 1, Kind: registry.Rewrite, Source: "b"}, func(_ registry.Ctx, ev *BufWritePre) (registry.Verdict, error) {
		ev.Buf = strings.ToUpper(ev.Buf)
		return registry.Verdict{}, nil
	})
	r.Hook(registry.Hook{Event: "BufWritePre", Pattern: "*.md", Priority: 0, Kind: registry.Veto, Source: "c",
		Fn: func(registry.Ctx, any) (registry.Verdict, error) {
			return registry.Verdict{Veto: true, Reason: "no markdown"}, nil
		}})
	ev := &BufWritePre{Buf: "main.go"}
	v, errs := r.Fire(context.Background(), "BufWritePre", "src/main.go", ev)
	if v.Veto || len(errs) > 0 || strings.Join(seen, ",") != "observe MAIN.GO" {
		t.Errorf("main.go: %+v %v, seen %v", v, errs, seen)
	}
	v, _ = r.Fire(context.Background(), "BufWritePre", "README.md", &BufWritePre{Buf: "README.md"})
	if !v.Veto || v.Reason != "no markdown" || len(seen) != 1 {
		t.Errorf("README.md: %+v, seen %v", v, seen)
	}
	r.Hook(registry.Hook{Event: "Permission", Kind: registry.Decide, Fn: func(registry.Ctx, any) (registry.Verdict, error) {
		return registry.Verdict{Decided: true, Value: "allow"}, nil
	}})
	if v, _ := r.Fire(context.Background(), "Permission", "", nil); !v.Decided || v.Value != "allow" {
		t.Errorf("decide: %+v", v)
	}
	hs := r.Hooks()
	if len(hs) != 4 || hs[0].Event != "BufWritePre" || hs[0].Source != "c" || hs[1].Calls != 1 {
		t.Errorf("hooks %+v", hs)
	}
}

// A hook that panics is turned off, as commands and views are; one that errors is reported.
func TestHookFaults(t *testing.T) {
	r := registry.New()
	var faults []registry.Fault
	r.OnFault(func(f registry.Fault) { faults = append(faults, f) })
	r.Hook(registry.Hook{Event: "X", Source: "boom", Fn: func(registry.Ctx, any) (registry.Verdict, error) { panic("oops") }})
	r.Hook(registry.Hook{Event: "X", Source: "err", Fn: func(registry.Ctx, any) (registry.Verdict, error) { return registry.Verdict{}, errors.New("bad") }})
	_, errs := r.Fire(context.Background(), "X", "", nil)
	if len(errs) != 2 || len(faults) != 1 || faults[0].Kind != registry.KindHook || faults[0].Source != "boom" {
		t.Fatalf("errs %v, faults %+v", errs, faults)
	}
	if _, errs := r.Fire(context.Background(), "X", "", nil); len(errs) != 1 {
		t.Errorf("the panicking hook still runs: %v", errs)
	}
}

func TestContributions(t *testing.T) {
	r := registry.New()
	r.Contribute(registry.AgentTool{Name: "logs", Source: "a"})
	d := r.Contribute(registry.AgentTool{Name: "logs", Source: "b"})
	r.Contribute(registry.PromptPack{Name: "hotfix"})
	if cs := r.Contributions("agent.tool"); len(cs) != 1 || cs[0].(registry.AgentTool).Source != "b" {
		t.Errorf("tools %+v", cs)
	}
	d.Dispose()
	if cs := r.Contributions("agent.tool"); cs[0].(registry.AgentTool).Source != "a" {
		t.Errorf("after dispose %+v", cs)
	}
}
