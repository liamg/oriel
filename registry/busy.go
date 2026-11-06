package registry

import "slices"

// Busy is work a contributor has under way off the loop, whose end changes what's shown: a read, a diff, an answer
// on its way. What waits for the app to settle (a test before it clicks or looks) waits until none is.
type Busy struct {
	Name   string
	Busy   func() bool
	Source string
}

func (Busy) ContributionKind() string   { return "busy" }
func (b Busy) ContributionName() string { return b.Name }

// BusyNow are the names of the work under way now, sorted.
func BusyNow(r Contributions) []string {
	var out []string
	for _, c := range r.Contributions("busy") {
		if b := c.(Busy); b.Busy != nil && b.Busy() {
			out = append(out, b.Name)
		}
	}
	slices.Sort(out)
	return out
}
