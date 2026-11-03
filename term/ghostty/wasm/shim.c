// Bulk calls over libghostty-vt's C API, so the host crosses the wasm boundary once per write, frame or query.
#include <ghostty/vt.h>

typedef unsigned int u32;
typedef unsigned short u16;
typedef unsigned char u8;

// The host reads this struct at fixed offsets (ghostty.go's off* constants); keep the two in step.
typedef struct {
	u8 *out;       // results of the last call
	u32 out_len;
	u8 *in;        // where the host puts bytes for oriel_write and friends
	u32 in_cap;
	u32 out_cap;
	u8 *reply;     // bytes the terminal wants written back to the pty
	u32 reply_len, reply_cap;
	u32 events;    // EV_* since the last oriel_events
	u8 held;       // synchronized output is holding the frame
	GhosttyTerminal t;
	GhosttyRenderState rs;
	GhosttyRenderStateRowIterator rows;
	GhosttyRenderStateRowCells cells;
	GhosttyKeyEncoder enc;
	GhosttyKeyEvent key;
} Term;

enum { EV_TITLE = 1, EV_PWD = 2, EV_BELL = 4, EV_HOLD = 8, EV_UNHOLD = 16 };

static void *grow(void *p, u32 *cap, u32 need) {
	if (need <= *cap) return p;
	u32 n = *cap ? *cap : 256;
	while (n < need) n *= 2;
	u8 *q = ghostty_wasm_alloc(n);
	if (!q) return 0;
	if (p) {
		__builtin_memcpy(q, p, *cap);
		ghostty_wasm_free(p, *cap);
	}
	*cap = n;
	return q;
}

static int reserve(Term *s, u32 n) {
	u8 *p = grow(s->out, &s->out_cap, n);
	if (!p) return 0;
	s->out = p;
	return 1;
}

static int put(Term *s, const void *b, u32 n) {
	if (!reserve(s, s->out_len + n)) return 0;
	__builtin_memcpy(s->out + s->out_len, b, n);
	s->out_len += n;
	return 1;
}

static void on_reply(GhosttyTerminal t, void *ud, const uint8_t *data, size_t len) {
	Term *s = ud;
	u8 *p = grow(s->reply, &s->reply_cap, s->reply_len + len);
	if (!p) return;
	s->reply = p;
	__builtin_memcpy(s->reply + s->reply_len, data, len);
	s->reply_len += len;
}

static void on_title(GhosttyTerminal t, void *ud) { ((Term *)ud)->events |= EV_TITLE; }
static void on_pwd(GhosttyTerminal t, void *ud) { ((Term *)ud)->events |= EV_PWD; }
static void on_bell(GhosttyTerminal t, void *ud) { ((Term *)ud)->events |= EV_BELL; }

static void on_hold(GhosttyTerminal t, void *ud, bool held) {
	Term *s = ud;
	if (held) ghostty_render_state_update(s->rs, t); // the frame the program wants left on screen
	s->held = held;
	s->events |= held ? EV_HOLD : EV_UNHOLD;
}

static void attach(Term *s, GhosttyTerminal t, u32 scrollback_bytes) {
	s->t = t;
	size_t sb = scrollback_bytes;
	ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_SCROLLBACK_MAX_BYTES, &sb);
	ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_USERDATA, s);
	ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_WRITE_PTY, (const void *)on_reply);
	ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_TITLE_CHANGED, (const void *)on_title);
	ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_PWD_CHANGED, (const void *)on_pwd);
	ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_BELL, (const void *)on_bell);
	ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_RENDER_HOLD, (const void *)on_hold);
	// Snapshots carry an unfinished sequence; tracking it needs a limit.
	size_t cont = 1 << 16;
	ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_CONTINUATION_MAX_BYTES, &cont);
}

Term *oriel_new(u32 cols, u32 rows, u32 scrollback_bytes) {
	Term *s = ghostty_wasm_alloc(sizeof(Term));
	if (!s) return 0;
	__builtin_memset(s, 0, sizeof(Term));
	GhosttyTerminal t = 0;
	if (ghostty_terminal_new(0, &t, cols, rows) || ghostty_render_state_new(0, &s->rs) ||
	    ghostty_render_state_row_iterator_new(0, &s->rows) || ghostty_render_state_row_cells_new(0, &s->cells) ||
	    ghostty_key_encoder_new(0, &s->enc) || ghostty_key_event_new(0, &s->key))
		return 0;
	attach(s, t, scrollback_bytes);
	// Grapheme clustering (mode 2027) on by default, as in Ghostty, so text is clustered as the editor clusters it.
	GhosttyTerminalModeConfig m = {.mode = GHOSTTY_MODE_GRAPHEME_CLUSTER, .value = true};
	ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_MODE_DEFAULT, &m);
	return s;
}

