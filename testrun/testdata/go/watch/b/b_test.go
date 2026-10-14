package b

import "testing"

func TestQuadruple(t *testing.T) {
	if Quadruple(1) != 4 {
		t.Fatal("Quadruple(1) != 4")
	}
}
