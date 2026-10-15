// Package agentbus connects Oriel to agentbus as one harness instance (crit's internal/agentbus): it takes the
// questions and review requests for this person, holds them for the inbox, sends the replies, and follows the
// review requests this person sent. It runs in the plugin's process, oriel-agentbus (cmd/oriel-agentbus), which the
// plugin's Lua (plugin/init.lua) drives over JSON-RPC (Serve).
package agentbus

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Item is one open question or review request, as the inbox shows it.
type Item struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"` // "ask" | "review"
	From     From      `json:"from"`
	Received time.Time `json:"received"`
	// Created is when the asker sent it; Received when it reached this instance.
	Created time.Time `json:"created"`
	Ask     *AskBody  `json:"ask,omitempty"`
	Review  *Review   `json:"review,omitempty"`
	// Deadline is when an unanswered ask gets an answer from this person's agent.
	Deadline *time.Time `json:"deadline,omitempty"`
	// Hidden is set by Later or Ignore: kept, but not shown as a card.
	Hidden bool `json:"hidden,omitempty"`
	// Claimed is set once this instance claimed the review.
	Claimed bool `json:"claimed,omitempty"`
	// Checking is set while Config.Vet decides on a review: held, not shown.
	Checking bool `json:"checking,omitempty"`
}

// AutoApproval is a review request approved by a rule.
type AutoApproval struct {
	ID   string    `json:"id"`
	PR   string    `json:"pr"`
	Repo string    `json:"repo"`
	Rule string    `json:"rule"`
	From From      `json:"from"`
	At   time.Time `json:"at"`
}

// Review is a review request with the pull request's details.
type Review struct {
	ReviewRequestBody
	Repo string `json:"repo"`
	PR   PRInfo `json:"info"`
}

// PRInfo is what the card shows of a pull request.
type PRInfo struct {
	Title     string `json:"title,omitempty"`
	Author    string `json:"author,omitempty"`
	Base      string `json:"base,omitempty"`
	Additions int    `json:"additions,omitempty"`
	Deletions int    `json:"deletions,omitempty"`
	Files     int    `json:"files,omitempty"`
}

// Settings are what this person takes.
type Settings struct {
	Asks    bool
	Reviews bool
	// Repos are the owner/name globs review requests are taken for.
	Repos []string
	// Presence is active, idle or dnd; empty is active.
	Presence string
}

// Config wires the instance to the rest of the process.
type Config struct {
	Instance Instance
	URL      string      // where the bus is; ResolveURL's when empty
	Header   http.Header // sent with the handshake
	// PRInfo fetches a pull request's details; may be nil.
	PRInfo func(ctx context.Context, prURL string) (PRInfo, error)
	// OnChange is called when the inbox, the status or the log changes.
	OnChange func()
	// LogPath is the JSON-lines file the log is kept in; empty keeps it in memory.
	LogPath string
	// Vet is offered each new review request before it's shown. It reports whether it dealt with the request; if
	// not, the card shows. May be nil.
	Vet func(ctx context.Context, it Item) bool
	// Watch is how often the requests this instance sent are read back, for the replies a push didn't bring;
	// RequestWatch if 0.
	Watch time.Duration
}

// AutoAnswerMargin is how long before an ask's answer_by this person's agent answers.
const AutoAnswerMargin = 30 * time.Second

// MinAnswerWindow is the shortest window, to answer_by or to the TTL's end, the agent answers inside: a shorter one
// leaves the human no time.
const MinAnswerWindow = time.Minute

// Bus is one instance on agentbus.
type Bus struct {
	cfg Config

	mu       sync.Mutex
	c        *Client
	settings Settings
	// sent is what the bus was last told this instance accepts, and sentPresence the presence.
	sent         *Accepts
	sentPresence string
	items        map[string]*Item
	auto         []AutoApproval
	// done is ids answered, claimed elsewhere or cancelled: offers can repeat.
	done map[string]time.Time
	// out is the review requests this instance sent and still follows.
	out map[string]*outgoing
	err string

	logMu  sync.Mutex
	logN   int
	memLog []LogEntry
}