void oriel_free(Term *s) {
	ghostty_key_event_free(s->key);
	ghostty_key_encoder_free(s->enc);
	ghostty_render_state_row_cells_free(s->cells);
	ghostty_render_state_row_iterator_free(s->rows);
	ghostty_render_state_free(s->rs);
	ghostty_terminal_free(s->t);
	ghostty_wasm_free(s->out, s->out_cap);
	ghostty_wasm_free(s->in, s->in_cap);
	ghostty_wasm_free(s->reply, s->reply_cap);
	ghostty_wasm_free(s, sizeof(Term));
}

// oriel_input makes room for n bytes at s->in.
u32 oriel_input(Term *s, u32 n) {
	u8 *p = grow(s->in, &s->in_cap, n);
	if (!p) return 0;
	s->in = p;
	return 1;
}

void oriel_write(Term *s, u32 n) { ghostty_terminal_vt_write(s->t, s->in, n); }

// oriel_take moves the pending replies and events to out: u32 events, then the reply bytes.
u32 oriel_take(Term *s) {
	s->out_len = 0;
	put(s, &s->events, 4);
	put(s, s->reply, s->reply_len);
	s->events = 0;
	s->reply_len = 0;
	return s->out_len;
}

int oriel_resize(Term *s, u32 cols, u32 rows) { return ghostty_terminal_resize(s->t, cols, rows, 1, 1); }

static void put_string(Term *s, GhosttyTerminalData d) {
	GhosttyString str = {0};
	s->out_len = 0;
	if (ghostty_terminal_get(s->t, d, &str) == GHOSTTY_SUCCESS) put(s, str.ptr, str.len);
}

u32 oriel_title(Term *s) { put_string(s, GHOSTTY_TERMINAL_DATA_TITLE); return s->out_len; }
u32 oriel_pwd(Term *s) { put_string(s, GHOSTTY_TERMINAL_DATA_PWD); return s->out_len; }

// oriel_mode is 1 if the mode is set, 0 if reset, -1 if unknown. ansi selects ANSI modes over DEC private ones.
int oriel_mode(Term *s, u32 mode, u32 ansi) {
	GhosttyTerminalModeConfig c = {.mode = ghostty_mode_new(mode, ansi)};
	if (ghostty_terminal_get(s->t, GHOSTTY_TERMINAL_DATA_MODE, &c)) return -1;
	return c.value;
}

// oriel_state writes u32s: cols, rows, cursor x, cursor y (active area), pending wrap, alt screen, scrollback total,
// viewport offset, viewport len, mouse tracking, held.
u32 oriel_state(Term *s) {
	u16 cols = 0, rows = 0, cx = 0, cy = 0;
	bool wrap = false, mouse = false;
	GhosttyTerminalScreen screen = 0;
	GhosttyTerminalScrollbar sb = {0};
	ghostty_terminal_get(s->t, GHOSTTY_TERMINAL_DATA_COLS, &cols);
	ghostty_terminal_get(s->t, GHOSTTY_TERMINAL_DATA_ROWS, &rows);
	ghostty_terminal_get(s->t, GHOSTTY_TERMINAL_DATA_CURSOR_X, &cx);
	ghostty_terminal_get(s->t, GHOSTTY_TERMINAL_DATA_CURSOR_Y, &cy);
	ghostty_terminal_get(s->t, GHOSTTY_TERMINAL_DATA_CURSOR_PENDING_WRAP, &wrap);
	ghostty_terminal_get(s->t, GHOSTTY_TERMINAL_DATA_ACTIVE_SCREEN, &screen);
	ghostty_terminal_get(s->t, GHOSTTY_TERMINAL_DATA_SCROLLBAR, &sb);
	ghostty_terminal_get(s->t, GHOSTTY_TERMINAL_DATA_MOUSE_TRACKING, &mouse);
	u32 v[] = {cols, rows, cx, cy, wrap, screen, (u32)sb.total, (u32)sb.offset, (u32)sb.len, mouse, s->held};
	s->out_len = 0;
	put(s, v, sizeof v);
	return s->out_len;
}

