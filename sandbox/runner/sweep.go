package runner

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/container"
)

const (
	// launchGrace keeps a sidecar whose agent is about to start.
	launchGrace = 2 * time.Minute
	// imageKeep is how long an image no launch has used is kept.
	imageKeep = 14 * 24 * time.Hour
)

// Sweep removes what idle sandboxes leave behind: an agent container whose
// terminal is gone (attached reports whether root has one), the egress
// sidecar and network of a workspace with no agent running, and images no
// launch has used for imageKeep. All of it is made again by the next launch.
// It returns what it removed.
func Sweep(ctx context.Context, attached func(root string) bool) []string {
	out := sweepAgents(ctx, attached)
	out = append(out, sweepSidecars(ctx)...)
	out = append(out, sweepImages(ctx)...)
	return append(out, sweepBuilds(ctx)...)
}

// buildKeep is how many of the newest oriel builds and of each agent's downloads stay,
// so the next launch does not wait on a rebuild or a download.
const buildKeep = 2

// sweepBuilds removes Linux oriel builds and agents' downloads that no oriel
// container mounts, beyond the newest buildKeep of each. A build is 20 MB and
// an agent's download 240 MB, and both arrive often.
func sweepBuilds(ctx context.Context) []string {
	ids, err := container.Run(ctx, "ps", "-q", "--filter", "label=oriel.root")
	if err != nil {
		return nil
	}
	var mounted []string
	if ids := strings.Fields(ids); len(ids) > 0 {
		args := append([]string{"inspect", "--format", `{{range .Mounts}}{{.Source}}{{"\n"}}{{end}}`}, ids...)
		out, err := container.Run(ctx, args...)
		if err != nil {
			// Without the list, any build may be in use.
			return nil
		}
		mounted = strings.Fields(out)
	}
	var removed []string
	oriels, _ := filepath.Glob(filepath.Join(sandbox.Base(), "bin", "oriel-linux-*"))
	oriels = slices.DeleteFunc(oriels, func(p string) bool { return strings.HasSuffix(p, ".stamp") })
	for _, p := range staleBuilds(oriels, mounted, buildKeep) {
		if os.Remove(p) == nil {
			_ = os.Remove(p + ".stamp")
			removed = append(removed, p)
		}
	}
	caches, _ := filepath.Glob(sandbox.AgentCache("*"))
	for _, c := range caches {
		downloads, _ := filepath.Glob(filepath.Join(c, "*"))
		for _, p := range staleBuilds(downloads, mounted, buildKeep) {
			if os.RemoveAll(p) == nil {
				removed = append(removed, p)
			}
		}
	}
	return removed
}

// staleBuilds are the paths that no mount source is at or under, other than
// the keep most recently changed.
func staleBuilds(paths, mounted []string, keep int) []string {
	type build struct {
		path string
		at   time.Time
	}
	var bs []build
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		bs = append(bs, build{p, fi.ModTime()})
	}
	slices.SortFunc(bs, func(a, b build) int { return b.at.Compare(a.at) })
	var out []string
	for i, b := range bs {
		if i < keep {
			continue
		}
		inUse := slices.ContainsFunc(mounted, func(m string) bool {
			return m == b.path || strings.HasPrefix(m, b.path+string(filepath.Separator))
		})
		if !inUse {
			out = append(out, b.path)
		}
	}
	return out
}

func sweepAgents(ctx context.Context, attached func(root string) bool) []string {
	out, err := container.Run(ctx, "ps", "-a", "--filter", "label=oriel.root",
		"--format", `{{.Names}}`+"\t"+`{{.Label "oriel.root"}}`)
	if err != nil {
		return nil
	}
	var removed []string
	for _, name := range orphanAgents(out, attached) {
		if container.Remove(ctx, name) == nil {
			removed = append(removed, name)
		}
	}
	return removed
}

// orphanAgents picks, from `docker ps` lines of name and root, this
// machine's agent containers with no terminal and no launch under way.
func orphanAgents(ps string, attached func(root string) bool) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(ps), "\n") {
		name, root, ok := strings.Cut(line, "\t")
		if _, ours := sandbox.Load(root); !ok || !ours || name != Name(root) {
			continue
		}
		if attached(root) || launchedWithin(root, launchGrace) {
			continue
		}
		out = append(out, name)
	}
	return out
}

