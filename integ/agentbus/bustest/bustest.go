// Package bustest is a fake agentbus: agentbusd's routing (github.com/infracost/agentbus/server) kept in memory
// behind an httptest server, for tests that must never reach a tailnet. Who a connection is comes from its
// X-Agentbus-User header, as the real server's dev identity reads it.
package bustest

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	ab "github.com/liamg/oriel/integ/agentbus"
)

// DefaultTTL is how long a request waits when its sender gives no TTL, as agentbusd's.
const DefaultTTL = 4 * time.Hour

// Bus is the fake.
type Bus struct {
	*httptest.Server
	Now         func() time.Time // time.Now if nil
	DefaultUser string           // who a connection with no X-Agentbus-User is: dev@localhost if empty

	mu    sync.Mutex
	conns map[*conn]bool
	recs  map[string]*record
	order []string
}

type conn struct {
	ws         *websocket.Conn
	send       chan []byte
	done       chan struct{}
	once       sync.Once
	user, node string
	hello      bool
	instance   ab.Instance
	accepts    *ab.Accepts
	presence   string
}

func (c *conn) close() { c.once.Do(func() { close(c.done) }) }

func (c *conn) from() ab.From {
	inst := c.instance
	return ab.From{User: c.user, Node: c.node, Instance: &inst}
}

type record struct {
	id, typ, replyTo, sender string
	to                       ab.Target
	from                     ab.From
	body                     json.RawMessage
	repo, state, claimedBy   string
	created, expires         time.Time
	delivered                map[string]bool
	replies                  []*record
}

func (r *record) frame() ab.Frame {
	from := r.from
	f := ab.Frame{Type: r.typ, ID: r.id, ReplyTo: r.replyTo, From: &from, Body: r.body, CreatedAt: r.created.UTC().Format(time.RFC3339)}
	if r.replyTo == "" {
		to := r.to
		f.To = &to
		f.TTL = r.expires.Sub(r.created).String()
	}
	return f
}

// New starts a fake bus that closes with the test.
func New(t testing.TB) *Bus {
	b := &Bus{conns: map[*conn]bool{}, recs: map[string]*record{}}
	mux := http.NewServeMux()
	mux.Handle("/v1/ws", websocket.Server{Handler: b.serve, Handshake: func(*websocket.Config, *http.Request) error { return nil }})
	b.Server = httptest.NewServer(mux)
	t.Cleanup(b.Close)
	return b
}

// URL is the bus's address, as AGENTBUS_URL gives it.
func (b *Bus) URL() string { return "ws" + strings.TrimPrefix(b.Server.URL, "http") + "/v1/ws" }

// Header is the handshake header that makes a connection user's.
func Header(user string) http.Header {
	h := http.Header{}
	h.Set("X-Agentbus-User", user)
	return h
}

func (b *Bus) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b *Bus) serve(ws *websocket.Conn) {
	r := ws.Request()
	c := &conn{ws: ws, send: make(chan []byte, 256), done: make(chan struct{}), user: strings.ToLower(r.Header.Get("X-Agentbus-User")),
		node: r.Header.Get("X-Agentbus-Node")}
	if c.user == "" {
		c.user = strings.ToLower(b.DefaultUser)
	}
	if c.user == "" {
		c.user = "dev@localhost"
	}
	if c.node == "" {
		c.node = "localhost"
	}
	go func() {
		for {
			select {
			case <-c.done:
				ws.Close()
				return
			case m := <-c.send:
				if websocket.Message.Send(ws, string(m)) != nil {
					c.close()
					return
				}
			}
		}
	}()
	for {
		var m []byte
		if err := websocket.Message.Receive(ws, &m); err != nil {
			break
		}
		var f ab.Frame
		if err := json.Unmarshal(m, &f); err != nil {
			b.push(c, errFrame("", ab.ErrBadRequest, "invalid JSON"))
			continue
		}
		b.handle(c, &f)
	}
	c.close()
	b.mu.Lock()
	delete(b.conns, c)
	b.mu.Unlock()
}

func (b *Bus) push(c *conn, f ab.Frame) {
	m, _ := json.Marshal(f)
	select {
	case c.send <- m:
	case <-c.done:
	}
}

