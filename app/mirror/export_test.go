package mirror

// What the package's outside tests read of its plugin.

func (x *Mirror) Wait() { x.wait() }
