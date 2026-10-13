package vim

import "strings"

// KeyHint is a key that can come next and what it does, for which-key.
type KeyHint struct {
	Keys string
	Desc string
}

// prefixHints are the engine's own commands under each prefix key, in Oriel's words.
var prefixHints = map[string][]KeyHint{
	"g": {
		{"g", "First line"}, {"j", "Down a screen line"}, {"k", "Up a screen line"}, {"_", "Last non-blank of the line"},
		{"e", "End of the previous word"}, {"E", "End of the previous WORD"}, {"o", "Go to a byte"},
		{"*", "Search the word, as part of others too"}, {"#", "Search the word backwards, as part of others"},
		{"'", "To a mark's line, without a jump"}, {"`", "To a mark, without a jump"},
		{"J", "Join lines as they are"}, {"p", "Put after, cursor after it"}, {"P", "Put before, cursor after it"},
		{"i", "Insert where insert last stopped"}, {"I", "Insert at column 1"}, {"v", "Reselect the last Visual area"},
		{"~", "Swap case (operator)"}, {"u", "Lowercase (operator)"}, {"U", "Uppercase (operator)"}, {"?", "Rot13 (operator)"},
		{"&", "Repeat the last :s on every line"}, {"-", "Older text state"}, {"+", "Newer text state"},
	},
	"z": {
		{"t", "Line at the top"}, {"<CR>", "Line at the top, first non-blank"}, {"z", "Line in the middle"},
		{".", "Line in the middle, first non-blank"}, {"b", "Line at the bottom"}, {"-", "Line at the bottom, first non-blank"},
		{"f", "Make a fold (operator)"}, {"F", "Make a fold of lines"}, {"d", "Delete the fold"}, {"D", "Delete folds, nested too"},
		{"E", "Delete every fold"}, {"o", "Open the fold"}, {"O", "Open folds, nested too"}, {"c", "Close the fold"},
		{"C", "Close folds, nested too"}, {"a", "Toggle the fold"}, {"A", "Toggle folds, nested too"}, {"v", "Open folds to show the cursor"},
		{"x", "Re-apply foldlevel"}, {"X", "Re-apply foldlevel, keep manual"}, {"m", "Fold more"}, {"M", "Close every fold"},
		{"r", "Fold less"}, {"R", "Open every fold"}, {"n", "Folding off"}, {"N", "Folding on"}, {"i", "Toggle folding"},
	},
	"[": {
		{"[", "Previous section"}, {"]", "Previous section end"}, {"(", "Unclosed ( before"}, {"{", "Unclosed { before"},
		{"m", "Previous method start"}, {"M", "Previous method end"}, {"p", "Put before, indented"}, {"P", "Put before, indented"},
		{"`", "Previous mark"}, {"'", "Previous mark's line"},
	},
	"]": {
		{"]", "Next section"}, {"[", "Next section end"}, {")", "Unclosed ) after"}, {"}", "Unclosed } after"},
		{"m", "Next method start"}, {"M", "Next method end"}, {"p", "Put after, indented"}, {"P", "Put before, indented"},
		{"`", "Next mark"}, {"'", "Next mark's line"},
	},
	"<C-w>": {
		{"s", "Split"}, {"v", "Split vertically"}, {"n", "New window"}, {"w", "Next window"}, {"W", "Previous window"},
		{"p", "Last window"}, {"t", "Top-left window"}, {"b", "Bottom-right window"}, {"h", "Window left"}, {"j", "Window below"},
		{"k", "Window above"}, {"l", "Window right"}, {"c", "Close the window"}, {"q", "Quit the window"}, {"o", "Only this window"},
		{"x", "Swap with the next window"}, {"r", "Rotate windows down"}, {"R", "Rotate windows up"}, {"^", "Split on the alternate buffer"},
	},
}

// NextKeys are the engine's own commands that can follow the keys pending, after any count and register, for
// which-key. Mappings aren't among them: they're the IDE's keymaps.
func (e *Engine) NextKeys() []KeyHint {
	if e.mode != Normal || e.cmdline != nil {
		return nil
	}
	keys := e.pend
	for len(keys) > 0 && (isDigit(keys[0]) && keys[0] != "0" || keys[0] == `"`) {
		if keys[0] == `"` {
			keys = keys[min(2, len(keys)):]
			continue
		}
		keys = keys[1:]
	}
	if len(keys) != 1 {
		return nil
	}
	return prefixHints[strings.TrimSpace(keys[0])]
}
