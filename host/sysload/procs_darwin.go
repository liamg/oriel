//go:build darwin && cgo

package sysload

/*
#include <libproc.h>
#include <string.h>
#include <mach/mach_time.h>
#include <sys/proc_info.h>
#include <sys/sysctl.h>

// task_of is pid's parent, name, CPU times (in mach time units) and resident bytes.
static int task_of(int pid, int *ppid, char *name, unsigned long long *user, unsigned long long *sys, unsigned long long *rss) {
	struct proc_taskallinfo info;
	if (proc_pidinfo(pid, PROC_PIDTASKALLINFO, 0, &info, sizeof(info)) != sizeof(info)) {
		return -1;
	}
	*ppid = info.pbsd.pbi_ppid;
	strlcpy(name, info.pbsd.pbi_comm, MAXCOMLEN + 1);
	*user = info.ptinfo.pti_total_user;
	*sys = info.ptinfo.pti_total_system;
	*rss = info.ptinfo.pti_resident_size;
	return 0;
}

static void timebase(unsigned int *numer, unsigned int *denom) {
	mach_timebase_info_data_t tb;
	mach_timebase_info(&tb);
	*numer = tb.numer;
	*denom = tb.denom;
}

// procargs is pid's KERN_PROCARGS2: its argument count, executable, arguments and environment.
static int procargs(int pid, void *buf, size_t *size) {
	int mib[3] = {CTL_KERN, KERN_PROCARGS2, pid};
	return sysctl(mib, 3, buf, size, NULL, 0);
}
*/
import "C"

import (
	"errors"
	"strings"
	"time"
	"unsafe"
)

func procs() ([]Proc, error) {
	n := C.proc_listallpids(nil, 0)
	if n <= 0 {
		return nil, errors.New("cannot list processes")
	}
	pids := make([]C.int, n+64) // room for some started since
	n = C.proc_listallpids(unsafe.Pointer(&pids[0]), C.int(len(pids))*C.int(unsafe.Sizeof(pids[0])))
	if n <= 0 {
		return nil, errors.New("cannot list processes")
	}
	var numer, denom C.uint
	C.timebase(&numer, &denom)
	if denom == 0 {
		numer, denom = 1, 1
	}
	var name [C.MAXCOMLEN + 1]C.char
	out := make([]Proc, 0, n)
	for _, pid := range pids[:min(int(n), len(pids))] {
		var ppid C.int
		var user, sys, rss C.ulonglong
		if C.task_of(pid, &ppid, &name[0], &user, &sys, &rss) != 0 {
			continue // another user's, or gone
		}
		ns := (uint64(user) + uint64(sys)) * uint64(numer) / uint64(denom)
		out = append(out, Proc{PID: int(pid), PPID: int(ppid), Command: C.GoString(&name[0]), CPU: time.Duration(ns), RSS: uint64(rss)})
	}
	return out, nil
}

// argsMax bounds what's read of a process's arguments and environment.
const argsMax = 256 << 10

func details(ps []Proc) {
	buf := make([]byte, argsMax)
	for i := range ps {
		size := C.size_t(len(buf))
		if C.procargs(C.int(ps[i].PID), unsafe.Pointer(&buf[0]), &size) != 0 {
			continue
		}
		args, env := parseProcArgs(buf[:size])
		if len(args) > 0 {
			ps[i].Command = strings.Join(args, " ")
		}
		ps[i].Session = sessionIn(env)
	}
}
