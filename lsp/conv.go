package lsp

import (
	"net/url"
	"path/filepath"
	"unicode/utf8"

	"github.com/liamg/oriel/lsp/protocol"
	"github.com/liamg/oriel/text"
)

// URI is a file path as a file URI.
func URI(path string) protocol.DocumentURI {
	return protocol.DocumentURI((&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String())
}

// Path is a file URI's path; any other URI comes back as it is.
func Path(uri protocol.DocumentURI) string {
	u, err := url.Parse(string(uri))
	if err != nil || u.Scheme != "file" {
		return string(uri)
	}
	return filepath.FromSlash(u.Path)
}

// toProto converts a byte position to the encoding's. A position past the end of its line or the text (one taken
// before an edit) is clamped to it.
func toProto(s text.Snapshot, enc string, p text.Pos) protocol.Position {
	if p.Line >= s.Lines() {
		p.Line = s.Lines() - 1
		p.Col = len(s.Line(p.Line))
	}
	p.Line = max(p.Line, 0)
	p.Col = min(max(p.Col, 0), s.LineEnd(p.Line)-s.LineStart(p.Line))
	switch enc {
	case protocol.UTF8:
	case protocol.UTF32:
		p.Col = utf8.RuneCount(s.Line(p.Line)[:p.Col])
	default:
		p.Col = s.UTF16(p)
	}
	return protocol.Position{Line: uint32(p.Line), Character: uint32(p.Col)}
}

func toProtoRange(s text.Snapshot, enc string, r Range) protocol.Range {
	return protocol.Range{Start: toProto(s, enc, r.Start), End: toProto(s, enc, r.End)}
}

// fromProto converts a position in the encoding to bytes. A position past the end of a line or the text is
// clamped to it, and one inside a rune moves to its start.
func fromProto(s text.Snapshot, enc string, p protocol.Position) text.Pos {
	line := int(p.Line)
	if line >= s.Lines() {
		last := s.Lines() - 1
		return text.Pos{Line: last, Col: s.LineEnd(last) - s.LineStart(last)}
	}
	col := int(p.Character)
	switch enc {
	case protocol.UTF8:
		b := s.Line(line)
		col = min(col, len(b))
		for col > 0 && col < len(b) && !utf8.RuneStart(b[col]) {
			col--
		}
		return text.Pos{Line: line, Col: col}
	case protocol.UTF32:
		b := s.Line(line)
		off := 0
		for n := 0; n < col && off < len(b); n++ {
			_, w := utf8.DecodeRune(b[off:])
			off += w
		}
		return text.Pos{Line: line, Col: off}
	}
	return s.PosFromUTF16(line, col)
}

// rawPos is a protocol position taken as bytes, for a file whose text can't be read.
func rawPos(p protocol.Position) text.Pos { return text.Pos{Line: int(p.Line), Col: int(p.Character)} }
