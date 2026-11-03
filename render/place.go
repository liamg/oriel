package render

// PlaceWindow is where a window opens, given the work areas of the screens (the first the main one), where it was
// last (zero for never), and the size it asks for. A saved place is kept on the screen it's mostly on, shrunk to fit
// and moved inside it; one on no screen, or none, is centred on the main screen, the asked size capped at 90% of it.
func PlaceWindow(screens []Rect, saved Rect, size Point) Rect {
	has := saved.Dx() > 0 && saved.Dy() > 0
	if len(screens) == 0 {
		if has {
			return saved
		}
		return Rect{Max: size}
	}
	best, area := 0, float32(0)
	for i, s := range screens {
		if a := overlap(s, saved); a > area {
			best, area = i, a
		}
	}
	s := screens[best]
	if !has || area == 0 {
		w, h := min(size.X, s.Dx()*.9), min(size.Y, s.Dy()*.9)
		if has {
			w, h = min(saved.Dx(), s.Dx()), min(saved.Dy(), s.Dy())
		}
		return XYWH(s.Min.X+(s.Dx()-w)/2, s.Min.Y+(s.Dy()-h)/2, w, h)
	}
	w, h := min(saved.Dx(), s.Dx()), min(saved.Dy(), s.Dy())
	x := min(max(saved.Min.X, s.Min.X), s.Max.X-w)
	y := min(max(saved.Min.Y, s.Min.Y), s.Max.Y-h)
	return XYWH(x, y, w, h)
}

func overlap(a, b Rect) float32 {
	w := min(a.Max.X, b.Max.X) - max(a.Min.X, b.Min.X)
	h := min(a.Max.Y, b.Max.Y) - max(a.Min.Y, b.Min.Y)
	if w <= 0 || h <= 0 {
		return 0
	}
	return w * h
}