func New(cfg Config) *Bus {
	return &Bus{cfg: cfg, items: map[string]*Item{}, done: map[string]time.Time{}, out: map[string]*outgoing{}}
}

// Run keeps the connection up until ctx ends.
func (b *Bus) Run(ctx context.Context) {
	for ctx.Err() == nil {
		url, err := ResolveURL(ctx, b.cfg.URL)
		if err != nil {
			b.setErr(err)
			sleep(ctx, time.Minute)
			continue
		}
		err = Run(ctx, Options{URL: url, Instance: b.cfg.Instance, Accepts: b.accepts(), Presence: b.presence(), Header: b.cfg.Header,
			OnFrame: b.onFrame}, b.serve)
		if ctx.Err() != nil {
			return
		}
		b.setErr(err)
		sleep(ctx, time.Minute)
	}
}

func (b *Bus) serve(ctx context.Context, c *Client) error {
	b.mu.Lock()
	b.c, b.err = c, ""
	b.sent, b.sentPresence = nil, ""
	b.mu.Unlock()
	log.Printf("agentbus: connected as %s", c.You().Instance.ID)
	b.changed()
	// Settings may have changed between dialling and now.
	b.pushAccepts(ctx)
	go b.watchRequests(ctx)
	defer func() {
		b.mu.Lock()
		b.c = nil
		b.mu.Unlock()
		b.changed()
	}()
	<-ctx.Done()
	return c.Err()
}

func (b *Bus) setErr(err error) {
	if err == nil {
		return
	}
	b.mu.Lock()
	changed := b.err != err.Error()
	b.err = err.Error()
	b.mu.Unlock()
	if changed {
		log.Printf("agentbus: %v", err)
		b.changed()
	}
}

// Status is the connection as the plugin shows it.
type Status struct {
	Connected bool   `json:"connected"`
	Instance  string `json:"instance,omitempty"`
	User      string `json:"user,omitempty"`
	Presence  string `json:"presence"`
	Error     string `json:"error,omitempty"`
}

func (b *Bus) Status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := Status{Error: b.err, Presence: presenceOf(b.settings)}
	if b.c != nil {
		you := b.c.You()
		st.Connected, st.User = true, you.User
		if you.Instance != nil {
			st.Instance = you.Instance.ID
		}
	}
	return st
}

func (b *Bus) accepts() *Accepts {
	b.mu.Lock()
	defer b.mu.Unlock()
	return acceptsFor(b.settings)
}

func (b *Bus) presence() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return presenceOf(b.settings)
}

// presenceOf is the settings' presence, active when unset or unknown.
func presenceOf(s Settings) string {
	switch s.Presence {
	case PresenceIdle, PresenceDND:
		return s.Presence
	}
	return PresenceActive
}

func acceptsFor(s Settings) *Accepts {
	a := &Accepts{Ask: s.Asks}
	if s.Reviews && len(s.Repos) > 0 {
		repos := append([]string(nil), s.Repos...)
		sort.Strings(repos)
		a.Review = &ReviewAccepts{Repos: repos}
	}
	return a
}

// SetSettings applies new settings and tells the bus when what this instance accepts, or its presence, has changed.
// Items of a kind turned off are dropped.
func (b *Bus) SetSettings(ctx context.Context, s Settings) {
	b.mu.Lock()
	b.settings = s
	for id, it := range b.items {
		if (it.Kind == "ask" && !s.Asks) || (it.Kind == "review" && !s.Reviews && !it.Claimed) {
			delete(b.items, id)
		}
	}
	b.mu.Unlock()
	b.pushAccepts(ctx)
	b.changed()
}

// pushAccepts tells the bus what this instance accepts and its presence, when that differs from what it was told.
func (b *Bus) pushAccepts(ctx context.Context) {
	b.mu.Lock()
	want, presence := acceptsFor(b.settings), presenceOf(b.settings)
	c, sent, sentPresence := b.c, b.sent, b.sentPresence
	b.mu.Unlock()
	if c == nil || (sent != nil && sameAccepts(sent, want) && sentPresence == presence) {
		return
	}
	if err := c.SetStatus(ctx, presence, want); err != nil {
		log.Printf("agentbus: update status: %v", err)
		return
	}
	b.mu.Lock()
	if b.c == c {
		b.sent, b.sentPresence = want, presence
	}
	b.mu.Unlock()
}

