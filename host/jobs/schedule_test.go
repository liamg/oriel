package jobs

import (
	"testing"
	"time"
)

// Every weekday, weekly at a time, or custom, in the host's time.
func TestScheduleNext(t *testing.T) {
	wed := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC) // Wednesday
	for _, c := range []struct {
		s    Schedule
		want string
	}{
		{Schedule{}, ""},
		{Schedule{Every: EveryWeekday, At: "07:00"}, "Thu 15 07:00"},
		{Schedule{Every: EveryWeekday, At: "10:30"}, "Wed 14 10:30"},
		{Schedule{Every: EveryWeek, Days: []time.Weekday{time.Monday}, At: "07:00"}, "Mon 19 07:00"},
		{Schedule{Every: EveryDays, Days: []time.Weekday{time.Saturday, time.Tuesday}, At: "06:15"}, "Sat 17 06:15"},
	} {
		got := ""
		if n := c.s.Next(wed); !n.IsZero() {
			got = n.Format("Mon 02 15:04")
		}
		if got != c.want {
			t.Errorf("%+v: next = %q, want %q", c.s, got, c.want)
		}
	}
	fri := time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC)
	if n := (Schedule{Every: EveryWeekday, At: "07:00"}).Next(fri); n.Weekday() != time.Monday {
		t.Errorf("after Friday's run, every weekday is next %s", n.Weekday())
	}
	for _, s := range []Schedule{{Every: EveryWeekday, At: "7am"}, {Every: EveryWeek, At: "07:00"}, {Every: "hourly", At: "07:00"}} {
		if s.Check() == nil {
			t.Errorf("%+v passed its check", s)
		}
	}
}
