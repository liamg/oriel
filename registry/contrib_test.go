package registry

import "testing"

// odd is another type that claims Busy's kind.
type odd struct{}

func (odd) ContributionKind() string { return "busy" }
func (odd) ContributionName() string { return "odd" }

// ContributionsOf reads a kind by its type: what's registered as one, and not what else claims the kind.
func TestContributionsOf(t *testing.T) {
	r := New()
	r.Contribute(Busy{Name: "a"})
	r.Contribute(odd{})
	r.Contribute(Busy{Name: "b"})
	got := ContributionsOf[Busy](r)
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" {
		t.Errorf("ContributionsOf[Busy] = %+v", got)
	}
}
