package agentbus

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// agentbus's wire format: JSON frames over a WebSocket, as github.com/infracost/agentbus/protocol defines them. The
// types are written here rather than imported: that module needs a newer Go than this workspace builds with.

// Frame types sent by clients.
const (
	TypeHello  = "hello"
	TypeStatus = "status"
	TypeWho    = "who"
	TypeGet    = "get"

	TypeAsk           = "ask"
	TypeAnswer        = "answer"
	TypeReviewRequest = "review.request"
	TypeReviewClaim   = "review.claim"
	TypeReviewResult  = "review.result"
	TypeCancel        = "cancel"
)

// Frame types sent by the server.
const (
	TypeWelcome   = "welcome"
	TypeAck       = "ack"
	TypeError     = "error"
	TypePresence  = "presence"
	TypeWhoResult = "who.result"
	TypeState     = "state"
)

// Presences.
const (
	PresenceActive = "active"
	PresenceIdle   = "idle"
	PresenceDND    = "dnd"
)

// Message states.
const (
	StatePending   = "pending"
	StateAnswered  = "answered"
	StateClaimed   = "claimed"
	StateDone      = "done"
	StateCancelled = "cancelled"
	StateExpired   = "expired"
)

// Error codes.
const (
	ErrBadRequest = "bad_request"
	ErrNotFound   = "not_found"
	ErrForbidden  = "forbidden"
	ErrConflict   = "conflict"
	ErrNoHello    = "no_hello"
)

// Frame is every message on the wire; which fields are set depends on Type.
type Frame struct {
	Type string `json:"type"`
	Ref  string `json:"ref,omitempty"` // set on a request, echoed on its ack, error or result

	Instance  *Instance `json:"instance,omitempty"`
	Session   *Session  `json:"session,omitempty"`
	Accepts   *Accepts  `json:"accepts,omitempty"`
	Presence  string    `json:"presence,omitempty"`
	Status    *string   `json:"status,omitempty"`
	PID       int       `json:"pid,omitempty"`
	Ancestors []int     `json:"ancestors,omitempty"`

	ID        string          `json:"id,omitempty"`
	ReplyTo   string          `json:"reply_to,omitempty"`
	To        *Target         `json:"to,omitempty"`
	From      *From           `json:"from,omitempty"`
	TTL       string          `json:"ttl,omitempty"`
	Body      json.RawMessage `json:"body,omitempty"`
	CreatedAt string          `json:"created_at,omitempty"`

	Filter    *WhoFilter `json:"filter,omitempty"`
	Instances []Online   `json:"instances,omitempty"`

	State   string  `json:"state,omitempty"`
	Message *Frame  `json:"message,omitempty"`
	Replies []Frame `json:"replies,omitempty"`

	You           *From  `json:"you,omitempty"`
	ServerVersion string `json:"server_version,omitempty"`

	Code  string `json:"code,omitempty"`
	Error string `json:"error,omitempty"`

	Online *bool `json:"online,omitempty"`
}

// Instance is one running harness. The server names it "<user>/<id>" in everything it sends.
type Instance struct {
	ID      string `json:"id"`
	Harness string `json:"harness,omitempty"`
	Version string `json:"version,omitempty"`
	Label   string `json:"label,omitempty"`
}

// Session is one agent session inside an instance.
type Session struct {
	ID           string `json:"id"`
	Agent        string `json:"agent,omitempty"`
	AgentVersion string `json:"agent_version,omitempty"`
	Model        string `json:"model,omitempty"`
}

// From is who sent a frame; the server sets User and Node.
type From struct {
	User     string    `json:"user"`
	Node     string    `json:"node"`
	Instance *Instance `json:"instance,omitempty"`
	Session  *Session  `json:"session,omitempty"`
}

// Accepts says which requests an instance takes.
type Accepts struct {
	Ask    bool           `json:"ask,omitempty"`
	Review *ReviewAccepts `json:"review,omitempty"`
}

// ReviewAccepts limits reviews to repos matching a glob ("infracost/*"); none means every repo.
type ReviewAccepts struct {
	Repos []string `json:"repos,omitempty"`
}

// Takes says whether a takes a request of msgType for repo.
func (a *Accepts) Takes(msgType, repo string) bool {
	if a == nil {
		return false
	}
	switch msgType {
	case TypeAsk:
		return a.Ask
	case TypeReviewRequest:
		return a.Review != nil && a.Review.Matches(repo)
	}
	return false
}

// Matches says whether repo is one r takes.
func (r *ReviewAccepts) Matches(repo string) bool {
	if len(r.Repos) == 0 {
		return true
	}
	for _, g := range r.Repos {
		if ok, _ := path.Match(strings.ToLower(g), strings.ToLower(repo)); ok {
			return true
		}
	}
	return false
}

