package sessions

import (
	"errors"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
)

// addQuick adds Quick session: session.quick.start, ⇧⌘N, the start view's entry and the tabs' + menu.
func (x *Launcher) addQuick(h plugin.Host, has func(registry.Args) bool) {
	x.Add(h.Command(registry.Command{ID: "session.quick.start",
		Title:     "Quick session: a work session on the defaults New session's Start uses, with no repos and no prompt",
		Available: has, Run: func(registry.Ctx, registry.Args) error { x.quick(); return nil }}))
	x.Add(h.Keymap(registry.Keymap{Mode: "global", Keys: "<D-N>", Command: "session.quick.start", Desc: "Quick Session"}))
	x.Add(h.Contribute(uireg.StartEntry{Name: "session.quick.start", Icon: "agent", Title: "Quick Session",
		Desc: "A work session on your defaults, no repos and no questions", Command: "session.quick.start", Order: 15, Source: "builtin:launcher"}))
	for _, it := range []registry.MenuItem{
		{Name: "session.quick.start", Label: "Quick session", Order: 10, Command: "session.quick.start"},
		{Name: "session.new", Label: "New session…", Order: 20, Command: "session.new"},
	} {
		it.Group, it.When, it.Source = "new", registry.MenuWhen{Help: kit.NewSessionHelp}, "builtin:launcher"
		if k := kit.BoundKey(h, it.Command, nil); k != "" {
			it.Key = k
		}
		x.Add(h.Contribute(it))
	}
}

// quick starts a work session on what the launcher would start with nothing changed (args, from openWith and settle):
// the default agent and model, the sandbox sandbox.default gives, no repos and no prompt. The agents are asked off
// the loop, as the launcher asks them.
func (x *Launcher) quick() {
	h, life := x.h, x.life
	v, _, _ := h.Options().Get("sandbox.default", registry.At{})
	sandbox := v == "sandbox"
	st := h.State()
	plugin.Go(h, "launcher: quick session", func() {
		err := plugin.Await(life, func(ctx registry.Ctx) error {
			c, err := kit.SessionCalls(h)
			if err != nil {
				return err
			}
			provider, model := kit.DefaultAgent(st, c.ProviderInfo(ctx))
			if provider == "" {
				return errors.New("no agents are set up on this machine: New session can start one with No agent")
			}
			a := registry.Args{"kind": string(core.KindWork), "prompt": "", "provider": provider, "model": model, "effort": "",
				"sandbox": sandbox, "branch": "", "ticket": ""}
			if sandbox {
				a["network"] = "ask"
			}
			return h.Run(ctx, "session.start", a)
		})
		if err != nil {
			h.Notify(core.Notice{ID: "session.quick.start", Severity: core.SevError, Key: true, Text: "Quick session: " + err.Error()})
		}
	})
}
