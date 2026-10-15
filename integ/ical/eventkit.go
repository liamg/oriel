package ical

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// EventKitScript reads this Mac's calendars: run it as osascript -l JavaScript -e <script> <seconds ahead>, and
// read what it prints with ParseEventKit. The plugin runs it from its own copy.
//
//go:embed plugin/eventkit.js
var EventKitScript string

// EventKitArgs is the osascript command line that reads the calendars ahead from now; status only asks whether
// there's access.
func EventKitArgs(ahead time.Duration, status bool) []string {
	args := []string{"osascript", "-l", "JavaScript", "-e", EventKitScript, fmt.Sprint(int(ahead / time.Second))}
	if status {
		args = append(args, "status")
	}
	return args
}

type eventKitAnswer struct {
	Status string `json:"status"`
	App    string `json:"app"`
	Events []struct {
		Title    string  `json:"title"`
		Location string  `json:"location"`
		Link     string  `json:"link"`
		People   int     `json:"people"`
		Declined bool    `json:"declined"`
		Start    float64 `json:"start"`
		End      float64 `json:"end"`
	} `json:"events"`
}

// EventKitStatus is the access the script reported: ok, undecided, restricted, denied or writeonly.
func EventKitStatus(out []byte) string {
	var a eventKitAnswer
	if json.Unmarshal(out, &a) != nil {
		return ""
	}
	return a.Status
}

// ParseEventKit reads the script's answer: the meetings not over by now, soonest first, leaving out those you
// declined. Without access, the error says how to give it.
func ParseEventKit(out []byte, now time.Time) ([]Event, error) {
	var a eventKitAnswer
	if err := json.Unmarshal(out, &a); err != nil {
		return nil, errors.New("could not read this Mac's calendar")
	}
	if a.Status != "ok" {
		return nil, errors.New(eventKitRefusal(a.Status, a.App))
	}
	var events []Event
	for _, e := range a.Events {
		ev := Event{Title: e.Title, Location: e.Location, Link: e.Link, People: e.People,
			Start: time.UnixMilli(int64(e.Start)), End: time.UnixMilli(int64(e.End))}
		if ev.End.After(now) && !e.Declined {
			events = append(events, ev)
		}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].Start.Before(events[j].Start) })
	return events, nil
}

// eventKitRefusal explains a missing grant. app is where Oriel runs, which is the app macOS lists under Calendars.
func eventKitRefusal(status, app string) string {
	if app == "" {
		app = "Oriel"
	}
	switch status {
	case "undecided":
		return "waiting for you to allow Calendar access on this Mac"
	case "restricted":
		return "Calendar access is blocked on this Mac"
	case "writeonly":
		return "set " + app + " to Full Access (not Add Only) in System Settings → Privacy & Security → Calendars"
	}
	return "allow " + app + " Full Access in System Settings → Privacy & Security → Calendars"
}