func errFrame(ref, code, msg string) ab.Frame {
	return ab.Frame{Type: ab.TypeError, Ref: ref, Code: code, Error: msg}
}

type protoErr struct{ code, msg string }

func (e *protoErr) Error() string { return e.code + ": " + e.msg }

func bad(format string, a ...any) error {
	return &protoErr{ab.ErrBadRequest, fmt.Sprintf(format, a...)}
}

func (b *Bus) handle(c *conn, f *ab.Frame) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if f.Type != ab.TypeHello && !c.hello {
		b.push(c, errFrame(f.Ref, ab.ErrNoHello, "send hello first"))
		return
	}
	var err error
	switch {
	case f.Type == ab.TypeHello:
		err = b.onHello(c, f)
	case f.Type == ab.TypeStatus:
		err = b.onStatus(c, f)
	case f.Type == ab.TypeWho:
		b.push(c, ab.Frame{Type: ab.TypeWhoResult, Ref: f.Ref, Instances: b.who(f.Filter)})
	case f.Type == ab.TypeGet:
		err = b.onGet(c, f)
	case ab.IsRequest(f.Type):
		err = b.onRequest(c, f)
	case ab.IsReply(f.Type):
		err = b.onReply(c, f)
	case f.Type == ab.TypeCancel:
		err = b.onCancel(c, f)
	default:
		err = bad("unknown type %s", f.Type)
	}
	if pe, ok := err.(*protoErr); ok {
		b.push(c, errFrame(f.Ref, pe.code, pe.msg))
	}
}

func validPresence(p string) bool {
	return p == ab.PresenceActive || p == ab.PresenceIdle || p == ab.PresenceDND
}

func (b *Bus) onHello(c *conn, f *ab.Frame) error {
	if c.hello {
		return bad("hello already sent; use status")
	}
	if f.Instance == nil || f.Instance.ID == "" {
		return bad("hello needs instance.id")
	}
	inst := *f.Instance
	inst.ID = c.user + "/" + f.Instance.ID
	c.instance, c.accepts, c.presence = inst, f.Accepts, ab.PresenceActive
	if f.Presence != "" {
		if !validPresence(f.Presence) {
			return bad("presence: want active, idle or dnd")
		}
		c.presence = f.Presence
	}
	c.hello = true
	for o := range b.conns {
		if o.instance.ID == c.instance.ID {
			delete(b.conns, o)
			o.close()
		}
	}
	b.conns[c] = true
	you := c.from()
	b.push(c, ab.Frame{Type: ab.TypeWelcome, Ref: f.Ref, You: &you, ServerVersion: "fake"})
	b.offerPending(c)
	return nil
}

func (b *Bus) onStatus(c *conn, f *ab.Frame) error {
	if f.Presence != "" && !validPresence(f.Presence) {
		return bad("presence: want active, idle or dnd")
	}
	wasDND := c.presence == ab.PresenceDND
	if f.Presence != "" {
		c.presence = f.Presence
	}
	if f.Accepts != nil {
		c.accepts = f.Accepts
	}
	b.push(c, ab.Frame{Type: ab.TypeAck, Ref: f.Ref})
	if wasDND && c.presence != ab.PresenceDND || f.Accepts != nil {
		b.offerPending(c)
	}
	return nil
}

func (b *Bus) who(filter *ab.WhoFilter) []ab.Online {
	out := []ab.Online{}
	for c := range b.conns {
		if filter != nil && (filter.User != "" && !strings.EqualFold(filter.User, c.user) || filter.Presence != "" && filter.Presence != c.presence) {
			continue
		}
		out = append(out, ab.Online{User: c.user, Node: c.node, Instance: c.instance, Accepts: c.accepts, Presence: c.presence, Connected: true})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Instance.ID < out[j].Instance.ID })
	return out
}

func newID() string {
	var x [6]byte
	rand.Read(x[:])
	return "msg_" + hex.EncodeToString(x[:])
}

