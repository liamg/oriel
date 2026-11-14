package prui

import (
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
)

// A review finding's tone, by severity.
var (
	TFindCritical = theme.Declare("review.finding.critical", "red")
	TFindHigh     = theme.Declare("review.finding.high", "red")
	TFindMedium   = theme.Declare("review.finding.medium", "amber")
	TFindLow      = theme.Declare("review.finding.low", "fg.faint")
)

// SeverityShort is a severity as a chip says it.
func SeverityShort(s string) string {
	if s == "medium" {
		return "med"
	}
	return s
}

// SeverityTone is a finding's tone by its severity.
func SeverityTone(th *theme.Resolved, s string) render.Color {
	switch s {
	case "critical":
		return th.C(TFindCritical)
	case "high":
		return th.C(TFindHigh)
	case "medium":
		return th.C(TFindMedium)
	}
	return th.C(TFindLow)
}
