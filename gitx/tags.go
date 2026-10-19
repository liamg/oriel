package gitx

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/liamg/oriel/semver"
)

// Tag is a tag and the commit it points at.
type Tag struct {
	Name, Commit string
	When         time.Time
	Annotated    bool
}

// Tags are the repo's tags, newest first: an annotated tag by when it was made, a lightweight one by its commit.
func (r Repo) Tags(ctx context.Context) ([]Tag, error) {
	out, err := r.git(ctx, "for-each-ref", "--sort=-creatordate", "refs/tags",
		"--format=%(refname:short)%00%(objecttype)%00%(objectname)%00%(*objectname)%00%(creatordate:unix)")
	if err != nil {
		return nil, err
	}
	var ts []Tag
	for l := range strings.SplitSeq(strings.TrimRight(string(out), "\n"), "\n") {
		f := strings.Split(l, "\x00")
		if len(f) != 5 {
			continue
		}
		n, _ := strconv.ParseInt(f[4], 10, 64)
		t := Tag{Name: f[0], Commit: f[2], When: time.Unix(n, 0), Annotated: f[1] == "tag"}
		if t.Annotated {
			t.Commit = f[3]
		}
		ts = append(ts, t)
	}
	return ts, nil
}

// LatestSemverTag is the highest tag that's a semantic version, with or without a leading v. Pre-releases count
// only when there's nothing else.
func (r Repo) LatestSemverTag(ctx context.Context) (string, bool) {
	out, err := r.git(ctx, "tag", "--list")
	if err != nil {
		return "", false
	}
	var best string
	var bestV semver.Version
	for _, name := range strings.Fields(string(out)) {
		v, err := semver.Parse(name)
		if err != nil {
			continue
		}
		if best == "" || (bestV.Pre != "" && v.Pre == "") || ((bestV.Pre == "") == (v.Pre == "") && v.Compare(bestV) > 0) {
			best, bestV = name, v
		}
	}
	return best, best != ""
}

// BumpVersion is tag with part ("patch", "minor" or "major") moved on, keeping a leading v. With no version to
// bump, it's v0.1.0, or v1.0.0 for major.
func BumpVersion(tag, part string) string {
	v, err := semver.Parse(tag)
	if err != nil {
		if part == "major" {
			return "v1.0.0"
		}
		return "v0.1.0"
	}
	prefix := ""
	if strings.HasPrefix(tag, "v") {
		prefix = "v"
	}
	return prefix + v.Bump(part).String()
}

// ValidTagName says why git wouldn't take name for a tag, or nil.
func (r Repo) ValidTagName(ctx context.Context, name string) error {
	if strings.TrimSpace(name) == "" || strings.HasPrefix(name, "-") {
		return fmt.Errorf("%q isn't a tag name", name)
	}
	if _, err := r.git(ctx, "check-ref-format", "refs/tags/"+name); err != nil {
		return fmt.Errorf("%q isn't a tag name git takes", name)
	}
	return nil
}

// TagExists reports whether there's a tag of this name.
func (r Repo) TagExists(ctx context.Context, name string) bool {
	_, err := r.git(ctx, "rev-parse", "--verify", "--quiet", "refs/tags/"+name)
	return err == nil
}

// TagReq is a tag to make: Name on Ref (HEAD when ""), with Message, signed if Sign.
type TagReq struct {
	Name, Ref, Message string
	Sign               bool
}

// CreateTag makes an annotated tag. One that exists already is refused.
func (r Repo) CreateTag(ctx context.Context, req TagReq) error {
	if err := r.ValidTagName(ctx, req.Name); err != nil {
		return err
	}
	if strings.TrimSpace(req.Message) == "" {
		return errors.New("a tag needs a message")
	}
	if r.TagExists(ctx, req.Name) {
		return fmt.Errorf("tag %s already exists", req.Name)
	}
	args := []string{"tag", "-a"}
	if req.Sign {
		args = append(args, "-s")
	}
	_, err := r.git(ctx, append(args, "-m", req.Message, "--", req.Name, cmp.Or(req.Ref, "HEAD"))...)
	return err
}

// PushTag pushes one tag to remote.
func (r Repo) PushTag(ctx context.Context, remote, name string) error {
	_, err := r.git(ctx, "push", "--quiet", remote, "refs/tags/"+name)
	return err
}

// SigningConfigured reports whether git has a key to sign tags with, or is set to sign them.
func (r Repo) SigningConfigured(ctx context.Context) bool {
	get := func(args ...string) string {
		out, _ := r.git(ctx, append([]string{"config"}, args...)...)
		return strings.TrimSpace(string(out))
	}
	if get("--get", "user.signingkey") != "" || get("--type=bool", "--get", "tag.gpgSign") == "true" {
		return true
	}
	return get("--get", "gpg.format") == "ssh" && get("--get", "gpg.ssh.defaultKeyCommand") != ""
}

// CommitOf is the commit ref names: its full hash and subject.
func (r Repo) CommitOf(ctx context.Context, ref string) (sha, subject string, err error) {
	out, err := r.git(ctx, "log", "-1", "--format=%H%x00%s", "--end-of-options", ref, "--")
	if err != nil {
		return "", "", err
	}
	sha, subject, _ = strings.Cut(strings.TrimRight(string(out), "\n"), "\x00")
	return sha, subject, nil
}
