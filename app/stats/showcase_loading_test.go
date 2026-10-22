//go:build showcase

package stats_test

import (
	"testing"
	"time"
)

func TestShowcaseLoadingStats(t *testing.T) {
	s, _, _ := statsScene(t)
	s.Light()
	s.Run("stats.open", nil)
	s.Frame()
	s.Now = s.Now.Add(time.Second)
	s.Shot("stats-loading")
}
