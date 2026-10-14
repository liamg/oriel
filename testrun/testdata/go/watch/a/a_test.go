package a

import "testing"

func TestDouble(t *testing.T) {
	if Double(2) != 4 {
		t.Fatal("Double(2) != 4")
	}
}

func TestSign(t *testing.T) {
	if Sign(-3) != -1 {
		t.Fatal("Sign(-3) != -1")
	}
}
