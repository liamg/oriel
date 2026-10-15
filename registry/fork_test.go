package registry_test

import (
	"context"
	"testing"

	"github.com/liamg/oriel/registry"
)

// RV-7: side questions go to the first forker by order, which says who answered.
func TestFork(t *testing.T) {
	r := registry.New()
	if _, _, err := registry.Fork(context.Background(), r, registry.ForkRequest{Session: "s1", Prompt: "why?"}); err == nil {
		t.Error("a fork with no forker answered")
	}
	r.Contribute(registry.Forker{Name: "agent", Order: 10, Fork: func(context.Context, registry.ForkRequest) (string, error) { return "agent", nil }})
	r.Contribute(registry.Forker{Name: "mine", Order: 1, Label: func(s string) string { return "mine · " + s },
		Fork: func(_ context.Context, q registry.ForkRequest) (string, error) {
			return q.Session + ": " + q.Prompt, nil
		}})
	out, by, err := registry.Fork(context.Background(), r, registry.ForkRequest{Session: "s1", Prompt: "why?"})
	if err != nil || out != "s1: why?" || by.Name != "mine" {
		t.Errorf("got %q from %q, %v", out, by.Name, err)
	}
	if got := registry.ForkLabel(r, "s1"); got != "mine · s1" {
		t.Errorf("label %q", got)
	}
}
