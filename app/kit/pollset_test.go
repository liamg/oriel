package kit

import (
	"testing"
	"time"
)

// "A nil one notes nothing": every call on a nil PollSet is a no-op, as a window without one hands out.
func TestNilPollSetNotesNothing(t *testing.T) {
	var p *PollSet
	p.Start(time.Second)()
	p.Reset()
	if f := p.FastestSince(); f != 0 {
		t.Errorf("FastestSince = %v", f)
	}
	if f := p.FastestNow(); f != 0 {
		t.Errorf("FastestNow = %v", f)
	}
}