func (b *Bus) onRequest(c *conn, f *ab.Frame) error {
	if f.To == nil {
		f.To = &ab.Target{Anyone: true}
	}
	ttl := DefaultTTL
	if f.TTL != "" {
		d, err := time.ParseDuration(f.TTL)
		if err != nil || d <= 0 {
			return bad("ttl: want a positive Go duration")
		}
		ttl = d
	}
	r := &record{id: newID(), typ: f.Type, sender: c.instance.ID, to: *f.To, from: c.from(), body: f.Body, state: ab.StatePending,
		created: b.now().UTC(), delivered: map[string]bool{}}
	r.expires = r.created.Add(ttl)
	switch f.Type {
	case ab.TypeAsk:
		var body ab.AskBody
		if err := json.Unmarshal(f.Body, &body); err != nil || strings.TrimSpace(body.Question) == "" {
			return bad("ask body needs a question")
		}
	case ab.TypeReviewRequest:
		var body ab.ReviewRequestBody
		if err := json.Unmarshal(f.Body, &body); err != nil {
			return bad("review.request body: %v", err)
		}
		u, repo, err := ab.ParsePR(body.PR)
		if err != nil {
			return bad("%v", err)
		}
		body.PR, r.repo = u, repo
		r.body, _ = json.Marshal(body)
	}
	b.recs[r.id] = r
	b.order = append(b.order, r.id)
	b.push(c, ab.Frame{Type: ab.TypeAck, Ref: f.Ref, ID: r.id})
	for o := range b.conns {
		if b.matches(r, o) {
			b.offer(o, r)
		}
	}
	return nil
}

func (b *Bus) matches(r *record, c *conn) bool {
	if c.instance.ID == r.sender || !c.accepts.Takes(r.typ, r.repo) {
		return false
	}
	switch {
	case r.to.User != "":
		return strings.EqualFold(r.to.User, c.user)
	case r.to.Instance != "":
		return strings.EqualFold(r.to.Instance, c.instance.ID)
	case r.to.Group != "":
		return false
	}
	return true
}

func (b *Bus) offer(c *conn, r *record) {
	if c.presence == ab.PresenceDND {
		return
	}
	r.delivered[c.instance.ID] = true
	b.push(c, r.frame())
}

func (b *Bus) offerPending(c *conn) {
	if c.presence == ab.PresenceDND {
		return
	}
	for _, id := range b.order {
		r := b.recs[id]
		if r.replyTo != "" || (r.state != ab.StatePending && r.state != ab.StateClaimed) {
			continue
		}
		if r.claimedBy != "" && r.claimedBy != c.instance.ID {
			continue
		}
		if b.matches(r, c) {
			b.offer(c, r)
		}
	}
}

func (b *Bus) onReply(c *conn, f *ab.Frame) error {
	if f.ReplyTo == "" {
		return bad("%s needs reply_to", f.Type)
	}
	r, ok := b.recs[f.ReplyTo]
	if !ok || r.replyTo != "" {
		return &protoErr{ab.ErrNotFound, "no message " + f.ReplyTo}
	}
	if !ab.ReplyOK(r.typ, f.Type) {
		return bad("%s cannot reply to %s", f.Type, r.typ)
	}
	if !r.delivered[c.instance.ID] {
		return &protoErr{ab.ErrForbidden, "message was not offered to this instance"}
	}
	switch r.state {
	case ab.StatePending:
	case ab.StateClaimed:
		if f.Type != ab.TypeReviewResult {
			return &protoErr{ab.ErrConflict, "already claimed"}
		}
	default:
		return &protoErr{ab.ErrConflict, "message is " + r.state}
	}
	switch f.Type {
	case ab.TypeAnswer:
		var body ab.AnswerBody
		if err := json.Unmarshal(f.Body, &body); err != nil || (body.Text == "" && body.Option == "") {
			return bad("answer body needs text or option")
		}
	case ab.TypeReviewResult:
		var body ab.ReviewResultBody
		if err := json.Unmarshal(f.Body, &body); err != nil {
			return bad("review.result body: %v", err)
		}
		switch body.State {
		case ab.ReviewApproved, ab.ReviewChangesRequested, ab.ReviewCommented:
		default:
			return bad("review.result state: want approved, changes_requested or commented")
		}
		if r.claimedBy != c.instance.ID {
			return &protoErr{ab.ErrConflict, "claim the review first"}
		}
	}
	reply := &record{id: newID(), typ: f.Type, replyTo: r.id, sender: c.instance.ID, from: c.from(), body: f.Body, state: ab.StateDone,
		created: b.now().UTC()}
	r.replies = append(r.replies, reply)
	b.push(c, ab.Frame{Type: ab.TypeAck, Ref: f.Ref, ID: reply.id})
	switch f.Type {
	case ab.TypeAnswer:
		r.state = ab.StateAnswered
	case ab.TypeReviewClaim:
		r.state, r.claimedBy = ab.StateClaimed, c.instance.ID
	case ab.TypeReviewResult:
		r.state = ab.StateDone
	}
	if f.Type != ab.TypeReviewResult {
		b.cancelOthers(r, c.instance.ID, "taken by "+c.instance.ID)
	}
	b.notifyRequester(r)
	return nil
}

