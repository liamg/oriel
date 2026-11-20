package mirror

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// defaultReaction is what a comment with no reactions offers at a click: an acknowledgement.
const defaultReaction = "👍"

// reactCommand toggles the user's reaction on a mirrored comment.
func (x *Mirror) reactCommand(inFront func(registry.Args) bool) registry.Command {
	return registry.Command{ID: "review.reaction.toggle", Grant: registry.Open,
		Title: "React to a mirrored comment on its code host as you, or take your reaction back: {thread, comment, emoji} (👍 by default)", Available: inFront,
		Args: []registry.Arg{registry.StringArg("comment"), registry.StringArg("emoji"), registry.StringArg("id"), registry.StringArg("thread")},
		Run:  func(_ registry.Ctx, a registry.Args) error { return x.react(a) }}
}

// reactKey names a reaction being sent: a comment's and an emoji's.
func reactKey(comment, emoji string) string { return comment + " " + emoji }

// react sends the user's reaction to its code host off the loop, or takes it back, then syncs the session so the
// comment shows what the host has. Until then the comment shows the user's last choice. A click while one is being
// sent only changes that choice; the sending sends it next if the host doesn't have it by then.
func (x *Mirror) react(a registry.Args) error {
	se, ok := x.session(a)
	if !ok {
		return errors.New("no session")
	}
	t, ok := se.Thread(core.ThreadID(a.String("thread")))
	if !ok || t.Hosted == nil {
		return errors.New("this thread isn't from a code host")
	}
	i := slices.IndexFunc(t.Comments, func(c core.Comment) bool { return c.ID == a.String("comment") })
	if i < 0 || t.Comments[i].HostID == "" {
		return errors.New("that comment isn't on its code host")
	}
	cm, emoji := t.Comments[i], cmp.Or(a.String("emoji"), defaultReaction)
	p, err := x.prOf(se.ID, t.Hosted)
	if err != nil {
		return err
	}
	// One already on the comment is one the host takes, though its picker may not offer it.
	present := slices.ContainsFunc(cm.Reactions, func(r core.Reaction) bool { return r.Emoji == emoji })
	if p.host.ReactNote == nil || !slices.Contains(p.host.Reactions, emoji) && !present {
		return fmt.Errorf("%s can't take a %s reaction from Oriel", p.host.Label(), emoji)
	}
	had := slices.ContainsFunc(cm.Reactions, func(r core.Reaction) bool { return r.Emoji == emoji && r.Mine })
	key := reactKey(cm.ID, emoji)
	x.mu.Lock()
	if want, sending := x.reacting[key]; sending {
		x.reacting[key] = !want
		x.mu.Unlock()
		return nil
	}
	x.reacting[key] = !had
	x.mu.Unlock()
	x.busy.Go(func() {
		err := x.sendReaction(key, had, func(on bool) error {
			ctx, cancel := context.WithTimeout(x.ctx, time.Minute)
			defer cancel()
			return registry.RecoverAsError(func() error { return p.host.ReactNote(ctx, p.repo, p.pr, hostThread(t), cm.HostID, emoji, on) })
		}, func() { x.syncNow(se.ID) })
		if err != nil {
			kit.NotifyFailure(x.h, core.Notice{ID: "review.react", Source: "mirror", Title: "React on " + p.host.Label()}, err)
		}
		x.h.Redraw()
	})
	return nil
}

// sendReaction sends the user's choice for key until the host has it (had: what it has now), then syncs. A click
// meanwhile changes the choice; one that undoes the last needs nothing sent. It's done with key when the host has
// the user's last choice, or when sending fails.
func (x *Mirror) sendReaction(key string, had bool, send func(on bool) error, sync func()) error {
	for {
		x.mu.Lock()
		want := x.reacting[key]
		x.mu.Unlock()
		if want != had {
			if err := send(want); err != nil {
				x.mu.Lock()
				delete(x.reacting, key)
				x.mu.Unlock()
				return err
			}
			had = want
		}
		sync()
		x.mu.Lock()
		if x.reacting[key] == had {
			delete(x.reacting, key)
			x.mu.Unlock()
			return nil
		}
		x.mu.Unlock()
	}
}

// syncNow syncs the session fresh and waits for that sync.
func (x *Mirror) syncNow(id core.SessionID) {
	x.sync(id, true)
	x.mu.Lock()
	done := x.synced[id]
	x.mu.Unlock()
	if done != nil {
		<-done
	}
}