static u32 color(GhosttyStyleColor c) {
	switch (c.tag) {
	case GHOSTTY_STYLE_COLOR_PALETTE: return 1u << 24 | c.value.palette;
	case GHOSTTY_STYLE_COLOR_RGB: return 2u << 24 | (u32)c.value.rgb.r << 16 | (u32)c.value.rgb.g << 8 | c.value.rgb.b;
	default: return 0;
	}
}

enum { A_BOLD = 1, A_ITALIC = 2, A_FAINT = 4, A_BLINK = 8, A_INVERSE = 16, A_INVISIBLE = 32, A_STRIKE = 64, A_OVERLINE = 128 };

// One cell as the host reads it: 20 bytes, then extra grapheme codepoints.
typedef struct {
	u32 cp;
	u8 wide, attrs, underline, extra;
	u32 fg, bg, ul;
} Cell;

static void put_row(Term *s, u16 y, bool dirty) {
	GhosttyRow raw = 0;
	ghostty_render_state_row_get(s->rows, GHOSTTY_RENDER_STATE_ROW_DATA_RAW, &raw);
	bool wrap = false, cont = false;
	GhosttyRowSemanticPrompt prompt = 0;
	ghostty_row_get(raw, GHOSTTY_ROW_DATA_WRAP, &wrap);
	ghostty_row_get(raw, GHOSTTY_ROW_DATA_WRAP_CONTINUATION, &cont);
	ghostty_row_get(raw, GHOSTTY_ROW_DATA_SEMANTIC_PROMPT, &prompt);
	ghostty_render_state_row_get(s->rows, GHOSTTY_RENDER_STATE_ROW_DATA_CELLS, &s->cells);
	u32 head = s->out_len;
	u16 h[4] = {y, (u16)(dirty | wrap << 1 | cont << 2 | (u16)prompt << 3), 0, 0};
	if (!put(s, h, sizeof h)) return;
	u16 n = 0;
	while (ghostty_render_state_row_cells_next(s->cells)) {
		GhosttyCell c = 0;
		GhosttyStyle st = GHOSTTY_INIT_SIZED(GhosttyStyle);
		uint32_t glen = 0;
		ghostty_render_state_row_cells_get(s->cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_RAW, &c);
		ghostty_render_state_row_cells_get(s->cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_STYLE, &st);
		ghostty_render_state_row_cells_get(s->cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_GRAPHEMES_LEN, &glen);
		Cell o = {0};
		GhosttyCellWide wide = 0;
		GhosttyCellContentTag tag = 0;
		ghostty_cell_get(c, GHOSTTY_CELL_DATA_WIDE, &wide);
		ghostty_cell_get(c, GHOSTTY_CELL_DATA_CONTENT_TAG, &tag);
		o.wide = wide;
		o.fg = color(st.fg_color);
		o.bg = color(st.bg_color);
		o.ul = color(st.underline_color);
		if (tag == GHOSTTY_CELL_CONTENT_BG_COLOR_PALETTE) {
			GhosttyColorPaletteIndex i = 0;
			ghostty_cell_get(c, GHOSTTY_CELL_DATA_COLOR_PALETTE, &i);
			o.bg = 1u << 24 | i;
		} else if (tag == GHOSTTY_CELL_CONTENT_BG_COLOR_RGB) {
			GhosttyColorRgb rgb = {0};
			ghostty_cell_get(c, GHOSTTY_CELL_DATA_COLOR_RGB, &rgb);
			o.bg = 2u << 24 | (u32)rgb.r << 16 | (u32)rgb.g << 8 | rgb.b;
		}
		o.attrs = st.bold * A_BOLD | st.italic * A_ITALIC | st.faint * A_FAINT | st.blink * A_BLINK |
		          st.inverse * A_INVERSE | st.invisible * A_INVISIBLE | st.strikethrough * A_STRIKE |
		          st.overline * A_OVERLINE;
		o.underline = st.underline;
		u32 cps[16];
		if (glen > 16) glen = 16;
		if (glen > 0) {
			ghostty_render_state_row_cells_get(s->cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_GRAPHEMES_BUF, cps);
			o.cp = cps[0];
			o.extra = glen - 1;
		}
		if (!put(s, &o, sizeof o) || (glen > 1 && !put(s, cps + 1, (glen - 1) * 4))) return;
		n++;
	}
	((u16 *)(s->out + head))[2] = n;
}

