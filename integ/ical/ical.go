// Package ical reads the next meetings out of an iCalendar feed (the secret address Google Calendar, Outlook and the
// rest give out for a calendar) or this Mac's calendars through EventKit. It's the engine of the calendar plugin
// (plugin/), whose process (cmd/oriel-ical) serves it to the plugin's Lua.
//
// It is not a full RFC 5545 reader. It understands what a work calendar is made of: one-off events, events that
// repeat daily, weekly or monthly with the occasional cancelled or moved instance, and invitations you declined.
package ical

import (
	"bufio"
	"errors"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Event is one meeting.
type Event struct {
	Title    string
	Start    time.Time
	End      time.Time
	Location string
	Link     string // the call's address: a Meet or Zoom link
	People   int    // who's invited, you included; 0 when the calendar doesn't say
}

// Options shape what Upcoming lists.
type Options struct {
	// Me are your addresses: an invitation one of them declined is left out. With none, the calendar's own name
	// (X-WR-CALNAME) counts when it's an address, as a Google calendar's is.
	Me []string
	// Zone is where floating times (with no zone of their own) are; time.Local when nil.
	Zone *time.Location
}

type vevent struct {
	uid, summary, location, link, status string
	start, end                           time.Time
	allDay                               bool
	rrule                                string
	exdates                              map[int64]bool
	recurrenceID                         time.Time
	attendees                            map[string]string // address → PARTSTAT
}

// Upcoming lists the events that have not ended by now and start before until, soonest first. All-day events are
// left out: they are not meetings. So are cancelled ones and those you declined.
func Upcoming(r io.Reader, now, until time.Time, o Options) ([]Event, error) {
	zone := o.Zone
	if zone == nil {
		zone = time.Local
	}
	events, calname, err := parse(r, zone)
	if err != nil {
		return nil, err
	}
	me := map[string]bool{}
	for _, m := range o.Me {
		me[strings.ToLower(strings.TrimSpace(m))] = true
	}
	if len(me) == 0 && strings.Contains(calname, "@") {
		me[strings.ToLower(calname)] = true
	}
	// A moved or cancelled instance of a repeating event is its own VEVENT, with the same UID and the start it
	// replaces as RECURRENCE-ID.
	overridden := map[string]map[int64]bool{}
	for _, e := range events {
		if !e.recurrenceID.IsZero() {
			if overridden[e.uid] == nil {
				overridden[e.uid] = map[int64]bool{}
			}
			overridden[e.uid][e.recurrenceID.Unix()] = true
		}
	}
	var out []Event
	for _, e := range events {
		if e.allDay || strings.EqualFold(e.status, "CANCELLED") || e.declined(me) {
			continue
		}
		dur := max(e.end.Sub(e.start), 0)
		for _, st := range occurrences(e, now.Add(-dur), until) {
			if e.recurrenceID.IsZero() && (e.exdates[st.Unix()] || overridden[e.uid][st.Unix()]) {
				continue
			}
			if !st.Add(dur).After(now) {
				continue
			}
			out = append(out, Event{Title: e.summary, Start: st, End: st.Add(dur), Location: e.location, Link: e.link, People: len(e.attendees)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, nil
}

// declined says whether one of me declined e.
func (e vevent) declined(me map[string]bool) bool {
	for addr, st := range e.attendees {
		if me[addr] && strings.EqualFold(st, "DECLINED") {
			return true
		}
	}
	return false
}

// occurrences lists the starts of e inside [from, until).
func occurrences(e vevent, from, until time.Time) []time.Time {
	if e.rrule == "" || !e.recurrenceID.IsZero() {
		if !e.start.Before(from) && e.start.Before(until) {
			return []time.Time{e.start}
		}
		return nil
	}
	rule := map[string]string{}
	for part := range strings.SplitSeq(e.rrule, ";") {
		if k, v, ok := strings.Cut(part, "="); ok {
			rule[strings.ToUpper(k)] = v
		}
	}
	interval := 1
	if n, err := strconv.Atoi(rule["INTERVAL"]); err == nil && n > 0 {
		interval = n
	}
	count := -1
	if n, err := strconv.Atoi(rule["COUNT"]); err == nil {
		count = n
	}
	end := until
	if u := rule["UNTIL"]; u != "" {
		if t, _, err := parseTime(u, nil, e.start.Location()); err == nil && t.Before(end) {
			end = t.Add(time.Second)
		}
	}
	var days []time.Weekday
	for d := range strings.SplitSeq(rule["BYDAY"], ",") {
		if wd, ok := weekdays[strings.ToUpper(strings.TrimLeft(d, "+-0123456789"))]; ok {
			days = append(days, wd)
		}
	}

	var out []time.Time
	seen := 0
	emit := func(t time.Time) bool {
		if t.Before(e.start) {
			return true
		}
		if !t.Before(end) || (count >= 0 && seen >= count) {
			return false
		}
		seen++
		if !t.Before(from) {
			out = append(out, t)
		}
		return true
	}
	// Steps are taken in the event's own zone, so a weekly 09:00 stays at 09:00 across a clock change.
	s := e.start
	at := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, s.Hour(), s.Minute(), s.Second(), 0, s.Location())
	}
	switch strings.ToUpper(rule["FREQ"]) {
	case "DAILY":
		for i := 0; ; i += interval {
			if !emit(at(s.Year(), s.Month(), s.Day()+i)) {
				return out
			}
		}
	case "WEEKLY":
		if len(days) == 0 {
			days = []time.Weekday{s.Weekday()}
		}
		sort.Slice(days, func(i, j int) bool { return monFirst(days[i]) < monFirst(days[j]) })
		// Weeks are counted from the Monday of the first one.
		monday := s.Day() - monFirst(s.Weekday())
		for w := 0; ; w += interval {
			for _, wd := range days {
				if !emit(at(s.Year(), s.Month(), monday+7*w+monFirst(wd))) {
					return out
				}
			}
		}
	case "MONTHLY":
		for i := 0; ; i += interval {
			t := at(s.Year(), s.Month()+time.Month(i), s.Day())
			if t.Day() != s.Day() {
				continue // no 31st this month
			}
			if !emit(t) {
				return out
			}
		}
	}
	return out
}

func monFirst(d time.Weekday) int { return (int(d) + 6) % 7 }

var weekdays = map[string]time.Weekday{
	"MO": time.Monday, "TU": time.Tuesday, "WE": time.Wednesday, "TH": time.Thursday,
	"FR": time.Friday, "SA": time.Saturday, "SU": time.Sunday,
}

// parse reads the feed's events, and the calendar's name.
func parse(r io.Reader, zone *time.Location) ([]vevent, string, error) {
	lines, err := unfold(r)
	if err != nil {
		return nil, "", err
	}
	var out []vevent
	var cur *vevent
	calname := ""
	for _, line := range lines {
		name, params, value := split(line)
		switch {
		case name == "BEGIN" && strings.EqualFold(value, "VEVENT"):
			cur = &vevent{exdates: map[int64]bool{}, attendees: map[string]string{}}
		case name == "END" && strings.EqualFold(value, "VEVENT"):
			if cur != nil && !cur.start.IsZero() {
				if cur.end.IsZero() {
					cur.end = cur.start
				}
				out = append(out, *cur)
			}
			cur = nil
		case cur == nil:
			if name == "X-WR-CALNAME" {
				calname = strings.TrimSpace(unescape(value))
			}
		case name == "UID":
			cur.uid = value
		case name == "SUMMARY":
			cur.summary = unescape(value)
		case name == "LOCATION":
			cur.location = unescape(value)
		case name == "X-GOOGLE-CONFERENCE" || name == "URL" && cur.link == "":
			cur.link = strings.TrimSpace(value)
		case name == "STATUS":
			cur.status = value
		case name == "RRULE":
			cur.rrule = value
		case name == "ATTENDEE":
			addr := strings.ToLower(strings.TrimSpace(value))
			addr = strings.TrimPrefix(addr, "mailto:")
			cur.attendees[addr] = params["PARTSTAT"]
		case name == "DTSTART":
			cur.start, cur.allDay, _ = parseTime(value, params, zone)
		case name == "DTEND":
			cur.end, _, _ = parseTime(value, params, zone)
		case name == "RECURRENCE-ID":
			cur.recurrenceID, _, _ = parseTime(value, params, zone)
		case name == "EXDATE":
			for v := range strings.SplitSeq(value, ",") {
				if t, _, err := parseTime(v, params, zone); err == nil {
					cur.exdates[t.Unix()] = true
				}
			}
		}
	}
	return out, calname, nil
}

// unfold joins continuation lines: a line starting with a space or a tab carries on the one before it.
func unfold(r io.Reader) ([]string, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var out []string
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) && len(out) > 0 {
			out[len(out)-1] += line[1:]
			continue
		}
		out = append(out, line)
	}
	return out, sc.Err()
}

// split breaks "NAME;P=V;Q=W:value" into its parts. A parameter's value may be quoted, colons and all.
func split(line string) (string, map[string]string, string) {
	head, value := line, ""
	quoted := false
	for i, r := range line {
		if r == '"' {
			quoted = !quoted
		}
		if r == ':' && !quoted {
			head, value = line[:i], line[i+1:]
			break
		}
	}
	parts := strings.Split(head, ";")
	params := map[string]string{}
	for _, p := range parts[1:] {
		if k, v, ok := strings.Cut(p, "="); ok {
			params[strings.ToUpper(k)] = strings.Trim(v, `"`)
		}
	}
	return strings.ToUpper(parts[0]), params, value
}

// parseTime reads a DATE or DATE-TIME. A trailing Z is UTC; a TZID names the zone; neither is floating time, read
// in fallback.
func parseTime(v string, params map[string]string, fallback *time.Location) (time.Time, bool, error) {
	v = strings.TrimSpace(v)
	if params["VALUE"] == "DATE" || len(v) == 8 {
		t, err := time.ParseInLocation("20060102", v, fallback)
		return t, true, err
	}
	if strings.HasSuffix(v, "Z") {
		t, err := time.Parse("20060102T150405Z", v)
		return t, false, err
	}
	loc := fallback
	if tz := params["TZID"]; tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	t, err := time.ParseInLocation("20060102T150405", v, loc)
	return t, false, err
}

var unescaper = strings.NewReplacer(`\n`, " ", `\N`, " ", `\,`, ",", `\;`, ";", `\\`, `\`)

func unescape(s string) string { return unescaper.Replace(s) }

// CleanURL accepts the address a calendar app gives out: webcal:// is the same feed over https.
func CleanURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if rest, ok := strings.CutPrefix(raw, "webcal://"); ok {
		raw = "https://" + rest
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", errors.New("the calendar must be an https:// (or webcal://) iCal address")
	}
	return raw, nil
}
