package nvim

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"slices"
)

// Ext is a msgpack plugin value; nvim uses them for Buffer, Window and Tabpage handles.
type Ext struct {
	Type int8
	Data []byte
}

// encode writes v as msgpack. It supports nil, bool, ints, float64, string, []byte, []any,
// []string, map[string]any and Ext.
func encode(w *bufio.Writer, v any) error {
	switch v := v.(type) {
	case nil:
		return w.WriteByte(0xc0)
	case bool:
		if v {
			return w.WriteByte(0xc3)
		}
		return w.WriteByte(0xc2)
	case int:
		return encodeInt(w, int64(v))
	case int64:
		return encodeInt(w, v)
	case uint64:
		if v > math.MaxInt64 {
			w.WriteByte(0xcf)
			return binary.Write(w, binary.BigEndian, v)
		}
		return encodeInt(w, int64(v))
	case float64:
		w.WriteByte(0xcb)
		return binary.Write(w, binary.BigEndian, math.Float64bits(v))
	case string:
		writeHeader(w, len(v), 0xa0, 31, 0xd9, 0xda, 0xdb)
		_, err := w.WriteString(v)
		return err
	case []byte:
		writeHeader(w, len(v), 0, -1, 0xc4, 0xc5, 0xc6)
		_, err := w.Write(v)
		return err
	case []string:
		writeHeader(w, len(v), 0x90, 15, 0, 0xdc, 0xdd)
		for _, e := range v {
			if err := encode(w, e); err != nil {
				return err
			}
		}
		return nil
	case []any:
		writeHeader(w, len(v), 0x90, 15, 0, 0xdc, 0xdd)
		for _, e := range v {
			if err := encode(w, e); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		writeHeader(w, len(v), 0x80, 15, 0, 0xde, 0xdf)
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			encode(w, k)
			if err := encode(w, v[k]); err != nil {
				return err
			}
		}
		return nil
	case Ext:
		writeHeader(w, len(v.Data), 0, -1, 0xc7, 0xc8, 0xc9)
		w.WriteByte(byte(v.Type))
		_, err := w.Write(v.Data)
		return err
	}
	return fmt.Errorf("msgpack: cannot encode %T", v)
}

func encodeInt(w *bufio.Writer, n int64) error {
	switch {
	case n >= 0 && n <= 127:
		return w.WriteByte(byte(n))
	case n < 0 && n >= -32:
		return w.WriteByte(byte(int8(n)))
	case n >= math.MinInt8 && n <= math.MaxInt8:
		w.WriteByte(0xd0)
		return w.WriteByte(byte(int8(n)))
	case n >= math.MinInt16 && n <= math.MaxInt16:
		w.WriteByte(0xd1)
		return binary.Write(w, binary.BigEndian, int16(n))
	case n >= math.MinInt32 && n <= math.MaxInt32:
		w.WriteByte(0xd2)
		return binary.Write(w, binary.BigEndian, int32(n))
	}
	w.WriteByte(0xd3)
	return binary.Write(w, binary.BigEndian, n)
}

// writeHeader writes a length header: fix form below fixMax (fixMax < 0: none), then 8, 16 and 32-bit forms.
// A zero code skips that width.
func writeHeader(w *bufio.Writer, n int, fix byte, fixMax int, c8, c16, c32 byte) {
	switch {
	case n <= fixMax:
		w.WriteByte(fix | byte(n))
	case c8 != 0 && n <= math.MaxUint8:
		w.WriteByte(c8)
		w.WriteByte(byte(n))
	case n <= math.MaxUint16:
		w.WriteByte(c16)
		binary.Write(w, binary.BigEndian, uint16(n))
	default:
		w.WriteByte(c32)
		binary.Write(w, binary.BigEndian, uint32(n))
	}
}

