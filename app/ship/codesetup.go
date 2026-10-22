package ship

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// The setup dialog: which code host a server is, when nothing else could tell,
// and the site's API URL and token. What's set is kept by hostname (code.hosts, code.api, the token's secret), for
// every repo on the server.

const codeSetupHelp = "git/ship#hosts"

// SetupKeyMode is the setup dialog's keys.
const SetupKeyMode = "code.setup"

// siteAsk is the setup dialog.
type siteAsk struct {
	hostname string
	hosts    []registry.CodeHost // to pick from; one when the host is known
	pick     int
	api      string
	token    string
	err      string
	focus    bool
}

// chosen is the host picked, false for "none of these".
func (a *siteAsk) chosen() (registry.CodeHost, bool) {
	if a.pick < len(a.hosts) {
		return a.hosts[a.pick], true
	}
	return registry.CodeHost{}, false
}

// site is the chosen host's site at the hostname: its public one, else one set up with the API typed.
func (a *siteAsk) site() (registry.HostSite, bool) {
	h, ok := a.chosen()
	if !ok {
		return registry.HostSite{}, false
	}
	if s, ok := h.PublicSite(a.hostname); ok {
		return registry.HostSite{CodeHost: h, Site: s}, true
	}
	return registry.HostSite{CodeHost: h, Site: h.Setup.For(a.hostname, strings.TrimSpace(a.api))}, true
}

func (x *CodeSites) activateSetup(h plugin.Host) {
	open := func(registry.Args) bool { return x.ask != nil }
	for _, c := range []registry.Command{
		{ID: "code.setup", Title: "Set up a code host's server: {hostname}, as {code_host} if known; asks which host it is, its API URL and its token",
			Run: func(_ registry.Ctx, a registry.Args) error {
				return x.openSetup(a.String("hostname"), a.String("code_host"))
			}},
		{ID: "code.setup.save", Title: "Save the code host setup dialog", Available: open,
			Run: func(registry.Ctx, registry.Args) error { return x.saveSetup() }},
		{ID: "code.setup.cancel", Title: "Close the code host setup dialog", Available: open,
			Run: func(registry.Ctx, registry.Args) error { x.ask = nil; return nil }},
	} {
		x.Add(h.Command(c))
	}
	x.Add(h.Keymap(registry.Keymap{Mode: SetupKeyMode, Keys: "<Esc>", Command: "code.setup.cancel", Desc: "Cancel"}))
	x.Add(h.View(kit.OverlayPrefix+"code.setup", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "code.setup", Fn: x.setupView}
	})))
}

func (x *CodeSites) openSetup(hostname, name string) error {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if hostname == "" {
		return errors.New("set up which server? Give its {hostname}")
	}
	a := &siteAsk{hostname: hostname, focus: true}
	if name == "" {
		name, _ = kit.SitesOf(x.h).Named(hostname)
	}
	if name != "" {
		h, ok := registry.HostByName(x.h, name)
		if !ok {
			return fmt.Errorf("no code host %q", name)
		}
		a.hosts = []registry.CodeHost{h}
	} else {
		for _, h := range registry.CodeHosts(x.h) {
			if _, public := h.PublicSite(hostname); public || h.Setup.API != "" || h.Setup.TokenHelp != "" {
				a.hosts = append(a.hosts, h)
			}
		}
		if len(a.hosts) == 0 {
			return errors.New("no code host is installed")
		}
	}
	if hs, ok := a.site(); ok {
		a.api = hs.Site.API
	}
	x.ask = a
	return nil
}

// saveSetup keeps the dialog's answers by hostname, and its token as the site's secret.
func (x *CodeSites) saveSetup() error {
	a := x.ask
	if a == nil {
		return errors.New("no code host is being set up")
	}
	hs, ok := a.site()
	if !ok {
		if err := kit.SitesOf(x.h).Remember(a.hostname, "none", ""); err != nil {
			return err
		}
		x.ask = nil
		return nil
	}
	if _, public := hs.PublicSite(a.hostname); !public {
		if err := kit.SitesOf(x.h).Remember(a.hostname, hs.Name, hs.Site.API); err != nil {
			a.err = err.Error()
			return err
		}
	}
	if t := strings.TrimSpace(a.token); t != "" && hs.Site.Secret != "" {
		if err := x.h.Secrets().SetSecret(hs.Site.Secret, t); err != nil {
			a.err = err.Error()
			return err
		}
	} else if !kit.SitesOf(x.h).Keyed(hs) {
		a.err = "paste the token first"
		return errors.New(a.err)
	}
	x.ask = nil
	x.h.Notify(core.Notice{ID: "code.setup", Severity: core.SevInfo, Title: hs.Label(), Text: a.hostname + " is set up"})
	for _, c := range []string{"ship.refresh", "reviews.refresh"} {
		if x.h.Available(c, nil) {
			x.h.Run(context.Background(), c, nil)
		}
	}
	return nil
}

