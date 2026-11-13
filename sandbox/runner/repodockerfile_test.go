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

// repoWorkspace is a sandboxed workspace whose primary project is repo, with df at the repo's DockerfilePath.
func repoWorkspace(t *testing.T, df string) (root, repo string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	root, repo = t.TempDir(), t.TempDir()
	if err := sandbox.SaveManifest(root, sandbox.Manifest{Projects: []sandbox.Project{{Name: "api", Repo: repo, Dir: "api", Primary: true}}}); err != nil {
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
	want := "FROM " + container.BaseImage() + "\nRUN apt-get install -y postgresql-18\n"
	if d.Dockerfile != want || d.Image != container.ProjectImage(want) {
		t.Errorf("image %q from\n%s", d.Image, d.Dockerfile)
	}
	if d.ImageFrom != filepath.Base(repo)+"'s "+DockerfilePath+" on the base image" {
		t.Errorf("from %q", d.ImageFrom)
	}
}

// A repo's Dockerfile with a FROM of its own is the whole image, built as written.
func TestRepoDockerfileWithFromIsBuiltAsWritten(t *testing.T) {
	for _, df := range []string{"FROM debian\nRUN true\n", "# syntax note\n  from debian AS b\n"} {
		root, repo := repoWorkspace(t, df)
		ctx := container.WithEngine(context.Background(), describeEngine(t))
		d := Describe(ctx, root, sandbox.Config{})
		if d.Dockerfile != df || d.Image != container.ProjectImage(df) || d.ImageFrom != filepath.Base(repo)+"'s "+DockerfilePath {
			t.Errorf("%q: image %q from %q:\n%s", df, d.Image, d.ImageFrom, d.Dockerfile)
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

// layerEngine is a CLI with no images but the base when base is set, recording its calls.
func layerEngine(t *testing.T, base bool) (container.Engine, string) {
	t.Helper()
	dir := t.TempDir()
	calls, bin := filepath.Join(dir, "calls"), filepath.Join(dir, "docker")
	has := "exit 1"
	if base {
		has = `case "$5" in oriel-sandbox:*) echo sha256:1;; *) exit 1;; esac`
	}
	script := `#!/bin/sh
echo "$*" >> ` + calls + `
case "$1 $2" in
"image inspect") ` + has + `;;
"build -t") cat > ` + filepath.Join(dir, "stdin") + `;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return container.Engine{Bin: bin}, dir
}

// Image makes sure of the base before building a Dockerfile on it: its FROM names a local tag.
func TestImageBuildsARepoDockerfileOnTheBase(t *testing.T) {
	root, _ := repoWorkspace(t, "RUN true\n")
	e, dir := layerEngine(t, true)
	tag, err := Image(container.WithEngine(context.Background(), e), root, sandbox.Config{})
	if err != nil {
		t.Fatal(err)
	}
	log, _ := os.ReadFile(filepath.Join(dir, "calls"))
	base := strings.Index(string(log), "image inspect --format {{.Id}} "+container.BaseImage())
	build := strings.Index(string(log), "build -t "+tag)
	if base < 0 || build < 0 || base > build {
		t.Errorf("calls:\n%s", log)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "stdin")); !strings.HasPrefix(string(got), "FROM "+container.BaseImage()+"\n") {
		t.Errorf("built %q", got)
	}
}

// A rebuild fetches a missing base first (as after an upgrade, before any session ran); the build's FROM would
// otherwise look for it on a registry.
func TestRebuildFetchesAMissingBaseFirst(t *testing.T) {
	root, _ := repoWorkspace(t, "RUN true\n")
	e, dir := layerEngine(t, false)
	if err := Rebuild(container.WithEngine(context.Background(), e), root, sandbox.Config{}); err != nil {
		t.Fatal(err)
	}
	log, _ := os.ReadFile(filepath.Join(dir, "calls"))
	pull := strings.Index(string(log), "pull "+container.PublishedBase())
	build := strings.Index(string(log), "build -t oriel-project:")
	if pull < 0 || build < 0 || pull > build || !strings.Contains(string(log)[build:], "--no-cache") {
		t.Errorf("calls:\n%s", log)
	}
}
