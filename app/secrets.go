package app

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

const (
	// SecretDialogMode is the secret dialog's key mode.
	SecretDialogMode = "secret"
	secretsHelp      = "plugins/secrets"
)

func (h *pluginHost) Net() *http.Client {
	if h.sh.cfg.Net != nil {
		return h.sh.cfg.Net
	}
	return http.DefaultClient
}

func (h *pluginHost) Secrets() plugin.Secrets { return h.sh.secretStore() }

// secretStore is the config's secrets, or secrets.json in the state directory; each value it hands out is
// redacted from the logs from then on (errors.md).
func (sh *shell) secretStore() plugin.Secrets {
	sh.secretsOnce.Do(func() {
		s := sh.cfg.Secrets
		if s == nil {
			dir := sh.cfg.StateDir
			if dir == "" {
				dir = logs.StateDir()
			}
			s = &plugin.SecretFile{Path: filepath.Join(dir, "secrets.json")}
		}
		var r *logs.Redactor
		if sh.cfg.Logs != nil {
			r = sh.cfg.Logs.Redact
		}
		sh.secrets = redacted{siteSecrets{s, sh}, r}
	})
	return sh.secrets
}

type redacted struct {
	plugin.Secrets
	r *logs.Redactor
}

func (s redacted) Secret(name string) (string, bool) {
	v, ok := s.Secrets.Secret(name)
	if ok && s.r != nil {
		s.r.Add(v)
	}
	return v, ok
}

// secretsPlugin sets the secrets plugins name (security.md): only you, through its dialog, and never shown back.
type secretsPlugin struct {
	disposer
	sh   *shell
	h    plugin.Host
	dlg  *secretDialog
	rows map[string]*secretRow // Settings › Secrets' rows, by secret
}

// secretRow is a row of Settings › Secrets as it's being edited.
type secretRow struct {
	value     string
	replacing bool // set, and its field shows to paste another
	confirm   bool // Clear was pressed once
	err       string
}

// declaredSecret is a secret a running plugin declares in its manifest's permissions.secrets.
type declaredSecret struct {
	name, plugin, host, why string
}

type secretDialog struct {
	name, value string
	set         bool // it has a value already
	focus       bool
	err         string
}

func (x *secretsPlugin) Manifest() plugin.Manifest { return plugin.Manifest{ID: "secrets"} }

func (x *secretsPlugin) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	open := func(a registry.Args) bool { return x.dlg != nil }
	for _, c := range []registry.Command{
		{ID: "Secret", Title: "Set a secret plugins name, in a dialog; :Secret NAME", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.open(strings.TrimSpace(a.String("args")))
		}},
		{ID: "secrets.set", Title: "Set a secret plugins name: {name}, its value typed into a dialog", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.open(a.String("name"))
		}},
		{ID: "secrets.delete", Title: "Delete a secret: {name}", Run: func(_ registry.Ctx, a registry.Args) error {
			name := a.String("name")
			if name == "" {
				return fmt.Errorf("secrets.delete needs a name")
			}
			return x.sh.secretStore().SetSecret(name, "")
		}},
		{ID: "secrets.dialog.save", Title: "Save the secret the dialog holds", Available: open,
			Run: func(registry.Ctx, registry.Args) error { x.save(); return nil }},
	} {
		x.add(h.Command(c))
	}
	x.add(h.Keymap(registry.Keymap{Mode: SecretDialogMode, Keys: "<D-CR>", Command: "secrets.dialog.save", Desc: "Save"}))
	x.add(h.View(OverlayPrefix+"secret", registry.ViewFunc(func(registry.ViewCtx) ui.Node { return &ui.Func{Key: "secret", Fn: x.dialog} })))
	x.rows = map[string]*secretRow{}
	x.add(h.Contribute(registry.SettingsSection{Name: "secrets", Build: x.section, Source: "builtin:secrets", Count: func() int {
		n := 0
		for _, d := range x.declared() {
			if _, ok := x.sh.secretStore().Secret(d.name); ok {
				n++
			}
		}
		return n
	}}))
	return nil
}

