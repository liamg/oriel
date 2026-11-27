package imagex

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/draw"
	"image/gif"
	"time"

	"golang.org/x/image/webp"
)

// Animation is an animated GIF or WebP, each frame composed onto the canvas as it's shown.
type Animation struct {
	Frames []*image.RGBA
	Delays []time.Duration // how long each frame shows
	Loops  int             // how many times it plays; 0 for forever
}

const (
	// minDelay is the shortest a frame shows: a delay at or under brief is drawn at minDelay, as browsers do.
	minDelay, brief = 100 * time.Millisecond, 10 * time.Millisecond
	// maxAnimBytes is the most an animation's composed frames may take; a bigger one is shown still.
	maxAnimBytes = 256 << 20
)

// ErrTooBig is an animation whose frames would take more than maxAnimBytes.
var ErrTooBig = errors.New("animation too big to play")

// Animate decodes an animated GIF or WebP; nil for anything else, or one with a single frame.
func Animate(b []byte) (*Animation, error) {
	switch {
	case bytes.HasPrefix(b, []byte("GIF8")):
		return animateGIF(b)
	case isWebP(b):
		return animateWebP(b)
	}
	return nil, nil
}

// Bytes is what its frames take; 0 for none.
func (a *Animation) Bytes() int64 {
	if a == nil {
		return 0
	}
	var n int64
	for _, f := range a.Frames {
		n += int64(len(f.Pix))
	}
	return n
}

// At is the frame showing elapsed into the animation, and how long until the next; 0 once it has stopped.
func (a *Animation) At(elapsed time.Duration) (image.Image, time.Duration) {
	var total time.Duration
	for _, d := range a.Delays {
		total += d
	}
	if total <= 0 {
		return a.Frames[0], 0
	}
	if a.Loops > 0 && elapsed >= total*time.Duration(a.Loops) {
		return a.Frames[len(a.Frames)-1], 0
	}
	t := elapsed % total
	for i, d := range a.Delays {
		if t < d {
			return a.Frames[i], d - t
		}
		t -= d
	}
	return a.Frames[len(a.Frames)-1], 0
}

func delay(d time.Duration) time.Duration {
	if d <= brief {
		return minDelay
	}
	return d
}

// canvas checks an animation's size against maxAnimBytes before it's composed.
func canvas(w, h, frames int) (*image.RGBA, error) {
	if w <= 0 || h <= 0 || int64(w)*int64(h)*4*int64(frames) > maxAnimBytes {
		return nil, ErrTooBig
	}
	return image.NewRGBA(image.Rect(0, 0, w, h)), nil
}

func snapshot(c *image.RGBA) *image.RGBA {
	f := image.NewRGBA(c.Rect)
	copy(f.Pix, c.Pix)
	return f
}

