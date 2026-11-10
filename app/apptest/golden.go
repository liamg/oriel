package apptest

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/render/snapshot"
	"github.com/liamg/oriel/theme"
)

// Golden draws two frames, once what they read is in, and checks the second against testdata/name.list and .png;
// ORIEL_UPDATE=1 writes them.
func (s *Scene) Golden(name string) {
	s.T.Helper()
	s.Quiet()
	s.SettleDrawn()
	if o := s.UI().Overflowing(); len(o) > 0 {
		s.T.Errorf("%s: text drawn past its box:\n%s", name, strings.Join(o, "\n"))
	}
	failed := s.T.Failed()
	snapshot.Golden(s.T, "testdata/"+name+".list", []byte(snapshot.List(s.List())))
	img := raster.Render(s.List(), s.Size(), s.Scale(), s.UI().Theme().C(theme.BgBase))
	snapshot.GoldenPNG(s.T, "testdata/"+name+".png", img, 0.02, 0.001)
	if !failed && s.T.Failed() { // what the status line's counts and the inbox were drawn from
		for _, n := range s.Store().State().Notifications {
			s.T.Logf("%s: a %s notice from %s, %q: %s", name, n.Severity, n.Source, n.Title, n.Text)
		}
	}
}

// Shot draws two frames and writes the second to $ORIEL_SHOTS/name.png: a screenshot for the docs.
func (s *Scene) Shot(name string) {
	s.T.Helper()
	s.Quiet()
	s.SettleReads()
	s.Frame()
	s.Frame()
	img := raster.Render(s.List(), s.Size(), 1, s.UI().Theme().C(theme.BgBase))
	f, err := os.Create(filepath.Join(os.Getenv("ORIEL_SHOTS"), name+".png"))
	if err != nil {
		s.T.Fatal(err)
	}
	defer f.Close()
	png.Encode(f, img)
}
