// The C side of package syntax: a small, flat ABI over the tree-sitter runtime for one grammar, built
// together with it into one WASM module. Pointers are offsets into the module's memory.
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include "tree_sitter/api.h"

#define EXPORT(name) __attribute__((export_name(#name)))

extern const TSLanguage *LANGUAGE_FN(void);

EXPORT(alloc) void *ts_shim_alloc(uint32_t n) { return malloc(n); }
EXPORT(free) void ts_shim_free(void *p) { free(p); }

EXPORT(parser_new) TSParser *ts_shim_parser_new(void) {
  TSParser *p = ts_parser_new();
  ts_parser_set_language(p, LANGUAGE_FN());
  return p;
}

EXPORT(parser_delete) void ts_shim_parser_delete(TSParser *p) { ts_parser_delete(p); }

// set_ranges limits the next parse to n ranges, each six uint32s: start byte, end byte, start row,
// start column, end row, end column. n == 0 parses the whole text again.
EXPORT(set_ranges) int ts_shim_set_ranges(TSParser *p, const uint32_t *r, uint32_t n) {
  TSRange *ranges = malloc(sizeof(TSRange) * (n ? n : 1));
  for (uint32_t i = 0; i < n; i++) {
    const uint32_t *x = r + i * 6;
    ranges[i] = (TSRange){{x[2], x[3]}, {x[4], x[5]}, x[0], x[1]};
  }
  bool ok = ts_parser_set_included_ranges(p, ranges, n);
  free(ranges);
  return ok;
}

EXPORT(parse) TSTree *ts_shim_parse(TSParser *p, TSTree *old, const char *src, uint32_t len) {
  return ts_parser_parse_string(p, old, src, len);
}

EXPORT(tree_delete) void ts_shim_tree_delete(TSTree *t) { ts_tree_delete(t); }

// changed_ranges is where the syntax differs between an edited tree and its reparse, as uint32 pairs of start and
// end bytes; the count of uint32s goes to *len.
static uint32_t *changed;
EXPORT(changed_ranges) const uint32_t *ts_shim_changed_ranges(TSTree *old, TSTree *t, uint32_t *len) {
  uint32_t n;
  TSRange *r = ts_tree_get_changed_ranges(old, t, &n);
  changed = realloc(changed, (n ? n : 1) * 2 * sizeof(uint32_t));
  for (uint32_t i = 0; i < n; i++) {
    changed[2 * i] = r[i].start_byte;
    changed[2 * i + 1] = r[i].end_byte;
  }
  free(r);
  *len = 2 * n;
  return changed;
}

EXPORT(tree_edit) void ts_shim_tree_edit(TSTree *t, const uint32_t *e) {
  TSInputEdit edit = {e[0], e[1], e[2], {e[3], e[4]}, {e[5], e[6]}, {e[7], e[8]}};
  ts_tree_edit(t, &edit);
}

// tree_has_error reports whether the tree holds syntax errors.
EXPORT(tree_has_error) int ts_shim_tree_has_error(TSTree *t) { return ts_node_has_error(ts_tree_root_node(t)); }

// query_new compiles a query; on failure it returns 0 and writes the error offset and type to err[0] and err[1].
EXPORT(query_new) TSQuery *ts_shim_query_new(const char *src, uint32_t len, uint32_t *err) {
  TSQueryError type;
  TSQuery *q = ts_query_new(LANGUAGE_FN(), src, len, &err[0], &type);
  err[1] = type;
  return q;
}

EXPORT(query_delete) void ts_shim_query_delete(TSQuery *q) { ts_query_delete(q); }

// query_pattern_span is where pattern i is in the query's source: its start and end bytes, to span[0] and span[1].
EXPORT(query_pattern_span) void ts_shim_query_pattern_span(TSQuery *q, uint32_t i, uint32_t *span) {
  span[0] = ts_query_start_byte_for_pattern(q, i);
  span[1] = ts_query_end_byte_for_pattern(q, i);
}

EXPORT(query_disable_pattern) void ts_shim_query_disable_pattern(TSQuery *q, uint32_t i) { ts_query_disable_pattern(q, i); }
EXPORT(query_pattern_count) uint32_t ts_shim_query_pattern_count(TSQuery *q) { return ts_query_pattern_count(q); }
EXPORT(query_capture_count) uint32_t ts_shim_query_capture_count(TSQuery *q) { return ts_query_capture_count(q); }
EXPORT(query_string_count) uint32_t ts_shim_query_string_count(TSQuery *q) { return ts_query_string_count(q); }

EXPORT(query_capture_name) const char *ts_shim_query_capture_name(TSQuery *q, uint32_t i, uint32_t *len) {
  return ts_query_capture_name_for_id(q, i, len);
}

EXPORT(query_string) const char *ts_shim_query_string(TSQuery *q, uint32_t i, uint32_t *len) {
  return ts_query_string_value_for_id(q, i, len);
}

// query_predicates returns pattern i's predicate steps as pairs of uint32s: type, value id.
EXPORT(query_predicates) const TSQueryPredicateStep *ts_shim_query_predicates(TSQuery *q, uint32_t i, uint32_t *len) {
  return ts_query_predicates_for_pattern(q, i, len);
}

static TSQueryCursor *cursor;
static uint32_t *out;
static uint32_t out_cap;
static TSNode *kids; // the window's root children
static uint32_t kids_n, kids_cap;

