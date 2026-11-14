package settings

import (
	"cmp"
	"context"
	"fmt"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// RepoURL is Oriel's home: its source, releases and issues.
const RepoURL = "https://github.com/liamg/oriel"

const (
	aboutHelp  = "shell/about"
	logoBlink  = 530 * time.Millisecond // a text cursor's half period
	aboutWidth = 300
)

// About is the title bar's brand (uireg.SlotTitleBrand): the logo, whose cursor blinks under the pointer,
// and on a click the About card: the version, how updates stand, the report, help and the website.
type About struct {
	kit.Disposer
	Version string // the build's, as the About card shows it
	h       plugin.Host

	open       bool
	hoverSince time.Time // when the pointer came onto the logo; zero while it's elsewhere
}

func (x *About) Manifest() plugin.Manifest { return plugin.Manifest{ID: "about"} }

func (x *About) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	for _, c := range []registry.Command{
		{ID: "about.toggle", Title: "Show the About card under the logo, or put it away", Run: func(registry.Ctx, registry.Args) error {
			x.open = !x.open
			return nil
		}},
		{ID: "about.close", Title: "Put the About card away", Run: func(registry.Ctx, registry.Args) error { x.open = false; return nil }},
		{ID: "about.website", Title: "Open Oriel's website in the browser", Run: func(registry.Ctx, registry.Args) error {
			return kit.OpenURL(x.h, RepoURL)
		}},
	} {
		c.Source = "builtin:about"
		x.Add(h.Command(c))
	}
	x.Add(h.Contribute(uireg.Indicator{Name: "about", Slot: uireg.SlotTitleBrand, Global: true, Help: aboutHelp,
		Build: func(registry.Ctx) (ui.Node, error) { return &ui.Func{Key: "about", Fn: x.brand}, nil }}))
	return nil
}

// brand is the logo and wordmark, and the About card under them while it's open.
func (x *About) brand(c *ui.Ctx) {
	c.Row("about", ui.Style{H: ui.Flex(1), Cross: ui.Center}, func(c *ui.Ctx) {
		th := c.Theme()
		id := c.Next("brand")
		now := c.Now()
		lit := true
		switch in := c.Interaction(id); {
		case !in.Hovered:
			x.hoverSince = time.Time{}
		case x.hoverSince.IsZero():
			x.hoverSince = now
		case !c.Still() && c.WindowFocused() && c.WindowVisible():
			half := now.Sub(x.hoverSince) / logoBlink
			lit = half%2 == 0
			c.WakeAt(x.hoverSince.Add((half + 1) * logoBlink))
		}
		glow := 1.0
		if !x.hoverSince.IsZero() {
			glow = 1.6
		}
		if widget.Clickable(c, "brand", ui.Style{Dir: ui.Row, Gap: 8, Cross: ui.Center, Pad: ui.PadXY(4, 3), Radius: 4, Label: "Oriel", Tooltip: "About Oriel",
			Help: aboutHelp}, func(c *ui.Ctx) {
			c.Paint("logo", ui.Px(22), ui.Px(22), func(f render.Frame, r render.Rect) { kit.LogoLit(f, r, th, lit, glow) })
			c.Text("word", "ORIEL", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Weight: typeset.Bold, Size: 12, Tracking: .2},
				Color: th.C(kit.TWordmark)})
		}).Clicked {
			x.h.Run(context.Background(), "about.toggle", nil)
		}
		widget.Popover(c, "card", id, &x.open, widget.PopoverOpts{Label: "About Oriel", Width: aboutWidth, Help: aboutHelp}, x.card)
	})
}

// card is the About card's body.
func (x *About) card(c *ui.Ctx) {
	th := c.Theme()
	run := func(command string) {
		if err := x.h.Run(context.Background(), command, nil); err != nil {
			kit.NotifyError(x.h, "about", err, true)
		}
	}
	c.Row("head", ui.Style{W: ui.Flex(1), Gap: 12, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Paint("logo", ui.Px(36), ui.Px(36), func(f render.Frame, r render.Rect) { kit.Logo(f, r, th) })
		c.Column("name", ui.Style{W: ui.Flex(1), Gap: 3}, func(c *ui.Ctx) {
			c.Text("word", "ORIEL", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Weight: typeset.Bold, Size: 18, Tracking: .24},
				Color: th.C(kit.TWordmark)})
			c.Text("version", cmp.Or(x.Version, "dev build"), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11}, Color: th.C(theme.FgFaint)})
		})
		if widget.IconButton(c, "close", "✕", widget.IconButtonOpts{Label: "Close", Help: aboutHelp, Size: 18}) {
			x.open = false
		}
	})
	status, tone := "Up to date", th.C(theme.FgMuted)
	switch {
	case !x.h.Available("update.check", nil):
		status = "Updates are off in this build"
	case x.h.Available("update.restart", nil):
		status, tone = "An update is installed: restart to use it", th.C(theme.Accent)
	case x.h.Available("update.install", nil):
		status, tone = "A newer version is out", th.C(theme.Accent)
	}
	c.Text("status", status, kit.UIFont(12, tone))
	c.Row("updates", ui.Style{W: ui.Flex(1), Gap: 6, Wrap: true}, func(c *ui.Ctx) {
		switch {
		case x.h.Available("update.restart", nil):
			if widget.Button(c, "restart", "Restart", widget.ButtonOpts{Primary: true, Help: "shell/update"}) {
				run("update.restart")
			}
		case x.h.Available("update.install", nil):
			if widget.Button(c, "notes", "What's new", widget.ButtonOpts{Primary: true, Help: "shell/update"}) {
				x.open = false
				run("update.notes")
			}
		}
		if x.h.Available("update.check", nil) && widget.Button(c, "check", "Check for updates", widget.ButtonOpts{Help: "shell/update#check"}) {
			run("update.check")
		}
	})
	widget.Rule(c, "rule")
	c.Row("links", ui.Style{W: ui.Flex(1), Gap: 6, Wrap: true}, func(c *ui.Ctx) {
		if widget.Button(c, "report", "Copy report", widget.ButtonOpts{Ghost: true, Help: "errors#log"}) {
			run("log.copy_report")
			x.h.Notify(core.Notice{ID: "about.report", Severity: core.SevInfo, Source: "about", AutoClear: true,
				Text: fmt.Sprintf("Report copied: paste it into an issue at %s/issues", RepoURL)})
		}
		if widget.Button(c, "help", "Help", widget.ButtonOpts{Ghost: true, Help: aboutHelp}) {
			x.open = false
			x.h.Run(context.Background(), "help.open", registry.Args{"id": "shell/help"})
		}
		if widget.Button(c, "website", "Website", widget.ButtonOpts{Ghost: true, Help: aboutHelp}) {
			run("about.website")
		}
	})
}
