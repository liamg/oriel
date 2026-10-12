package main

import (
	"bytes"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		args []string
		code int
		out  string
	}{
		{[]string{"version"}, 0, "oriel dev\n"},
		{[]string{"worker"}, 1, ""},
		{[]string{"dev"}, 1, ""},
		{[]string{"dev", "nope"}, 1, ""},
		{[]string{"nope"}, 2, ""},
	}
	for _, tt := range tests {
		var out, errOut bytes.Buffer
		if code := run(tt.args, &out, &errOut); code != tt.code || out.String() != tt.out {
			t.Errorf("run(%q) = %d, %q; want %d, %q", tt.args, code, out.String(), tt.code, tt.out)
		}
	}
}
