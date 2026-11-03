//go:build darwin && cgo

package sysload

/*
#include <mach/mach.h>
#include <mach/mach_host.h>
#include <sys/sysctl.h>

static mach_port_t host_port;

static mach_port_t the_host(void) {
	if (host_port == MACH_PORT_NULL) {
		host_port = mach_host_self();
	}
	return host_port;
}

// cpu_ticks is the ticks each core has spent in each state, summed: user, system, idle, nice.
static int cpu_ticks(unsigned int out[4]) {
	host_cpu_load_info_data_t info;
	mach_msg_type_number_t n = HOST_CPU_LOAD_INFO_COUNT;
	if (host_statistics(the_host(), HOST_CPU_LOAD_INFO, (host_info_t)&info, &n) != KERN_SUCCESS) {
		return -1;
	}
	out[0] = info.cpu_ticks[CPU_STATE_USER];
	out[1] = info.cpu_ticks[CPU_STATE_SYSTEM];
	out[2] = info.cpu_ticks[CPU_STATE_IDLE];
	out[3] = info.cpu_ticks[CPU_STATE_NICE];
	return 0;
}

// mem is memory in use as vm_stat counts it (active, wired and compressed pages), and all there is.
static int mem(unsigned long long *used, unsigned long long *total) {
	size_t len = sizeof(*total);
	if (sysctlbyname("hw.memsize", total, &len, NULL, 0) != 0) {
		return -1;
	}
	vm_size_t page;
	if (host_page_size(the_host(), &page) != KERN_SUCCESS) {
		return -1;
	}
	vm_statistics64_data_t vm;
	mach_msg_type_number_t n = HOST_VM_INFO64_COUNT;
	if (host_statistics64(the_host(), HOST_VM_INFO64, (host_info64_t)&vm, &n) != KERN_SUCCESS) {
		return -1;
	}
	*used = ((unsigned long long)vm.active_count + vm.wire_count + vm.compressor_page_count) * page;
	return 0;
}
*/
import "C"

import "errors"

// tickCarry carries on the kernel's tick counters, which are 32 bits and wrap; its Reader's mu guards it.
type tickCarry struct {
	prev [4]uint32
	sum  [4]uint64
	have bool
}

func ticks(c *tickCarry) (cpuTimes, error) {
	var raw [4]C.uint
	if C.cpu_ticks(&raw[0]) != 0 {
		return cpuTimes{}, errors.New("cannot read the CPU's ticks")
	}
	for i, v := range raw {
		if c.have {
			c.sum[i] += uint64(uint32(v) - c.prev[i])
		} else {
			c.sum[i] = uint64(v)
		}
		c.prev[i] = uint32(v)
	}
	c.have = true
	busy := c.sum[0] + c.sum[1] + c.sum[3]
	return cpuTimes{busy: busy, total: busy + c.sum[2]}, nil
}

func readMem() (used, total uint64, err error) {
	var u, t C.ulonglong
	if C.mem(&u, &t) != 0 {
		return 0, 0, errors.New("cannot read the memory in use")
	}
	return min(uint64(u), uint64(t)), uint64(t), nil
}
