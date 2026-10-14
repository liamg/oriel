// The shim's API for cgo (shim_native.c).
#include <ghostty/vt.h>

typedef unsigned int u32;
typedef unsigned char u8;

typedef struct {
	u8 *out;
	u32 out_len;
	u8 *in;
	u32 in_cap;
	u32 out_cap;
	u8 *reply;
	u32 reply_len, reply_cap;
	u32 events;
	u8 held;
} TermHead;

void *oriel_new(u32 cols, u32 rows, u32 scrollback_bytes);
void oriel_free(void *s);
u32 oriel_input(void *s, u32 n);
void oriel_write(void *s, u32 n);
u32 oriel_take(void *s);
int oriel_resize(void *s, u32 cols, u32 rows);
u32 oriel_title(void *s);
u32 oriel_pwd(void *s);
int oriel_mode(void *s, u32 mode, u32 ansi);
u32 oriel_state(void *s);
u32 oriel_grid(void *s, u32 all, u32 force);
u32 oriel_cursor(void *s);
void oriel_scroll(void *s, u32 tag, int delta);
u32 oriel_text(void *s, u32 x0, u32 y0, u32 x1, u32 y1);
GhosttyTrackedGridRef oriel_mark(void *s, u32 x, int y);
u32 oriel_mark_point(void *s, GhosttyTrackedGridRef r);
void oriel_mark_free(GhosttyTrackedGridRef r);
u32 oriel_key(void *s, u32 key, u32 mods, u32 action, u32 textlen, u32 unshifted);
u32 oriel_paste(void *s, u32 n);
u32 oriel_snapshot(void *s);
int oriel_restore(void *s, u32 n, u32 scrollback_bytes);

#include <stdint.h>
// Grid refs cross to Go as integers, as they do from wasm.
static inline GhosttyTrackedGridRef oriel_ref(uintptr_t v) { return (GhosttyTrackedGridRef)v; }
static inline uintptr_t oriel_refval(GhosttyTrackedGridRef r) { return (uintptr_t)r; }
