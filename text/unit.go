package text

// Unit is a syntax node worth talking about: a function, method, type, block, if, loop or closure.
// Explain, the outline, text objects, the review checklist and agent anchors all use it.
type Unit struct {
	Kind   string // "function", "method", "type", "block", "if", "switch", "case", "loop", "closure", "section"
	Name   string // empty when the unit has none
	Range  Range
	Parent int // index of the smallest unit around this one in the same list, or -1
}
