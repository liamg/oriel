// Command changelog prints a release's notes from the commits after since up to until (tools/changelog), as
// release.yml writes the draft's body: since is the last published release, so a version that failed and never went
// out (a tag with no release) doesn't hide its changes.
//
//	go run ./tools/cmd/changelog -since v0.0.14 -until <sha> -repo liamg/oriel
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/liamg/oriel/tools/changelog"
)

func main() {
	since := flag.String("since", "", "the last published release's tag; every commit up to -until when empty")
	until := flag.String("until", "HEAD", "the release's commit")
	repo := flag.String("repo", "liamg/oriel", "owner/name, for the links to pull requests")
	flag.Parse()
	commits, err := log(*since, *until)
	if err != nil {
		fmt.Fprintln(os.Stderr, "changelog:", err)
		os.Exit(1)
	}
	fmt.Print(changelog.Notes(commits, *repo))
}

// log is the commits in since..until, oldest first.
func log(since, until string) ([]changelog.Commit, error) {
	span := until
	if since != "" {
		span = since + ".." + until
	}
	out, err := exec.Command("git", "log", "--reverse", "--format=%s%x1f%b%x1e", span).Output()
	if err != nil {
		return nil, fmt.Errorf("git log %s: %w", span, err)
	}
	var commits []changelog.Commit
	for rec := range strings.SplitSeq(string(out), "\x1e") {
		subject, body, _ := strings.Cut(strings.TrimLeft(rec, "\n"), "\x1f")
		if strings.TrimSpace(subject) != "" {
			commits = append(commits, changelog.Commit{Subject: subject, Body: body})
		}
	}
	return commits, nil
}
