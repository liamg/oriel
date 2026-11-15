package app_test

import (
	"testing"

	"github.com/liamg/oriel/app/apptest"
)

// A full-window page's text selects: what a setting does, an error a page shows.
func TestAPagesTextSelects(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	s.Run("settings.open", nil)
	s.Quiet()
	if got := s.SelectAcross("Theme"); got == "" {
		t.Errorf("a drag across the settings page's text selected nothing")
	}
}
