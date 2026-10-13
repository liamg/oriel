package c

import "testing"

func TestSixteen(t *testing.T) {
	if Sixteen(1) != 16 {
		t.Fatal("Sixteen(1) != 16")
	}
}
