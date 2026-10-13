package d

import "testing"

func TestHello(t *testing.T) {
	if Hello() != "hello" {
		t.Fatal("Hello() != hello")
	}
}