// declared are the secrets the running plugins declare, by plugin then name.
func (x *secretsPlugin) declared() []declaredSecret {
	var out []declaredSecret
	for _, m := range x.h.Plugins() {
		for _, g := range m.Permissions {
			if g.Cap != plugin.CapSecrets {
				continue
			}
			for _, sc := range g.Scope {
				name, host, _ := strings.Cut(sc, "@")
				out = append(out, declaredSecret{name: name, plugin: m.ID, host: host, why: g.Why})
			}
		}
	}
	return out
}

// section is Settings › Secrets: a row per secret a plugin declares, set or not, never showing its value.
func (x *secretsPlugin) section(c *ui.Ctx) {
	th := c.Theme()
	ds := x.declared()
	widget.Wrap(c, "about", "The secrets your plugins declare. Each is kept in Oriel's secrets file, readable only by you, and sent only "+
		"to the host its plugin's manifest binds it to. Plugins never see one.", 700, uiFont(12.5, th.C(theme.FgFaint)))
	if len(ds) == 0 {
		widget.Wrap(c, "none", "No plugin you run declares a secret.", 700, uiFont(12.5, th.C(theme.FgMuted)))
	}
	for _, d := range ds {
		x.secretRowView(c, d)
	}
	c.Row("help", ui.Style{Pad: ui.Insets{T: 8}}, func(c *ui.Ctx) {
		if widget.Button(c, "help", "About secrets", widget.ButtonOpts{Ghost: true, Help: secretsHelp}) {
			x.h.Run(context.Background(), "help.open", registry.Args{"id": secretsHelp})
		}
	})
}

func (x *secretsPlugin) secretRowView(c *ui.Ctx, d declaredSecret) {
	th := c.Theme()
	r := x.rows[d.name]
	if r == nil {
		r = &secretRow{}
		x.rows[d.name] = r
	}
	_, set := x.sh.secretStore().Secret(d.name)
	help := secretsHelp + "#settings"
	c.Row(d.plugin+"/"+d.name, ui.Style{W: ui.Flex(1), Pad: ui.PadXY(0, 10), Gap: 16, Cross: ui.Center, Help: help}, func(c *ui.Ctx) {
		c.Column("text", ui.Style{W: ui.Flex(1), Gap: 4}, func(c *ui.Ctx) {
			c.Row("name", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("n", d.name, monoFont(12.5, th.C(theme.Fg)))
				c.Text("p", d.plugin, monoFont(10, th.C(theme.FgGhost)))
			})
			if d.why != "" {
				widget.Wrap(c, "why", d.why, 520, uiFont(12.5, th.C(theme.FgFaint)))
			}
			if d.host != "" {
				c.Text("host", "sent only to "+d.host, monoFont(10.5, th.C(theme.FgFaint)))
			}
			if r.err != "" {
				widget.Wrap(c, "err", r.err, 520, monoFont(11, th.C(theme.Error)))
			}
		})
		c.Row("ctl", ui.Style{W: ui.Px(360), Gap: 8, Cross: ui.Center, Main: ui.End}, func(c *ui.Ctx) {
			if set && !r.replacing {
				widget.Chip(c, "set", "set", th.C(theme.StatusQuiet), help)
				c.Text("dots", "••••••••", monoFont(12, th.C(theme.FgGhost)))
				if widget.Button(c, "replace", "Replace", widget.ButtonOpts{Help: help}) {
					r.replacing, r.confirm, r.err = true, false, ""
				}
				label := "Clear"
				if r.confirm {
					label = "Sure?"
				}
				if widget.Button(c, "clear", label, widget.ButtonOpts{Ghost: true, Help: help}) {
					if !r.confirm {
						r.confirm = true
						return
					}
					r.confirm = false
					if err := x.sh.secretStore().SetSecret(d.name, ""); err != nil {
						r.err = err.Error()
					}
				}
				return
			}
			save := false
			c.Box("in", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
				res := widget.Input(c, "value", &r.value, widget.InputOpts{Label: d.name, Placeholder: "paste it here", Secret: true, Help: help,
					Invalid: r.err != ""})
				switch {
				case res.Submitted:
					save = true
				case res.Cancelled:
					r.value, r.replacing, r.err = "", false, ""
				case res.Changed:
					r.err = ""
				}
			})
			if r.replacing && widget.Button(c, "cancel", "Cancel", widget.ButtonOpts{Ghost: true, Help: help}) {
				r.value, r.replacing, r.err = "", false, ""
			}
			if widget.Button(c, "save", "Save", widget.ButtonOpts{Primary: true, Help: help}) || save {
				x.saveRow(d.name, r)
			}
		})
	})
}

