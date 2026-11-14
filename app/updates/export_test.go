package updates

import "github.com/liamg/oriel/update"

const (
	UpdateAsk       = updateAsk
	UpdateAutomatic = updateAutomatic
)

func (x *Update) Updater() *update.Updater { return x.updater() }
func (x *Update) Ready() (string, bool)    { return x.ready() }

// WaitStartup waits for an automatic start's check to have run.
func (x *Update) WaitStartup() { x.start.Wait() }
