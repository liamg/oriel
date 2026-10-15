package ical

import (
	"os"
	"strings"
	"testing"
	"time"
)

func cal(events ...string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\n" + strings.Join(events, "") + "END:VCALENDAR\r\n"
}

func ev(lines ...string) string {
	return "BEGIN:VEVENT\r\n" + strings.Join(lines, "\r\n") + "\r\nEND:VEVENT\r\n"
}

func upcoming(t *testing.T, feed string, now time.Time, o Options) []Event {
	t.Helper()
	got, err := Upcoming(strings.NewReader(feed), now, now.Add(10*24*time.Hour), o)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func zone(t *testing.T, name string) *time.Location {
	t.Helper()
	l, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

var now = time.Date(2026, 9, 23, 15, 0, 0, 0, time.UTC)

func TestOneOffEvents(t *testing.T) {
	got := upcoming(t, cal(
		ev("UID:past", "SUMMARY:Done", "DTSTART:20260923T100000Z", "DTEND:20260923T103000Z"),
		ev("UID:later", "SUMMARY:Later", "DTSTART:20260924T090000Z", "DTEND:20260924T093000Z"),
		ev("UID:soon", "SUMMARY:Stand-up\\, daily", "DTSTART:20260923T160000Z", "DTEND:20260923T161500Z"),
		ev("UID:allday", "SUMMARY:Holiday", "DTSTART;VALUE=DATE:20260923", "DTEND;VALUE=DATE:20260924"),
		ev("UID:gone", "SUMMARY:Cancelled", "STATUS:CANCELLED", "DTSTART:20260923T170000Z"),
	), now, Options{})
	if len(got) != 2 || got[0].Title != "Stand-up, daily" || got[1].Title != "Later" {
		t.Fatalf("got %+v", got)
	}
}

// A meeting in progress is still the one that matters.
func TestInProgressIsListed(t *testing.T) {
	got := upcoming(t, cal(ev("UID:a", "SUMMARY:Now", "DTSTART:20260923T144500Z", "DTEND:20260923T153000Z")), now, Options{})
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
}

func TestWeeklyWithZoneAndExceptions(t *testing.T) {
	london := zone(t, "Europe/London")
	got := upcoming(t, cal(
		ev("UID:w", "SUMMARY:Sync", "DTSTART;TZID=Europe/London:20260803T093000",
			"DTEND;TZID=Europe/London:20260803T100000", "RRULE:FREQ=WEEKLY;BYDAY=MO,TH",
			"EXDATE;TZID=Europe/London:20260924T093000"),
		// The Monday instance moved to the afternoon.
		ev("UID:w", "SUMMARY:Sync (moved)", "RECURRENCE-ID;TZID=Europe/London:20260928T093000",
			"DTSTART;TZID=Europe/London:20260928T140000", "DTEND;TZID=Europe/London:20260928T143000"),
	), now, Options{})
	want := []time.Time{
		time.Date(2026, 9, 28, 14, 0, 0, 0, london),
		time.Date(2026, 10, 1, 9, 30, 0, 0, london),
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i, w := range want {
		if !got[i].Start.Equal(w) {
			t.Errorf("event %d starts %v, want %v", i, got[i].Start, w)
		}
	}
	if got[0].Title != "Sync (moved)" {
		t.Errorf("moved instance title = %q", got[0].Title)
	}
}

func TestRepeatsStop(t *testing.T) {
	got := upcoming(t, cal(
		ev("UID:u", "SUMMARY:Until", "DTSTART:20260901T090000Z", "RRULE:FREQ=DAILY;UNTIL=20260922T235959Z"),
		ev("UID:c", "SUMMARY:Count", "DTSTART:20260920T090000Z", "RRULE:FREQ=DAILY;COUNT=5"),
	), now, Options{})
	if len(got) != 1 || !got[0].Start.Equal(time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("got %+v", got)
	}
}

// RFC 5545 §3.1: "a long line can be split between any two characters by inserting a CRLF immediately followed by a
// single linear white-space character".
func TestFoldedLines(t *testing.T) {
	feed := cal("BEGIN:VEVENT\r\nUID:f\r\nSUMMARY:A very long\r\n  title\r\nDTSTART:20260923T160000Z\r\nEND:VEVENT\r\n")
	got := upcoming(t, feed, now, Options{})
	if len(got) != 1 || got[0].Title != "A very long title" {
		t.Fatalf("got %+v", got)
	}
}

// A Google Calendar feed as recorded, at 14:12 in London on 15 October (mockup 10's clock): the meeting I declined is
// left out (the feed is mine: its X-WR-CALNAME), so the next is Pricing sync at 14:30 with its four people and Meet
// link; the all-day offsite and the cancelled 1:1 are left out; New York's noon is 17:00 here; the stand-up skips
// its EXDATE, takes the moved Monday's title and length, and keeps 09:30 in London across the clock change.
func TestGoogleFeed(t *testing.T) {
	london := zone(t, "Europe/London")
	at := time.Date(2026, 10, 15, 14, 12, 0, 0, london)
	got, err := Upcoming(strings.NewReader(fixture(t, "google.ics")), at, at.Add(14*24*time.Hour), Options{})
	if err != nil {
		t.Fatal(err)
	}
	type want struct {
		title    string
		start    time.Time
		long     time.Duration
		place    string
		link     string
		people   int
		declined bool
	}
	wants := []want{
		{title: "Pricing sync", start: time.Date(2026, 10, 15, 14, 30, 0, 0, london), long: 30 * time.Minute, link: "https://meet.google.com/abc-defg-hij", people: 4},
		{title: "Review with NY", start: time.Date(2026, 10, 15, 17, 0, 0, 0, london), long: 30 * time.Minute, place: "Room 4, 2nd floor"},
		{title: "Stand-up (long one)", start: time.Date(2026, 10, 19, 9, 30, 0, 0, london), long: 30 * time.Minute},
		{title: "Stand-up", start: time.Date(2026, 10, 20, 9, 30, 0, 0, london), long: 15 * time.Minute, people: 2},
	}
	if len(got) < len(wants) {
		t.Fatalf("got %+v", got)
	}
	for i, w := range wants {
		g := got[i]
		if g.Title != w.title || !g.Start.Equal(w.start) || g.End.Sub(g.Start) != w.long || g.Location != w.place || g.Link != w.link ||
			g.People != w.people {
			t.Errorf("event %d = %+v, want %+v", i, g, w)
		}
	}
	after := time.Date(2026, 10, 26, 9, 30, 0, 0, london) // GMT by then
	found := false
	for _, g := range got {
		if g.Title == "Vendor demo" || g.Title == "Team offsite" || g.Title == "Cancelled 1:1" {
			t.Errorf("listed %q", g.Title)
		}
		if g.Start.Equal(time.Date(2026, 10, 16, 9, 30, 0, 0, london)) {
			t.Errorf("the EXDATE's stand-up is listed")
		}
		found = found || g.Start.Equal(after) && g.Start.UTC().Hour() == 9
	}
	if !found {
		t.Errorf("no stand-up at 09:30 on 26 October, after the clock change: %+v", got)
	}

	// Somebody else's feed: the declined meeting is theirs, not mine.
	other, _ := Upcoming(strings.NewReader(fixture(t, "google.ics")), at, at.Add(time.Hour), Options{Me: []string{"sam@acme.dev"}})
	if len(other) == 0 || other[0].Title != "Vendor demo" {
		t.Errorf("for sam, the vendor demo should be in progress: %+v", other)
	}
}

// Every other Thursday across the clock change, the 31st only in months that have one, a COUNT with a declined
// instance in it, and floating time read in the zone given.
func TestRepeats(t *testing.T) {
	london := zone(t, "Europe/London")
	at := time.Date(2026, 10, 15, 14, 12, 0, 0, london)
	got, err := Upcoming(strings.NewReader(fixture(t, "repeats.ics")), at, time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		Options{Me: []string{"liam@acme.dev"}, Zone: london})
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, e := range got {
		lines = append(lines, e.Start.In(london).Format("Jan 2 15:04 MST ")+e.Title)
	}
	want := []string{
		"Oct 16 11:00 BST Floating lunch",
		"Oct 17 09:00 BST Daily check",
		"Oct 29 09:00 GMT Planning",
		"Oct 31 16:00 GMT Month-end close",
		"Nov 12 09:00 GMT Planning",
		"Nov 26 09:00 GMT Planning",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

func TestCleanURL(t *testing.T) {
	for in, want := range map[string]string{
		"":                                  "",
		" webcal://cal.example/x.ics ":      "https://cal.example/x.ics",
		"https://calendar.google.com/a.ics": "https://calendar.google.com/a.ics",
	} {
		if got, err := CleanURL(in); err != nil || got != want {
			t.Errorf("CleanURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"file:///etc/passwd", "not a url", "ftp://x/y"} {
		if _, err := CleanURL(bad); err == nil {
			t.Errorf("CleanURL(%q) accepted", bad)
		}
	}
}