func sameAccepts(a, b *Accepts) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func (b *Bus) changed() {
	if b.cfg.OnChange != nil {
		b.cfg.OnChange()
	}
}

func (b *Bus) onFrame(f Frame) {
	switch f.Type {
	case TypeAsk:
		var body AskBody
		if json.Unmarshal(f.Body, &body) != nil || f.From == nil {
			return
		}
		now := time.Now()
		it := &Item{ID: f.ID, Kind: "ask", From: *f.From, Received: now, Created: now, Ask: &body}
		if t, err := time.Parse(time.RFC3339, f.CreatedAt); err == nil {
			it.Created = t
		}
		if d, ok := deadline(body.AnswerBy, f.CreatedAt, f.TTL); ok {
			it.Deadline = &d
		}
		if cp, ok := b.add(it); ok {
			b.log(cp, EventReceived, body.Question, "")
		}
	case TypeReviewRequest:
		var body ReviewRequestBody
		if json.Unmarshal(f.Body, &body) != nil || f.From == nil {
			return
		}
		_, repo, err := ParsePR(body.PR)
		if err != nil {
			return
		}
		now := time.Now()
		it := &Item{ID: f.ID, Kind: "review", From: *f.From, Received: now, Created: now,
			Review: &Review{ReviewRequestBody: body, Repo: repo}, Checking: b.cfg.Vet != nil}
		if t, err := time.Parse(time.RFC3339, f.CreatedAt); err == nil {
			it.Created = t
		}
		cp, ok := b.add(it)
		if !ok {
			return
		}
		b.log(cp, EventReceived, body.PR, body.Note)
		if b.cfg.PRInfo != nil {
			go b.fillPR(cp.ID, body.PR)
		}
		if b.cfg.Vet != nil {
			go b.vet(cp)
		}
	case TypeReviewClaim, TypeReviewResult:
		var r Request
		if f.From != nil {
			r.ClaimBy = f.From.User
		}
		if f.Type == TypeReviewResult {
			var body ReviewResultBody
			if json.Unmarshal(f.Body, &body) != nil {
				return
			}
			r.Result, r.Summary = body.State, body.Summary
		}
		b.noteRequest(f.ReplyTo, r)
	case TypeState:
		// The bus pushes a sent request's state to its sender as replies come.
		b.noteRequest(f.ID, requestOf(f.ID, f))
	case TypeCancel:
		if it, ok := b.Item(f.ReplyTo); ok {
			b.log(it, EventCancelled, "", "")
		}
		b.remove(f.ReplyTo)
	}
}

// deadline is AutoAnswerMargin before answer_by, else before the ask's TTL ends, whichever first gives a window of at
// least MinAnswerWindow.
func deadline(answerBy, created, ttl string) (time.Time, bool) {
	c, cerr := time.Parse(time.RFC3339, created)
	if t, err := time.Parse(time.RFC3339, answerBy); err == nil {
		if cerr != nil || t.Sub(c) >= MinAnswerWindow {
			return t.Add(-AutoAnswerMargin), true
		}
	}
	d, err := time.ParseDuration(ttl)
	if cerr != nil || err != nil || d < MinAnswerWindow {
		return time.Time{}, false
	}
	return c.Add(d).Add(-AutoAnswerMargin), true
}

// add stores an offer unless it's already known, and returns a copy taken before anything else could change it, and
// whether it was new.
func (b *Bus) add(it *Item) (Item, bool) {
	b.mu.Lock()
	_, seen := b.items[it.ID]
	_, done := b.done[it.ID]
	cp := *it
	if !seen && !done {
		b.items[it.ID] = it
	}
	b.mu.Unlock()
	if !seen && !done {
		b.changed()
	}
	return cp, !seen && !done
}