func (b *Bus) cancelOthers(r *record, keep, reason string) {
	body, _ := json.Marshal(ab.CancelBody{Reason: reason})
	for c := range b.conns {
		if c.instance.ID != keep && r.delivered[c.instance.ID] {
			b.push(c, ab.Frame{Type: ab.TypeCancel, ReplyTo: r.id, Body: body})
		}
	}
}

func (b *Bus) state(r *record) ab.Frame {
	msg := r.frame()
	f := ab.Frame{Type: ab.TypeState, ID: r.id, State: r.state, Message: &msg, Replies: []ab.Frame{}}
	for _, x := range r.replies {
		f.Replies = append(f.Replies, x.frame())
	}
	return f
}

func (b *Bus) notifyRequester(r *record) {
	f := b.state(r)
	for c := range b.conns {
		if c.instance.ID == r.sender {
			b.push(c, f)
		}
	}
}

func (b *Bus) onGet(c *conn, f *ab.Frame) error {
	r, ok := b.recs[f.ID]
	if !ok || r.replyTo != "" || r.from.User != c.user && !r.delivered[c.instance.ID] {
		return &protoErr{ab.ErrNotFound, "no message " + f.ID}
	}
	st := b.state(r)
	st.Ref = f.Ref
	b.push(c, st)
	return nil
}

func (b *Bus) onCancel(c *conn, f *ab.Frame) error {
	r, ok := b.recs[f.ID]
	if !ok || r.replyTo != "" {
		return &protoErr{ab.ErrNotFound, "no message " + f.ID}
	}
	if r.from.User != c.user {
		return &protoErr{ab.ErrForbidden, "only the sender can cancel"}
	}
	if r.state != ab.StatePending && r.state != ab.StateClaimed {
		return &protoErr{ab.ErrConflict, "message is " + r.state}
	}
	r.state = ab.StateCancelled
	b.push(c, ab.Frame{Type: ab.TypeAck, Ref: f.Ref, ID: r.id})
	b.cancelOthers(r, "", "cancelled by sender")
	b.notifyRequester(r)
	return nil
}

// Expire ends the requests whose TTL has run out, as agentbusd's sweep does.
func (b *Bus) Expire() {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	for _, id := range b.order {
		r := b.recs[id]
		if r.replyTo != "" || (r.state != ab.StatePending && r.state != ab.StateClaimed) || now.Before(r.expires) {
			continue
		}
		r.state = ab.StateExpired
		b.cancelOthers(r, "", "expired")
		b.notifyRequester(r)
	}
}

// Reply is a request's reply, as the fake keeps it.
type Reply struct {
	Type string
	From string // the user
	Body json.RawMessage
}

// Replies are what came back to request id, oldest first, and its state.
func (b *Bus) Replies(id string) (string, []Reply) {
	b.mu.Lock()
	defer b.mu.Unlock()
	r, ok := b.recs[id]
	if !ok {
		return "", nil
	}
	var out []Reply
	for _, x := range r.replies {
		out = append(out, Reply{Type: x.typ, From: x.from.User, Body: x.body})
	}
	return r.state, out
}

// Online is who's connected, as who answers it.
func (b *Bus) Online() []ab.Online {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.who(nil)
}
