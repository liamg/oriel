package ui

import "github.com/liamg/oriel/render"

// SubjectAt is what was right-clicked at p stands for, when target took the click: the topmost node at p inside
// target with a subject (a tree's row in its list's clickable item), else target's or the nearest around it.
func (u *UI) SubjectAt(target ID, p render.Point) string {
	for _, id := range u.Hit(p) {
		if id == target {
			break
		}
		if s := u.flags[id].subject; s != "" && u.within(id, target) {
			return s
		}
	}
	return u.Subject(target)
}

// LayerOf is the highest layer of the floats id is in, as last frame drew it (Style.Z): ui.LayerDialog for a node
// in a dialog, 0 for one in the flow.
func (u *UI) LayerOf(id ID) int {
	z := 0
	for ; id != 0; id = u.parents[id] {
		z = max(z, u.flags[id].z)
	}
	return z
}

// OfferAt is what the node right-clicked at p offers a menu's items to act on (Style.Offer), when target took the
// click: the topmost node at p inside target with an offer, else target's or the nearest around it; nil for none.
func (u *UI) OfferAt(target ID, p render.Point) any {
	for _, id := range u.Hit(p) {
		if id == target {
			break
		}
		if o := u.flags[id].offer; o != nil && u.within(id, target) {
			return o
		}
	}
	for id := target; id != 0; id = u.parents[id] {
		if o := u.flags[id].offer; o != nil {
			return o
		}
	}
	return nil
}
