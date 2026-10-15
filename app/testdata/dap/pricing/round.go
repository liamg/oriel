package pricing

import "math"

// roundHalfEven rounds v to p decimal places, ties to even.
func roundHalfEven(v float64, p int) float64 {
	scaled := v * math.Pow10(p)
	floor := math.Trunc(scaled)
	diff := scaled - floor
	switch {
	case diff > 0.5:
		return (floor + 1) / math.Pow10(p)
	case diff == 0.5:
		if math.Mod(floor, 2) == 0 {
			return floor / math.Pow10(p)
		}
		return (floor + 1) / math.Pow10(p)
	}
	return floor / math.Pow10(p)
}
