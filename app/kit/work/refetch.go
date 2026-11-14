package work

// Refetch is a list asked for off the loop, one ask at a time. A refresh asked for while an ask is under way makes that
// ask go once more as it answers: it may have started before what the refresh is for. Its methods are called with
// the owner's lock held.
type Refetch struct {
	busy  chan struct{}
	again bool
}

// Busy is the ask under way, closed when it answers; nil when there's none.
func (r *Refetch) Busy() <-chan struct{} {
	if r.busy == nil {
		return nil
	}
	return r.busy
}

// Begin starts an ask, answering the channel to close when it's done and true; with one under way it answers that one
// and false, and a refresh marks it to go again.
func (r *Refetch) Begin(refresh bool) (chan struct{}, bool) {
	if r.busy != nil {
		r.again = r.again || refresh
		return r.busy, false
	}
	r.busy = make(chan struct{})
	return r.busy, true
}

// Again says whether the ask that just answered is to go once more, for a refresh asked for while it was out.
func (r *Refetch) Again() bool {
	a := r.again
	r.again = false
	return a
}

// End ends the ask under way; its owner closes the channel Begin gave, once it's taken the answer.
func (r *Refetch) End() { r.busy = nil }
