package sysload

import "testing"

func TestParseProcStat(t *testing.T) {
	s := "cpu  100 5 50 800 20 0 25 0 7 0\ncpu0 1 2 3 4 5 6 7 8 9 10\n"
	got, ok := parseProcStat(s)
	if !ok {
		t.Fatal("not parsed")
	}
	if got.total != 1000 || got.busy != 180 {
		t.Fatalf("got %+v, want total 1000 busy 180", got)
	}
}

func TestBusyPercent(t *testing.T) {
	a := cpuTimes{busy: 100, total: 1000}
	b := cpuTimes{busy: 150, total: 1200}
	if p := busyPercent(a, b); p != 25 {
		t.Fatalf("got %v, want 25", p)
	}
	if p := busyPercent(b, a); p != 0 {
		t.Fatalf("counters going backwards: got %v, want 0", p)
	}
}

func TestParseMeminfo(t *testing.T) {
	s := "MemTotal:       16000000 kB\nMemFree:         1000000 kB\nMemAvailable:    4000000 kB\n"
	used, total, ok := parseMeminfo(s)
	if !ok || total != 16000000*1024 || used != 12000000*1024 {
		t.Fatalf("got used %d total %d ok %v", used, total, ok)
	}
	if _, _, ok := parseMeminfo("MemTotal: 10 kB\n"); ok {
		t.Fatal("parsed without MemAvailable")
	}
}

func TestParseVMStat(t *testing.T) {
	s := `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                               12345.
Pages active:                            100.
Pages inactive:                           999.
Pages wired down:                          50.
Pages occupied by compressor:              10.
`
	used, ok := parseVMStat(s)
	if !ok || used != 160*16384 {
		t.Fatalf("got %d ok %v", used, ok)
	}
}

func TestRead(t *testing.T) {
	r := Read()
	if r.Cores < 1 {
		t.Fatalf("cores %d", r.Cores)
	}
	if r.CPU < 0 || r.CPU > 100 {
		t.Fatalf("cpu %v", r.CPU)
	}
}