func (x *CodeSites) setupView(c *ui.Ctx) {
	a := x.ask
	if a == nil {
		return
	}
	th := c.Theme()
	open := true
	widget.Modal(c, "codesetup", &open, "Set up "+a.hostname, widget.ModalOpts{Width: 540, Help: codeSetupHelp}, func(c *ui.Ctx) {
		if len(a.hosts) > 1 {
			widget.Wrap(c, "which", "Which code host is "+a.hostname+"?", 500, kit.UIFont(13, th.C(theme.Fg)))
			labels := make([]string, 0, len(a.hosts)+1)
			for _, h := range a.hosts {
				labels = append(labels, h.Label())
			}
			before := a.pick
			widget.Segmented(c, "hosts", append(labels, "None of these"), &a.pick, widget.SegmentedOpts{Label: "Code host", Help: codeSetupHelp})
			if a.pick != before {
				if hs, ok := a.site(); ok {
					a.api = hs.Site.API
				}
			}
		}
		hs, ok := a.site()
		if !ok {
			widget.Wrap(c, "none", "Oriel won't ask again; its pull request steps stay off for "+a.hostname+".", 500, kit.UIFont(12, th.C(theme.FgMuted)))
		} else {
			_, public := hs.PublicSite(a.hostname)
			if !public && hs.Setup.API != "" {
				kit.Heading(c, "api", "API URL", "")
				widget.Input(c, "api", &a.api, widget.InputOpts{Label: "API URL", Help: codeSetupHelp, KeyMode: SetupKeyMode})
			}
			if help := strings.ReplaceAll(hs.Setup.TokenHelp, "{host}", a.hostname); help != "" {
				widget.Wrap(c, "help", help, 500, kit.UIFont(12, th.C(theme.FgMuted)))
			}
			if hs.Site.Secret != "" {
				note := "not set yet"
				if kit.SitesOf(x.h).Keyed(hs) {
					note = "set: this replaces it"
				}
				kit.Heading(c, "token", "Token", note)
				if a.focus {
					c.RequestFocus(c.Next("token"))
					a.focus = false
				}
				res := widget.Input(c, "token", &a.token, widget.InputOpts{Label: hs.Site.Secret, Placeholder: "paste it here", Secret: true,
					Help: codeSetupHelp, KeyMode: SetupKeyMode, Invalid: a.err != ""})
				if res.Submitted {
					x.saveSetup()
				}
				if page := hs.Setup.TokenPage(a.hostname); page != "" && widget.Button(c, "make", "Create a token ↗", widget.ButtonOpts{Ghost: true, Help: codeSetupHelp}) {
					if err := kit.OpenURL(x.h, page); err != nil {
						a.err = err.Error()
					}
				}
				widget.Wrap(c, "about", "Kept as "+hs.Site.Secret+" in Oriel's secrets, and sent only to "+a.hostname+", by the "+hs.Label()+
					" plugin.", 500, kit.UIFont(11.5, th.C(theme.FgFaint)))
			}
		}
		if a.err != "" {
			widget.Wrap(c, "err", a.err, 500, kit.MonoFont(11, th.C(theme.Error)))
		}
		c.Row("foot", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center, Main: ui.End, Pad: ui.Insets{T: 6}}, func(c *ui.Ctx) {
			if widget.Button(c, "cancel", "Cancel", widget.ButtonOpts{Key: "esc", Help: codeSetupHelp}) {
				x.ask = nil
			}
			if widget.Button(c, "ok", "Save", widget.ButtonOpts{Primary: true, Key: "↵", Help: codeSetupHelp}) {
				x.saveSetup()
			}
		})
	})
	if !open {
		x.ask = nil
	}
}
