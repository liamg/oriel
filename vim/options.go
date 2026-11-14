package vim

import (
	"fmt"
	"strconv"
)

// OptionScope is where an option's value lives, as in vim: everywhere, per buffer, or per window.
type OptionScope int

const (
	GlobalOption OptionScope = iota
	BufferOption
	WindowOption
)

// OptionKind is what an option's value is.
type OptionKind int

const (
	BoolKind OptionKind = iota
	IntKind
	StringKind
	EnumKind // one of OptionDef.Values
)

// OptionDef is one of the engine's options, for a host that keeps them (the IDE's settings).
type OptionDef struct {
	Name    string // vim's name: "shiftwidth"
	Short   string // "sw"
	Kind    OptionKind
	Values  []string // an enum's
	Default any
	Min     int // an int's least value
	Scope   OptionScope
	Help    string
	get     func(o *Options) any
	set     func(o *Options, v any)
}

// OptionDefs are the engine's options, with nvim's defaults (DefaultOptions).
func OptionDefs() []OptionDef {
	d := DefaultOptions()
	return []OptionDef{
		{Name: "shiftwidth", Short: "sw", Kind: IntKind, Default: d.ShiftWidth, Scope: BufferOption,
			Help: "Columns an indent step (>>, <<, ⌃T) takes.",
			get:  func(o *Options) any { return o.ShiftWidth }, set: func(o *Options, v any) { o.ShiftWidth = v.(int) }},
		{Name: "tabstop", Short: "ts", Kind: IntKind, Default: d.TabStop, Min: 1, Scope: BufferOption,
			Help: "Columns a tab takes.",
			get:  func(o *Options) any { return o.TabStop }, set: func(o *Options, v any) { o.TabStop = v.(int) }},
		{Name: "expandtab", Short: "et", Kind: BoolKind, Default: d.ExpandTab, Scope: BufferOption,
			Help: "Indent with spaces, not tabs.",
			get:  func(o *Options) any { return o.ExpandTab }, set: func(o *Options, v any) { o.ExpandTab = v.(bool) }},
		{Name: "ignorecase", Short: "ic", Kind: BoolKind, Default: d.IgnoreCase, Scope: GlobalOption,
			Help: "Searches ignore case.",
			get:  func(o *Options) any { return o.IgnoreCase }, set: func(o *Options, v any) { o.IgnoreCase = v.(bool) }},
		{Name: "smartcase", Short: "scs", Kind: BoolKind, Default: d.SmartCase, Scope: GlobalOption,
			Help: "With ignorecase, a search with a capital letter matches case.",
			get:  func(o *Options) any { return o.SmartCase }, set: func(o *Options, v any) { o.SmartCase = v.(bool) }},
		{Name: "wrapscan", Short: "ws", Kind: BoolKind, Default: d.WrapScan, Scope: GlobalOption,
			Help: "Searches wrap around the end of the file.",
			get:  func(o *Options) any { return o.WrapScan }, set: func(o *Options, v any) { o.WrapScan = v.(bool) }},
		{Name: "wrap", Kind: BoolKind, Default: d.Wrap, Scope: GlobalOption,
			Help: "Long lines wrap onto the next row; off, they run past the edge and the window scrolls sideways.",
			get:  func(o *Options) any { return o.Wrap }, set: func(o *Options, v any) { o.Wrap = v.(bool) }},
		{Name: "sidescroll", Short: "ss", Kind: IntKind, Default: d.SideScroll, Scope: GlobalOption,
			Help: "With wrap off, the fewest columns the window scrolls sideways to follow the cursor; 0 centres it.",
			get:  func(o *Options) any { return o.SideScroll }, set: func(o *Options, v any) { o.SideScroll = v.(int) }},
		{Name: "sidescrolloff", Short: "siso", Kind: IntKind, Default: d.SideScrollOff, Scope: GlobalOption,
			Help: "With wrap off, the columns kept to the left and right of the cursor.",
			get:  func(o *Options) any { return o.SideScrollOff }, set: func(o *Options, v any) { o.SideScrollOff = v.(int) }},
		{Name: "magic", Kind: BoolKind, Default: d.Magic, Scope: GlobalOption,
			Help: "Patterns use vim's magic syntax.",
			get:  func(o *Options) any { return o.Magic }, set: func(o *Options, v any) { o.Magic = v.(bool) }},
		{Name: "report", Kind: IntKind, Default: d.Report, Scope: GlobalOption,
			Help: "Report a change to more lines than this.",
			get:  func(o *Options) any { return o.Report }, set: func(o *Options, v any) { o.Report = v.(int) }},
		{Name: "nrformats", Short: "nf", Kind: StringKind, Default: d.NrFormats, Scope: BufferOption,
			Help: "The number formats ⌃A and ⌃X recognise: bin, hex, octal, alpha.",
			get:  func(o *Options) any { return o.NrFormats }, set: func(o *Options, v any) { o.NrFormats = v.(string) }},
		{Name: "hidden", Short: "hid", Kind: BoolKind, Default: d.Hidden, Scope: GlobalOption,
			Help: "A buffer can be hidden with unsaved changes.",
			get:  func(o *Options) any { return o.Hidden }, set: func(o *Options, v any) { o.Hidden = v.(bool) }},
		{Name: "clipboard", Kind: EnumKind, Values: []string{"system", "registers"}, Default: d.Clipboard, Scope: GlobalOption,
			Help: "What y, d, c, x and p use: the system clipboard, or vim's registers (the clipboard is then \"+ and \"*).",
			get:  func(o *Options) any { return o.Clipboard }, set: func(o *Options, v any) { o.Clipboard = v.(string) }},
	}
}

