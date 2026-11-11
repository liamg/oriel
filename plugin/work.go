package plugin

import (
	"sync"

	"github.com/liamg/oriel/internal/chaos"
)

// Tracker is a host that counts the work its plugins run off the loop, so what waits for the app to settle (a
// test's Quiet) waits for that work and what it hands back to the loop.
type Tracker interface {
	// Begin counts what as under way until end is called.
	Begin(what string) (end func())
}

// Begin counts what as under way on h, when h tracks work (Tracker), until end; end may be called more than once.
func Begin(h any, what string) (end func()) {
	t, ok := h.(Tracker)
	if !ok {
		return func() {}
	}
	var once sync.Once
	done := t.Begin(what)
	return func() { once.Do(done) }
}

// Go runs fn on a goroutine of its own, counted as under way on h until it returns. What fn hands the loop (Do)
// before it returns is queued by then, so a wait for the app to settle sees one or the other. It's for work that
// ends: what runs as long as the plugin does is a kit.Work Loop's.
func Go(h any, what string, fn func()) {
	end := Begin(h, what)
	go func() {
		defer end()
		Chaos()
		fn()
	}()
}

// ChaosEnv names the variable that slows the work started off the loop (Go, Load) by a random while up to its
// value in milliseconds: a test that passes only because that work was quick fails under it (make test-chaos).
const ChaosEnv = chaos.Env

// Chaos sleeps up to ORIEL_TEST_CHAOS milliseconds when it's set, and returns at once when it isn't.
func Chaos() { chaos.Sleep() }
