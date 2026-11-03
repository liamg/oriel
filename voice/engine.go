package voice

import (
	"context"
	"encoding/binary"
	"errors"
)

// engine is a model loaded into whisper.cpp in this process.
type engine interface {
	transcribe(ctx context.Context, samples []float32, prompt string) (string, error)
	close()
}

// loadEngine loads a model into the whisper.cpp built into Oriel (the whisper_native tag); nil when there's none, or
// this CPU can't run the one built in.
var loadEngine func(path string) (engine, error)

// Native reports whether whisper.cpp is built into this binary and can run here.
func Native() bool { return loadEngine != nil }

// decodeWAV is a 16-bit PCM WAV's first channel as samples in [-1, 1].
func decodeWAV(b []byte) ([]float32, error) {
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, errors.New("not a WAV file")
	}
	channels, bits := 0, 0
	for p := 12; p+8 <= len(b); {
		id, n := string(b[p:p+4]), int(binary.LittleEndian.Uint32(b[p+4:]))
		body := b[p+8 : min(len(b), p+8+n)]
		switch id {
		case "fmt ":
			if len(body) < 16 || binary.LittleEndian.Uint16(body) != 1 {
				return nil, errors.New("the WAV isn't PCM")
			}
			channels, bits = int(binary.LittleEndian.Uint16(body[2:])), int(binary.LittleEndian.Uint16(body[14:]))
		case "data":
			if bits != 16 || channels < 1 {
				return nil, errors.New("the WAV isn't 16-bit PCM")
			}
			step := 2 * channels
			out := make([]float32, len(body)/step)
			for i := range out {
				out[i] = float32(int16(binary.LittleEndian.Uint16(body[i*step:]))) / 32768
			}
			return out, nil
		}
		p += 8 + n + n%2
	}
	return nil, errors.New("the WAV has no audio")
}
