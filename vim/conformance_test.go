package vim_test

import (
	"testing"

	"github.com/liamg/oriel/vim/vimtest"
	"github.com/liamg/oriel/vim/vimtest/nvim"
)

func TestConformance(t *testing.T) {
	for _, c := range vimtest.Load(t, "testdata/*/*.vt") {
		t.Run(c.Name, func(t *testing.T) {
			c.RequireRef(t)
			if vimtest.Oracle() {
				got := vimtest.Run(t, nvim.New(), c)
				if c.Follows == "vim" || c.Deviation != "" {
					for _, d := range c.Diff(got) {
						t.Logf("%s (allowed: follows=%q deviation=%q): %s", got.Source, c.Follows, c.Deviation, d)
					}
				} else {
					c.Compare(t, got)
				}
			}
			t.Skip("no vim engine yet: ED-2 runs these cases against it")
		})
	}
}
