package runner

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/container"
)

// DockerfilePath is where a repository asks for its own sandbox image.
const DockerfilePath = ".oriel/sandbox.Dockerfile"

// Image picks root's image, building it if needed: the project's image named in settings, else the base with the
// primary project's Dockerfile on it when the session allows it (Manifest.CustomImage), else the image named in
// settings, else the base.
func Image(ctx context.Context, root string, cfg sandbox.Config) (string, error) {
	p := pick(ctx, root, cfg)
	if p.refused != nil {
		return "", p.refused
	}
	if p.dockerfile == "" {
		return p.tag, nil
	}
	p, err := ensureBase(ctx, p)
	if err != nil {
		return "", err
	}
	return p.tag, ensureImage(ctx, p.tag, p.dockerfile)
}

// picked is the image root's sandbox runs: its tag, its source ("" for one named in settings) and where it comes
// from. layers is a repo's Dockerfile, built on the base, which has to be there first; refused is why a repo's
// Dockerfile can't be.
type picked struct {
	tag, dockerfile, from string
	layers                string
	refused               error
}

func pick(ctx context.Context, root string, cfg sandbox.Config) picked {
	if ws, ok := sandbox.LoadManifest(root); ok && ws.CustomImage {
		for _, p := range ws.Projects {
			if !p.Primary {
				continue
			}
			ps := cfg.Projects[p.Repo]
			if ps.Image != "" {
				return picked{tag: ps.Image, from: "sandbox.image for " + filepath.Base(p.Repo)}
			}
			if df, ok := RepoDockerfile(p.Repo); ok {
				from := filepath.Base(p.Repo) + "'s " + DockerfilePath
				if hasFrom(df) {
					return picked{dockerfile: df, from: from, refused: fmt.Errorf("%s has a FROM: a repo's sandbox Dockerfile only "+
						"adds to Oriel's base image, so it can't name an image of its own. Remove its FROM lines", from)}
				}
				return picked{from: from + " on the base image", layers: df}.onBase(ctx)
			}
		}
	}
	if cfg.Image != "" {
		return picked{tag: cfg.Image, from: "sandbox.image"}
	}
	return picked{tag: container.BaseImage(), dockerfile: container.BaseDockerfile, from: "the base image"}
}

// hasFrom says whether df names an image of its own, as a base or a build stage. A repo's Dockerfile may only add
// to Oriel's base.
func hasFrom(df string) bool {
	for line := range strings.SplitSeq(df, "\n") {
		if f := strings.Fields(line); len(f) > 0 && strings.EqualFold(f[0], "FROM") {
			return true
		}
	}
	return false
}

// onBase puts the base under p's layers, which gives its tag.
func (p picked) onBase(ctx context.Context) picked {
	if p.layers == "" {
		return p
	}
	p.dockerfile = "FROM " + baseRef(ctx) + "\n" + p.layers
	p.tag = container.ProjectImage(p.dockerfile)
	return p
}

// baseRef is the name a Dockerfile's FROM gives the base. A pulled base goes by where it came from: an image store
// that keeps only some of a pulled image's layers (Docker's containerd store) fetches the rest from the registry the
// FROM names, and the local name means Docker Hub. A base built here has every layer under its local name.
func baseRef(ctx context.Context) string {
	if container.ImageExists(ctx, container.PublishedBase()) {
		return container.PublishedBase()
	}
	return container.BaseImage()
}

// ensureBase fetches the base p's layers build on, then names it in p's Dockerfile as it now is.
func ensureBase(ctx context.Context, p picked) (picked, error) {
	if p.layers == "" {
		return p, nil
	}
	if err := ensureImage(ctx, container.BaseImage(), container.BaseDockerfile); err != nil {
		return p, err
	}
	return p.onBase(ctx), nil
}

// BuildLog is the file tag's last build wrote to.
func BuildLog(tag string) string {
	return filepath.Join(sandbox.Base(), "builds", strings.NewReplacer(":", "-", "/", "-").Replace(tag)+".log")
}

// ensureImage builds tag from dockerfile if it isn't there yet, saying so first.
func ensureImage(ctx context.Context, tag, dockerfile string) error {
	if !container.ImageExists(ctx, tag) {
		step := "Building the sandbox image"
		if dockerfile == container.BaseDockerfile {
			step = "Fetching the sandbox image"
		}
		sandbox.Report(ctx, sandbox.Progress{Step: step, Note: "This happens once: later sessions reuse the image and start in seconds."})
	}
	return container.EnsureImage(ctx, tag, dockerfile, BuildLog(tag))
}

