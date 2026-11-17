package registry_test

import (
	"context"
	"testing"

	"github.com/liamg/oriel/registry"
)

// An ex command is an alias of a command's id: :Watch x runs the command whose Ex is Watch, with what was typed as
// the argument it names, and the id alone isn't the ex name.
func TestExNameRunsItsCommand(t *testing.T) {
	r := registry.New()
	var got []registry.Args
	r.Command(registry.Command{ID: "debug.watch.add", Ex: "Watch", ExArg: "expression", Run: func(_ registry.Ctx, a registry.Args) error {
		got = append(got, a)
		return nil
	}})
	r.Command(registry.Command{ID: "term.open", Ex: "term", Run: func(_ registry.Ctx, a registry.Args) error {
		got = append(got, a)
		return nil
	}})
	ctx := context.Background()
	if err := r.RunEx(ctx, "Watch", "  len(xs) ", false); err != nil {
		t.Fatal(err)
	}
	if err := r.RunEx(ctx, "term", "htop", true); err != nil {
		t.Fatal(err)
	}
	if err := r.RunEx(ctx, "Watch", "", false); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].String("expression") != "len(xs)" || got[1].String("args") != "htop" || !got[1].Bool("bang") ||
		got[2].String("expression") != "" {
		t.Errorf("ran with %v", got)
	}
	if err := r.RunEx(ctx, "debug.watch.add", "x", false); !registry.IsNoCommand(err, "debug.watch.add") {
		t.Errorf("an id ran as an ex name: %v", err)
	}
	if err := r.Run(ctx, "Watch", nil); !registry.IsNoCommand(err, "Watch") {
		t.Errorf("an ex name ran as an id: %v", err)
	}
}

// An ex name a lazy plugin's placeholder stands for loads it, then runs.
func TestExNameLoadsItsPlaceholder(t *testing.T) {
	r := registry.New()
	provided := 0
	var ran string
	r.Placeholder(registry.Placeholder{Name: "acme/stats", Commands: []string{"stats.open", "Stats"}, Provide: func() error {
		provided++
		r.Command(registry.Command{ID: "stats.open", Ex: "Stats", Run: func(_ registry.Ctx, a registry.Args) error {
			ran = a.String("args")
			return nil
		}})
		return nil
	}})
	if err := r.RunEx(context.Background(), "Stats", "7d", false); err != nil {
		t.Fatal(err)
	}
	if provided != 1 || ran != "7d" {
		t.Errorf("provided %d times, ran with %q", provided, ran)
	}
}
