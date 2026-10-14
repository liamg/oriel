package panics

import "testing"

func TestBefore(t *testing.T) {}

func TestPanics(t *testing.T) {
	var m map[string]int
	m["boom"] = 1
}

func TestAfter(t *testing.T) {}
