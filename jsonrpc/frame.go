package jsonrpc

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Framing is how messages are cut from a byte stream and written to one.
type Framing struct {
	Read  func(r *bufio.Reader) ([]byte, error) // the next message's content; io.EOF at a clean end
	Write func(w io.Writer, body []byte) error
}

var (
	// Headers is LSP's and DAP's framing: a Content-Length header, a blank line, then the content.
	Headers = Framing{Read: ReadFrame, Write: WriteFrame}
	// Lines is one message a line: MCP's stdio transport, Codex's app-server and Oriel's plugins.
	Lines = Framing{Read: ReadLine, Write: WriteLine}
)

// maxFrame is the largest message content either framing accepts.
const maxFrame = 64 << 20

// ErrFrameTooLarge is a message past maxFrame.
var ErrFrameTooLarge = errors.New("jsonrpc: message too large")

// WriteFrame writes body with its header.
func WriteFrame(w io.Writer, body []byte) error {
	_, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n%s", len(body), body)
	return err
}

// ReadFrame reads one message's content.
func ReadFrame(r *bufio.Reader) ([]byte, error) {
	length := -1
	for first := true; ; first = false {
		line, err := r.ReadString('\n')
		if err != nil {
			if first && line == "" && err == io.EOF {
				return nil, io.EOF
			}
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("jsonrpc: bad header %q", line)
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "content-length":
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("jsonrpc: bad Content-Length %q", value)
			}
			if n > maxFrame {
				return nil, fmt.Errorf("%w: Content-Length %d", ErrFrameTooLarge, n)
			}
			length = n
		case "content-type":
			if err := checkCharset(value); err != nil {
				return nil, err
			}
		}
	}
	if length < 0 {
		return nil, errors.New("jsonrpc: no Content-Length")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return body, nil
}

func checkCharset(ct string) error {
	for _, p := range strings.Split(ct, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
		if ok && strings.EqualFold(k, "charset") {
			if v = strings.ToLower(strings.Trim(v, `"`)); v != "utf-8" && v != "utf8" {
				return fmt.Errorf("jsonrpc: unsupported charset %q", v)
			}
		}
	}
	return nil
}

// WriteLine writes body and a newline.
func WriteLine(w io.Writer, body []byte) error {
	b := make([]byte, len(body)+1)
	copy(b, body)
	b[len(body)] = '\n'
	_, err := w.Write(b)
	return err
}

// ReadLine reads the next line that isn't blank, without its surrounding space. The last line needn't end in a
// newline.
func ReadLine(r *bufio.Reader) ([]byte, error) {
	for {
		var line []byte
		var err error
		for {
			var part []byte
			part, err = r.ReadSlice('\n')
			if len(line)+len(part) > maxFrame+2 {
				return nil, fmt.Errorf("%w: a line past %d bytes", ErrFrameTooLarge, maxFrame)
			}
			line = append(line, part...)
			if err != bufio.ErrBufferFull {
				break
			}
		}
		if line = bytes.TrimSpace(line); len(line) > 0 {
			return line, nil
		}
		if err != nil {
			return nil, err
		}
	}
}
