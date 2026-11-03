package broken

import "testing"

func TestBroken(t *testing.T) {
	var x int = "not an int"
	_ = x
}
