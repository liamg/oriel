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

// OptionDef is one of the engine's options, for a host that keeps them (the IDE's settings).
type OptionDef struct {
	Name    string   // vim's name: "shiftwidth"
	Short   string   // "sw"
	Kind    string   // "bool", "int", "string" or "enum"
	Values  []string // an enum's
	Default any
	Scope   OptionScope
	Help    string
	get     func(o *Options) any
	set     func(o *Options, v any)
}

// OptionDefs are the engine's options, with nvim's defaults (DefaultOptions).
func OptionDefs() []OptionDef {
	d := DefaultOptions()
	return []OptionDef{
		{Name: "shiftwidth", Short: "sw", Kind: "int", Default: d.ShiftWidth, Scope: BufferOption,
			Help: "Columns an indent step (>>, <<, ⌃T) takes.",
			get:  func(o *Options) any { return o.ShiftWidth }, set: func(o *Options, v any) { o.ShiftWidth = v.(int) }},
		{Name: "expandtab", Short: "et", Kind: "bool", Default: d.ExpandTab, Scope: BufferOption,
			Help: "Indent with spaces, not tabs.",
			get:  func(o *Options) any { return o.ExpandTab }, set: func(o *Options, v any) { o.ExpandTab = v.(bool) }},
		{Name: "ignorecase", Short: "ic", Kind: "bool", Default: d.IgnoreCase, Scope: GlobalOption,
			Help: "Searches ignore case.",
			get:  func(o *Options) any { return o.IgnoreCase }, set: func(o *Options, v any) { o.IgnoreCase = v.(bool) }},
		{Name: "smartcase", Short: "scs", Kind: "bool", Default: d.SmartCase, Scope: GlobalOption,
			Help: "With ignorecase, a search with a capital letter matches case.",
			get:  func(o *Options) any { return o.SmartCase }, set: func(o *Options, v any) { o.SmartCase = v.(bool) }},
		{Name: "wrapscan", Short: "ws", Kind: "bool", Default: d.WrapScan, Scope: GlobalOption,
			Help: "Searches wrap around the end of the file.",
			get:  func(o *Options) any { return o.WrapScan }, set: func(o *Options, v any) { o.WrapScan = v.(bool) }},
		{Name: "magic", Kind: "bool", Default: d.Magic, Scope: GlobalOption,
			Help: "Patterns use vim's magic syntax.",
			get:  func(o *Options) any { return o.Magic }, set: func(o *Options, v any) { o.Magic = v.(bool) }},
		{Name: "report", Kind: "int", Default: d.Report, Scope: GlobalOption,
			Help: "Report a change to more lines than this.",
			get:  func(o *Options) any { return o.Report }, set: func(o *Options, v any) { o.Report = v.(int) }},
		{Name: "nrformats", Short: "nf", Kind: "string", Default: d.NrFormats, Scope: BufferOption,
			Help: "The number formats ⌃A and ⌃X recognise: bin, hex, octal, alpha.",
			get:  func(o *Options) any { return o.NrFormats }, set: func(o *Options, v any) { o.NrFormats = v.(string) }},
		{Name: "hidden", Short: "hid", Kind: "bool", Default: d.Hidden, Scope: GlobalOption,
			Help: "A buffer can be hidden with unsaved changes.",
			get:  func(o *Options) any { return o.Hidden }, set: func(o *Options, v any) { o.Hidden = v.(bool) }},
		{Name: "clipboard", Kind: "enum", Values: []string{"system", "registers"}, Default: d.Clipboard, Scope: GlobalOption,
			Help: "What y, d, c, x and p use: the system clipboard, or vim's registers (the clipboard is then \"+ and \"*).",
			get:  func(o *Options) any { return o.Clipboard }, set: func(o *Options, v any) { o.Clipboard = v.(string) }},
	}
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
		if v, ok := e.settings.Get(d.Name, e.buffer.name, e.windowKey()); ok {
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
	case "bool":
		return on, nil
	case "int":
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
		return n, nil
	case "enum":
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
