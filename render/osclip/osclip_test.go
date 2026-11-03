package osclip

import (
	"slices"
	"testing"
)

// RFC 2483, 5: "The format of text/uri-list resources is: 1) Any lines beginning with the '#' character are comment
// lines and are ignored during processing."
func TestURIList(t *testing.T) {
	got := uriList("# copied\r\nfile:///Users/ada/shot%201.png\r\nhttps://example.com/x\r\n\r\nfile:///tmp/a.go\n")
	if want := []string{"/Users/ada/shot 1.png", "/tmp/a.go"}; !slices.Equal(got, want) {
		t.Errorf("uriList = %q, want %q", got, want)
	}
}