func (b *Bus) remove(id string) {
	b.mu.Lock()
	_, ok := b.items[id]
	delete(b.items, id)
	b.done[id] = time.Now()
	for k, t := range b.done {
		if time.Since(t) > 48*time.Hour {
			delete(b.done, k)
		}
	}
	b.mu.Unlock()
	if ok {
		b.changed()
	}
}

func (b *Bus) fillPR(id, prURL string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	info, err := b.cfg.PRInfo(ctx, prURL)
	if err != nil {
		log.Printf("agentbus: PR details for %s: %v", prURL, err)
		return
	}
	b.mu.Lock()
	if it, ok := b.items[id]; ok {
		it.Review.PR = info
	}
	b.mu.Unlock()
	b.changed()
}

func (b *Bus) vet(it Item) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	done := make(chan bool, 1)
	go func() { done <- b.cfg.Vet(ctx, it) }()
	select {
	case ok := <-done:
		if ok {
			return
		}
	case <-ctx.Done():
	}
	b.Reveal(it.ID)
}

// Reveal shows an item as a card: done checking, and not hidden even if claimed.
func (b *Bus) Reveal(id string) {
	b.mu.Lock()
	it, ok := b.items[id]
	if ok {
		it.Checking, it.Hidden = false, false
	}
	b.mu.Unlock()
	if ok {
		b.changed()
	}
}

// NoteAutoApproved logs that rule approved it, and keeps it for the plugin to say so.
func (b *Bus) NoteAutoApproved(it Item, rule string) {
	a := AutoApproval{ID: it.ID, Rule: rule, From: it.From, At: time.Now()}
	if it.Review != nil {
		a.PR, a.Repo = it.Review.ReviewRequestBody.PR, it.Review.Repo
	}
	b.mu.Lock()
	b.auto = append(b.auto, a)
	for len(b.auto) > 0 && time.Since(b.auto[0].At) > autoKeep {
		b.auto = b.auto[1:]
	}
	b.mu.Unlock()
	b.log(it, EventAutoApproved, a.PR, rule)
	b.changed()
}

// autoKeep is how long an auto-approval is kept to be shown.
const autoKeep = 10 * time.Minute

// AutoApproved lists the auto-approvals of the last autoKeep, oldest first.
func (b *Bus) AutoApproved() []AutoApproval {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []AutoApproval{}
	for _, a := range b.auto {
		if time.Since(a.At) <= autoKeep {
			out = append(out, a)
		}
	}
	return out
}

// Items is the inbox, oldest first.
func (b *Bus) Items() []Item {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Item, 0, len(b.items))
	for _, it := range b.items {
		out = append(out, *it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Received.Before(out[j].Received) })
	return out
}

// Item is one inbox item.
func (b *Bus) Item(id string) (Item, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	it, ok := b.items[id]
	if !ok {
		return Item{}, false
	}
	return *it, true
}

var errOffline = errors.New("not connected to agentbus")

func (b *Bus) client() (*Client, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.c == nil {
		return nil, errOffline
	}
	return b.c, nil
}

// Answer sends this person's answer; body.By is AnsweredByHuman or AnsweredByAgent.
func (b *Bus) Answer(ctx context.Context, id string, body AnswerBody) error {
	it, ok := b.Item(id)
	if !ok {
		it = Item{ID: id, Kind: "ask"}
	}
	return b.AnswerItem(ctx, it, body)
}

// AnswerItem is Answer for an item already handed out by Due.
func (b *Bus) AnswerItem(ctx context.Context, it Item, body AnswerBody) error {
	c, err := b.client()
	if err != nil {
		return err
	}
	err = c.Answer(ctx, it.ID, body)
	if err == nil {
		text := body.Text
		if text == "" {
			text = body.Option
		}
		b.log(it, EventAnswered, text, body.By)
	}
	if err == nil || isGone(err) {
		b.remove(it.ID)
	}
	return err
}

// Hide keeps an item but stops showing it as a card (Later, Ignore).
func (b *Bus) Hide(id, why string) {
	b.mu.Lock()
	it, ok := b.items[id]
	var cp Item
	if ok {
		it.Hidden = true
		cp = *it
	}
	b.mu.Unlock()
	if ok {
		b.log(cp, why, "", "")
	}
	b.changed()
}

