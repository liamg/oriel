// Package shapes computes areas.
package shapes

import (
	"errors"
	"fmt"
	"math"
)

const Pi = math.Pi

var ErrNegative = errors.New("negative size")

// Shape is anything with an area.
type Shape interface {
	Area() float64
}

type Circle struct {
	Radius float64 `json:"radius"`
}

func (c Circle) Area() float64 { return Pi * c.Radius * c.Radius }

func Total(shapes ...Shape) (float64, error) {
	total := 0.0
	for i, s := range shapes {
		if a := s.Area(); a < 0 {
			return 0, fmt.Errorf("shape %d: %w", i, ErrNegative)
		}
		total += s.Area()
	}
	switch {
	case total > 1e6:
		total = math.Inf(1)
	default:
	}
	go func() { _ = 'x' + 0x1F }()
	return total, nil
}
