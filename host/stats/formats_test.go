package stats

import (
	"github.com/liamg/oriel/agent/claude"
	"github.com/liamg/oriel/agent/codex"
	"github.com/liamg/oriel/agent/usage"
)

// testFormats are the formats the providers' logs are read in.
func testFormats(name string) (usage.Format, bool) {
	for _, f := range []usage.Format{claude.UsageLog, codex.UsageLog, usage.Events} {
		if f.Name == name {
			return f, true
		}
	}
	return usage.Format{}, false
}
