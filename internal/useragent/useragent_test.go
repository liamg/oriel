package useragent

import (
	"net/http"
	"testing"
)

func TestSetFillsAnEmptyUserAgent(t *testing.T) {
	r, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	Set(r)
	if got := r.Header.Get("User-Agent"); got != Value {
		t.Errorf("User-Agent = %q, want %q", got, Value)
	}
}

func TestSetKeepsTheCallersUserAgent(t *testing.T) {
	r, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	r.Header.Set("User-Agent", "mine/1.0")
	Set(r)
	if got := r.Header.Get("User-Agent"); got != "mine/1.0" {
		t.Errorf("User-Agent = %q, want mine/1.0", got)
	}
}
