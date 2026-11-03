package text

import (
	"bytes"
	"slices"
	"strings"
	"unicode/utf8"
)

// Unit is a syntax node worth talking about: a function, method, type, block, if, loop or closure.
// Explain, the outline, text objects, the review checklist and agent anchors all use it.
type Unit struct {
	Kind   string // "function", "method", "type", "block", "if", "switch", "case", "loop", "closure", "section"
	Name   string // empty when the unit has none
	Range  Range
	Parent int // index of the smallest unit around this one in the same list, or -1
}

// Nest sets each unit's Parent to the smallest unit around it, in a list sorted by start (and by end, longest
// first, among units that start together).
func Nest(us []Unit) {
	for i := range us {
		us[i].Parent = -1
		for j := i - 1; j >= 0; j-- {
			if us[j].Range.Start <= us[i].Range.Start && us[i].Range.End <= us[j].Range.End {
				us[i].Parent = j
				break
			}
		}
	}
}

// FilterUnits is the units keep keeps, nested again among themselves.
func FilterUnits(us []Unit, keep func(Unit) bool) []Unit {
	var out []Unit
	for _, u := range us {
		if keep(u) {
			out = append(out, u)
		}
	}
	Nest(out)
	return out
}

// Innermost is the index of the smallest unit that holds all of r, or -1.
func Innermost(us []Unit, r Range) int {
	best := -1
	for i, u := range us {
		if u.Range.Start <= r.Start && r.End <= u.Range.End && (best < 0 || u.Range.Len() <= us[best].Range.Len()) {
			best = i
		}
	}
	return best
}

// UnitPath is unit i and the units around it, outermost first: a breadcrumb.
func UnitPath(us []Unit, i int) []int {
	var out []int
	for ; i >= 0; i = us[i].Parent {
		out = append(out, i)
	}
	slices.Reverse(out)
	return out
}

// Owners are the smallest units owning each of rs, by index, in order and once each: one range can belong to
// a unit another range's owner holds, and two ranges can share an owner.
func Owners(us []Unit, rs []Range) []int {
	var out []int
	for _, r := range rs {
		if i := Innermost(us, r); i >= 0 && !slices.Contains(out, i) {
			out = append(out, i)
		}
	}
	slices.Sort(out)
	return out
}

// unitWords are the words a named unit's kind reads as in a label.
var unitWords = map[string]string{"function": "func", "method": "func", "closure": "func"}

// Label names a unit for a breadcrumb: its kind and name ("func roundHalfEven", "type Money"), or for one with no
// name the start of its first line ("switch diff.Cmp(half)", "case 0", "if !ok").
func (u Unit) Label(src []byte) string {
	if u.Name != "" {
		w, ok := unitWords[u.Kind]
		if !ok {
			w = u.Kind
		}
		return w + " " + u.Name
	}
	return u.Head(src)
}

// Head is the start of the unit's first line, without the brace or colon that opens its body.
func (u Unit) Head(src []byte) string {
	s := src[min(u.Range.Start, len(src)):min(u.Range.End, len(src))]
	if i := bytes.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	h := strings.TrimSpace(string(s))
	h = strings.TrimSpace(strings.TrimRight(h, "{:"))
	if utf8.RuneCountInString(h) > 40 {
		h = string([]rune(h)[:39]) + "…"
	}
	return h
}

// Explainable says whether explain stops at a unit: every kind but a plain block, which is the body of the unit
// around it.
func Explainable(u Unit) bool { return u.Kind != "block" }
