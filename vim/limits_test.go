package vim_test

import (
	"testing"

	"github.com/liamg/oriel/app/kit/vimreg"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/vim"
)

// A register that runs itself stops at 'maxmapdepth' levels with an error, rather than overflowing the stack. nvim
// loops until interrupted, so there's no oracle for this.
func TestRecursiveRegisterStops(t *testing.T) {
	e := multiEngine(t, "abc", 0, 1)
	keys(e, "qa0@aq")
	keys(e, "$@a")
	if n := e.LastNotice(); n.ID != "vim.recursive-register" {
		t.Fatalf("notice %q (%s), want vim.recursive-register", n.ID, n.Text)
	}
	if got := e.Cursor(); got != (vim.Pos{}) {
		t.Errorf("cursor %v, want 0:0", got)
	}
}

type fixedSettings map[string]any

func (s fixedSettings) Get(name, _, _ string) (any, bool) { v, ok := s[name]; return v, ok }
func (s fixedSettings) Set(string, vim.OptionScope, string, string, any) error {
	return nil
}

// A tabstop or shiftwidth out of range from the settings is ignored: the defaults (8 and 8, tabs) stand.
func TestOptionsOutOfRangeFromSettings(t *testing.T) {
	e := multiEngine(t, "\tx", 0, 0)
	e.SetSettings(fixedSettings{"tabstop": 0, "shiftwidth": -1})
	keys(e, ">>$")
	if got := bufText(e); got != "\t\tx" {
		t.Errorf("buffer %q, want %q", got, "\t\tx")
	}
}

// The registry refuses what :set refuses (options.txt *E487*): a tabstop under 1, a negative shiftwidth.
func TestRegistryRefusesOutOfRange(t *testing.T) {
	r := registry.New()
	vimreg.Register(r)
	o := r.Options()
	for name, v := range map[string]int{"editor.tab_stop": 0, "editor.shift_width": -1, "editor.report": -1} {
		if err := o.Set(name, registry.LayerUser, registry.At{}, v); err == nil {
			t.Errorf("%s = %d: no error", name, v)
		}
	}
	if err := o.Set("editor.shift_width", registry.LayerUser, registry.At{}, 0); err != nil {
		t.Errorf("shift_width = 0: %v", err)
	}
}
