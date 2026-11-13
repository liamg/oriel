package sessions_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry"
)

const ticketDescription = "## Steps to reproduce\n\n1. Bill a tiered usage plan.\n2. Compare the invoice with Stripe's.\n\nThe invoice is 1¢ out on lines that land on a half cent."

// A plan started from a ticket says so in its docs' Source as a row, Ticket ENG-2231, not the ticket's text squeezed
// into the column; the row opens the ticket in a tab of its own, its description read in full.
func TestPlanSourceOpensItsTicketInATab(t *testing.T) {
	s := planScene(t)
	s.Registry().Contribute(registry.TicketTracker{Name: "linear", Title: "Linear", Keys: regexp.MustCompile(`^ENG-\d+$`),
		Lookup: func(_ context.Context, key string) (registry.Ticket, error) {
			return registry.Ticket{Key: key, Title: "Invoices drift by 1¢ on tiered plans", State: "In Progress", Stage: "started",
				Description: ticketDescription}, nil
		}})
	s.Run("plan.open", nil)
	s.SettleReads()
	if s.Find("Ticket ENG-2231") == nil {
		t.Fatalf("no ticket row in the plan's docs:\n%s", s.Text())
	}
	if strings.Contains(s.Text(), "Rounding on invoices drifts by 1¢ for tiered usage plans.") {
		t.Error("the ticket's text is still squeezed into the docs' column")
	}
	s.ClickNode("Ticket ENG-2231")
	s.Quiet()
	if !s.Showing(kit.TicketView) {
		t.Fatal("the ticket row didn't open the ticket's tab")
	}
	s.WantFrame("ENG-2231", "Invoices drift by 1¢ on tiered plans", "IN PROGRESS", "Steps to reproduce", "Compare the invoice with Stripe's.")
	s.Golden("ticket-tab")
}
