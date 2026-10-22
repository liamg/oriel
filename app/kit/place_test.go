package kit_test

import (
	"testing"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/text"
)

// A place names characters, not bytes, and whole lines by their numbers alone.
func TestPlaces(t *testing.T) {
	snap := text.New([]byte("héllo world\nsecond\nthird\n"), text.Author{}).Snapshot()
	for _, c := range []struct {
		got  kit.Place
		want string
	}{
		{kit.PlaceAt(snap, text.Pos{Line: 0, Col: 7}), "1:7"},
		{kit.PlaceOf(snap, text.Range{Start: 7, End: 12}), "1:7-1:11"},
		{kit.PlaceOf(snap, text.Range{Start: 13, End: 16}), "2:1-2:3"},
		{kit.PlaceOf(snap, text.Range{Start: 0, End: snap.LineEnd(1)}), "1-2"},
		{kit.PlaceOf(snap, text.Range{Start: 3, End: 16}), "1:3-2:3"},
	} {
		if s := c.got.String(); s != c.want {
			t.Errorf("%+v is %q, want %q", c.got, s, c.want)
		}
	}
}
