package overview

import "github.com/liamg/oriel/internal/clock"

const PanelBudget = panelBudget

func (o *Overview) SetClock(c clock.Clock) { o.clock = c }

// Done says every card's header and panels have been computed.
func (o *Overview) Done() bool {
	for _, cd := range o.cards {
		if cd.git && cd.header == nil {
			return false
		}
		for _, p := range cd.panels {
			if !p.done {
				return false
			}
		}
	}
	return len(o.cards) > 0
}
