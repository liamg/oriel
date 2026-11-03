package svg

import (
	"strconv"
	"strings"

	"github.com/liamg/oriel/render"
)

// named are the CSS colour keywords badges and icons use.
var named = map[string]uint32{
	"black": 0x000000, "white": 0xffffff, "red": 0xff0000, "green": 0x008000, "blue": 0x0000ff, "yellow": 0xffff00,
	"orange": 0xffa500, "purple": 0x800080, "gray": 0x808080, "grey": 0x808080, "silver": 0xc0c0c0, "maroon": 0x800000,
	"olive": 0x808000, "lime": 0x00ff00, "aqua": 0x00ffff, "cyan": 0x00ffff, "teal": 0x008080, "navy": 0x000080,
	"fuchsia": 0xff00ff, "magenta": 0xff00ff, "pink": 0xffc0cb, "brown": 0xa52a2a, "gold": 0xffd700, "indigo": 0x4b0082,
	"violet": 0xee82ee, "crimson": 0xdc143c, "coral": 0xff7f50, "salmon": 0xfa8072, "tomato": 0xff6347,
	"orangered": 0xff4500, "darkorange": 0xff8c00, "khaki": 0xf0e68c, "beige": 0xf5f5dc, "ivory": 0xfffff0,
	"lightgray": 0xd3d3d3, "lightgrey": 0xd3d3d3, "darkgray": 0xa9a9a9, "darkgrey": 0xa9a9a9, "dimgray": 0x696969,
	"dimgrey": 0x696969, "gainsboro": 0xdcdcdc, "whitesmoke": 0xf5f5f5, "slategray": 0x708090, "slategrey": 0x708090,
	"lightblue": 0xadd8e6, "skyblue": 0x87ceeb, "steelblue": 0x4682b4, "royalblue": 0x4169e1, "dodgerblue": 0x1e90ff,
	"deepskyblue": 0x00bfff, "darkblue": 0x00008b, "midnightblue": 0x191970, "cornflowerblue": 0x6495ed,
	"lightgreen": 0x90ee90, "darkgreen": 0x006400, "forestgreen": 0x228b22, "seagreen": 0x2e8b57, "limegreen": 0x32cd32,
	"yellowgreen": 0x9acd32, "darkred": 0x8b0000, "firebrick": 0xb22222, "hotpink": 0xff69b4, "deeppink": 0xff1493,
	"tan": 0xd2b48c, "chocolate": 0xd2691e, "sienna": 0xa0522d, "turquoise": 0x40e0d0, "lavender": 0xe6e6fa,
	"plum": 0xdda0dd, "orchid": 0xda70d6, "darkviolet": 0x9400d3, "rebeccapurple": 0x663399,
}

// parseColor reads #rgb, #rgba, #rrggbb, #rrggbbaa, rgb()/rgba() or a colour keyword; currentColor is black.
func parseColor(s string) (render.Color, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if v, ok := named[s]; ok {
		return render.Color{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}, true
	}
	switch {
	case s == "currentcolor":
		return render.Color{A: 255}, true
	case strings.HasPrefix(s, "#"):
		h := s[1:]
		if len(h) == 3 || len(h) == 4 {
			var b strings.Builder
			for _, c := range h {
				b.WriteRune(c)
				b.WriteRune(c)
			}
			h = b.String()
		}
		if len(h) == 6 {
			h += "ff"
		}
		v, err := strconv.ParseUint(h, 16, 32)
		if len(h) != 8 || err != nil {
			return render.Color{}, false
		}
		return render.Color{R: uint8(v >> 24), G: uint8(v >> 16), B: uint8(v >> 8), A: uint8(v)}, true
	case strings.HasPrefix(s, "rgb"):
		open, end := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
		if open < 0 || end < open {
			return render.Color{}, false
		}
		parts := strings.FieldsFunc(s[open+1:end], func(r rune) bool { return r == ',' || r == ' ' || r == '/' })
		if len(parts) < 3 {
			return render.Color{}, false
		}
		var ch [3]uint8
		for i := range ch {
			p := parts[i]
			if v, ok := strings.CutSuffix(p, "%"); ok {
				f, _ := strconv.ParseFloat(v, 64)
				ch[i] = uint8(min(max(f, 0), 100) * 255 / 100)
				continue
			}
			f, _ := strconv.ParseFloat(p, 64)
			ch[i] = uint8(min(max(f, 0), 255))
		}
		c := render.Color{R: ch[0], G: ch[1], B: ch[2], A: 255}
		if len(parts) > 3 {
			if a, ok := fraction(parts[3]); ok {
				c.A = uint8(a * 255)
			}
		}
		return c, true
	}
	return render.Color{}, false
}
