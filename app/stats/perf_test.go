package stats_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/stats"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/sysload"
)

// The status line's CPU opens the Performance page: Oriel's own load and frames, the machine's, and the processes
// Oriel started, by the session each runs for. Escape closes it.
func TestCPUOpensThePerformancePage(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	s.Now = apptest.At(14, 0, 0)
	apptest.PluginOf[*stats.Perf](s, "perf").Every = 50 * time.Millisecond
	se := core.SessionState{ID: "s-perf", Name: "perf-session", Kind: core.KindWork, Status: core.StatusWorking}
	s.Apply(core.SessionSynced{Session: se}, core.SessionFocused{ID: se.ID},
		core.MachineChanged{Machine: core.MachineState{Host: "mbp", Cores: 12, CPU: .34, Mem: 11 << 30, MemTotal: 32 << 30, Sampled: s.Now}})
	agent := exec.Command("sleep", "30")
	agent.Env = append(os.Environ(), sysload.SessionEnv+"="+string(se.ID))
	if err := agent.Start(); err != nil {
		t.Skip("no sleep:", err)
	}
	t.Cleanup(func() { agent.Process.Kill(); agent.Wait() })

	s.ClickNode("CPU 34%, Performance")
	if s.Find("Performance") == nil {
		t.Fatalf("no Performance page:\n%s", s.Text())
	}
	s.Until("two samples of the processes", func() bool {
		text := s.Text()
		return strings.Contains(text, "oriel (this window)") && strings.Contains(text, "sleep 30") && !strings.Contains(text, "—\nof a core")
	})
	text := s.Text()
	for _, want := range []string{"ORIEL", "GOROUTINES", "FRAMES", "THIS MACHINE", "34%", "DISK FREE", "SESSIONS", "perf-session", "PROCESSES"} {
		if !strings.Contains(text, want) {
			t.Errorf("the page lacks %q:\n%s", want, text)
		}
	}
	s.Press("<Esc>")
	s.Frame()
	if s.Find("Performance") != nil {
		t.Error("Escape left the Performance page up")
	}
}