// Show brings back an item put off with Later or Ignore. A claimed review stays hidden: it's being worked on.
func (b *Bus) Show(id string) bool {
	b.mu.Lock()
	it, ok := b.items[id]
	ok = ok && it.Hidden && !it.Claimed
	if ok {
		it.Hidden = false
	}
	b.mu.Unlock()
	if ok {
		b.changed()
	}
	return ok
}

// Claim claims a review for this instance.
func (b *Bus) Claim(ctx context.Context, id string) error {
	c, err := b.client()
	if err != nil {
		return err
	}
	if err := c.ClaimReview(ctx, id); err != nil {
		if isGone(err) {
			b.remove(id)
		}
		return err
	}
	b.mu.Lock()
	it, ok := b.items[id]
	var cp Item
	if ok {
		it.Claimed, it.Hidden = true, true
		cp = *it
	}
	b.mu.Unlock()
	if ok {
		b.log(cp, EventClaimed, "", "")
	}
	b.changed()
	return nil
}

// Result sends a claimed review's outcome and drops it from the inbox.
func (b *Bus) Result(ctx context.Context, id string, body ReviewResultBody) error {
	c, err := b.client()
	if err != nil {
		return err
	}
	it, _ := b.Item(id)
	err = c.ReviewResult(ctx, id, body)
	if err == nil {
		if it.ID == "" {
			it = Item{ID: id, Kind: "review"}
		}
		b.log(it, EventReviewed, body.Summary, body.State)
	}
	if err == nil || isGone(err) {
		b.remove(id)
	}
	return err
}

// isGone is an error meaning the item is no longer this instance's to act on.
func isGone(err error) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Code == ErrConflict || e.Code == ErrNotFound || e.Code == ErrForbidden
}

// Due returns the asks past their deadline that nobody has answered, and forgets them, so each is handed out once.
func (b *Bus) Due(now time.Time) []Item {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []Item
	for id, it := range b.items {
		if it.Kind != "ask" || it.Deadline == nil || now.Before(*it.Deadline) {
			continue
		}
		out = append(out, *it)
		delete(b.items, id)
		b.done[id] = now
	}
	if len(out) > 0 {
		go b.changed()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Received.Before(out[j].Received) })
	return out
}

// FromUser says whether an item came from the same person as this instance.
func (b *Bus) FromUser(it Item) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.c != nil && strings.EqualFold(b.c.You().User, it.From.User)
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// Who lists the instances connected to the bus.
func (b *Bus) Who(ctx context.Context) ([]Online, error) {
	c, err := b.client()
	if err != nil {
		return nil, err
	}
	return c.Who(ctx, nil)
}

// RequestReview asks one person to review the PR; "" is anyone who takes reviews for its repo.
func (b *Bus) RequestReview(ctx context.Context, prURL, note, user string) (string, error) {
	c, err := b.client()
	if err != nil {
		return "", err
	}
	to := Target{Anyone: true}
	if user != "" {
		to = Target{User: user}
	}
	id, err := c.RequestReview(ctx, to, 0, ReviewRequestBody{PR: prURL, Note: note})
	if err != nil {
		return "", err
	}
	b.mu.Lock()
	b.out[id] = &outgoing{pr: prURL, sent: time.Now()}
	b.mu.Unlock()
	b.log(Item{ID: id, Kind: "review"}, EventRequested, prURL, note)
	return id, nil
}

// Ask sends a person a question from this person.
func (b *Bus) Ask(ctx context.Context, user, question string) (string, error) {
	c, err := b.client()
	if err != nil {
		return "", err
	}
	id, err := c.Ask(ctx, Target{User: user}, 0, AskBody{Question: question})
	if err != nil {
		return "", err
	}
	b.log(Item{ID: id, Kind: "ask", From: From{User: user}}, EventAsked, question, "")
	return id, nil
}

