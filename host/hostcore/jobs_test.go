package hostcore

import (
	"slices"
	"testing"
)

// Jobs say which are still running, by name and how many, and stop counting each once it ends.
func TestJobsSayWhatsStillRunning(t *testing.T) {
	var j Jobs
	hold, ended := make(chan struct{}), make(chan struct{})
	j.Go("a sign-in", func() { <-hold })
	j.Go("a btw", func() { <-hold })
	j.Go("a btw", func() { <-hold })
	j.Go("naming a session", func() { close(ended) })
	<-ended
	want := []string{"a btw ×2", "a sign-in"}
	for got := j.Running(); !slices.Equal(got, want); got = j.Running() {
		if !slices.Contains(got, "naming a session") {
			t.Fatalf("Running = %q, want %q", got, want)
		}
	}
	close(hold)
	j.Wait()
	if got := j.Running(); len(got) != 0 {
		t.Errorf("Running after Wait = %q", got)
	}
}
