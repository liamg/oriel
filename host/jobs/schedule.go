package jobs

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// How often a schedule runs a job.
const (
	EveryWeekday = "weekdays" // Monday to Friday
	EveryWeek    = "weekly"   // on Days[0]
	EveryDays    = "days"     // on each of Days: custom
)

// Schedule is when a job runs by itself, in the host's local time. A run is skipped while the last is still going.
type Schedule struct {
	Every string         `json:"every,omitempty"` // "" for manual
	Days  []time.Weekday `json:"days,omitempty"`
	At    string         `json:"at,omitempty"` // "07:00"
}

func (s Schedule) clock() (int, int, error) {
	t, err := time.Parse("15:04", s.At)
	if err != nil {
		return 0, 0, fmt.Errorf("a schedule's time is HH:MM, not %q", s.At)
	}
	return t.Hour(), t.Minute(), nil
}

// Check says what's wrong with the schedule, or nil.
func (s Schedule) Check() error {
	switch s.Every {
	case "":
		return nil
	case EveryWeekday:
	case EveryWeek:
		if len(s.Days) != 1 {
			return errors.New("a weekly schedule names one day")
		}
	case EveryDays:
		if len(s.Days) == 0 {
			return errors.New("a custom schedule names its days")
		}
	default:
		return fmt.Errorf("a schedule is weekdays, weekly or days, not %q", s.Every)
	}
	_, _, err := s.clock()
	return err
}

func (s Schedule) on(d time.Weekday) bool {
	if s.Every == EveryWeekday {
		return d >= time.Monday && d <= time.Friday
	}
	return slices.Contains(s.Days, d)
}

// Next is when the schedule next fires after t, in t's zone; zero for a manual one.
func (s Schedule) Next(t time.Time) time.Time {
	if s.Every == "" || s.Check() != nil {
		return time.Time{}
	}
	h, m, _ := s.clock()
	for i := range 8 {
		d := t.AddDate(0, 0, i)
		at := time.Date(d.Year(), d.Month(), d.Day(), h, m, 0, 0, t.Location())
		if at.After(t) && s.on(at.Weekday()) {
			return at
		}
	}
	return time.Time{}
}

// Last is when the schedule last fired after from and by now, in from's zone; zero if it didn't.
func (s Schedule) Last(from, now time.Time) time.Time {
	var last time.Time
	for at := s.Next(from); !at.IsZero() && !at.After(now); at = s.Next(at) {
		last = at
	}
	return last
}

// String is how the schedule reads: "manual", "every weekday 07:00", "weekly Mon 07:00", "Mon, Thu 07:00".
func (s Schedule) String() string {
	var days []string
	for _, d := range s.Days {
		days = append(days, d.String()[:3])
	}
	switch s.Every {
	case EveryWeekday:
		return "every weekday " + s.At
	case EveryWeek:
		return "weekly " + strings.Join(days, ", ") + " " + s.At
	case EveryDays:
		return strings.Join(days, ", ") + " " + s.At
	}
	return "manual"
}
