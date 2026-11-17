package core

import (
	"encoding/json"
	"testing"
)

// A thread older logs kept as GitHub's alone (origin "github", no host, its first comment's id as root, its
// comments' ids as githubId, its verdict in GitHub's words) reads as GitHub's code host thread, keyed by those ids;
// another host's reads as it was.
func TestOldGitHubThreadsReadAsACodeHosts(t *testing.T) {
	var old Thread
	if err := json.Unmarshal([]byte(`{"id":"t1","status":"open","origin":"github",
		"github":{"project":"api","repo":"acme/api","pr":7,"root":41,"reviewer":"priya","state":"CHANGES_REQUESTED"},
		"comments":[{"id":"c1","author":"host","body":"Name it.","githubId":41},{"id":"c2","author":"you","body":"Done.","draft":false,"githubId":42}]}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.Origin != OriginCodeHost || old.Hosted == nil || old.Hosted.Host != "github" || old.Hosted.Key != "41" || old.Hosted.Repo != "acme/api" ||
		old.Hosted.State != "changes" {
		t.Errorf("the thread: %+v %+v", old, old.Hosted)
	}
	if old.Comments[0].HostID != "41" || old.Comments[1].HostID != "42" {
		t.Errorf("the comments: %+v", old.Comments)
	}
	convo := Thread{}
	if err := json.Unmarshal([]byte(`{"id":"t2","status":"open","origin":"github","github":{"project":"api","repo":"acme/api","pr":7}}`), &convo); err != nil {
		t.Fatal(err)
	}
	if convo.Hosted.Host != "github" || convo.Hosted.Key != "" {
		t.Errorf("the conversation: %+v", convo.Hosted)
	}

	gl := Thread{ID: "t3", Status: ThreadOpen, Origin: OriginCodeHost, Hosted: &HostThread{Host: "gitlab", Key: "d1", Repo: "acme/web", PR: 57},
		Comments: []Comment{{ID: "c3", HostID: "n1"}}}
	b, err := json.Marshal(gl)
	if err != nil {
		t.Fatal(err)
	}
	var back Thread
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Origin != OriginCodeHost || *back.Hosted != *gl.Hosted || back.Comments[0].HostID != "n1" {
		t.Errorf("another host's thread came back as %+v %+v", back, back.Hosted)
	}
}