// shown are a comment's reactions as the host last had them, with those being sent applied.
func (x *Mirror) shown(cm core.Comment) []core.Reaction {
	out := slices.Clone(cm.Reactions)
	x.mu.Lock()
	defer x.mu.Unlock()
	for key, on := range x.reacting {
		emoji, ok := strings.CutPrefix(key, cm.ID+" ")
		if !ok {
			continue
		}
		i := slices.IndexFunc(out, func(r core.Reaction) bool { return r.Emoji == emoji })
		switch {
		case i < 0 && on:
			out = append(out, core.Reaction{Emoji: emoji, Count: 1, Mine: true})
		case i >= 0 && on && !out[i].Mine:
			out[i].Count, out[i].Mine, out[i].Who = out[i].Count+1, true, nil
		case i >= 0 && !on && out[i].Mine:
			out[i].Count, out[i].Mine, out[i].Who = out[i].Count-1, false, nil
		}
	}
	// A reaction being taken back stays, at 0, so a second click can put it back.
	return slices.DeleteFunc(out, func(r core.Reaction) bool {
		_, sending := x.reacting[reactKey(cm.ID, r.Emoji)]
		return r.Count <= 0 && !sending
	})
}

// reactions is the row under a mirrored comment: its reactions, each a toggle lit when it's the user's, 👍 when it
// has none, and a picker of the host's every reaction.
func (x *Mirror) reactions(c *ui.Ctx, args registry.Args, t core.Thread, cm core.Comment) {
	h, ok := registry.HostByName(x.h, t.Hosted.Host)
	if !ok || h.ReactNote == nil || len(h.Reactions) == 0 || cm.HostID == "" {
		return
	}
	shown := x.shown(cm)
	who := "the comment"
	if cm.Name != "" {
		who = cm.Name + "'s comment"
	}
	toggle := func(emoji string) {
		a := registry.Args{"emoji": emoji}
		for k, v := range args {
			a[k] = v
		}
		x.run("review.reaction.toggle", a)
	}
	c.Row("reactions", ui.Style{Gap: 6, Cross: ui.Center, Pad: ui.Insets{T: 6}}, func(c *ui.Ctx) {
		for _, r := range shown {
			label := fmt.Sprintf("%s %d", r.Emoji, r.Count)
			tip := "React with " + r.Emoji
			if r.Mine {
				tip = "Take your " + r.Emoji + " back"
			}
			if who := reactors(r); who != "" {
				tip = who + " · " + tip
			}
			if widget.ToggleChip(c, r.Emoji, label, r.Mine, widget.ToggleChipOpts{Label: label + " on " + who, Tooltip: tip, Help: "git/github#reactions"}) != r.Mine {
				toggle(r.Emoji)
			}
		}
		if len(shown) == 0 && slices.Contains(h.Reactions, defaultReaction) {
			if widget.ToggleChip(c, "default", defaultReaction, false, widget.ToggleChipOpts{Label: "React " + defaultReaction + " to " + who,
				Tooltip: "React with " + defaultReaction, Help: "git/github#reactions"}) {
				toggle(defaultReaction)
			}
		}
		items := make([]widget.MenuItem, len(h.Reactions))
		for i, e := range h.Reactions {
			items[i] = widget.MenuItem{Label: e, Checkable: true, Checked: slices.ContainsFunc(shown, func(r core.Reaction) bool { return r.Emoji == e && r.Mine })}
		}
		if i := widget.MenuButton(c, "pick", items, widget.MenuButtonOpts{Label: "Add a reaction to " + who, Glyph: "☺", Size: 18, Width: 120,
			Help: "git/github#reactions"}); i >= 0 {
			toggle(h.Reactions[i])
		}
	})
}

// reactors names who reacted with r: "bo, priya-k and 3 more", as a host may name only the first few. A reaction
// being sent names no one: the host hasn't said who has it yet.
func reactors(r core.Reaction) string {
	if len(r.Who) == 0 {
		return ""
	}
	names := strings.Join(r.Who, ", ")
	if more := r.Count - len(r.Who); more > 0 {
		return fmt.Sprintf("%s and %d more", names, more)
	}
	return names
}

func coreReactions(rs []registry.PRReaction) []core.Reaction {
	var out []core.Reaction
	for _, r := range rs {
		out = append(out, core.Reaction{Emoji: r.Emoji, Count: r.Count, Mine: r.Mine, Who: r.Who})
	}
	return out
}
