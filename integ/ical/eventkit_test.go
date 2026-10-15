package ical

import (
	"strings"
	"testing"
	"time"
)

// A recorded answer from the script: one over, one declined, two to come out of order.
func TestParseEventKit(t *testing.T) {
	now := time.UnixMilli(1_790_000_000_000)
	out := []byte(`{"status":"ok","events":[
		{"title":"Later","start":1790007200000,"end":1790009000000,"people":0},
		{"title":"Over","start":1789990000000,"end":1789993600000},
		{"title":"Skipping","start":1790001000000,"end":1790002000000,"declined":true},
		{"title":"Soon","location":"Room 4","link":"https://meet.google.com/x","people":4,"start":1790001800000,"end":1790003600000}]}`)
	got, err := ParseEventKit(out, now)
	if err != nil || len(got) != 2 || got[0].Title != "Soon" || got[1].Title != "Later" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if g := got[0]; g.Location != "Room 4" || g.Link != "https://meet.google.com/x" || g.People != 4 || !g.Start.Equal(time.UnixMilli(1790001800000)) {
		t.Errorf("Soon = %+v", g)
	}
	if _, err := ParseEventKit([]byte(`{"status":"writeonly","app":"Oriel"}`), now); err == nil || !strings.Contains(err.Error(), "set Oriel to Full Access") {
		t.Errorf("write-only access: err = %v", err)
	}
	if _, err := ParseEventKit([]byte(`{"status":"denied"}`), now); err == nil || !strings.Contains(err.Error(), "allow Oriel Full Access") {
		t.Errorf("denied: err = %v", err)
	}
	if _, err := ParseEventKit([]byte(`execution error`), now); err == nil {
		t.Error("not JSON: no error")
	}
}

func TestEventKitArgs(t *testing.T) {
	args := EventKitArgs(14*24*time.Hour, false)
	if len(args) != 6 || args[0] != "osascript" || args[4] != EventKitScript || args[5] != "1209600" {
		t.Errorf("args = %q", args)
	}
	if !strings.Contains(EventKitScript, "EKEventStore") {
		t.Error("the embedded script isn't the EventKit reader")
	}
}
