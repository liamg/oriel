package gitx_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/gitx/gittest"
)

// ref: oracle git — git-for-each-ref(1) --sort=-creatordate; an annotated tag's creatordate is its tagger date,
// a lightweight one's is its commit's.
func TestTagsNewestFirst(t *testing.T) {
	ctx := context.Background()
	gittest.Isolate(t)
	g := gittest.New(t)
	g.Write(map[string]string{"a": "1\n"})
	g.Commit("Ada", "one")
	g.Git("tag", "v0.9.3")
	g.Git("tag", "release-candidate")
	g.Write(map[string]string{"a": "2\n"})
	g.Commit("Ada", "two")
	g.GitEnv([]string{"GIT_COMMITTER_DATE=2026-10-01T10:00:00Z"}, "tag", "-a", "-m", "ten", "v0.10.0")
	g.Write(map[string]string{"a": "3\n"})
	g.Commit("Ada", "three")
	g.Git("tag", "v1.0.0-rc.1")
	r := gitx.Repo{Dir: g.Dir}

	tags, err := r.Tags(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tg := range tags {
		got = append(got, tg.Name)
	}
	if strings.Join(got, " ") != "v0.10.0 v1.0.0-rc.1 release-candidate v0.9.3" &&
		strings.Join(got, " ") != "v0.10.0 v1.0.0-rc.1 v0.9.3 release-candidate" {
		t.Errorf("Tags = %v", got)
	}
	if tags[0].Commit != rev(g, "HEAD~1") || !tags[0].Annotated || !tags[0].When.Equal(time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("v0.10.0 = %+v", tags[0])
	}
	if tags[1].Commit != rev(g, "HEAD") || tags[1].Annotated {
		t.Errorf("v1.0.0-rc.1 = %+v", tags[1])
	}

	if v, ok := r.LatestSemverTag(ctx); !ok || v != "v0.10.0" {
		t.Errorf("LatestSemverTag = %q, %v", v, ok)
	}
	if sha, subject, err := r.CommitOf(ctx, "v0.10.0"); err != nil || sha != rev(g, "HEAD~1") || subject != "two" {
		t.Errorf("CommitOf(v0.10.0) = %s %q %v", sha, subject, err)
	}
	if _, _, err := r.CommitOf(ctx, "nope"); err == nil {
		t.Error("CommitOf a ref that isn't there")
	}
}

// ref:   semver/semver.md#Semantic Versioning Specification (SemVer)
// quote: "Major, minor, and patch versions are always compared numerically."
func TestLatestSemverTag(t *testing.T) {
	ctx := context.Background()
	gittest.Isolate(t)
	g := gittest.New(t)
	r := gitx.Repo{Dir: g.Dir}
	g.Write(map[string]string{"a": "1\n"})
	g.Commit("Ada", "one")
	if v, ok := r.LatestSemverTag(ctx); ok || v != "" {
		t.Errorf("no tags: %q, %v", v, ok)
	}
	g.Git("tag", "v2.0.0-beta.2")
	g.Git("tag", "v2.0.0-beta.10")
	g.Git("tag", "v2.0.0-alpha")
	if v, _ := r.LatestSemverTag(ctx); v != "v2.0.0-beta.10" {
		t.Errorf("only pre-releases: %q", v)
	}
	g.Git("tag", "1.2.0")
	g.Git("tag", "v1.1.9")
	g.Git("tag", "v1.10")
	if v, _ := r.LatestSemverTag(ctx); v != "1.2.0" {
		t.Errorf("with and without v: %q", v)
	}
}

func TestBumpVersion(t *testing.T) {
	for _, c := range []struct{ tag, part, want string }{
		{"v0.4.2", "patch", "v0.4.3"},
		{"v0.4.2", "minor", "v0.5.0"},
		{"v0.4.2", "major", "v1.0.0"},
		{"1.9.9", "minor", "1.10.0"},
		{"v1.2.3-rc.1+build.5", "patch", "v1.2.4"},
		{"", "patch", "v0.1.0"},
		{"", "minor", "v0.1.0"},
		{"", "major", "v1.0.0"},
		{"release", "patch", "v0.1.0"},
		{"release", "major", "v1.0.0"},
	} {
		if got := gitx.BumpVersion(c.tag, c.part); got != c.want {
			t.Errorf("BumpVersion(%q, %q) = %q, want %q", c.tag, c.part, got, c.want)
		}
	}
}

// ref: oracle git — git-check-ref-format(1), git-tag(1) -a, git-push(1) of one refspec.
func TestCreateAndPushTag(t *testing.T) {
	ctx := context.Background()
	_, clone, bare := originFixture(t)
	r := gitx.Repo{Dir: clone.Dir}
	for _, bad := range []string{"bad..name", "", "-v1", "has space"} {
		if err := r.ValidTagName(ctx, bad); err == nil {
			t.Errorf("ValidTagName(%q) took it", bad)
		}
	}
	if err := r.ValidTagName(ctx, "v1.2.3"); err != nil {
		t.Errorf("ValidTagName(v1.2.3) = %v", err)
	}

	if r.TagExists(ctx, "v1.0.0") {
		t.Error("TagExists before it was made")
	}
	if err := r.CreateTag(ctx, gitx.TagReq{Name: "v1.0.0", Ref: "origin/other", Message: "First release"}); err != nil {
		t.Fatal(err)
	}
	if !r.TagExists(ctx, "v1.0.0") {
		t.Error("TagExists after it was made")
	}
	if got := strings.TrimSpace(clone.Git("cat-file", "-t", "v1.0.0")); got != "tag" {
		t.Errorf("v1.0.0 is a %s, want an annotated tag", got)
	}
	if got := strings.TrimSpace(clone.Git("tag", "-l", "--format=%(contents:subject)", "v1.0.0")); got != "First release" {
		t.Errorf("message = %q", got)
	}
	if rev(clone, "v1.0.0^{commit}") != rev(clone, "origin/other") {
		t.Error("tagged the wrong commit")
	}
	err := r.CreateTag(ctx, gitx.TagReq{Name: "v1.0.0", Message: "again"})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("CreateTag of an existing tag: %v", err)
	}

	if err := r.CreateTag(ctx, gitx.TagReq{Name: "v0.9.0", Message: "older"}); err != nil {
		t.Fatal(err)
	}
	if err := r.PushTag(ctx, "origin", "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace((&gittest.Repo{T: t, Dir: bare}).Git("tag", "-l")); got != "v1.0.0" {
		t.Errorf("origin's tags = %q, want only v1.0.0", got)
	}
}

// ref: oracle git — git-config(1) user.signingKey, gpg.format, gpg.ssh.defaultKeyCommand, tag.gpgSign.
func TestSigningConfigured(t *testing.T) {
	ctx := context.Background()
	gittest.Isolate(t)
	g := gittest.New(t)
	r := gitx.Repo{Dir: g.Dir}
	if r.SigningConfigured(ctx) {
		t.Error("signing with nothing configured")
	}
	for _, kv := range [][2]string{{"user.signingkey", "ABCDEF"}, {"tag.gpgSign", "true"}, {"gpg.ssh.defaultKeyCommand", "ssh-add -L"}} {
		if kv[0] == "gpg.ssh.defaultKeyCommand" {
			g.Git("config", "gpg.format", "ssh")
			if r.SigningConfigured(ctx) {
				t.Error("gpg.format=ssh with no key")
			}
		}
		g.Git("config", kv[0], kv[1])
		if !r.SigningConfigured(ctx) {
			t.Errorf("not signing with %s=%s", kv[0], kv[1])
		}
		g.Git("config", "--unset", kv[0])
	}
	g.Git("config", "tag.gpgSign", "false")
	if r.SigningConfigured(ctx) {
		t.Error("signing with tag.gpgSign=false")
	}
}
