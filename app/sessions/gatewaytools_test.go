package sessions_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/registry"
)

// A Lua plugin adds a tool to sandboxed agents' gateways: it's handed the call's project and arguments, and its
// answer, a table, crosses as JSON.
func TestLuaGatewayTool(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	s.Lua(`oriel.agent.gateway_tool{ name = "forge_tags", desc = "List tags.", read_only = true,
  schema = { type = "object", properties = { prefix = { type = "string" } } },
  run = function(call, done) done({ dir = call.dir, prefix = call.args.prefix }) end }`)
	var tool registry.GatewayTool
	for _, g := range registry.ContributionsOf[registry.GatewayTool](s.Registry()) {
		if g.Name == "forge_tags" {
			tool = g
		}
	}
	if tool.Run == nil || !tool.ReadOnly || string(tool.InputSchema) == "" {
		t.Fatalf("the tool: %+v", tool)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	type answer struct {
		out string
		err error
	}
	got := make(chan answer, 1)
	go func() {
		out, err := tool.Run(ctx, registry.GatewayCall{Project: "api", Dir: "/src/api", Args: json.RawMessage(`{"prefix":"v1."}`)})
		got <- answer{out, err}
	}()
	var a answer
	s.Until("its answer", func() bool {
		select {
		case a = <-got:
			return true
		default:
			return false
		}
	})
	var v map[string]string
	if a.err != nil || json.Unmarshal([]byte(a.out), &v) != nil || v["dir"] != "/src/api" || v["prefix"] != "v1." {
		t.Errorf("answer %q, err %v", a.out, a.err)
	}
}
