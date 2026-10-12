package theme

import (
	"fmt"
	"slices"
	"strings"
)

// Effects with no value anywhere in the stack.
var defaultEffects = map[string]float64{"glow": 1, "grid": 0, "scanline": 0, "brackets": 1, "blur": 0}

// Resolved is a theme flattened for one variant: every token's colour, read by id.
type Resolved struct {
	Name    string
	Variant Variant
	colors  []Color
	byName  map[string]Color
	palette map[string]Color
	syntax  map[string]Syntax
	effects map[string]float64
	links   map[string]Link
}

// Syntax is a resolved highlight group.
type Syntax struct {
	FG, BG                  Color // BG is zero when the group has none
	Italic, Bold, Underline bool
}

// Link is one step of a token's chain: where its value came from, and the expression there.
type Link struct {
	Name string
	Expr string
	From string // the theme that set it, or "default"
}

// C is a token's colour. Ids declared after the theme resolved are zero until it resolves again.
func (r *Resolved) C(id TokenID) Color {
	if int(id) < len(r.colors) {
		return r.colors[id]
	}
	return Color{}
}

// Color looks a token, then a palette colour, up by name.
func (r *Resolved) Color(name string) (Color, bool) {
	if c, ok := r.byName[name]; ok {
		return c, true
	}
	c, ok := r.palette[name]
	return c, ok
}

// Syntax resolves a tree-sitter capture or LSP token, falling back along its dotted path:
// @keyword.return, then @keyword, then plain text in fg.
func (r *Resolved) Syntax(capture string) Syntax {
	for name := capture; ; {
		if s, ok := r.syntax[name]; ok {
			return s
		}
		i := strings.LastIndexByte(name, '.')
		if i < 0 {
			return Syntax{FG: r.C(Fg)}
		}
		name = name[:i]
	}
}

func (r *Resolved) Effect(name string) float64 { return r.effects[name] }

// Chain is how name got its value: the token, then each name its expression is just a reference to,
// down to the palette colour or literal at the end.
func (r *Resolved) Chain(name string) []Link {
	var out []Link
	seen := map[string]bool{}
	for name != "" && !seen[name] {
		seen[name] = true
		l, ok := r.links[name]
		if !ok {
			break
		}
		out = append(out, l)
		e, err := parseExpr(l.Expr)
		if err != nil || e.name == "" {
			break
		}
		next := e.name
		if _, isToken := r.byName[next]; !isToken || next == name {
			next = "palette:" + next
		}
		name = next
	}
	return out
}