// oriel_grid writes the viewport's rows (only dirty ones unless all) and clears the dirty flags: u32 dirty state, then
// per row u16 y, u16 flags (1 dirty, 2 wrapped, 4 wrap continuation, 8*prompt), u16 cells, u16 0, then the cells.
// The frame stays as it was while synchronized output holds it, unless force.
u32 oriel_grid(Term *s, u32 all, u32 force) {
	if (!s->held || force) ghostty_render_state_update(s->rs, s->t);
	GhosttyRenderStateDirty dirty = 0;
	ghostty_render_state_get(s->rs, GHOSTTY_RENDER_STATE_DATA_DIRTY, &dirty);
	s->out_len = 0;
	u32 d = dirty;
	put(s, &d, 4);
	ghostty_render_state_get(s->rs, GHOSTTY_RENDER_STATE_DATA_ROW_ITERATOR, &s->rows);
	u16 y = 0;
	for (; ghostty_render_state_row_iterator_next(s->rows); y++) {
		bool rd = false;
		ghostty_render_state_row_get(s->rows, GHOSTTY_RENDER_STATE_ROW_DATA_DIRTY, &rd);
		rd = rd || dirty == GHOSTTY_RENDER_STATE_DIRTY_FULL;
		if (all || rd) put_row(s, y, rd);
	}
	ghostty_render_state_clean(s->rs);
	return s->out_len;
}

// oriel_cursor writes u32s: has value in the viewport, x, y, visible, blinking, style, wide tail.
u32 oriel_cursor(Term *s) {
	if (!s->held) ghostty_render_state_update(s->rs, s->t);
	GhosttyRenderStateCursor c = GHOSTTY_INIT_SIZED(GhosttyRenderStateCursor);
	ghostty_render_state_get(s->rs, GHOSTTY_RENDER_STATE_DATA_CURSOR, &c);
	u32 v[] = {c.viewport_has_value, c.viewport_x, c.viewport_y, c.visible, c.blinking, c.visual_style, c.wide_tail};
	s->out_len = 0;
	put(s, v, sizeof v);
	return s->out_len;
}

void oriel_scroll(Term *s, u32 tag, int delta) {
	GhosttyTerminalScrollViewport v = {.tag = tag};
	if (tag == GHOSTTY_SCROLL_VIEWPORT_DELTA) v.value.delta = delta;
	if (tag == GHOSTTY_SCROLL_VIEWPORT_ROW) v.value.row = (size_t)delta;
	ghostty_terminal_scroll_viewport(s->t, v);
}

static GhosttyPoint screen_point(u32 x, u32 y) {
	GhosttyPoint p = {.tag = GHOSTTY_POINT_TAG_SCREEN};
	p.value.coordinate.x = x;
	p.value.coordinate.y = y;
	return p;
}

// oriel_text writes the screen from (x0, y0) to (x1, y1) inclusive as plain text; rows count from the top of the
// scrollback.
u32 oriel_text(Term *s, u32 x0, u32 y0, u32 x1, u32 y1) {
	s->out_len = 0;
	GhosttySelection sel = GHOSTTY_INIT_SIZED(GhosttySelection);
	sel.start.size = sizeof(GhosttyGridRef);
	sel.end.size = sizeof(GhosttyGridRef);
	if (ghostty_terminal_grid_ref(s->t, screen_point(x0, y0), &sel.start) ||
	    ghostty_terminal_grid_ref(s->t, screen_point(x1, y1), &sel.end))
		return 0;
	GhosttyFormatterTerminalOptions o = GHOSTTY_INIT_SIZED(GhosttyFormatterTerminalOptions);
	o.emit = GHOSTTY_FORMATTER_FORMAT_PLAIN;
	o.unwrap = true;
	o.trim = true;
	o.extra.size = sizeof(GhosttyFormatterTerminalExtra);
	o.extra.screen.size = sizeof(GhosttyFormatterScreenExtra);
	o.selection = &sel;
	GhosttyFormatter f = 0;
	if (ghostty_formatter_terminal_new(0, &f, s->t, o)) return 0;
	uint8_t *p = 0;
	size_t n = 0;
	if (ghostty_formatter_format_alloc(f, 0, &p, &n) == GHOSTTY_SUCCESS) {
		put(s, p, n);
		ghostty_free(0, p, n);
	}
	ghostty_formatter_free(f);
	return s->out_len;
}

// oriel_mark tracks a cell as the terminal scrolls: the screen point (x, y), or the cursor's when y < 0.
GhosttyTrackedGridRef oriel_mark(Term *s, u32 x, int y) {
	GhosttyPoint p = screen_point(x, y);
	if (y < 0) {
		u16 cx = 0, cy = 0;
		ghostty_terminal_get(s->t, GHOSTTY_TERMINAL_DATA_CURSOR_X, &cx);
		ghostty_terminal_get(s->t, GHOSTTY_TERMINAL_DATA_CURSOR_Y, &cy);
		p.tag = GHOSTTY_POINT_TAG_ACTIVE;
		p.value.coordinate.x = cx;
		p.value.coordinate.y = cy;
	}
	GhosttyTrackedGridRef r = 0;
	if (ghostty_terminal_grid_ref_track(s->t, p, &r)) return 0;
	return r;
}

