package app_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/registry"
)

// Refresh pressed while a tracker is being asked for your tickets isn't lost: once that ask answers, with what it read
// before the change, the tracker is asked again, and the change shows.
func TestTicketsRefreshDuringAnAskAsksAgain(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	var mu sync.Mutex
	titles, hold := []string{"Invoices drift by 1¢"}, false
	entered, release := make(chan struct{}), make(chan struct{})
	s.Registry().Contribute(registry.TicketTracker{Name: "held", Title: "Held", Assigned: func(context.Context) ([]registry.Ticket, error) {
		mu.Lock()
		held := hold
		hold = false
		now := append([]string(nil), titles...) // what the tracker had when asked
		mu.Unlock()
		if held {
			close(entered)
			<-release
		}
		var out []registry.Ticket
		for i, t := range now {
			out = append(out, registry.Ticket{Key: "ENG-" + string(rune('1'+i)), Title: t, State: "Todo", Stage: "todo"})
		}
		return out, nil
	}})
	s.Run("tickets.open", nil)
	s.Until("the first answer", func() bool { return strings.Contains(s.Text(), "Invoices drift by 1¢") })

	mu.Lock()
	hold = true // the next ask, which this refresh starts, waits until it's let go
	mu.Unlock()
	s.Run("tickets.refresh", nil)
	<-entered
	mu.Lock()
	titles = append(titles, "Rotate the signing keys")
	mu.Unlock()
	s.Run("tickets.refresh", nil)
	close(release)
	s.Until("the change, asked for again", func() bool { return strings.Contains(s.Text(), "Rotate the signing keys") })
}