// Resolve flattens stack (built-in first, then each layer over it: user, project, session) for variant v.
func Resolve(stack []*Spec, v Variant) (*Resolved, error) {
	name := "default"
	if len(stack) > 0 {
		name = stack[len(stack)-1].Name
	}
	r := &Resolved{Name: name, Variant: v, byName: map[string]Color{}, palette: map[string]Color{},
		syntax: map[string]Syntax{}, effects: map[string]float64{}, links: map[string]Link{}}

	palette := map[string]Link{}
	tokenDefs := map[string]TokenDef{}
	syntax := map[string]SyntaxDef{}
	for _, t := range Tokens() {
		tokenDefs[t.Name] = TokenDef{Expr: t.Default, On: t.On}
		r.links[t.Name] = Link{Name: t.Name, Expr: t.Default, From: "default"}
	}
	for k, x := range defaultEffects {
		r.effects[k] = x
	}
	for _, s := range stack {
		for _, l := range []*Layer{&s.Layer, s.variant(v)} {
			for k, x := range l.Palette {
				palette[k] = Link{Name: "palette:" + k, Expr: x, From: s.Name}
			}
			for k, d := range l.Tokens {
				if d.On == "" {
					d.On = tokenDefs[k].On
				}
				tokenDefs[k] = d
				r.links[k] = Link{Name: k, Expr: d.Expr, From: s.Name}
			}
			for k, d := range l.Syntax {
				syntax[k] = d
			}
			for k, x := range l.Effects {
				r.effects[k] = x
			}
		}
	}
	for _, l := range palette {
		r.links[l.Name] = l
	}

	ev := evaluator{palette: palette, tokens: tokenDefs, pal: map[string]Color{}, tok: map[string]Color{}}
	var errs []string
	names := make([]string, 0, len(palette))
	for k := range palette {
		names = append(names, k)
	}
	slices.Sort(names)
	for _, k := range names {
		c, err := ev.paletteColor(k, nil)
		if err != nil {
			errs = append(errs, err.Error())
		}
		r.palette[k] = c
	}
	names = names[:0]
	for k := range tokenDefs {
		names = append(names, k)
	}
	slices.Sort(names)
	for _, k := range names {
		c, err := ev.token(k, nil)
		if err != nil {
			errs = append(errs, err.Error())
		}
		r.byName[k] = c
	}
	for _, t := range Tokens() {
		r.colors = append(r.colors, r.byName[t.Name])
	}
	for k, d := range syntax {
		s := Syntax{FG: r.C(Fg)}
		if d.FG != "" {
			c, err := ev.eval(d.FG, "")
			if err != nil {
				errs = append(errs, k+": "+err.Error())
			}
			s.FG = c
		}
		if d.BG != "" {
			c, err := ev.eval(d.BG, "")
			if err != nil {
				errs = append(errs, k+": "+err.Error())
			}
			s.BG = c
		}
		for _, f := range []struct {
			p   *bool
			out *bool
		}{{d.Italic, &s.Italic}, {d.Bold, &s.Bold}, {d.Underline, &s.Underline}} {
			if f.p != nil {
				*f.out = *f.p
			}
		}
		r.syntax[k] = s
	}
	if len(errs) > 0 {
		slices.Sort(errs)
		return r, fmt.Errorf("theme %s (%s): %s", name, v, strings.Join(errs, "; "))
	}
	return r, nil
}

type evaluator struct {
	palette map[string]Link
	tokens  map[string]TokenDef
	pal     map[string]Color
	tok     map[string]Color
}

func (ev *evaluator) paletteColor(name string, stack []string) (Color, error) {
	if c, ok := ev.pal[name]; ok {
		return c, nil
	}
	l, ok := ev.palette[name]
	if !ok {
		return Color{}, fmt.Errorf("no colour %q", name)
	}
	if slices.Contains(stack, "palette:"+name) {
		return Color{}, fmt.Errorf("cycle: %s → %s", strings.Join(stack, " → "), name)
	}
	stack = append(stack, "palette:"+name)
	e, err := parseExpr(l.Expr)
	if err != nil {
		return Color{}, fmt.Errorf("palette %s: %w", name, err)
	}
	c, err := e.eval(func(ref string) (Color, error) { return ev.paletteColor(ref, stack) })
	if err != nil {
		return Color{}, err
	}
	ev.pal[name] = c
	return c, nil
}

// token resolves a token. A name in its expression is a token unless it's the token's own name,
// which means the palette colour of that name; otherwise an unknown token falls back to the palette.
func (ev *evaluator) token(name string, stack []string) (Color, error) {
	if c, ok := ev.tok[name]; ok {
		return c, nil
	}
	d := ev.tokens[name]
	if slices.Contains(stack, name) {
		return Color{}, fmt.Errorf("cycle: %s → %s", strings.Join(stack, " → "), name)
	}
	stack = append(stack, name)
	c, err := ev.evalIn(d.Expr, name, stack)
	if err != nil {
		return Color{}, fmt.Errorf("%s: %w", name, err)
	}
	if d.On != "" && c.A < 255 {
		bg, err := ev.token(d.On, stack)
		if err != nil {
			return Color{}, err
		}
		c = Over(c, bg)
	}
	ev.tok[name] = c
	return c, nil
}

func (ev *evaluator) eval(src, self string) (Color, error) { return ev.evalIn(src, self, nil) }

func (ev *evaluator) evalIn(src, self string, stack []string) (Color, error) {
	e, err := parseExpr(src)
	if err != nil {
		return Color{}, err
	}
	return e.eval(func(ref string) (Color, error) {
		if _, ok := ev.tokens[ref]; ok && ref != self {
			return ev.token(ref, stack)
		}
		return ev.paletteColor(ref, nil)
	})
}