// oriel_mark_point writes the mark's screen point as two u32s, or returns 0 once it has been discarded.
u32 oriel_mark_point(Term *s, GhosttyTrackedGridRef r) {
	GhosttyPointCoordinate c = {0};
	s->out_len = 0;
	if (ghostty_tracked_grid_ref_point(r, GHOSTTY_POINT_TAG_SCREEN, &c)) return 0;
	u32 v[] = {c.x, c.y};
	put(s, v, sizeof v);
	return s->out_len;
}

void oriel_mark_free(GhosttyTrackedGridRef r) { ghostty_tracked_grid_ref_free(r); }

// oriel_key encodes a key event for the terminal's current modes into out. in holds the event's UTF-8 text.
u32 oriel_key(Term *s, u32 key, u32 mods, u32 action, u32 textlen, u32 unshifted) {
	ghostty_key_encoder_setopt_from_terminal(s->enc, s->t);
	ghostty_key_event_set_key(s->key, key);
	ghostty_key_event_set_mods(s->key, mods);
	ghostty_key_event_set_action(s->key, action);
	ghostty_key_event_set_utf8(s->key, (const char *)s->in, textlen);
	ghostty_key_event_set_unshifted_codepoint(s->key, unshifted);
	ghostty_key_event_set_consumed_mods(s->key, 0);
	s->out_len = 0;
	size_t n = 0;
	if (ghostty_key_encoder_encode(s->enc, s->key, 0, 0, &n) != GHOSTTY_OUT_OF_SPACE || !reserve(s, n)) return 0;
	if (ghostty_key_encoder_encode(s->enc, s->key, (char *)s->out, s->out_cap, &n)) return 0;
	s->out_len = n;
	return n;
}

// oriel_paste encodes the n bytes at in as a paste for the terminal's current modes.
u32 oriel_paste(Term *s, u32 n) {
	GhosttyTerminalModeConfig c = {.mode = GHOSTTY_MODE_BRACKETED_PASTE};
	ghostty_terminal_get(s->t, GHOSTTY_TERMINAL_DATA_MODE, &c);
	s->out_len = 0;
	size_t need = 0;
	ghostty_paste_encode((char *)s->in, n, c.value, 0, 0, &need);
	if (!reserve(s, need)) return 0;
	if (ghostty_paste_encode((char *)s->in, n, c.value, (char *)s->out, s->out_cap, &need)) return 0;
	s->out_len = need;
	return need;
}

u32 oriel_snapshot(Term *s) {
	uint8_t *p = 0;
	size_t n = 0;
	s->out_len = 0;
	if (ghostty_snapshot_encode_alloc(s->t, 0, &p, &n)) return 0;
	put(s, p, n);
	ghostty_free(0, p, n);
	return s->out_len;
}

// oriel_restore replaces the terminal with one decoded from the n snapshot bytes at s->in.
int oriel_restore(Term *s, u32 n, u32 scrollback_bytes) {
	GhosttySnapshotDecoder d = 0;
	if (ghostty_snapshot_decoder_new_buf(0, &d, s->in, n)) return 0;
	GhosttyTerminal t = 0;
	GhosttyResult r = ghostty_snapshot_decoder_decode(d, &t);
	ghostty_snapshot_decoder_free(d);
	if (r) return 0;
	GhosttyRenderState rs = 0;
	if (ghostty_render_state_new(0, &rs)) {
		ghostty_terminal_free(t);
		return 0;
	}
	ghostty_render_state_free(s->rs);
	ghostty_terminal_free(s->t);
	s->rs = rs;
	s->held = 0;
	attach(s, t, scrollback_bytes);
	GhosttyTerminalModeConfig m = {.mode = GHOSTTY_MODE_GRAPHEME_CLUSTER};
	ghostty_terminal_get(t, GHOSTTY_TERMINAL_DATA_MODE, &m);
	bool now = m.value;
	m.value = true;
	ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_MODE_DEFAULT, &m);
	m.value = now;
	ghostty_terminal_set(t, GHOSTTY_TERMINAL_OPT_MODE, &m);
	return 1;
}
