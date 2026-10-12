package scenes

import (
	"image/color"

	"github.com/liamg/oriel/render/typeset"
)

func hex(v uint32) color.NRGBA {
	return color.NRGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 0xff}
}

func rgba(v uint32, a float32) color.NRGBA {
	c := hex(v)
	c.A = uint8(a*255 + 0.5)
	return c
}

func alpha(c color.NRGBA, a float32) color.NRGBA {
	c.A = uint8(float32(c.A)*a + 0.5)
	return c
}

// The default theme's tokens, as in the mockups' base.css.
var (
	bg0, bg1, bg2, bg3, bg4 = hex(0x060606), hex(0x0c0c0b), hex(0x121210), hex(0x191815), hex(0x23211c)
	line, line2             = hex(0x24221d), hex(0x332f27)
	fg, fg2, fg3, fg4       = hex(0xece6d8), hex(0xb3ab99), hex(0x7c7566), hex(0x4f4a40)

	accent   = hex(0xffd23f)
	accentBg = rgba(0xffd23f, .08)
	brand2   = hex(0xff8a1f)
	green    = hex(0x4fe0a0)
	warn     = hex(0xff9b45)
	red      = hex(0xff5f6d)
	working  = hex(0x4cc9f0)
	violet   = hex(0xa98bff)
	pink     = hex(0xff6fae)
	ciBlue   = hex(0x5c8dff)
	synK     = hex(0xc792ff)
	synS     = hex(0xa5e075)
	synN     = hex(0xff9e64)
	synF     = hex(0x5cd2ff)
	synT     = hex(0xffcf5c)
	synC     = hex(0x5c6b7e)
	synP     = hex(0x89ddff)
	termFg   = hex(0xc8d2dc)
	termBg   = hex(0x05070a)
	ink      = hex(0x141006)
)

func mono(size float32, w typeset.Weight, c color.NRGBA) TextStyle {
	return TextStyle{Face: Mono, Weight: w, Size: size, Color: c}
}

func ui(size float32, w typeset.Weight, c color.NRGBA) TextStyle {
	return TextStyle{Face: UI, Weight: w, Size: size, Color: c}
}

// capStyle is the mono micro-label: 600 9.5px, .14em, uppercase.
var capStyle = TextStyle{Face: Mono, Weight: typeset.SemiBold, Size: 9.5, Color: fg3, Spacing: .14}