// animateGIF composes a GIF's frames by their disposal methods: left in place, cleared, or restored to what was under.
func animateGIF(b []byte) (*Animation, error) {
	g, err := gif.DecodeAll(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	if len(g.Image) < 2 {
		return nil, nil
	}
	cv, err := canvas(g.Config.Width, g.Config.Height, len(g.Image))
	if err != nil {
		return nil, err
	}
	a := &Animation{}
	switch {
	case g.LoopCount == 0:
	case g.LoopCount < 0:
		a.Loops = 1
	default:
		a.Loops = g.LoopCount + 1
	}
	for i, fr := range g.Image {
		var disposal byte
		if i < len(g.Disposal) {
			disposal = g.Disposal[i]
		}
		var under *image.RGBA
		if disposal == gif.DisposalPrevious {
			under = snapshot(cv)
		}
		draw.Draw(cv, fr.Rect, fr, fr.Rect.Min, draw.Over)
		a.Frames = append(a.Frames, snapshot(cv))
		a.Delays = append(a.Delays, delay(time.Duration(g.Delay[i])*10*time.Millisecond))
		switch disposal {
		case gif.DisposalBackground:
			draw.Draw(cv, fr.Rect, image.Transparent, image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			cv = under
		}
	}
	return a, nil
}

type riffChunk struct {
	id   string
	data []byte
}

// chunks are a RIFF payload's chunks, each padded to an even length.
func chunks(b []byte) ([]riffChunk, error) {
	var out []riffChunk
	for len(b) >= 8 {
		n := binary.LittleEndian.Uint32(b[4:8])
		if uint64(n) > uint64(len(b)-8) {
			return nil, errors.New("webp: a chunk runs past the file")
		}
		out = append(out, riffChunk{id: string(b[:4]), data: b[8 : 8+n]})
		b = b[8+n:]
		if n%2 == 1 && len(b) > 0 {
			b = b[1:]
		}
	}
	return out, nil
}

func u24(b []byte) int { return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 }

// animateWebP composes an animated WebP's ANMF frames onto its canvas, each blended or not and disposed as it says.
func animateWebP(b []byte) (*Animation, error) {
	cs, err := chunks(b[12:])
	if err != nil {
		return nil, err
	}
	if len(cs) == 0 || cs[0].id != "VP8X" || len(cs[0].data) < 10 || cs[0].data[0]&0x02 == 0 {
		return nil, nil
	}
	var frames []riffChunk
	a := &Animation{}
	for _, c := range cs[1:] {
		switch c.id {
		case "ANIM":
			if len(c.data) >= 6 {
				a.Loops = int(binary.LittleEndian.Uint16(c.data[4:6]))
			}
		case "ANMF":
			if len(c.data) < 16 {
				return nil, errors.New("webp: a short ANMF chunk")
			}
			frames = append(frames, c)
		}
	}
	if len(frames) < 2 {
		return nil, nil
	}
	cv, err := canvas(1+u24(cs[0].data[4:]), 1+u24(cs[0].data[7:]), len(frames))
	if err != nil {
		return nil, err
	}
	var dispose image.Rectangle
	for _, f := range frames {
		d := f.data
		x, y, w, h := 2*u24(d), 2*u24(d[3:]), 1+u24(d[6:]), 1+u24(d[9:])
		ms, flags := u24(d[12:]), d[15]
		img, err := webpFrame(d[16:], w, h)
		if err != nil {
			return nil, err
		}
		if !dispose.Empty() {
			draw.Draw(cv, dispose, image.Transparent, image.Point{}, draw.Src)
		}
		r := image.Rect(x, y, x+w, y+h)
		op := draw.Over
		if flags&0x02 != 0 {
			op = draw.Src
		}
		draw.Draw(cv, r, img, img.Bounds().Min, op)
		a.Frames = append(a.Frames, snapshot(cv))
		a.Delays = append(a.Delays, delay(time.Duration(ms)*time.Millisecond))
		dispose = image.Rectangle{}
		if flags&0x01 != 0 {
			dispose = r
		}
	}
	return a, nil
}

// webpFrame decodes an ANMF chunk's frame data, an optional ALPH chunk and a VP8 or VP8L one, as a still WebP.
func webpFrame(data []byte, w, h int) (image.Image, error) {
	cs, err := chunks(data)
	if err != nil {
		return nil, err
	}
	var body bytes.Buffer
	chunk := func(id string, b []byte) {
		body.WriteString(id)
		_ = binary.Write(&body, binary.LittleEndian, uint32(len(b)))
		body.Write(b)
		if len(b)%2 == 1 {
			body.WriteByte(0)
		}
	}
	var alpha, bits []byte
	bitsID := ""
	for _, c := range cs {
		switch c.id {
		case "ALPH":
			alpha = c.data
		case "VP8 ", "VP8L":
			bits, bitsID = c.data, c.id
		}
	}
	if bitsID == "" {
		return nil, errors.New("webp: a frame with no image")
	}
	if alpha != nil && bitsID == "VP8 " {
		hdr := make([]byte, 10)
		hdr[0] = 0x10 // alpha
		hdr[4], hdr[5], hdr[6] = byte(w-1), byte((w-1)>>8), byte((w-1)>>16)
		hdr[7], hdr[8], hdr[9] = byte(h-1), byte((h-1)>>8), byte((h-1)>>16)
		chunk("VP8X", hdr)
		chunk("ALPH", alpha)
	}
	chunk(bitsID, bits)
	var file bytes.Buffer
	file.WriteString("RIFF")
	_ = binary.Write(&file, binary.LittleEndian, uint32(4+body.Len()))
	file.WriteString("WEBP")
	file.Write(body.Bytes())
	return webp.Decode(&file)
}