// Rebuild builds root's image again from its source, skipping the cache. An image named in settings has none.
func Rebuild(ctx context.Context, root string, cfg sandbox.Config) error {
	p := pick(ctx, root, cfg)
	if p.refused != nil {
		return p.refused
	}
	if p.dockerfile == "" {
		return fmt.Errorf("the sandbox runs %s, named in %s: Oriel doesn't build it", p.tag, p.from)
	}
	p, err := ensureBase(ctx, p)
	if err != nil {
		return err
	}
	return container.Rebuild(ctx, p.tag, p.dockerfile, BuildLog(p.tag))
}

// Describe is root's container and image, and what the running container mounts.
func Describe(ctx context.Context, root string, cfg sandbox.Config) sandbox.Description {
	p := pick(ctx, root, cfg)
	d := sandbox.Description{Container: Name(root), Image: p.tag, ImageFrom: p.from, Dockerfile: p.dockerfile}
	if p.refused != nil {
		d.Refused = p.refused.Error()
	} else {
		d.Built = container.ImageExists(ctx, p.tag)
	}
	if _, err := os.Stat(BuildLog(p.tag)); p.tag != "" && err == nil {
		d.BuildLog = BuildLog(p.tag)
	}
	d.Mounts, d.Running = containerMounts(ctx, Name(root))
	if id, err := sandbox.CurrentIdentity(); err == nil {
		d.Places = map[string]string{
			sandbox.HomeDir(root): id.Home,
			sandbox.GoCache():     GoCacheDir,
			toolchainDir():        MiseDir,
		}
	}
	for _, m := range d.Mounts {
		if _, ok := d.Places[m.Host]; !ok && m.Host != m.Agent && d.Places != nil {
			d.Places[m.Host] = m.Agent
		}
	}
	return d
}

// Shell is the command that runs a shell in root's running container, in cwd, as the agent runs: its user, its
// environment and its network.
func Shell(ctx context.Context, e container.Engine, root, cwd string) ([]string, error) {
	if _, running := containerMounts(container.WithEngine(ctx, e), Name(root)); !running {
		return nil, errors.New("the agent's sandbox isn't running: start the session's agent first")
	}
	args := []string{"exec", "-i", "-t", "-e", "TERM=xterm-256color", "-e", "COLORTERM=truecolor", "-e", "TERM_PROGRAM=oriel", "-w", cwd, Name(root)}
	return e.Command(append(args, sandbox.ShellArgv...)...), nil
}

// containerMounts are what the container name mounts, read from the engine, while it runs.
func containerMounts(ctx context.Context, name string) ([]sandbox.Bind, bool) {
	out, err := container.Run(ctx, "container", "inspect", "--format", "{{.State.Running}} {{json .Mounts}}", name)
	if err != nil {
		return nil, false
	}
	running, js, _ := strings.Cut(strings.TrimSpace(out), " ")
	var ms []struct {
		Source, Destination string
		RW                  bool
	}
	_ = json.Unmarshal([]byte(js), &ms)
	var binds []sandbox.Bind
	for _, m := range ms {
		binds = append(binds, sandbox.Bind{Host: m.Source, Agent: m.Destination, ReadOnly: !m.RW})
	}
	slices.SortFunc(binds, func(a, b sandbox.Bind) int { return cmp.Compare(a.Agent, b.Agent) })
	return binds, running == "true"
}

// RepoDockerfile reads the repo's sandbox Dockerfile: the checkout's, committed or not, else its default branch's.
func RepoDockerfile(repo string) (string, bool) {
	if b, err := os.ReadFile(filepath.Join(repo, DockerfilePath)); err == nil && strings.TrimSpace(string(b)) != "" {
		return string(b), true
	}
	base := defaultBranch(repo)
	out, err := sandbox.Git(context.Background(), repo, "show", base+":"+DockerfilePath)
	if err != nil || strings.TrimSpace(out) == "" {
		return "", false
	}
	return out, true
}

// defaultBranch is repo's default branch: origin's HEAD, else the first of
// the usual names that exists.
func defaultBranch(repo string) string {
	if s, err := sandbox.Git(context.Background(), repo, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"); err == nil {
		ref := strings.TrimSpace(s)
		if p := strings.TrimPrefix(ref, "refs/remotes/"); p != ref && p != "" {
			return p
		}
	}
	for _, c := range []string{"origin/main", "origin/master", "main", "master", "develop", "trunk"} {
		if _, err := sandbox.Git(context.Background(), repo, "rev-parse", "--verify", "--quiet", c+"^{commit}"); err == nil {
			return c
		}
	}
	return "HEAD"
}
