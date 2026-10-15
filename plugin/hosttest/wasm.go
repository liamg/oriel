// Package hosttest has test helpers for the plugin hosts.
package hosttest

// leb is n as unsigned LEB128.
func leb(n int) []byte {
	var out []byte
	for {
		b := byte(n & 0x7f)
		n >>= 7
		if n != 0 {
			b |= 0x80
		}
		out = append(out, b)
		if n == 0 {
			return out
		}
	}
}

func vec(items ...[]byte) []byte {
	out := leb(len(items))
	for _, it := range items {
		out = append(out, it...)
	}
	return out
}

func name(s string) []byte { return append(leb(len(s)), s...) }

func section(id byte, body []byte) []byte {
	return append(append([]byte{id}, leb(len(body))...), body...)
}

func cat(bs ...[]byte) []byte {
	var out []byte
	for _, b := range bs {
		out = append(out, b...)
	}
	return out
}

// Module is a WASM module made by hand (the binary format, WebAssembly Core Specification §5), exporting:
// add(i32, i32) i32; oriel_alloc, a bump allocator; echo(ptr, len) i64, which answers with its input; ask(ptr,
// len) i64, which passes its input to the host's fs.read and answers with the host's answer; and spin(), which
// never returns.
func Module() []byte {
	const i32, i64 = 0x7f, 0x7e
	fn := func(params, results []byte) []byte {
		return cat([]byte{0x60}, vec(bytesOf(params)...), vec(bytesOf(results)...))
	}
	types := vec(
		fn([]byte{i32, i32}, []byte{i32}),           // 0 add
		fn([]byte{i32}, []byte{i32}),                // 1 alloc
		fn([]byte{i32, i32}, []byte{i64}),           // 2 echo, ask
		fn([]byte{i32, i32, i32, i32}, []byte{i64}), // 3 oriel.request
		fn(nil, nil), // 4 spin
	)
	imports := vec(cat(name("oriel"), name("request"), []byte{0x00, 3}))
	funcs := vec([]byte{0}, []byte{1}, []byte{2}, []byte{2}, []byte{4})
	memory := vec([]byte{0x00, 1})
	globals := vec([]byte{i32, 0x01, 0x41, 0x80, 0x08, 0x0b}) // mut i32 = 1024
	exports := vec(cat(name("memory"), []byte{0x02, 0}), cat(name("add"), []byte{0x00, 1}), cat(name("oriel_alloc"), []byte{0x00, 2}),
		cat(name("echo"), []byte{0x00, 3}), cat(name("ask"), []byte{0x00, 4}), cat(name("spin"), []byte{0x00, 5}))
	body := func(code ...byte) []byte {
		b := append([]byte{0x00}, code...) // no locals
		return append(leb(len(b)), b...)
	}
	code := vec(
		body(0x20, 0, 0x20, 1, 0x6a, 0x0b),                             // a + b
		body(0x23, 0, 0x23, 0, 0x20, 0, 0x6a, 0x24, 0, 0x0b),           // heap, then heap += n
		body(0x20, 0, 0xad, 0x42, 32, 0x86, 0x20, 1, 0xad, 0x84, 0x0b), // ptr << 32 | len
		body(0x41, 0, 0x41, 7, 0x20, 0, 0x20, 1, 0x10, 0, 0x0b),        // request("fs.read", input)
		body(0x03, 0x40, 0x0c, 0, 0x0b, 0x0b),                          // loop br 0
	)
	data := vec(cat([]byte{0x00, 0x41, 0x00, 0x0b}, name("fs.read")))
	return cat([]byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}, section(1, types), section(2, imports), section(3, funcs),
		section(5, memory), section(6, globals), section(7, exports), section(10, code), section(11, data))
}

func bytesOf(b []byte) [][]byte {
	var out [][]byte
	for _, x := range b {
		out = append(out, []byte{x})
	}
	return out
}
