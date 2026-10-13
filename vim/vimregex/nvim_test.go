package vimregex

import (
	"errors"
	"strings"
	"testing"

	"github.com/liamg/oriel/vim/vimtest/nvimtext"
)

// nvimText is an error as nvim words it, so cases can compare against nvim's message.
func nvimText(err error) string {
	var e *Error
	if !errors.As(err, &e) {
		return err.Error()
	}
	return nvimtext.Regex(e.ID, e.Args)
}

func TestEveryErrorHasNvimWording(t *testing.T) {
	for id := range errText {
		if got := nvimText(&Error{ID: id}); strings.HasPrefix(got, "no nvim wording") {
			t.Error(got)
		}
	}
}
