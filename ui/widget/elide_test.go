package widget

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/render/typeset"
)

// Elide cuts text to a width with an ellipsis, measured by the shaper; text that fits is left alone.
func TestElide(t *testing.T) {
	sh, err := typeset.NewBundled()
	if err != nil {
		t.Fatal(err)
	}
	font := typeset.FontSpec{Family: typeset.UI, Size: 12.5}
	long := "In api, web and pricing, bump every dependency that's behind on a minor or patch version."
	got := Elide(sh, long, font, 200)
	if w := sh.Shape(got, font).Advance; w > 200 || !strings.HasSuffix(got, "…") || !strings.HasPrefix(long, strings.TrimSuffix(got, "…")) {
		t.Errorf("Elide to 200px: %q (%vpx)", got, w)
	}
	if got := Elide(sh, "short", font, 200); got != "short" {
		t.Errorf("text that fits changed: %q", got)
	}
	if got := Elide(sh, long, font, 1); got != "" {
		t.Errorf("no room for even the ellipsis: %q", got)
	}
}
