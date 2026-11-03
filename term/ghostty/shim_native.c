//go:build ghostty_native

// The shim (wasm/shim.c) compiled natively for the ghostty_native build: the C heap stands in for the wasm allocator.
#include <ghostty/vt.h>
#include <stdlib.h>
#define ghostty_wasm_alloc(n) malloc(n)
#define ghostty_wasm_free(p, n) free(p)
#include "wasm/shim.c"
