package ship

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/host/hostapi"
)

// Without origin, why a push can't run names the remotes the repo does have, so a remote under another name isn't
// mistaken for none at all.
func TestNoOriginSaysWhichRemotesThereAre(t *testing.T) {
	if why := cantPushWhy(shipRead{Ship: hostapi.Ship{Branch: "fix"}}); why != noOrigin {
		t.Errorf("no remotes: %q", why)
	}
	why := cantPushWhy(shipRead{Ship: hostapi.Ship{Branch: "fix", Remotes: []string{"upstream", "fork"}}})
	if !strings.Contains(why, "no remote named origin") || !strings.Contains(why, "upstream, fork") {
		t.Errorf("with upstream and fork: %q", why)
	}
}
