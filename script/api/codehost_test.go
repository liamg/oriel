package api

import (
	"testing"
	"time"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
)

// A code host gives a review request's times as RFC 3339 or seconds since 1970, in a request or in a detail's.
func TestCodeHostTimesEitherWay(t *testing.T) {
	at := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	var r registry.ReviewRequest
	if err := decodeTo(withTimes(script.Table{"url": "u", "number": 7.0, "asked": float64(at.Unix()), "opened": "2026-10-19T09:00:00Z"}), &r); err != nil {
		t.Fatal(err)
	}
	if !r.Asked.Equal(at) || !r.Opened.Equal(at) || r.Number != 7 {
		t.Errorf("request %+v", r)
	}
	var d registry.ReviewDetail
	if err := decodeTo(withTimes(script.Table{"state": "open", "request": script.Table{"updated": float64(at.Unix())}}), &d); err != nil {
		t.Fatal(err)
	}
	if !d.Request.Updated.Equal(at) || d.State != "open" {
		t.Errorf("detail %+v", d)
	}
}
