//go:build live

package gateway

import (
	"context"
	"encoding/json"
	"testing"
)

// The recommended servers against the real services, with this machine's
// own logins:  go test -tags live -run TestRecommendedLive ./internal/gateway/
func TestRecommendedLive(t *testing.T) {
	for _, r := range Recommended {
		var raw rawServer
		_ = json.Unmarshal(r.Config, &raw)
		s, _ := toServer(r.Name, "oriel", raw, nil)
		st := Probe(context.Background(), s)
		t.Logf("%s: %+v", r.Name, st)
	}
}
