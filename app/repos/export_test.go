package repos

// What the package's outside tests set of its plugin.

// Cloning shows the Add repo dialog cloning, git saying progress.
func (x *Repos) Cloning(progress string) { x.dialog.busy, x.dialog.progress = true, progress }
