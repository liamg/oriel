package work_test

import (
	"testing"

	"github.com/liamg/oriel/app/kit/work"
)

// One ask at a time; a refresh while one is out marks it to go again, once; a plain ask while one is out doesn't.
func TestRefetch(t *testing.T) {
	var r work.Refetch
	done, started := r.Begin(false)
	if !started || r.Busy() == nil {
		t.Fatal("the first ask didn't start")
	}
	if d, s := r.Begin(false); s || d != done {
		t.Error("a second ask started while one was out")
	}
	if r.Again() {
		t.Error("a plain ask while one was out asked again")
	}
	r.Begin(true)
	r.Begin(true)
	if !r.Again() || r.Again() {
		t.Error("a refresh while an ask was out didn't make it go again exactly once")
	}
	r.End()
	if r.Busy() != nil {
		t.Error("still busy after End")
	}
	if _, s := r.Begin(false); !s {
		t.Error("no ask started after the last ended")
	}
}