// Target is who a request is for: one of a user, an instance or a group, or "anyone" (the first to answer or claim
// wins), or "*" (everyone replies).
type Target struct {
	User     string `json:"user,omitempty"`
	Instance string `json:"instance,omitempty"`
	Group    string `json:"group,omitempty"`
	Anyone   bool   `json:"-"`
	All      bool   `json:"-"`
}

func (t Target) MarshalJSON() ([]byte, error) {
	switch {
	case t.All:
		return []byte(`"*"`), nil
	case t.Anyone:
		return []byte(`"anyone"`), nil
	}
	type plain Target
	return json.Marshal(plain(t))
}

func (t *Target) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		switch s {
		case "anyone", "":
			*t = Target{Anyone: true}
		case "*":
			*t = Target{All: true}
		default:
			return fmt.Errorf("target: unknown value %q", s)
		}
		return nil
	}
	type plain Target
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	n := 0
	for _, v := range []string{p.User, p.Instance, p.Group} {
		if v != "" {
			n++
		}
	}
	if n != 1 {
		return fmt.Errorf("target: set exactly one of user, instance, group")
	}
	*t = Target(p)
	return nil
}

// WhoFilter narrows a who request; empty fields match everything.
type WhoFilter struct {
	User     string `json:"user,omitempty"`
	Accepts  string `json:"accepts,omitempty"`
	Presence string `json:"presence,omitempty"`
}

// Online is one row of a who result.
type Online struct {
	User      string    `json:"user"`
	Node      string    `json:"node"`
	Instance  Instance  `json:"instance"`
	Accepts   *Accepts  `json:"accepts,omitempty"`
	Presence  string    `json:"presence"`
	Status    string    `json:"status,omitempty"`
	Sessions  []Session `json:"sessions,omitempty"`
	Connected bool      `json:"connected"`
}

// AskBody is a question. AnswerBy (RFC 3339) is when the asker stops waiting: a harness that hasn't heard from its
// human by then may answer for them.
type AskBody struct {
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
	AnswerBy string   `json:"answer_by,omitempty"`
}

// AnswerBody is an answer; By is AnsweredByAgent when an agent answered for its absent human.
type AnswerBody struct {
	Text   string `json:"text,omitempty"`
	Option string `json:"option,omitempty"`
	By     string `json:"by,omitempty"`
}

// Who answered.
const (
	AnsweredByHuman = "human"
	AnsweredByAgent = "agent"
)

// ReviewRequestBody asks for a review of a PR.
type ReviewRequestBody struct {
	PR   string `json:"pr"`
	Note string `json:"note,omitempty"`
}

// ReviewResultBody is how a claimed review went.
type ReviewResultBody struct {
	State   string `json:"state"`
	Summary string `json:"summary,omitempty"`
	URL     string `json:"url,omitempty"`
}

// CancelBody says why a request was cancelled.
type CancelBody struct {
	Reason string `json:"reason,omitempty"`
}

// Review results.
const (
	ReviewApproved         = "approved"
	ReviewChangesRequested = "changes_requested"
	ReviewCommented        = "commented"
)

var shortPR = regexp.MustCompile(`^([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)#([0-9]+)$`)
var prNumber = regexp.MustCompile(`^[0-9]+$`)

// ParsePR reads a GitHub PR URL or "owner/repo#123" and returns the PR's URL and "owner/repo".
func ParsePR(s string) (prURL, repo string, err error) {
	s = strings.TrimSpace(s)
	if m := shortPR.FindStringSubmatch(s); m != nil {
		return fmt.Sprintf("https://github.com/%s/%s/pull/%s", m[1], m[2], m[3]), m[1] + "/" + m[2], nil
	}
	u, err := url.Parse(s)
	if err != nil || u.Host != "github.com" {
		return "", "", fmt.Errorf("pr: want a github.com PR URL or owner/repo#123, got %q", s)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 || parts[2] != "pull" || !prNumber.MatchString(parts[3]) {
		return "", "", fmt.Errorf("pr: want a github.com PR URL or owner/repo#123, got %q", s)
	}
	return fmt.Sprintf("https://github.com/%s/%s/pull/%s", parts[0], parts[1], parts[3]), parts[0] + "/" + parts[1], nil
}

// replyTypes are the replies each request takes.
var replyTypes = map[string][]string{
	TypeAsk:           {TypeAnswer},
	TypeReviewRequest: {TypeReviewClaim, TypeReviewResult},
}

// IsRequest says whether t starts a conversation.
func IsRequest(t string) bool {
	_, ok := replyTypes[t]
	return ok
}

// IsReply says whether t replies to a request.
func IsReply(t string) bool {
	switch t {
	case TypeAnswer, TypeReviewClaim, TypeReviewResult:
		return true
	}
	return false
}

// ReplyOK says whether a reply of type reply answers a request of type req.
func ReplyOK(req, reply string) bool {
	for _, t := range replyTypes[req] {
		if t == reply {
			return true
		}
	}
	return false
}
