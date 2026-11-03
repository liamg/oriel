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
