package eventstore

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/liamg/oriel/core"
)

// A log written before kinds had their own data still reads: a review's PR and draft, a job's name and a work
// session's Unplanned move into their kind's data, and the state writes them there from then on.
func TestOldLogMovesIntoKindData(t *testing.T) {
	f, err := os.Open("testdata/kinddata-old.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s := New()
	if err := core.Replay(f, s); err != nil {
		t.Fatal(err)
	}
	check := func(st core.State) {
		t.Helper()
		get := func(id string) core.SessionState {
			se, _ := st.Session(core.SessionID(id))
			if se == nil {
				t.Fatalf("no session %s", id)
			}
			return se.Snapshot()
		}
		rv, ok := core.DataOf[core.ReviewData](get("rv"))
		if !ok || rv.PR != "https://github.com/acme/api/pull/7" || rv.PRRef != "merge-requests/7/head" || rv.Draft == nil ||
			rv.Draft.Summary != "looks fine" || len(rv.Draft.Sent) != 1 {
			t.Errorf("review's data = %+v", rv)
		}
		if jb, ok := core.DataOf[core.JobData](get("jb")); !ok || jb.Job != "sweep stale deps" {
			t.Errorf("job's data = %+v", jb)
		}
		if wk, ok := core.DataOf[core.WorkData](get("wk")); !ok || !wk.Unplanned {
			t.Errorf("work's data = %+v", wk)
		}
		if pl := get("pl"); pl.Data(core.KindPlan) != nil || pl.Ticket != "ENG-1" {
			t.Errorf("plan = %+v", pl)
		}
		if d, ok := get("in").Data("incident").(core.OtherData); !ok || string(d.Raw) != `{"pager":"P-9"}` {
			t.Errorf("a plugin kind's data = %+v", get("in").Data("incident"))
		}
		if _, ok := core.DataOf[core.ReviewData](get("jb")); ok {
			t.Error("a job reads as a review")
		}
	}
	check(s.State())
	b, err := json.Marshal(s.State())
	if err != nil {
		t.Fatal(err)
	}
	for _, old := range []string{`"pr":"https://github.com/acme/api/pull/7","prRef"`, `"job":"sweep`, `"unplanned":true`} {
		if !bytes.Contains(b, []byte(`"data":{`+old)) {
			t.Errorf("state doesn't write %s under data: %s", old, b)
		}
	}
	if strings.Contains(string(b), `"review":{`) {
		t.Errorf("state still writes the flat review: %s", b)
	}
	var again core.State
	if err := json.Unmarshal(b, &again); err != nil {
		t.Fatal(err)
	}
	check(again)
	b2, _ := json.Marshal(again)
	if !bytes.Equal(b, b2) {
		t.Errorf("not stable:\n%s\n%s", b, b2)
	}
}
