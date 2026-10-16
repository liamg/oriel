package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/integ/slack"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/plugin/hosttest"
	"github.com/liamg/oriel/registry"
)

const slackHook = "https://hooks.slack.com/services/T000/B000/XXXXSECRETXXXX"

// webhook is a fake Slack incoming webhook: what it was sent, and what it answers.
type webhook struct {
	mu     sync.Mutex
	posts  []map[string]any
	hosts  []string
	status int
	reply  string
}

func (w *webhook) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	var p map[string]any
	json.Unmarshal(b, &p)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.posts = append(w.posts, p)
	w.hosts = append(w.hosts, r.Host+r.URL.Path)
	rw.WriteHeader(w.status)
	rw.Write([]byte(w.reply))
}

func (w *webhook) sent() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.posts)
}

func (w *webhook) last() (map[string]any, string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.posts[len(w.posts)-1], w.hosts[len(w.hosts)-1]
}

func (w *webhook) answer(status int, reply string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status, w.reply = status, reply
}

// noticeWith waits for a notice whose text has want.
func (s *errScene) noticeWith(t *testing.T, want string) core.Notification {
	t.Helper()
	var got core.Notification
	s.until(t, "a notice with "+want, func() bool {
		for _, n := range s.store.State().Notifications {
			if strings.Contains(n.Text, want) {
				got = n
				return true
			}
		}
		return false
	})
	return got
}

// slackScene is 18 with web-dashboard's PR at its URL, the oriel/slack plugin running, and a fake webhook answering
// for hooks.slack.com.
func slackScene(t *testing.T) (*errScene, core.SessionState, *webhook, string) {
	t.Helper()
	hook := &webhook{status: 200, reply: "ok"}
	srv := httptest.NewTLSServer(hook)
	t.Cleanup(srv.Close)
	s, se := mockup18(t)
	secrets := filepath.Join(t.TempDir(), "secrets.json")
	s.sh.cfg.Net, s.sh.cfg.Secrets = hosttest.Net(srv), &plugin.SecretFile{Path: secrets}
	s.ship().reports[se.ID].ships[1].PR.URL = "https://github.com/acme/web-dashboard/pull/812"
	p := ScriptPlugin(slack.Files)
	if p.Manifest().ID != "oriel/slack" {
		t.Fatalf("the manifest: %+v", p.Manifest())
	}
	if err := p.Activate(context.Background(), s.sh.host(p.Manifest().ID)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Deactivate(context.Background()) })
	s.run("ship.open", nil)
	s.settleGit(t)
	return s, se, hook, secrets
}

