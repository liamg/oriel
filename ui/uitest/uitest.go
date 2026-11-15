// Package uitest makes a toolkit UI for a test that draws without an app.
package uitest

import (
	"testing"

	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// New is a UI with the bundled faces and the built-in theme's v variant, its motion on.
func New(t testing.TB, v theme.Variant) *ui.UI {
	t.Helper()
	ts, err := typeset.NewBundled()
	if err != nil {
		t.Fatal(err)
	}
	st, err := theme.Stack(theme.Builtins(), "oriel")
	if err != nil {
		t.Fatal(err)
	}
	th, err := theme.Resolve(st, v)
	if err != nil {
		t.Fatal(err)
	}
	u := ui.New(ts, ui.TextStyle{})
	u.SetTheme(th)
	o := u.Options()
	o.ReducedMotion = false
	u.SetOptions(o)
	return u
}
