package theme

import (
	"fmt"
	"slices"
	"strings"

	"github.com/liamg/oriel/toml"
)

type Variant string

const (
	Dark  Variant = "dark"
	Light Variant = "light"
)

// Spec is what a theme file decodes into. Only what differs from what it extends is set.
type Spec struct {
	Name    string
	Extends string
	Variant Variant // the variant the theme is, when it has no [dark] and [light] tables; "" follows what's asked
	Layer           // shared by both variants
	Dark    Layer
	Light   Layer
}

// Layer is one set of overrides.
type Layer struct {
	Palette map[string]string
	Tokens  map[string]TokenDef // [semantic] and [component]
	Syntax  map[string]SyntaxDef
	Effects map[string]float64
}

type TokenDef struct {
	Expr string
	On   string // the token it sits on, which makes a translucent value opaque
}

type SyntaxDef struct {
	FG, BG                  string
	Italic, Bold, Underline *bool
}

// HasVariants reports whether s has [dark] or [light] tables, so an edit belongs under one.
func (s *Spec) HasVariants() bool { return !s.Dark.empty() || !s.Light.empty() }

// Effective is the variant stack resolves at when want is asked: the variant of the topmost theme that sets one,
// unless a theme above or at it has [dark]/[light] tables, which follow what's asked.
func Effective(stack []*Spec, want Variant) Variant {
	for _, s := range slices.Backward(stack) {
		if s.HasVariants() {
			return want
		}
		if s.Variant != "" {
			return s.Variant
		}
	}
	return want
}

func (l *Layer) empty() bool {
	return len(l.Palette) == 0 && len(l.Tokens) == 0 && len(l.Syntax) == 0 && len(l.Effects) == 0
}

func (s *Spec) variant(v Variant) *Layer {
	if v == Light {
		return &s.Light
	}
	return &s.Dark
}

// ParseSpec reads a theme file.
func ParseSpec(src []byte) (*Spec, error) {
	doc, err := toml.Parse(src)
	if err != nil {
		return nil, err
	}
	s := &Spec{}
	for k, v := range doc.Value() {
		var err error
		switch k {
		case "name":
			s.Name, err = str(k, v)
		case "extends":
			s.Extends, err = str(k, v)
		case "variant":
			var vs string
			vs, err = str(k, v)
			if err == nil && vs != string(Dark) && vs != string(Light) {
				err = fmt.Errorf("variant is dark or light, not %q", vs)
			}
			s.Variant = Variant(vs)
		case "dark", "light":
			m, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("[%s] is a table", k)
			}
			l := &s.Dark
			if k == "light" {
				l = &s.Light
			}
			err = l.decode(k+".", m)
		default:
			err = s.Layer.decodeSection("", k, v)
		}
		if err != nil {
			return nil, err
		}
	}
	return s, nil
}

func str(k string, v any) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s is a string, not %T", k, v)
	}
	return s, nil
}

func (l *Layer) decode(prefix string, m map[string]any) error {
	for k, v := range m {
		if err := l.decodeSection(prefix, k, v); err != nil {
			return err
		}
	}
	return nil
}

// tokenKeys flattens a table's keys: TOML reads bg.base = … as a nested table, theme files write it dotted.
// A table holding color or on is a value.
func tokenKeys(prefix string, m map[string]any, fn func(name string, v any) error) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		v := m[k]
		if sub, ok := v.(map[string]any); ok && sub["color"] == nil && sub["on"] == nil {
			if err := tokenKeys(prefix+k+".", sub, fn); err != nil {
				return err
			}
			continue
		}
		if err := fn(prefix+k, v); err != nil {
			return err
		}
	}
	return nil
}

func (l *Layer) decodeSection(prefix, section string, v any) error {
	m, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("[%s%s] is a table", prefix, section)
	}
	where := "[" + prefix + section + "] "
	switch section {
	case "palette":
		l.Palette = map[string]string{}
		return tokenKeys("", m, func(name string, v any) error {
			s, err := str(where+name, v)
			l.Palette[name] = s
			return err
		})
	case "semantic", "component":
		if l.Tokens == nil {
			l.Tokens = map[string]TokenDef{}
		}
		return tokenKeys("", m, func(name string, v any) error {
			switch v := v.(type) {
			case string:
				l.Tokens[name] = TokenDef{Expr: v}
			case map[string]any:
				c, err := str(where+name+".color", v["color"])
				if err != nil {
					return err
				}
				on, _ := v["on"].(string)
				l.Tokens[name] = TokenDef{Expr: c, On: on}
			default:
				return fmt.Errorf("%s%s is a colour or { color, on }, not %T", where, name, v)
			}
			return nil
		})
	case "syntax":
		l.Syntax = map[string]SyntaxDef{}
		for name, v := range m {
			var d SyntaxDef
			switch v := v.(type) {
			case string:
				d.FG = v
			case map[string]any:
				d.FG, _ = v["fg"].(string)
				d.BG, _ = v["bg"].(string)
				for k, p := range map[string]**bool{"italic": &d.Italic, "bold": &d.Bold, "underline": &d.Underline} {
					if b, ok := v[k].(bool); ok {
						*p = &b
					}
				}
			default:
				return fmt.Errorf("%s%s is a colour or a style table", where, name)
			}
			if !strings.HasPrefix(name, "@") {
				return fmt.Errorf("%s%s: syntax groups start with @", where, name)
			}
			l.Syntax[name] = d
		}
	case "effects":
		l.Effects = map[string]float64{}
		for name, v := range m {
			switch n := v.(type) {
			case int64:
				l.Effects[name] = float64(n)
			case float64:
				l.Effects[name] = n
			default:
				return fmt.Errorf("%s%s is a number", where, name)
			}
		}
	default:
		return fmt.Errorf("unknown section [%s%s]", prefix, section)
	}
	return nil
}
