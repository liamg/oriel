package sysload

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func run(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err
}

// readCPU sums every process's share from ps: a percent of one core each,
// so the total is spread over all of them.
func readCPU() (float64, error) {
	s, err := run("ps", "-A", "-o", "%cpu=")
	if err != nil {
		return 0, err
	}
	var sum float64
	for f := range strings.FieldsSeq(s) {
		if n, err := strconv.ParseFloat(f, 64); err == nil {
			sum += n
		}
	}
	return sum / float64(runtime.NumCPU()), nil
}

func readMem() (used, total uint64, err error) {
	s, err := run("sysctl", "-n", "hw.memsize")
	if err != nil {
		return 0, 0, err
	}
	total, err = strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, 0, err
	}
	s, err = run("vm_stat")
	if err != nil {
		return 0, 0, err
	}
	used, ok := parseVMStat(s)
	if !ok {
		return 0, 0, errors.New("cannot read vm_stat")
	}
	return min(used, total), total, nil
}
