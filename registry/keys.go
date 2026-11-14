package registry

// KeyLayer is a keymap mode laid over every other while Active says so: a popup's keys, as the completion menu's
// <C-n> and <CR>. A key the layer doesn't map goes on as it would without it.
type KeyLayer struct {
	Name   string // unique among layers: "completion"
	Mode   string // the keymap mode its keys are in
	Active func() bool
	Source string
}

func (KeyLayer) ContributionKind() string   { return "key.layer" }
func (l KeyLayer) ContributionName() string { return l.Name }

// ActiveKeyModes are the modes of the layers active now, by name.
func ActiveKeyModes(r Contributions) []string {
	var out []string
	for _, l := range ContributionsOf[KeyLayer](r) {
		on := false
		Call(func() error { on = l.Active != nil && l.Active(); return nil })
		if on {
			out = append(out, l.Mode)
		}
	}
	return out
}

// CmdlineHint is text shown at the right of the editor's : line while it applies: the matches a :s has, or a better
// way to do what's typed.
type CmdlineHint struct {
	Name   string
	Hint   func(line string) string // "" for none
	Source string
}

func (CmdlineHint) ContributionKind() string   { return "cmdline.hint" }
func (h CmdlineHint) ContributionName() string { return h.Name }

// CmdlineHints are the hints that apply to line, by name.
func CmdlineHints(r Contributions, line string) []string {
	var out []string
	for _, h := range ContributionsOf[CmdlineHint](r) {
		s := ""
		Call(func() error { s = h.Hint(line); return nil })
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ExclusiveKeys is a key mode that stands alone while Active says so (nil: always): keys resolving in it, the modes
// after it ("n", "global") lend it only the keymaps Allow lets through (nil: none), and none of their longer keys, so
// a screen keeps to its own keys (the start view to its entries'). Key layers before it are untouched.
type ExclusiveKeys struct {
	Mode   string
	Active func() bool
	Allow  func(Keymap) bool
	Source string
}

func (ExclusiveKeys) ContributionKind() string   { return "keys.exclusive" }
func (e ExclusiveKeys) ContributionName() string { return e.Mode }

// RawKeys is a key mode whose widget takes control keys itself: while it has focus, global maps of ⌃ keys (without ⌘
// or ⌥) don't apply, so a terminal's program gets ⌃W and ⌃J. Its own mode's maps still do.
type RawKeys struct {
	Mode   string
	Source string
}

func (RawKeys) ContributionKind() string   { return "keys.raw" }
func (r RawKeys) ContributionName() string { return r.Mode }

// IsRawKeyMode reports whether mode takes control keys itself.
func IsRawKeyMode(r Contributions, mode string) bool {
	for _, k := range ContributionsOf[RawKeys](r) {
		if k.Mode == mode {
			return true
		}
	}
	return false
}
