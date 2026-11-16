package help

// What the package's outside tests read of its plugin.

func (v *View) Open() bool   { return v.open }
func (v *View) Page() string { return v.cur }
