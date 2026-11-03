package widget

import (
	"testing"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/render/snapshot"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// pickerFixture is a date picker on a page, on Wed Oct 14 2026, counting what it reports.
type pickerFixture struct {
	r                 DateRange
	picked, cancelled int
}

func (f *pickerFixture) build(c *ui.Ctx) {
	c.Column("page", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.Pad(20), Fill: c.Theme().C(theme.BgBase)}, func(c *ui.Ctx) {
		res := DatePicker(c, "dp", &f.r, DatePickerOpts{Today: day(2026, 10, 14), Location: time.UTC, Label: "Range", Help: "test/datepicker"})
		if res.Picked {
			f.picked++
		}
		if res.Cancelled {
			f.cancelled++
		}
	})
}

var pickerSize = render.Point{X: 360, Y: 340}

// The keyboard picks a range: arrows move the day (a day across, a week up and down), ⏎ picks the start and
// then the end, in either order; the calendar follows the day into another month. Escape cancels.
func TestDatePickerKeys(t *testing.T) {
	f := &pickerFixture{}
	h := newHarness(t, theme.Dark, pickerSize, f.build)
	h.frame()
	if !h.drew("October 2026") {
		t.Fatalf("the calendar didn't open on today's month: %q", h.texts())
	}
	for range 13 {
		h.keys("Left")
	}
	h.keys("CR")
	if f.picked != 0 {
		t.Fatal("one ⏎ picked a range: it picks the start")
	}
	h.keys("Up")
	if !h.drew("September 2026") {
		t.Errorf("a week up from Oct 1 didn't show September: %q", h.texts())
	}
	h.keys("CR")
	if want := (DateRange{From: day(2026, 9, 24), To: day(2026, 10, 1)}); f.picked != 1 || !f.r.From.Equal(want.From) || !f.r.To.Equal(want.To) {
		t.Errorf("picked %d, range %v – %v; want Sep 24 – Oct 1", f.picked, f.r.From, f.r.To)
	}
	h.keys("PageDown", "Esc")
	if f.cancelled != 1 || !f.r.To.Equal(day(2026, 10, 1)) {
		t.Errorf("Esc: cancelled %d, range %v – %v", f.cancelled, f.r.From, f.r.To)
	}
}

// Two clicks pick a range; the arrows beside the month page through months.
func TestDatePickerMouse(t *testing.T) {
	f := &pickerFixture{}
	h := newHarness(t, theme.Dark, pickerSize, f.build)
	h.clickNode("2026-10-09")
	h.clickNode("2026-10-03")
	h.frame()
	if f.picked != 1 || !f.r.From.Equal(day(2026, 10, 3)) || !f.r.To.Equal(day(2026, 10, 9)) {
		t.Errorf("picked %d, range %v – %v; want Oct 3 – 9", f.picked, f.r.From, f.r.To)
	}
	if n := h.find("2026-10-05"); n == nil || !n.Selected {
		t.Errorf("a day in the range isn't marked selected: %+v", n)
	}
	h.clickNode("Previous month")
	h.frame()
	if !h.drew("September 2026") {
		t.Errorf("‹ didn't page back: %q", h.texts())
	}
}

// A range typed as 2026-09-01..2026-09-30 is picked with ⏎; one that doesn't parse isn't.
func TestDatePickerTyped(t *testing.T) {
	f := &pickerFixture{}
	h := newHarness(t, theme.Dark, pickerSize, f.build)
	h.clickNode("Range, typed")
	h.typeText("2026-09-31..x")
	h.keys("CR")
	if f.picked != 0 {
		t.Fatalf("a range that doesn't parse was picked: %v – %v", f.r.From, f.r.To)
	}
	for range len("2026-09-31..x") {
		h.keys("BS")
	}
	h.typeText("2026-09-01..2026-09-30")
	h.keys("CR")
	if f.picked != 1 || !f.r.From.Equal(day(2026, 9, 1)) || !f.r.To.Equal(day(2026, 9, 30)) {
		t.Errorf("picked %d, range %v – %v; want Sep 1 – 30", f.picked, f.r.From, f.r.To)
	}
}

func TestDatePickerSnapshot(t *testing.T) {
	f := &pickerFixture{r: DateRange{From: day(2026, 9, 28), To: day(2026, 10, 9)}}
	h := newHarness(t, theme.Dark, pickerSize, f.build)
	h.frame()
	snapshot.GoldenPNG(t, "testdata/datepicker.png", raster.Render(h.list, pickerSize, 1, resolved(t, theme.Dark).C(theme.BgBase)), 0.02, 0.001)
}

// A date select is a button showing its range that opens the picker in a popover under it; a pick closes it
// and reports the range, Escape closes it with nothing picked.
func TestDateSelect(t *testing.T) {
	var r DateRange
	picks := 0
	h := newHarness(t, theme.Dark, render.Point{X: 400, Y: 400}, func(c *ui.Ctx) {
		c.Column("page", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.Pad(20)}, func(c *ui.Ctx) {
			if v, ok := DateSelect(c, "sel", r, DateSelectOpts{Label: "Range", Placeholder: "custom", Today: day(2026, 10, 14), Location: time.UTC,
				Help: "test/datepicker"}); ok {
				r, picks = v, picks+1
			}
		})
	})
	h.clickNode("custom")
	h.frame()
	if h.find("Range, calendar") == nil {
		t.Fatalf("the button didn't open the picker: %q", h.texts())
	}
	h.keys("Left", "CR", "Right", "Right", "CR")
	h.frame()
	if picks != 1 || !r.From.Equal(day(2026, 10, 13)) || !r.To.Equal(day(2026, 10, 15)) || h.find("Range, calendar") != nil {
		t.Errorf("picks %d, range %v – %v, picker still open %v", picks, r.From, r.To, h.find("Range, calendar") != nil)
	}
	if !h.drew("Oct 13 – Oct 15") {
		t.Errorf("the button doesn't show the range: %q", h.texts())
	}
	h.clickNode("Oct 13 – Oct 15")
	h.frame()
	h.keys("Esc")
	h.frame()
	if picks != 1 || h.find("Range, calendar") != nil {
		t.Errorf("Escape: picks %d, still open %v", picks, h.find("Range, calendar") != nil)
	}
}
