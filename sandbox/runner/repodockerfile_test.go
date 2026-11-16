package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/container"
)

// repoWorkspace is a sandboxed workspace whose session allows a custom image, and whose primary project is repo, with
// df at the repo's DockerfilePath.
func repoWorkspace(t *testing.T, df string) (root, repo string) {
	t.Helper()
	return repoWorkspaceWith(t, df, true)
}

func repoWorkspaceWith(t *testing.T, df string, custom bool) (root, repo string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	root, repo = t.TempDir(), t.TempDir()
	m := sandbox.Manifest{Projects: []sandbox.Project{{Name: "api", Repo: repo, Dir: "api", Primary: true}}, CustomImage: custom}
	if err := sandbox.SaveManifest(root, m); err != nil {
		t.Fatal(err)
	}
	writeRepoDockerfile(t, repo, df)
	return root, repo
}

func writeRepoDockerfile(t *testing.T, repo, df string) {
	t.Helper()
	path := filepath.Join(repo, DockerfilePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(df), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "init.defaultBranch=main",
		"-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// A repo's Dockerfile with no FROM adds to the base image: the base goes under it, so a new base gives a new tag
// and a fresh build. It's used as found, with nothing to approve.
func TestRepoDockerfileWithoutFromBuildsOnTheBase(t *testing.T) {
	root, repo := repoWorkspace(t, "RUN apt-get install -y postgresql-18\n")
	ctx := container.WithEngine(context.Background(), describeEngine(t))
	d := Describe(ctx, root, sandbox.Config{})
	want := "FROM " + container.PublishedBase() + "\nRUN apt-get install -y postgresql-18\n"
	if d.Dockerfile != want || d.Image != container.ProjectImage(want) {
		t.Errorf("image %q from\n%s", d.Image, d.Dockerfile)
	}
	if d.ImageFrom != filepath.Base(repo)+"'s "+DockerfilePath+" on the base image" {
		t.Errorf("from %q", d.ImageFrom)
	}
}

// A repo's Dockerfile can only add to the base: one with a FROM of its own, as its base or a build stage, is refused,
// and nothing is pulled or built for it.
func TestRepoDockerfileWithFromIsRefused(t *testing.T) {
	for _, df := range []string{"FROM debian\nRUN true\n", "# syntax note\n  from debian AS b\n", "RUN true\nFROM\tdebian\n"} {
		root, repo := repoWorkspace(t, df)
		d := Describe(container.WithEngine(context.Background(), describeEngine(t)), root, sandbox.Config{})
		if d.Refused == "" || d.Image != "" || d.Built || d.Dockerfile != df || d.ImageFrom != filepath.Base(repo)+"'s "+DockerfilePath {
			t.Errorf("%q: described %+v", df, d)
		}
		e, dir := layerEngine(t, true, true)
		ctx := container.WithEngine(context.Background(), e)
		if _, err := Image(ctx, root, sandbox.Config{}); err == nil || !strings.Contains(err.Error(), "FROM") {
			t.Errorf("%q: started with %v", df, err)
		}
		if err := Rebuild(ctx, root, sandbox.Config{}); err == nil {
			t.Errorf("%q: rebuilt", df)
		}
		if _, log := built(dir); strings.Contains(log, "build") || strings.Contains(log, "pull") {
			t.Errorf("%q: calls:\n%s", df, log)
		}
	}
}

// A session that doesn't allow a custom image runs the base, whatever the repo's Dockerfile says.
func TestRepoDockerfileIsIgnoredUnlessTheSessionAllowsIt(t *testing.T) {
	for _, df := range []string{"RUN apt-get install -y postgresql-18\n", "FROM debian\n"} {
		root, _ := repoWorkspaceWith(t, df, false)
		d := Describe(container.WithEngine(context.Background(), describeEngine(t)), root, sandbox.Config{})
		if d.Image != container.BaseImage() || d.Dockerfile != container.BaseDockerfile || d.Refused != "" {
			t.Errorf("%q: image %q from %q, refused %q", df, d.Image, d.ImageFrom, d.Refused)
		}
	}
}

// The file in the checkout is used even when it isn't committed, and wins over the committed one; with none in the
// checkout, the default branch's is used.
func TestRepoDockerfileIsReadFromTheCheckoutFirst(t *testing.T) {
	root, repo := repoWorkspace(t, "RUN committed\n")
	gitRun(t, repo, "init", "-q")
	gitRun(t, repo, "add", DockerfilePath)
	gitRun(t, repo, "commit", "-q", "-m", "x")
	writeRepoDockerfile(t, repo, "RUN local\n")
	ctx := container.WithEngine(context.Background(), describeEngine(t))
	if d := Describe(ctx, root, sandbox.Config{}); !strings.HasSuffix(d.Dockerfile, "\nRUN local\n") {
		t.Errorf("with a local edit:\n%s", d.Dockerfile)
	}
	if err := os.Remove(filepath.Join(repo, DockerfilePath)); err != nil {
		t.Fatal(err)
	}
	if d := Describe(ctx, root, sandbox.Config{}); !strings.HasSuffix(d.Dockerfile, "\nRUN committed\n") {
		t.Errorf("with none in the checkout:\n%s", d.Dockerfile)
	}
}

// layerEngine is a CLI that records its calls and the Dockerfile it builds. The base is there under its local name
// with local, under its registry name with remote; a pull brings both, as Oriel's pull tags it locally too.
func layerEngine(t *testing.T, local, remote bool) (container.Engine, string) {
	t.Helper()
	dir := t.TempDir()
	for name, on := range map[string]bool{"local": local, "remote": remote} {
		if on {
			if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	bin := filepath.Join(dir, "docker")
	script := `#!/bin/sh
d=` + dir + `
echo "$*" >> $d/calls
case "$1" in pull) touch $d/local $d/remote;; esac
case "$1 $2" in
"image inspect") case "$5" in
  ghcr.io/*) [ -f $d/remote ] || exit 1;;
  oriel-sandbox:*) [ -f $d/local ] || exit 1;;
  *) exit 1;;
  esac; echo sha256:1;;
"build -t") cat > $d/stdin;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return container.Engine{Bin: bin}, dir
}

// built is what the engine in dir was last asked to build, and its calls.
func built(dir string) (dockerfile, calls string) {
	df, _ := os.ReadFile(filepath.Join(dir, "stdin"))
	log, _ := os.ReadFile(filepath.Join(dir, "calls"))
	return string(df), string(log)
}

// A base built here (its pull failed) has all its layers under its local name, so the Dockerfile names it so.
func TestImageBuildsOnABaseBuiltHere(t *testing.T) {
	root, _ := repoWorkspace(t, "RUN true\n")
	e, dir := layerEngine(t, true, false)
	tag, err := Image(container.WithEngine(context.Background(), e), root, sandbox.Config{})
	if err != nil {
		t.Fatal(err)
	}
	df, log := built(dir)
	base := strings.Index(log, "image inspect --format {{.Id}} "+container.BaseImage())
	build := strings.Index(log, "build -t "+tag)
	if base < 0 || build < 0 || base > build {
		t.Errorf("calls:\n%s", log)
	}
	if !strings.HasPrefix(df, "FROM "+container.BaseImage()+"\n") {
		t.Errorf("built %q", df)
	}
}

// A pulled base is named by where it came from: an image store that keeps only some of a pulled image's layers
// (Docker's containerd store) fetches the rest from the registry the FROM names, and the local name points at
// Docker Hub, which hasn't got it. The name is chosen after the pull, so a first start gets it too.
func TestImageBuildsOnAPulledBaseByItsRegistryName(t *testing.T) {
	root, _ := repoWorkspace(t, "RUN true\n")
	e, dir := layerEngine(t, false, false)
	tag, err := Image(container.WithEngine(context.Background(), e), root, sandbox.Config{})
	if err != nil {
		t.Fatal(err)
	}
	df, log := built(dir)
	pull := strings.Index(log, "pull "+container.PublishedBase())
	build := strings.Index(log, "build -t "+tag)
	if pull < 0 || build < 0 || pull > build {
		t.Errorf("calls:\n%s", log)
	}
	want := "FROM " + container.PublishedBase() + "\nRUN true\n"
	if df != want || tag != container.ProjectImage(want) {
		t.Errorf("built %s as %q", df, tag)
	}
}

// A rebuild fetches a missing base first (as after an upgrade, before any session ran), and names it as a start does.
func TestRebuildFetchesAMissingBaseFirst(t *testing.T) {
	root, _ := repoWorkspace(t, "RUN true\n")
	e, dir := layerEngine(t, false, false)
	if err := Rebuild(container.WithEngine(context.Background(), e), root, sandbox.Config{}); err != nil {
		t.Fatal(err)
	}
	df, log := built(dir)
	pull := strings.Index(log, "pull "+container.PublishedBase())
	build := strings.Index(log, "build -t oriel-project:")
	if pull < 0 || build < 0 || pull > build || !strings.Contains(log[build:], "--no-cache") {
		t.Errorf("calls:\n%s", log)
	}
	if !strings.HasPrefix(df, "FROM "+container.PublishedBase()+"\n") {
		t.Errorf("built %q", df)
	}
}