func sweepSidecars(ctx context.Context) []string {
	out, err := container.Run(ctx, "ps", "-a", "--filter", "label=oriel.role=egress",
		"--format", `{{.Names}}`+"\t"+`{{.Label "oriel.root"}}`)
	if err != nil {
		return nil
	}
	// A sidecar is only removed when docker says its agent is not running:
	// no answer is not a no.
	ps, err := container.Run(ctx, "ps", "--format", "{{.Names}}")
	if err != nil {
		return nil
	}
	running := strings.Fields(ps)
	var removed []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		name, root, ok := strings.Cut(line, "\t")
		// Only this machine's own: another oriel sharing the daemon has its own sweep.
		if _, ours := sandbox.Load(root); !ok || !ours || name != ProxyName(root) {
			continue
		}
		if slices.Contains(running, Name(root)) || launchedWithin(root, launchGrace) {
			continue
		}
		if container.Remove(ctx, name) != nil {
			continue
		}
		container.RemoveNetwork(ctx, NetworkName(root))
		container.RemoveVolume(ctx, RunVolumeName(root))
		removed = append(removed, name)
	}
	return removed
}

// launchedWithin reports a launch of root's sandbox in the last d.
func launchedWithin(root string, d time.Duration) bool {
	fi, err := os.Stat(launchStamp(root))
	return err == nil && time.Since(fi.ModTime()) < d
}

// launchStamp is the file whose time says when root's sandbox was last launched: the agent's env file, written as
// a launch ends.
func launchStamp(root string) string { return filepath.Join(sandbox.Dir(root), "agent.env") }

// markLaunch says root's sandbox is launching, before a launch touches its sidecar or network, so a sweep meanwhile
// leaves them be.
func markLaunch(root string) error {
	now := time.Now()
	if err := os.Chtimes(launchStamp(root), now, now); !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.WriteFile(launchStamp(root), nil, 0o600)
}

func imageStamps() string { return filepath.Join(sandbox.Base(), "cache", "images") }

// markImageUsed records a launch from image, for the sweep.
func markImageUsed(image string) {
	repo, tag, ok := strings.Cut(image, ":")
	if !ok || !slices.Contains(imageRepos, repo) {
		return
	}
	touch(filepath.Join(repoStamps(repo), tag))
}

func touch(path string) {
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	now := time.Now()
	if os.Chtimes(path, now, now) != nil {
		_ = os.WriteFile(path, nil, 0o600)
	}
}

// imageRepos are the repositories Oriel tags its own images under.
var imageRepos = []string{"oriel-sandbox"}

// repoStamps is where the launches of repo's images are recorded.
func repoStamps(repo string) string {
	if repo == imageRepos[0] {
		return imageStamps()
	}
	return filepath.Join(imageStamps(), repo)
}

func sweepImages(ctx context.Context) []string {
	var removed []string
	for _, repo := range imageRepos {
		out, err := container.Run(ctx, "images", repo, "--format", "{{.Tag}}")
		if err != nil {
			continue
		}
		stamps := repoStamps(repo)
		for _, tag := range staleImages(strings.Fields(out), stamps, time.Now()) {
			// Without -f: an image a container still uses stays.
			if _, err := container.Run(ctx, "rmi", repo+":"+tag); err == nil {
				_ = os.Remove(filepath.Join(stamps, tag))
				removed = append(removed, repo+":"+tag)
			}
		}
	}
	return removed
}

// staleImages are the tags last used longer than imageKeep before now. A tag
// seen for the first time starts its clock here rather than being removed.
func staleImages(tags []string, stamps string, now time.Time) []string {
	var out []string
	for _, tag := range tags {
		fi, err := os.Stat(filepath.Join(stamps, tag))
		if err != nil {
			touch(filepath.Join(stamps, tag))
			continue
		}
		if now.Sub(fi.ModTime()) > imageKeep {
			out = append(out, tag)
		}
	}
	return out
}