// decode reads one msgpack value. Integers decode as int64 (uint64 above MaxInt64), maps as
// map[string]any when every key is a string and map[any]any otherwise.
func decode(r *bufio.Reader) (any, error) {
	b, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	switch {
	case b <= 0x7f:
		return int64(b), nil
	case b >= 0xe0:
		return int64(int8(b)), nil
	case b&0xe0 == 0xa0:
		return readStr(r, int(b&0x1f))
	case b&0xf0 == 0x90:
		return readArray(r, int(b&0x0f))
	case b&0xf0 == 0x80:
		return readMap(r, int(b&0x0f))
	}
	switch b {
	case 0xc0:
		return nil, nil
	case 0xc2:
		return false, nil
	case 0xc3:
		return true, nil
	case 0xc4, 0xc5, 0xc6:
		n, err := readLen(r, b-0xc4)
		if err != nil {
			return nil, err
		}
		buf := make([]byte, n)
		_, err = io.ReadFull(r, buf)
		return buf, err
	case 0xc7, 0xc8, 0xc9:
		n, err := readLen(r, b-0xc7)
		if err != nil {
			return nil, err
		}
		return readExt(r, n)
	case 0xca:
		var f uint32
		err := binary.Read(r, binary.BigEndian, &f)
		return float64(math.Float32frombits(f)), err
	case 0xcb:
		var f uint64
		err := binary.Read(r, binary.BigEndian, &f)
		return math.Float64frombits(f), err
	case 0xcc, 0xcd, 0xce, 0xcf:
		n, err := readUint(r, 1<<(b-0xcc))
		if err != nil || n > math.MaxInt64 {
			return n, err
		}
		return int64(n), nil
	case 0xd0, 0xd1, 0xd2, 0xd3:
		size := 1 << (b - 0xd0)
		n, err := readUint(r, size)
		shift := 64 - 8*size
		return int64(n<<shift) >> shift, err
	case 0xd4, 0xd5, 0xd6, 0xd7, 0xd8:
		return readExt(r, 1<<(b-0xd4))
	case 0xd9, 0xda, 0xdb:
		n, err := readLen(r, b-0xd9)
		if err != nil {
			return nil, err
		}
		return readStr(r, n)
	case 0xdc, 0xdd:
		n, err := readLen(r, b-0xdc+1)
		if err != nil {
			return nil, err
		}
		return readArray(r, n)
	case 0xde, 0xdf:
		n, err := readLen(r, b-0xde+1)
		if err != nil {
			return nil, err
		}
		return readMap(r, n)
	}
	return nil, fmt.Errorf("msgpack: unknown type byte 0x%02x", b)
}

// readLen reads a big-endian length of 1, 2 or 4 bytes (width 0, 1 or 2).
func readLen(r *bufio.Reader, width byte) (int, error) {
	n, err := readUint(r, 1<<width)
	return int(n), err
}

func readUint(r *bufio.Reader, size int) (uint64, error) {
	var buf [8]byte
	if _, err := io.ReadFull(r, buf[:size]); err != nil {
		return 0, err
	}
	var n uint64
	for _, c := range buf[:size] {
		n = n<<8 | uint64(c)
	}
	return n, nil
}

func readStr(r *bufio.Reader, n int) (string, error) {
	buf := make([]byte, n)
	_, err := io.ReadFull(r, buf)
	return string(buf), err
}

func readExt(r *bufio.Reader, n int) (Ext, error) {
	t, err := r.ReadByte()
	if err != nil {
		return Ext{}, err
	}
	data := make([]byte, n)
	_, err = io.ReadFull(r, data)
	return Ext{Type: int8(t), Data: data}, err
}

func readArray(r *bufio.Reader, n int) ([]any, error) {
	out := make([]any, n)
	for i := range out {
		v, err := decode(r)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func readMap(r *bufio.Reader, n int) (any, error) {
	keys := make([]any, n)
	vals := make([]any, n)
	allStrings := true
	for i := range n {
		k, err := decode(r)
		if err != nil {
			return nil, err
		}
		v, err := decode(r)
		if err != nil {
			return nil, err
		}
		if _, ok := k.(string); !ok {
			allStrings = false
		}
		keys[i], vals[i] = k, v
	}
	if allStrings {
		m := make(map[string]any, n)
		for i, k := range keys {
			m[k.(string)] = vals[i]
		}
		return m, nil
	}
	m := make(map[any]any, n)
	for i, k := range keys {
		m[k] = vals[i]
	}
	return m, nil
}
