package ical

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/liamg/oriel/agent/jsonrpc"
)

// Ahead is how far ahead the plugin looks for the next meeting.
const Ahead = 14 * 24 * time.Hour

// Meeting is an event as the plugin's Lua gets it: times in seconds since the epoch.
type Meeting struct {
	Title    string  `json:"title"`
	Start    float64 `json:"start"`
	Finish   float64 `json:"finish"`
	Location string  `json:"location,omitempty"`
	Link     string  `json:"link,omitempty"`
	People   int     `json:"people,omitempty"`
}

// Request is what the Lua asks for: the meetings from now (its clock) for Ahead, at most Limit (5 if 0).
type Request struct {
	URL    string   `json:"url"`    // upcoming: the feed
	Me     []string `json:"me"`     // upcoming: your addresses
	Output string   `json:"output"` // eventkit: what the script printed
	Now    float64  `json:"now"`
	Limit  int      `json:"limit"`
}

// Answer is the meetings, soonest first.
type Answer struct {
	Events []Meeting `json:"events"`
}

// Server answers the plugin's Lua: upcoming reads a feed, eventkit reads what the EventKit script printed (the Lua
// runs it, as a sandboxed process can't reach EventKit).
type Server struct {
	Client *http.Client // http.DefaultClient when nil
}

// Handle answers one request.
func (s *Server) Handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	var r Request
	if len(params) > 0 {
		if err := json.Unmarshal(params, &r); err != nil {
			return nil, &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: err.Error()}
		}
	}
	now := time.Now()
	if r.Now > 0 {
		now = time.UnixMilli(int64(r.Now * 1000))
	}
	var events []Event
	var err error
	switch method {
	case "upcoming":
		events, err = s.fetch(ctx, r.URL, now, Options{Me: r.Me})
	case "eventkit":
		events, err = ParseEventKit([]byte(r.Output), now)
	default:
		return nil, &jsonrpc.Error{Code: jsonrpc.MethodNotFound, Message: "no method " + method}
	}
	if err != nil {
		return nil, err
	}
	limit := r.Limit
	if limit <= 0 {
		limit = 5
	}
	a := Answer{Events: []Meeting{}}
	for _, e := range events[:min(limit, len(events))] {
		a.Events = append(a.Events, Meeting{Title: e.Title, Start: seconds(e.Start), Finish: seconds(e.End), Location: e.Location, Link: e.Link,
			People: e.People})
	}
	return a, nil
}

func seconds(t time.Time) float64 { return float64(t.UnixMilli()) / 1000 }

// fetch reads the feed at raw. The address is the credential, so no error names it.
func (s *Server) fetch(ctx context.Context, raw string, now time.Time, o Options) ([]Event, error) {
	feed, err := CleanURL(raw)
	if err != nil {
		return nil, err
	}
	if feed == "" {
		return nil, errors.New("no calendar address")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feed, nil)
	if err != nil {
		return nil, errors.New("the calendar address isn't valid")
	}
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("could not reach the calendar")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the calendar answered %s", resp.Status)
	}
	events, err := Upcoming(io.LimitReader(resp.Body, 32<<20), now, now.Add(Ahead), o)
	if err != nil {
		return nil, errors.New("the calendar is not an iCal feed")
	}
	return events, nil
}

// Serve answers requests on r, a JSON-RPC 2.0 message a line, writing to w, until r ends: the plugin's process.
func Serve(r io.Reader, w io.Writer, s *Server) {
	conn := jsonrpc.NewConn(r, w, jsonrpc.Handlers{
		Request: func(req *jsonrpc.Request) {
			go func() { req.Reply(s.Handle(context.Background(), req.Method, req.Params)) }()
		},
	}, jsonrpc.Options{Version: "2.0"})
	<-conn.Done()
}