// Check says why v isn't a value d can hold.
func (d OptionDef) Check(v any) error {
	if d.Kind == IntKind {
		n, ok := v.(int)
		if !ok {
			return fmt.Errorf("%s: want a number, got %v", d.Name, v)
		}
		if n < d.Min {
			return fmt.Errorf("%s: must be at least %d", d.Name, d.Min)
		}
	}
	return nil
}

func optionDef(name string) (OptionDef, bool) {
	for _, d := range OptionDefs() {
		if d.Name == name || d.Short != "" && d.Short == name {
			return d, true
		}
	}
	return OptionDef{}, false
}

// Settings keeps the engine's options outside it: the IDE's settings, read at a buffer (its name) and window, and
// written by :set.
type Settings interface {
	Get(name, buffer, window string) (any, bool)
	Set(name string, scope OptionScope, buffer, window string, v any) error
}

// SetSettings makes s where the engine's options come from, read before each key and Ex command.
func (e *Engine) SetSettings(s Settings) {
	e.settings = s
	e.loadOptions()
}

func (e *Engine) windowKey() string { return strconv.Itoa(e.window.id) }

// loadOptions reads the options for the current buffer and window from the settings.
func (e *Engine) loadOptions() {
	if e.settings == nil {
		return
	}
	o := DefaultOptions()
	for _, d := range OptionDefs() {
		if v, ok := e.settings.Get(d.Name, e.buffer.name, e.windowKey()); ok && d.Check(v) == nil {
			d.set(&o, v)
		}
	}
	e.opts = o
}

// setOption is :set name to v: into the settings when there are some, at the option's scope.
func (e *Engine) setOption(d OptionDef, v any) error {
	if e.settings != nil {
		if err := e.settings.Set(d.Name, d.Scope, e.buffer.name, e.windowKey(), v); err != nil {
			return err
		}
	}
	d.set(&e.opts, v)
	return nil
}

// setValue parses :set's text for d: "name", "noname", "name=val", "name+=val" and the rest.
func (e *Engine) setValue(d OptionDef, arg, val string, hasVal, on bool, op byte) (any, error) {
	switch d.Kind {
	case BoolKind:
		return on, nil
	case IntKind:
		n, err := strconv.Atoi(val)
		if err != nil {
			return nil, errNumberRequired.with(arg)
		}
		cur := d.get(&e.opts).(int)
		switch op {
		case '+':
			n = cur + n
		case '-':
			n = cur - n
		case '^':
			n = cur * n
		}
		if n < d.Min {
			return nil, fmt.Errorf("E487: Argument must be positive: %s", arg)
		}
		return n, nil
	case EnumKind:
		// vim's 'clipboard' values say the same: unnamed and unnamedplus are the system clipboard.
		if d.Name == "clipboard" {
			switch val {
			case "unnamed", "unnamedplus":
				val = "system"
			case "":
				val = "registers"
			}
		}
		for _, x := range d.Values {
			if x == val {
				return val, nil
			}
		}
		return nil, fmt.Errorf("E474: Invalid argument: %s", arg)
	}
	if !hasVal {
		return nil, fmt.Errorf("E474: Invalid argument: %s", arg)
	}
	return listOption(d.get(&e.opts).(string), val, op), nil
}
