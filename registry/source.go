package registry

// WithSource returns a view of r that stamps src on every command, keymap, hook, option and contribution registered
// through it without a source of its own. Each plugin registers through one, so listings can say where things came from.
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

func (s sourced) Hook(h Hook) Disposable {
	if h.Source == "" {
		h.Source = s.src
	}
	return s.Registry.Hook(h)
}

func (s sourced) HelpPages(h HelpSource) Disposable {
	if h.Source == "" {
		h.Source = s.src
	}
	return s.Registry.HelpPages(h)
}

func (s sourced) Option(o OptionSpec) Disposable {
	if o.Source == "" {
		o.Source = s.src
	}
	return s.Registry.Option(o)
}

func (s sourced) Contribute(c Contribution) Disposable {
	return s.Registry.Contribute(WithSourceOf(c, s.src))
}