// PL-9, crit-reference.md → Slack: "A 'PTAL' button posting a review request through an incoming webhook", built as
// a third party would build it (plugins.md, principle 0): a Lua plugin adding a PR action, whose post goes through
// http.request with the webhook a secret it names and never sees (security.md, "Secrets never enter the plugin").
// What it posts is crit's (internal/slack): "PTAL: *title* — <url|owner/name#n>", then "Bump! …", "2nd Bump! …".
func TestSlackPTAL(t *testing.T) {
	s, se, hook, secrets := slackScene(t)
	s.wantFrame(t, "#812 · feat(search): back off on 429 using Retry-After", "Slack")
	s.lint("the Ship page with the Slack button")

	// No webhook yet: the notice says how to set one, and nothing is sent.
	s.click(centre(s.access("Slack").Bounds))
	s.noticeWith(t, "SLACK_WEBHOOK isn't set: run :Secret SLACK_WEBHOOK")
	if hook.sent() != 0 {
		t.Fatal("posted with no webhook")
	}

	// Set it in the secret's dialog: a field that shows dots, kept in the secrets file.
	s.run("slack.webhook", nil)
	s.frame()
	s.wantFrame(t, "Set SLACK_WEBHOOK", "not set yet")
	s.lint("the secret dialog")
	for _, r := range slackHook {
		s.typeKeys(string(r))
	}
	if v := s.access("SLACK_WEBHOOK").Value; v != strings.Repeat("•", len(slackHook)) || strings.Contains(s.frameText(), "XXXXSECRET") {
		t.Errorf("the field shows %q", v)
	}
	s.typeKeys("CR")
	if b, err := os.ReadFile(secrets); err != nil || !strings.Contains(string(b), slackHook) {
		t.Fatalf("the secrets file: %s %v", b, err)
	}
	if fi, _ := os.Stat(secrets); fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("the secrets file is %v", fi.Mode().Perm())
	}

	// PTAL.
	s.click(centre(s.access("Slack").Bounds))
	s.noticeWith(t, "Posted to Slack")
	p, at := hook.last()
	if at != "hooks.slack.com/services/T000/B000/XXXXSECRETXXXX" ||
		p["text"] != "PTAL: *feat(search): back off on 429 using Retry-After* — <https://github.com/acme/web-dashboard/pull/812|acme/web-dashboard#812>" {
		t.Errorf("posted %q to %s", p["text"], at)
	}
	for _, k := range []string{"username", "icon_emoji", "channel"} {
		if _, ok := p[k]; ok {
			t.Errorf("%s was sent: the webhook's own should apply", k)
		}
	}

	// Again: a bump, its button counting them.
	s.wantFrame(t, "Bump Slack")
	s.click(centre(s.access("Bump Slack").Bounds))
	s.noticeWith(t, "Bump 1 posted to Slack")
	if p, _ := hook.last(); !strings.HasPrefix(p["text"].(string), "Bump! PTAL: *feat(search)") {
		t.Errorf("the first bump: %q", p["text"])
	}
	s.click(centre(s.access("Bump 2").Bounds))
	s.noticeWith(t, "Bump 2 posted to Slack")
	if p, _ := hook.last(); !strings.HasPrefix(p["text"].(string), "2nd Bump! PTAL:") {
		t.Errorf("the second bump: %q", p["text"])
	}

	// Slack's own reason for refusing, which doesn't count as a post.
	hook.answer(http.StatusNotFound, "channel_not_found")
	s.click(centre(s.access("Bump 3").Bounds))
	s.noticeWith(t, "Slack refused the message: channel_not_found")
	s.wantFrame(t, "Bump 3")

	// The pull request panel has it too.
	s.run("project.open", registry.Args{"project": "web-dashboard"})
	s.run("project.panel", registry.Args{"name": "pull-request"})
	s.frame()
	s.wantFrame(t, "PR open", "Bump 3")

	// The webhook never reached a notice or the log, which saw it handed out.
	for _, n := range s.store.State().Notifications {
		if strings.Contains(n.Text+n.Detail, "XXXXSECRET") {
			t.Errorf("a notice shows the webhook: %q", n.Text)
		}
	}
	if got := s.sh.cfg.Logs.Redact.Redact("posting to " + slackHook); strings.Contains(got, "XXXXSECRET") {
		t.Errorf("the log would show it: %s", got)
	}

	// A closed PR has no button.
	s.ship().reports[se.ID].ships[1].PR.State = "closed"
	s.frame()
	if strings.Contains(s.frameText(), "Bump 3") {
		t.Error("a closed PR has the Slack button")
	}
}

// security.md: the webhook is bound to hooks.slack.com. Set to anywhere else, the host refuses to send it, and
// says why without showing it.
func TestSlackWebhookBoundToItsHost(t *testing.T) {
	s, _, hook, _ := slackScene(t)
	s.sh.secretStore().SetSecret("SLACK_WEBHOOK", "https://evil.example/services/XXXXSECRETXXXX")
	s.click(centre(s.access("Slack").Bounds))
	n := s.noticeWith(t, "Slack: ")
	if !strings.Contains(n.Text, `"net" capability isn't declared for evil.example`) || strings.Contains(n.Text, "XXXXSECRET") {
		t.Errorf("the notice: %q", n.Text)
	}
	if hook.sent() != 0 {
		t.Error("the webhook was sent to another host")
	}
}

// crit's message (internal/slack's PRText, BumpText, normalizeEmoji and normalizeChannel), through slack.ptal's
// args: a title's Slack markup escaped and cut to 160 characters, a URL that isn't a PR's in angle brackets, and a
// channel, emoji and name of its own.
func TestSlackPTALMessage(t *testing.T) {
	s, _, hook, _ := slackScene(t)
	s.sh.secretStore().SetSecret("SLACK_WEBHOOK", slackHook)
	title := "fix <script> & “quotes” " + strings.Repeat("é", 200)
	s.run("slack.ptal", registry.Args{"url": "https://example.com/review/7", "title": title, "channel": "prs", "emoji": "eyes", "username": "oriel"})
	s.noticeWith(t, "Posted to #prs")
	p, _ := hook.last()
	want := "PTAL: *fix &lt;script&gt; &amp; “quotes” " + strings.Repeat("é", 160-len([]rune("fix <script> & “quotes” "))) + "…* — <https://example.com/review/7>"
	if p["text"] != want || p["channel"] != "#prs" || p["icon_emoji"] != ":eyes:" || p["username"] != "oriel" {
		t.Errorf("posted %+v\nwant text %q", p, want)
	}
	s.run("slack.ptal", registry.Args{"url": "https://example.com/review/7", "emoji": "👀"})
	s.noticeWith(t, "Bump 1 posted to Slack")
	if p, _ := hook.last(); p["text"] != "Bump! PTAL: <https://example.com/review/7>" || p["icon_emoji"] != nil {
		t.Errorf("a bump with no title and an emoji character: %+v", p)
	}
}
