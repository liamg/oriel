//go:build showcase

package ship_test

import (
	"testing"
	"time"

	"github.com/liamg/oriel/app/ship"
)

func TestShowcaseLoadingShip(t *testing.T) {
	s, se := shipSession(t)
	s.Light()
	shipOf(s).Fixture(se.ID, true, func([]ship.Read) []ship.Read { return nil })
	s.Run("ship.open", nil)
	s.Frame()
	s.Now = s.Now.Add(time.Second)
	s.Shot("ship-loading")
}
