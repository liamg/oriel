package gitx

import (
	"context"
	"strings"
	"testing"

	"github.com/liamg/oriel/gitx/gittest"
)

// Blame names each line's commit and author kind: lines you committed, an agent's (its name, or a Co-Authored-By
// trailer naming one), someone else's, and lines not committed yet, from a buffer's text rather than the file's.
// ref: oracle git
func TestBlameKinds(t *testing.T) {
	r := gittest.New(t)
	r.Git("config", "user.email", "ada@example.com")
	r.Write(map[string]string{"f.txt": "one\n"})
	r.Commit("Ada", "mine")
	r.Write(map[string]string{"f.txt": "one\ntwo\n"})
	r.Commit("Bob", "bob's")
	r.Write(map[string]string{"f.txt": "one\ntwo\nthree\n"})
	r.Commit("Ada", "paired\n\nCo-Authored-By: Claude <noreply@anthropic.com>")
	r.Write(map[string]string{"f.txt": "one\ntwo\nthree\nfour\n"})
	r.Commit("Codex", "the agent's own")
	buffer := "one\ntwo\nthree\nfour\nfive, unsaved\n"
	bl, err := Repo{Dir: r.Dir}.Blame(context.Background(), "f.txt", []byte(buffer))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, l := range bl {
		got = append(got, l.Kind.String()+" "+l.Author+" "+l.Summary)
	}
	want := []string{"you Ada mine", "other Bob bob's", "agent Ada paired", "agent Codex the agent's own", "uncommitted  "}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("blame:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(bl[0].Hash) != 40 || bl[0].When.IsZero() || bl[4].Hash != "" {
		t.Errorf("first %+v, last %+v", bl[0], bl[4])
	}
}
