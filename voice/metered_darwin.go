//go:build cgo

package voice

/*
#cgo darwin LDFLAGS: -framework Network
#include <Network/Network.h>
#include <dispatch/dispatch.h>

// oriel_metered: 1 when the current path is expensive (a hotspot, cellular) or constrained (Low Data Mode).
static int oriel_metered(void) {
	__block int out = 0;
	dispatch_semaphore_t got = dispatch_semaphore_create(0);
	dispatch_queue_t q = dispatch_queue_create("oriel.metered", DISPATCH_QUEUE_SERIAL);
	nw_path_monitor_t m = nw_path_monitor_create();
	nw_path_monitor_set_queue(m, q);
	nw_path_monitor_set_update_handler(m, ^(nw_path_t path) {
		out = nw_path_is_expensive(path) || nw_path_is_constrained(path);
		dispatch_semaphore_signal(got);
	});
	nw_path_monitor_start(m);
	long timedOut = dispatch_semaphore_wait(got, dispatch_time(DISPATCH_TIME_NOW, 2 * NSEC_PER_SEC));
	nw_path_monitor_cancel(m);
	dispatch_sync(q, ^{});
	int metered = timedOut ? 0 : out;
	nw_release(m);
	dispatch_release(q);
	dispatch_release(got);
	return metered;
}
*/
import "C"

// Metered reports whether macOS counts the network as expensive or constrained (Low Data Mode).
func Metered() bool { return C.oriel_metered() != 0 }
