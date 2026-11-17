package luaconfig

// Eval runs a line in the Lua REPL, as typing it there does.
func (x *Lua) Eval(line string) { x.eval(line) }

// Repl is the REPL's lines, and each one's kind as a number: replInput 0, replOutput 1, replError 2, replFrame 3.
func (x *Lua) Repl() (texts []string, kinds []int) {
	for _, l := range x.repl {
		texts, kinds = append(texts, l.text), append(kinds, int(l.kind))
	}
	return texts, kinds
}

func (x *Lua) Complete(line string) []string { _, names := x.complete(line); return names }
func (x *Lua) SetInput(s string)             { x.input = s }
