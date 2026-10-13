package app

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/liamg/oriel/render"
)

// windowPlace is where the window was when it last closed, kept in the state directory so it opens there again.
type windowPlace struct{ path string }

type windowFrame struct {
	X, Y, W, H float32
}

func (p windowPlace) load() render.Rect {
	var f windowFrame
	if b, err := os.ReadFile(p.path); err != nil || json.Unmarshal(b, &f) != nil {
		return render.Rect{}
	}
	return render.XYWH(f.X, f.Y, f.W, f.H)
}

// save keeps w's frame, if it knows it.
func (p windowPlace) save(w render.Window) error {
	pl, ok := w.(render.Placed)
	if !ok {
		return nil
	}
	r, ok := pl.Placement()
	if !ok || r.Dx() <= 0 || r.Dy() <= 0 {
		return nil
	}
	b, err := json.Marshal(windowFrame{X: r.Min.X, Y: r.Min.Y, W: r.Dx(), H: r.Dy()})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p.path, b, 0o644)
}