// window limits the cursor to matches within the root's children that touch [start, end), and the one after
// (what follows a doc comment), and keeps those children for parent lookups.
static void window(TSNode root, uint32_t start, uint32_t end) {
  kids_n = 0;
  TSTreeCursor c = ts_tree_cursor_new(root);
  uint32_t lo = 0xffffffff, hi = 0;
  bool after = false;
  if (ts_tree_cursor_goto_first_child(&c)) {
    do {
      TSNode k = ts_tree_cursor_current_node(&c);
      uint32_t ks = ts_node_start_byte(k), ke = ts_node_end_byte(k);
      if (ke <= start && ke != ks) continue;
      if (ks >= end && ks != start) {
        if (after) break;
        after = true;
      }
      if (kids_n == kids_cap) {
        kids_cap = kids_cap * 2 + 32;
        kids = realloc(kids, kids_cap * sizeof(TSNode));
      }
      kids[kids_n++] = k;
      if (ks < lo) lo = ks;
      if (ke > hi) hi = ke;
    } while (ts_tree_cursor_goto_next_sibling(&c));
  }
  ts_tree_cursor_delete(&c);
  if (lo == 0xffffffff) lo = start, hi = end;
  ts_query_cursor_set_containing_byte_range(cursor, lo, hi > lo ? hi : lo + 1);
}

// parent_symbol is a node's parent's symbol, found down from the window's child that holds it rather than from
// the root, which would step over every top-level node before it.
static uint32_t parent_symbol(TSNode root, TSNode node) {
  uint32_t s = ts_node_start_byte(node), e = ts_node_end_byte(node);
  for (uint32_t i = 0; i < kids_n; i++) {
    TSNode k = kids[i];
    if (ts_node_eq(k, node)) return ts_node_symbol(root);
    if (ts_node_start_byte(k) > s || ts_node_end_byte(k) < e) continue;
    for (TSNode cur = k;;) {
      TSNode next = ts_node_child_with_descendant(cur, node);
      if (ts_node_is_null(next)) break;
      if (ts_node_eq(next, node)) return ts_node_symbol(cur);
      cur = next;
    }
  }
  TSNode p = ts_node_parent(node);
  return ts_node_is_null(p) ? 0xffffffff : ts_node_symbol(p);
}

// query_matches runs a query over a byte range of a tree and returns its matches as uint32s: pattern index,
// capture count, then for each capture: capture index, start byte, end byte, start row, start column, end row,
// end column, and the symbol of the node's parent (0xffffffff for none, or when not wanted: only captures with
// want[index] set get theirs). With windowed, the matches are those within the root's children around the
// range (see window); a pattern on the root itself needs windowed off. The count of uint32s goes to *len.
EXPORT(query_matches) const uint32_t *ts_shim_query_matches(TSQuery *q, TSTree *t, uint32_t start, uint32_t end, uint32_t *len, const uint8_t *want, uint32_t windowed) {
  if (!cursor) cursor = ts_query_cursor_new();
  TSNode root = ts_tree_root_node(t);
  ts_query_cursor_set_byte_range(cursor, start, end);
  if (windowed) {
    window(root, start, end);
    ts_query_cursor_set_max_start_depth(cursor, 0xffffffff);
  } else {
    // the patterns on the root itself: none starts below it
    kids_n = 0;
    ts_query_cursor_set_containing_byte_range(cursor, 0, 0);
    ts_query_cursor_set_max_start_depth(cursor, 0);
  }
  ts_query_cursor_exec(cursor, q, root);
  uint32_t n = 0;
  TSQueryMatch m;
  while (ts_query_cursor_next_match(cursor, &m)) {
    uint32_t need = n + 2 + (uint32_t)m.capture_count * 8;
    if (need > out_cap) {
      out_cap = need * 2 + 1024;
      out = realloc(out, out_cap * sizeof(uint32_t));
    }
    out[n++] = m.pattern_index;
    out[n++] = m.capture_count;
    for (uint16_t i = 0; i < m.capture_count; i++) {
      TSNode node = m.captures[i].node;
      TSPoint s = ts_node_start_point(node), e = ts_node_end_point(node);
      out[n++] = m.captures[i].index;
      out[n++] = ts_node_start_byte(node);
      out[n++] = ts_node_end_byte(node);
      out[n++] = s.row;
      out[n++] = s.column;
      out[n++] = e.row;
      out[n++] = e.column;
      out[n++] = want && want[m.captures[i].index] ? parent_symbol(root, node) : 0xffffffff;
    }
  }
  *len = n;
  return out;
}

// root_type is the kind of a tree's root node: source_file, program, document.
EXPORT(root_type) const char *ts_shim_root_type(TSTree *t, uint32_t *len) {
  const char *s = ts_node_type(ts_tree_root_node(t));
  *len = strlen(s);
  return s;
}

// symbol_for_name looks up a node kind, for the host's #has-parent? predicates; 0 when there is none.
EXPORT(symbol_for_name) uint32_t ts_shim_symbol_for_name(const char *name, uint32_t len) {
  return ts_language_symbol_for_name(LANGUAGE_FN(), name, len, true);
}

// node_path is the kinds of the named nodes from the root down to the smallest one at byte off, each followed by a
// NUL, for :Inspect.
static char path_buf[4096];
EXPORT(node_path) const char *ts_shim_node_path(TSTree *t, uint32_t off, uint32_t *len) {
  TSNode n = ts_node_named_descendant_for_byte_range(ts_tree_root_node(t), off, off);
  const char *kinds[256];
  int k = 0;
  for (; !ts_node_is_null(n) && k < 256; n = ts_node_parent(n)) {
    if (ts_node_is_named(n)) kinds[k++] = ts_node_type(n);
  }
  uint32_t w = 0;
  for (int i = k - 1; i >= 0; i--) {
    size_t l = strlen(kinds[i]);
    if (w + l + 1 > sizeof(path_buf)) break;
    memcpy(path_buf + w, kinds[i], l);
    w += l;
    path_buf[w++] = 0;
  }
  *len = w;
  return path_buf;
}
