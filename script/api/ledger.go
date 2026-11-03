package api

import "github.com/liamg/oriel/registry"

//oriel:api name=ledger cap=agent.read
type Ledger struct{ a *APIs }

// count is a project's review ledger: by the session's name for it or a path in it, the project in front if "".
func (l Ledger) count(project string) registry.LedgerCount {
	h := l.a.Host
	root := h.Here().Project
	if project != "" {
		root = expand(project)
		st := h.State()
		if se, ok := st.Session(st.Focus); ok {
			for _, p := range se.Snapshot().Projects {
				if p.Name == project {
					root = p.Dir
				}
			}
		}
	}
	led, _ := registry.LedgerOf(h, root)
	return led.All
}

//oriel:api doc="How many agent-written lines of a project's change aren't read yet (unread, or only skimmed): project is the session's name for it or a path in it, the project in front if none." optional=project
func (l Ledger) Unread(project string) int { return l.count(project).NotRead() }

//oriel:api doc="The share of a project's agent-written lines read, 0 to 1 (1 with none to read); project as for unread." optional=project
func (l Ledger) Frac(project string) float64 { return l.count(project).Frac() }
