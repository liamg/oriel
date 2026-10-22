package shell_test

import (
	"context"
	"testing"
	"time"

	"github.com/liamg/oriel/app/shell"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/registry"
)

// 37-errors: a hook turned off after 3 errors, its line marked in the open file, the toast, and the log open on it.
func TestLuaErrorsSnapshot(t *testing.T) {
	s := apptest.LuaScene(t, apptest.InitLua)
	s.Now = apptest.At(14, 7, 3)
	shell.SetNow(t, func() time.Time { return s.Now })
	for range 3 {
		s.Registry().Fire(context.Background(), "Permission", "", map[string]any{"tool": "Bash", "input": "go test ./..."})
	}
	n, _ := apptest.LastNotice(s.Store().State(), "failed.hook.")
	a, _ := apptest.Action(n, "Open at line")
	s.Run(a.Command, registry.Args(a.Args))
	s.Run("log.open", registry.Args{"seq": n.Seq})
	s.WaitHighlights()
	s.Golden("lua-errors")
}
