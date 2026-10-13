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

// query_matches runs a query over a byte range of a tree and returns its matches as uint32s: pattern index,
// capture count, then for each capture: capture index, start byte, end byte, start row, start column, end row,
// end column, and the symbol of the node's parent (0xffffffff for none). The count of uint32s goes to *len.
EXPORT(query_matches) const uint32_t *ts_shim_query_matches(TSQuery *q, TSTree *t, uint32_t start, uint32_t end, uint32_t *len) {
  if (!cursor) cursor = ts_query_cursor_new();
  ts_query_cursor_set_byte_range(cursor, start, end);
  ts_query_cursor_exec(cursor, q, ts_tree_root_node(t));
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
      TSNode parent = ts_node_parent(node);
      out[n++] = m.captures[i].index;
      out[n++] = ts_node_start_byte(node);
      out[n++] = ts_node_end_byte(node);
      out[n++] = s.row;
      out[n++] = s.column;
      out[n++] = e.row;
      out[n++] = e.column;
      out[n++] = ts_node_is_null(parent) ? 0xffffffff : ts_node_symbol(parent);
    }
  }
  *len = n;
  return out;
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