// saveRow keeps a row's value as its secret, and empties the field.
func (x *secretsPlugin) saveRow(name string, r *secretRow) {
	v := strings.TrimSpace(r.value)
	if v == "" {
		r.err = "Paste its value first."
		return
	}
	if err := x.sh.secretStore().SetSecret(name, v); err != nil {
		r.err = err.Error()
		return
	}
	r.value, r.replacing, r.err = "", false, ""
	x.h.Notify(core.Notice{ID: "secrets.set", Severity: core.SevInfo, Title: "Secrets", Text: name + " is set"})
}

func (x *secretsPlugin) open(name string) error {
	if name == "" || strings.ContainsAny(name, " \t@") {
		return fmt.Errorf("a secret's name is one word, such as SLACK_WEBHOOK")
	}
	_, set := x.sh.secretStore().Secret(name)
	x.dlg = &secretDialog{name: name, set: set, focus: true}
	x.h.Redraw()
	return nil
}

func (x *secretsPlugin) save() {
	d := x.dlg
	if d == nil {
		return
	}
	if strings.TrimSpace(d.value) == "" {
		d.err = "Paste its value, or Escape to leave it as it is."
		return
	}
	if err := x.sh.secretStore().SetSecret(d.name, strings.TrimSpace(d.value)); err != nil {
		d.err = err.Error()
		return
	}
	x.dlg = nil
	x.h.Notify(core.Notice{ID: "secrets.set", Severity: core.SevInfo, Title: "Secrets", Text: d.name + " is set"})
}

func (x *secretsPlugin) dialog(c *ui.Ctx) {
	d := x.dlg
	if d == nil {
		return
	}
	th := c.Theme()
	open := true
	widget.Modal(c, "secretdlg", &open, "Set "+d.name, widget.ModalOpts{Width: 520, Help: secretsHelp}, func(c *ui.Ctx) {
		note := "not set yet"
		if d.set {
			note = "set: this replaces it"
		}
		heading(c, "h", "Value", note)
		if d.focus {
			// Asked for until it lands: opened from another overlay (the omnibox), that one holds focus a frame more.
			id := c.Next("value")
			c.Focus(id)
			d.focus = !c.Interaction(id).Focused
		}
		res := widget.Input(c, "value", &d.value, widget.InputOpts{Label: d.name, Placeholder: "paste it here", Secret: true, Help: secretsHelp,
			KeyMode: SecretDialogMode, Invalid: d.err != ""})
		switch {
		case res.Cancelled:
			x.dlg = nil
			return
		case res.Submitted:
			x.save()
		case res.Changed:
			d.err = ""
		}
		if d.err != "" {
			widget.Wrap(c, "err", d.err, 480, monoFont(11, th.C(theme.Error)))
		}
		widget.Wrap(c, "about", "Kept in Oriel's secrets file, readable only by you. Plugins never see it: Oriel sends it for a plugin only "+
			"to the host its manifest binds it to.", 480, uiFont(11.5, th.C(theme.FgFaint)))
		c.Row("foot", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center, Main: ui.End, Pad: ui.Insets{T: 6}}, func(c *ui.Ctx) {
			if widget.Button(c, "cancel", "Cancel", widget.ButtonOpts{Key: "esc", Help: secretsHelp}) {
				x.dlg = nil
			}
			if widget.Button(c, "ok", "Save", widget.ButtonOpts{Primary: true, Key: "⌘↵", Help: secretsHelp}) {
				x.save()
			}
		})
	})
	if !open {
		x.dlg = nil
	}
}
