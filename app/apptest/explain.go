package apptest

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
)

// FakeExplainer answers "Answer n (depth)", or its answer once it's set, and keeps what it was asked.
type FakeExplainer struct {
	mu     sync.Mutex
	answer string
	reqs   []registry.ExplainRequest
}

// Explain is a registry.Explainer's Explain.
func (f *FakeExplainer) Explain(_ context.Context, r registry.ExplainRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, r)
	if f.answer != "" {
		return f.answer, nil
	}
	return fmt.Sprintf("Answer %d (%s)", len(f.reqs), r.Depth), nil
}

// SetAnswer has every explanation from now on be a.
func (f *FakeExplainer) SetAnswer(a string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answer = a
}

// Asked is what it's been asked, in order.
func (f *FakeExplainer) Asked() []registry.ExplainRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.reqs)
}

// ExplainScene is round.go committed, then changed in the work tree, open in the editor, with a fake explainer; it
// answers round.go's path.
func ExplainScene(t *testing.T) (*Scene, *FakeExplainer, string) {
	r := gittest.New(t)
	r.Write(map[string]string{"round.go": rig.ExplainBase})
	r.Commit("Ada", "rounding")
	r.Write(map[string]string{"round.go": rig.ExplainWork})
	s := New(t, Options{WorkDir: r.Dir})
	s.WaitGit()
	f := &FakeExplainer{}
	s.Registry().Contribute(registry.Explainer{Name: "fake", Explain: f.Explain, Label: func() string { return "fork of fake · knows this session" }})
	path := filepath.Join(r.Dir, "round.go")
	s.Run("editor.open", registry.Args{"path": path})
	s.WaitSyntax()
	s.ExplainSettle()
	return s, f, path
}

// ExplainSettle frames until the units, the base and the answers worked out off the loop are in (Quiet).
func (s *Scene) ExplainSettle() {
	s.T.Helper()
	s.Quiet()
}

// ExplainKeys presses keys as the window does, through the keymaps and key layers, settling after each.
func (s *Scene) ExplainKeys(keys ...string) {
	for _, k := range keys {
		s.Press(k)
		s.ExplainSettle()
	}
}

// UnitMarks are the lines (1-based) with a ? in the current editor window's gutter, and each one's style.
func (s *Scene) UnitMarks() map[int]string {
	out := map[int]string{}
	for line, ds := range s.Decorations(0, s.EditorWindow().Text.Snapshot().Lines()) {
		for _, d := range ds {
			if d.Kind == uireg.Sign && d.Text == "?" {
				out[line+1] = d.Style
			}
		}
	}
	return out
}

// WalkScene is a session on two projects, pricing (round.go and internal/billing/invoice.go changed) and billing-ui
// (Total.tsx changed), with a fake explainer that writes rig.WalkAnswer; it answers the two projects' directories.
func WalkScene(t *testing.T) (s *Scene, f *FakeExplainer, pricing, billing string) {
	t.Helper()
	return WalkSceneAt(t, render.Point{X: 1600, Y: 1000})
}

// WalkSceneAt is WalkScene in a window of size.
func WalkSceneAt(t *testing.T, size render.Point) (s *Scene, f *FakeExplainer, pricing, billing string) {
	t.Helper()
	p := gittest.New(t)
	p.Write(map[string]string{"round.go": rig.ExplainBase, "internal/billing/invoice.go": rig.InvoiceBase})
	p.Commit("Ada", "rounding")
	p.Write(map[string]string{"round.go": rig.ExplainWork, "internal/billing/invoice.go": rig.InvoiceWork})
	b := gittest.New(t)
	b.Write(map[string]string{"Total.tsx": "export const Total = ({ lines }) => sum(lines);\n"})
	b.Commit("Ada", "total")
	b.Write(map[string]string{"Total.tsx": "export const Total = ({ lines }) => sumRounded(lines);\n"})
	s = New(t, Options{WorkDir: p.Dir, Size: size})
	se := core.SessionState{ID: "pricing-rounding", Name: "pricing-rounding", Kind: core.KindWork, Dir: p.Dir,
		Projects: []core.Project{{Name: "pricing", Repo: p.Dir, Dir: p.Dir, Branch: "rethink/rounding", Primary: true},
			{Name: "billing-ui", Repo: b.Dir, Dir: b.Dir, Branch: "rethink/rounding"}},
		Agent: core.AgentState{Provider: "claude"}}
	s.Apply(core.SessionSynced{Session: se}, core.SessionFocused{ID: se.ID})
	s.WaitGit()
	f = &FakeExplainer{answer: rig.WalkAnswer}
	s.Registry().Contribute(registry.Explainer{Name: "fake", Explain: f.Explain, Label: func() string { return "fork of fake · knows this session" },
		Byline: func() string { return "written by fake from the session's own context" }})
	return s, f, p.Dir, b.Dir
}

// WalkSettle frames until the walkthrough asked for off the loop is in (Quiet).
func (s *Scene) WalkSettle() {
	s.T.Helper()
	s.Quiet()
}

// WalkKeys presses keys as the window does, a frame after each.
func (s *Scene) WalkKeys(keys ...string) {
	for _, k := range keys {
		s.Press(k)
		s.Frames(1)
	}
}
