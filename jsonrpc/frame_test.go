package jsonrpc

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// ref: lsp/specification.md#headerPart
// quote: "Each header field is terminated by '\\r\\n'. Considering the last header field and the overall header itself are each terminated with '\\r\\n', and that at least one header is mandatory, this means that two '\\r\\n' sequences always immediately precede the content part of a message."
func TestWriteFrame(t *testing.T) {
	var b bytes.Buffer
	if err := WriteFrame(&b, []byte(`{"a":"é"}`)); err != nil {
		t.Fatal(err)
	}
	// ref: lsp/specification.md#headerPart
	// quote: "Content-Length    | number      | The length of the content part in bytes. This header is required."
	if got, want := b.String(), "Content-Length: 10\r\n\r\n{\"a\":\"é\"}"; got != want {
		t.Errorf("frame = %q, want %q", got, want)
	}
}

func TestReadFrame(t *testing.T) {
	// ref: lsp/specification.md#contentPart
	// quote: "For backwards compatibility it is highly recommended that a client and a server treat the string `utf8` as `utf-8`."
	in := "Content-Length: 2\r\nContent-Type: application/vscode-jsonrpc; charset=utf8\r\n\r\n{}" +
		"content-length: 4\r\n\r\nnull"
	r := bufio.NewReader(strings.NewReader(in))
	for _, want := range []string{"{}", "null"} {
		got, err := ReadFrame(r)
		if err != nil || string(got) != want {
			t.Fatalf("ReadFrame = %q, %v; want %q", got, err, want)
		}
	}
	if _, err := ReadFrame(r); err != io.EOF {
		t.Errorf("at the end ReadFrame err = %v, want EOF", err)
	}
}

func TestReadFrameErrors(t *testing.T) {
	for _, in := range []string{
		"Content-Type: x\r\n\r\n{}", // no length
		"Content-Length: x\r\n\r\n{}",
		"Content-Length: 2\r\nContent-Type: application/vscode-jsonrpc; charset=latin1\r\n\r\n{}",
		"Content-Length: 9\r\n\r\n{}",
	} {
		if _, err := ReadFrame(bufio.NewReader(strings.NewReader(in))); err == nil {
			t.Errorf("ReadFrame(%q) succeeded", in)
		}
	}
}

// A length past maxFrame is refused before anything is allocated for it.
func TestReadFrameTooLarge(t *testing.T) {
	for _, n := range []string{fmt.Sprint(maxFrame + 1), "9223372036854775807"} {
		in := "Content-Length: " + n + "\r\n\r\n{}"
		if _, err := ReadFrame(bufio.NewReader(strings.NewReader(in))); !errors.Is(err, ErrFrameTooLarge) {
			t.Errorf("Content-Length %s: err = %v, want ErrFrameTooLarge", n, err)
		}
	}
}

func TestReadLine(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("{\"a\":1}\r\n\n  \n{\"b\":2}"))
	for _, want := range []string{`{"a":1}`, `{"b":2}`} {
		got, err := ReadLine(r)
		if err != nil || string(got) != want {
			t.Fatalf("ReadLine = %q, %v; want %q", got, err, want)
		}
	}
	if _, err := ReadLine(r); err != io.EOF {
		t.Errorf("at the end ReadLine err = %v, want EOF", err)
	}
}

// endless is an endless line.
type endless struct{}

func (endless) Read(b []byte) (int, error) {
	for i := range b {
		b[i] = 'x'
	}
	return len(b), nil
}

// A line past maxFrame is refused rather than read without end.
func TestReadLineTooLarge(t *testing.T) {
	if _, err := ReadLine(bufio.NewReader(endless{})); !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("err = %v, want ErrFrameTooLarge", err)
	}
}
