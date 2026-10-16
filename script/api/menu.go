package api

import (
	"fmt"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
)

//oriel:api name=menu cap=ui
type Menu struct{ a *APIs }

//oriel:api doc="Add a right-click menu entry: { label, run = fn(at) or a command id, args (the command's), when = { view, file = '*.go', selection, help }, group, order (50 by default), key }. at has view, instance, file, selection and help. An entry that runs agent.fix gets an 'Ask the agent to explain' twin beside it, with the same args."
func (m Menu) Add(spec script.Table, where string) error {
	label := str(spec, "label")
	if label == "" {
		return fmt.Errorf("a menu entry needs a label")
	}
	it := registry.MenuItem{Name: where, Label: label, Key: str(spec, "key"), Group: str(spec, "group"), Order: 50, Source: where}
	if o, ok := spec["order"].(float64); ok {
		it.Order = int(o)
	}
	if w, ok := spec["when"].(script.Table); ok {
		sel, _ := w["selection"].(bool)
		it.When = registry.MenuWhen{View: str(w, "view"), File: str(w, "file"), Selection: sel, Help: str(w, "help")}
	}
	switch run := spec["run"].(type) {
	case string:
		it.Command = run
		if a, ok := spec["args"].(script.Table); ok {
			it.Args = registry.Args(a)
		}
	case *script.Fn:
		it.Run = func(_ registry.Ctx, at registry.MenuCtx) error {
			_, err := m.a.VM.Call(run, script.Table{"view": at.View, "instance": at.Instance, "project": at.Project, "file": at.File,
				"selection": at.Selection, "help": at.Help})
			return err
		}
	default:
		return fmt.Errorf("%s: run is a function or a command id", label)
	}
	m.a.own(m.a.Host.Contribute(it))
	return nil
}
