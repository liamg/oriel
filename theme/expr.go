package theme

import (
	"fmt"
	"strconv"
	"strings"
)

// An expression is a colour literal, a name (a token or palette colour), or a function of them:
// mix(a, b, p), lighten(c, p), darken(c, p), alpha(c, p), over(fg, bg). Amounts are numbers or percentages.
type expr struct {
	lit   *Color
	name  string
	fn    string
	args  []expr
	num   float64
	isNum bool
}

func parseExpr(s string) (expr, error) {
	p := exprParser{s: s}
	e, err := p.expr()
	if err != nil {
		return expr{}, fmt.Errorf("%q: %w", s, err)
	}
	p.ws()
	if p.i != len(p.s) {
		return expr{}, fmt.Errorf("%q: unexpected %q", s, p.s[p.i:])
	}
	return e, nil
}

type exprParser struct {
	s string
	i int
}

func (p *exprParser) ws() {
	for p.i < len(p.s) && (p.s[p.i] == ' ' || p.s[p.i] == '\t') {
		p.i++
	}
}

func isNameChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-' || c == '@' || c == '%'
}

func (p *exprParser) expr() (expr, error) {
	p.ws()
	if p.i < len(p.s) && p.s[p.i] == '#' {
		start := p.i
		for p.i++; p.i < len(p.s) && isNameChar(p.s[p.i]); p.i++ {
		}
		c, err := ParseColor(p.s[start:p.i])
		return expr{lit: &c}, err
	}
	start := p.i
	for p.i < len(p.s) && isNameChar(p.s[p.i]) {
		p.i++
	}
	word := p.s[start:p.i]
	if word == "" {
		return expr{}, fmt.Errorf("expected a colour, a name or a function at %d", start)
	}
	if p.i < len(p.s) && p.s[p.i] == '(' {
		switch word {
		case "rgb", "rgba", "oklch":
			end := strings.IndexByte(p.s[p.i:], ')')
			if end < 0 {
				return expr{}, fmt.Errorf("unclosed %s(", word)
			}
			c, err := ParseColor(p.s[start : p.i+end+1])
			p.i += end + 1
			return expr{lit: &c}, err
		}
		p.i++
		e := expr{fn: word}
		for {
			arg, err := p.expr()
			if err != nil {
				return expr{}, err
			}
			e.args = append(e.args, arg)
			p.ws()
			if p.i < len(p.s) && p.s[p.i] == ',' {
				p.i++
				continue
			}
			if p.i < len(p.s) && p.s[p.i] == ')' {
				p.i++
				return e, e.check()
			}
			return expr{}, fmt.Errorf("expected , or ) in %s()", word)
		}
	}
	if n, err := number(word, 1); err == nil && (word[0] >= '0' && word[0] <= '9' || word[0] == '.' || word[0] == '-') {
		return expr{num: n, isNum: true}, nil
	}
	return expr{name: word}, nil
}

var arity = map[string]int{"mix": 3, "lighten": 2, "darken": 2, "alpha": 2, "over": 2}

func (e expr) check() error {
	n, ok := arity[e.fn]
	if !ok {
		return fmt.Errorf("unknown function %s()", e.fn)
	}
	if len(e.args) != n {
		return fmt.Errorf("%s() takes %d arguments, not %d", e.fn, n, len(e.args))
	}
	last := e.args[n-1]
	switch e.fn {
	case "over":
		if last.isNum || e.args[0].isNum {
			return fmt.Errorf("over() takes two colours")
		}
	default:
		if !last.isNum {
			return fmt.Errorf("%s()'s last argument is an amount", e.fn)
		}
		for _, a := range e.args[:n-1] {
			if a.isNum {
				return fmt.Errorf("%s() takes colours before its amount", e.fn)
			}
		}
	}
	return nil
}

// refs lists the names e refers to.
func (e expr) refs() []string {
	if e.name != "" {
		return []string{e.name}
	}
	var out []string
	for _, a := range e.args {
		out = append(out, a.refs()...)
	}
	return out
}

func (e expr) eval(lookup func(name string) (Color, error)) (Color, error) {
	switch {
	case e.lit != nil:
		return *e.lit, nil
	case e.name != "":
		return lookup(e.name)
	}
	var cs []Color
	for _, a := range e.args {
		if a.isNum {
			continue
		}
		c, err := a.eval(lookup)
		if err != nil {
			return Color{}, err
		}
		cs = append(cs, c)
	}
	amt := e.args[len(e.args)-1].num
	switch e.fn {
	case "mix":
		return Mix(cs[0], cs[1], amt), nil
	case "lighten":
		return Lighten(cs[0], amt), nil
	case "darken":
		return Lighten(cs[0], -amt), nil
	case "alpha":
		return WithAlpha(cs[0], amt*float64(cs[0].A)/255), nil
	case "over":
		return Over(cs[0], cs[1]), nil
	}
	return Color{}, fmt.Errorf("unknown function %s()", e.fn)
}

func (e expr) String() string {
	switch {
	case e.lit != nil:
		return Hex(*e.lit)
	case e.name != "":
		return e.name
	case e.isNum:
		return strconv.FormatFloat(e.num, 'g', -1, 64)
	}
	parts := make([]string, len(e.args))
	for i, a := range e.args {
		parts[i] = a.String()
	}
	return e.fn + "(" + strings.Join(parts, ", ") + ")"
}
