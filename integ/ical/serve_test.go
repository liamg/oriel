package ical

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// The process's upcoming: the recorded feed over HTTP at mockup 10's 14:12, the five soonest, in seconds.
func TestServeUpcoming(t *testing.T) {
	feed, _ := os.ReadFile("testdata/google.ics")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/secret-token/basic.ics" {
			http.NotFound(w, r)
			return
		}
		w.Write(feed)
	}))
	defer srv.Close()
	at := time.Date(2026, 10, 15, 13, 12, 0, 0, time.UTC)
	s := &Server{}
	raw, _ := json.Marshal(Request{URL: srv.URL + "/secret-token/basic.ics", Now: float64(at.Unix())})
	v, err := s.Handle(context.Background(), "upcoming", raw)
	if err != nil {
		t.Fatal(err)
	}
	a := v.(Answer)
	if len(a.Events) != 5 {
		t.Fatalf("got %d events: %+v", len(a.Events), a.Events)
	}
	if e := a.Events[0]; e.Title != "Pricing sync" || e.Start != float64(at.Add(18*time.Minute).Unix()) || e.Finish != e.Start+1800 || e.People != 4 ||
		e.Link != "https://meet.google.com/abc-defg-hij" {
		t.Errorf("first = %+v", e)
	}

	raw, _ = json.Marshal(Request{URL: srv.URL + "/secret-token/other.ics"})
	if _, err := s.Handle(context.Background(), "upcoming", raw); err == nil || strings.Contains(err.Error(), "secret") ||
		!strings.Contains(err.Error(), "404") {
		t.Errorf("a missing feed: %v", err)
	}
	raw, _ = json.Marshal(Request{URL: "https://127.0.0.1:1/secret-token.ics"})
	if _, err := s.Handle(context.Background(), "upcoming", raw); err == nil || strings.Contains(err.Error(), "secret") {
		t.Errorf("error = %v, want one that does not name the URL", err)
	}
}

// Serve speaks JSON-RPC a line at a time, as the process host does.
func TestServe(t *testing.T) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go func() { Serve(inR, outW, &Server{}); outW.Close() }()
	go func() {
		io.WriteString(inW, `{"jsonrpc":"2.0","id":1,"method":"eventkit","params":{"output":"{\"status\":\"ok\",\"events\":[{\"title\":\"A\",\"start\":2000,\"end\":3000}]}","now":1}}`+"\n")
		io.WriteString(inW, `{"jsonrpc":"2.0","id":2,"method":"nope"}`+"\n")
	}()
	sc := bufio.NewScanner(outR)
	got := map[string]string{}
	for len(got) < 2 && sc.Scan() {
		var m struct {
			ID     json.RawMessage
			Result json.RawMessage
			Error  *struct{ Code int }
		}
		json.Unmarshal(sc.Bytes(), &m)
		if m.Error != nil {
			got[string(m.ID)] = "error"
		} else {
			got[string(m.ID)] = string(m.Result)
		}
	}
	inW.Close()
	if got["1"] != `{"events":[{"title":"A","start":2,"finish":3}]}` || got["2"] != "error" {
		t.Errorf("got %v", got)
	}
}
