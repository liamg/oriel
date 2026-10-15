package app

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/ext"
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

func (h *host) Net() *http.Client {
	if h.sh.cfg.Net != nil {
		return h.sh.cfg.Net
	}
	return http.DefaultClient
}

func (h *host) Secrets() ext.Secrets { return h.sh.secretStore() }

// secretStore is the config's secrets, or secrets.json in the state directory; each value it hands out is
// redacted from the logs from then on (errors.md).
func (sh *shell) secretStore() ext.Secrets {
	sh.secretsOnce.Do(func() {
		s := sh.cfg.Secrets
		if s == nil {
			dir := sh.cfg.StateDir
			if dir == "" {
				dir = logs.StateDir()
			}
			s = &ext.SecretFile{Path: filepath.Join(dir, "secrets.json")}
		}
		var r *logs.Redactor
		if sh.cfg.Logs != nil {
			r = sh.cfg.Logs.Redact
		}
		sh.secrets = redacted{s, r}
	})
	return sh.secrets
}

type redacted struct {
	ext.Secrets
	r *logs.Redactor
}

func (s redacted) Secret(name string) (string, bool) {
	v, ok := s.Secrets.Secret(name)
	if ok && s.r != nil {
		s.r.Add(v)
	}
	return v, ok
}

// secretsExt sets the secrets plugins name (security.md): only you, through its dialog, and never shown back.
type secretsExt struct {
	disposer
	sh  *shell
	h   ext.Host
	dlg *secretDialog
}

type secretDialog struct {
	name, value string
	set         bool // it has a value already
	focus       bool
	err         string
}

func (x *secretsExt) Manifest() ext.Manifest { return ext.Manifest{ID: "secrets"} }

func (x *secretsExt) Activate(_ context.Context, h ext.Host) error {
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
	return nil
}

func (x *secretsExt) open(name string) error {
	if name == "" || strings.ContainsAny(name, " \t@") {
		return fmt.Errorf("a secret's name is one word, such as SLACK_WEBHOOK")
	}
	_, set := x.sh.secretStore().Secret(name)
	x.dlg = &secretDialog{name: name, set: set, focus: true}
	x.h.Redraw()
	return nil
}

func (x *secretsExt) save() {
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

func (x *secretsExt) dialog(c *ui.Ctx) {
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
			c.Focus(c.Next("value"))
			d.focus = false
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
