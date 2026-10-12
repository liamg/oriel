package toml

import (
	"fmt"
	"strings"
	"time"
)

// LocalDate is a date with no time or offset.
type LocalDate struct {
	Year  int
	Month time.Month
	Day   int
}

func (d LocalDate) String() string { return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day) }

// LocalTime is a time of day with no date or offset.
type LocalTime struct {
	Hour, Minute, Second, Nanosecond int
}

func (t LocalTime) String() string {
	s := fmt.Sprintf("%02d:%02d:%02d", t.Hour, t.Minute, t.Second)
	if t.Nanosecond > 0 {
		s += strings.TrimRight(fmt.Sprintf(".%09d", t.Nanosecond), "0")
	}
	return s
}

// LocalDateTime is a date and time with no offset.
type LocalDateTime struct {
	LocalDate
	LocalTime
}

func (dt LocalDateTime) String() string { return dt.LocalDate.String() + "T" + dt.LocalTime.String() }

// ParseError is where and why a document failed to parse. Line and Col are 1-based; Col counts bytes.
type ParseError struct {
	Line, Col int
	Msg       string
}

func (e *ParseError) Error() string { return fmt.Sprintf("toml: %d:%d: %s", e.Line, e.Col, e.Msg) }

func errAt(src []byte, off int, format string, args ...any) *ParseError {
	line, col := 1, 1
	for _, b := range src[:min(off, len(src))] {
		if b == '\n' {
			line, col = line+1, 1
		} else {
			col++
		}
	}
	return &ParseError{Line: line, Col: col, Msg: fmt.Sprintf(format, args...)}
}
