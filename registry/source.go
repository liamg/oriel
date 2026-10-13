package registry

// WithSource returns a view of r that stamps src on every command, keymap and option registered through it
// without a source of its own. Each extension registers through one, so listings can say where things came from.
func WithSource(r Registry, src string) Registry { return sourced{r, src} }

type sourced struct {
	Registry
	src string
}

func (s sourced) Command(c Command) Disposable {
	if c.Source == "" {
		c.Source = s.src
	}
	return s.Registry.Command(c)
}

func (s sourced) Keymap(k Keymap) Disposable {
	if k.Source == "" {
		k.Source = s.src
	}
	return s.Registry.Keymap(k)
}

func (s sourced) Picker(p PickerSource) Disposable {
	if p.Source == "" {
		p.Source = s.src
	}
	return s.Registry.Picker(p)
}

func (s sourced) Option(o OptionSpec) Disposable {
	if o.Source == "" {
		o.Source = s.src
	}
	return s.Registry.Option(o)
}