// AskAnswer is the reply to a question this person asked, if one has come.
type AskAnswer struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	Answered bool   `json:"answered"`
	By       string `json:"by,omitempty"`
	Text     string `json:"text,omitempty"`
}

// AskState reads back a question this person asked.
func (b *Bus) AskState(ctx context.Context, id string) (AskAnswer, error) {
	c, err := b.client()
	if err != nil {
		return AskAnswer{}, err
	}
	st, err := c.Get(ctx, id)
	if err != nil {
		return AskAnswer{}, err
	}
	out := AskAnswer{ID: id, State: st.State}
	for _, r := range st.Replies {
		if r.Type != TypeAnswer {
			continue
		}
		var body AnswerBody
		if json.Unmarshal(r.Body, &body) == nil {
			out.Answered = true
			out.Text = body.Text
			if out.Text == "" {
				out.Text = body.Option
			}
			if r.From != nil {
				out.By = r.From.User
			}
		}
	}
	return out, nil
}

// outgoing is a review request this instance sent, and how far the log has followed it.
type outgoing struct {
	pr       string
	sent     time.Time
	claimed  bool
	reviewed bool
}

// RequestWatch is how often sent requests are read back; requestGiveUp is when one stops being followed.
const (
	RequestWatch  = 30 * time.Second
	requestGiveUp = 7 * 24 * time.Hour
)

// noteRequest logs a sent request's claim and review, each once.
func (b *Bus) noteRequest(id string, r Request) {
	b.mu.Lock()
	o, ok := b.out[id]
	var claim, review bool
	if ok {
		claim = r.ClaimBy != "" && !o.claimed
		review = r.Result != "" && !o.reviewed
		o.claimed = o.claimed || claim
		o.reviewed = o.reviewed || review
		if o.reviewed {
			delete(b.out, id)
		}
	}
	b.mu.Unlock()
	if !ok {
		return
	}
	it := Item{ID: id, Kind: "review", From: From{User: r.ClaimBy}}
	if claim {
		b.log(it, EventRequestClaimed, o.pr, "")
	}
	if review {
		detail := r.Result
		if r.Summary != "" {
			detail += " · " + r.Summary
		}
		b.log(it, EventRequestReviewed, o.pr, detail)
	}
	if claim || review {
		b.changed()
	}
}

// watchRequests reads back each sent request until it's reviewed, gone, or too old to wait for.
func (b *Bus) watchRequests(ctx context.Context) {
	every := b.cfg.Watch
	if every <= 0 {
		every = RequestWatch
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		b.mu.Lock()
		var ids []string
		for id, o := range b.out {
			if time.Since(o.sent) > requestGiveUp {
				delete(b.out, id)
				continue
			}
			ids = append(ids, id)
		}
		b.mu.Unlock()
		for _, id := range ids {
			r, err := b.RequestState(ctx, id)
			if err != nil {
				if isGone(err) {
					b.mu.Lock()
					delete(b.out, id)
					b.mu.Unlock()
				}
				continue
			}
			b.noteRequest(id, r)
		}
	}
}

// Request is the state of a request this instance sent.
type Request struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	ClaimBy string `json:"claimBy,omitempty"`
	Result  string `json:"result,omitempty"`
	Summary string `json:"summary,omitempty"`
	URL     string `json:"url,omitempty"`
}

// RequestState reads a request this instance sent.
func (b *Bus) RequestState(ctx context.Context, id string) (Request, error) {
	c, err := b.client()
	if err != nil {
		return Request{}, err
	}
	st, err := c.Get(ctx, id)
	if err != nil {
		return Request{}, err
	}
	return requestOf(id, st), nil
}

// requestOf reads a state frame's claim and result.
func requestOf(id string, st Frame) Request {
	out := Request{ID: id, State: st.State}
	for _, r := range st.Replies {
		switch r.Type {
		case TypeReviewClaim:
			if r.From != nil {
				out.ClaimBy = r.From.User
			}
		case TypeReviewResult:
			var body ReviewResultBody
			if json.Unmarshal(r.Body, &body) == nil {
				out.Result, out.Summary, out.URL = body.State, body.Summary, body.URL
			}
		}
	}
	return out
}
