// Package chaos slows work started off its caller by a random while, so a test that passes only because that work
// was quick fails (make test-chaos).
package chaos

import (
	"math/rand/v2"
	"os"
	"strconv"
	"time"
)

// Env names the variable that sets the longest while, in milliseconds.
const Env = "ORIEL_TEST_CHAOS"

// Sleep sleeps up to ORIEL_TEST_CHAOS milliseconds when it's set, and returns at once when it isn't.
func Sleep() {
	v := os.Getenv(Env)
	if v == "" {
		return
	}
	ms, err := strconv.Atoi(v)
	if err != nil || ms <= 0 {
		ms = 50
	}
	time.Sleep(time.Duration(rand.Int64N(int64(ms)+1)) * time.Millisecond)
}
