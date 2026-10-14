// Package container runs oriel's sandboxes with the docker CLI: one long-lived
// container per workspace, which each agent launch execs into.
package container

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Bin is the docker CLI; ORIEL_DOCKER=podman swaps in Podman, which answers
// to the same commands.
var Bin = func() string {
	if b := os.Getenv("ORIEL_DOCKER"); b != "" {
		return b
	}
	return "docker"
}()

// Mount binds a host path into the container.
type Mount struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"readOnly,omitempty"`
	// Volume makes Source a named docker volume rather than a host path.
	Volume bool `json:"volume,omitempty"`
}

// Spec is everything a container is created with. A long-lived container
// whose spec has changed is replaced.
type Spec struct {
	Name    string            `json:"name"`
	Image   string            `json:"image"`
	User    string            `json:"user"`             // uid:gid
	Groups  []string          `json:"groups,omitempty"` // supplementary, --group-add
	Home    string            `json:"home"`
	Workdir string            `json:"workdir"`
	Mounts  []Mount           `json:"mounts"`
	Tmpfs   []string          `json:"tmpfs,omitempty"`
	Network string            `json:"network,omitempty"`
	Env     []string          `json:"env,omitempty"`
	Hosts   []string          `json:"hosts,omitempty"` // --add-host entries
	Labels  map[string]string `json:"-"`
	// Entrypoint and Command default to an idle `sleep infinity`.
	Entrypoint string   `json:"entrypoint,omitempty"`
	Command    []string `json:"command,omitempty"`
	// Memory caps the container's memory in docker's units ("8g"); ""
	// is DefaultMemory and "none" no cap.
	Memory string `json:"memory,omitempty"`
	// PidsLimit caps its processes; 0 is DefaultPidsLimit and -1 no cap.
	PidsLimit int `json:"pidsLimit,omitempty"`
}

// The limits a container gets unless its spec says otherwise: enough for a
// build, not enough for a runaway to take the machine down.
const (
	DefaultMemory    = "8g"
	DefaultPidsLimit = 4096
)

// Hash identifies the spec, for spotting a container made from another one.
func (s Spec) Hash() string {
	c := s
	c.Labels = nil
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:16]
}

// specLabel carries Hash on the container.
const specLabel = "oriel.spec"

// runArgs are the docker run arguments for s, detached.
func (s Spec) runArgs() []string { return s.args("-d") }

// InteractiveArgv is the command that runs s in the foreground on the
// caller's terminal, removed when it exits. Signals reach the process inside.
func (s Spec) InteractiveArgv(envFile string) []string {
	mode := []string{"--rm", "-i", "-t"}
	if envFile != "" {
		mode = append(mode, "--env-file", envFile)
	}
	return append([]string{Bin}, s.args(mode...)...)
}

// OnceArgv is the command that runs the container to completion with no
// terminal, for a step whose output goes to a log.
func (s Spec) OnceArgv(envFile string) []string {
	mode := []string{"--rm"}
	if envFile != "" {
		mode = append(mode, "--env-file", envFile)
	}
	return append([]string{Bin}, s.args(mode...)...)
}

func (s Spec) args(mode ...string) []string {
	args := append([]string{"run"}, mode...)
	args = append(args, "--name", s.Name, "--init",
		"--user", s.User,
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--label", specLabel+"="+s.Hash(),
		"-w", s.Workdir,
		"-e", "HOME="+s.Home,
	)
	switch s.Memory {
	case "none":
	case "":
		args = append(args, "--memory", DefaultMemory)
	default:
		args = append(args, "--memory", s.Memory)
	}
	pids := s.PidsLimit
	if pids == 0 {
		pids = DefaultPidsLimit
	}
	args = append(args, "--pids-limit", strconv.Itoa(pids))
	labels := make([]string, 0, len(s.Labels))
	for k, v := range s.Labels {
		labels = append(labels, k+"="+v)
	}
	sort.Strings(labels)
	for _, l := range labels {
		args = append(args, "--label", l)
	}
	if s.Network != "" {
		args = append(args, "--network", s.Network)
	}
	for _, g := range s.Groups {
		args = append(args, "--group-add", g)
	}
	for _, h := range s.Hosts {
		args = append(args, "--add-host", h)
	}
	for _, e := range s.Env {
		args = append(args, "-e", e)
	}
	for _, t := range s.Tmpfs {
		args = append(args, "--tmpfs", t)
	}
	for _, m := range s.Mounts {
		kind := "bind"
		if m.Volume {
			kind = "volume"
		}
		v := "type=" + kind + ",source=" + m.Source + ",target=" + m.Target
		if m.ReadOnly {
			v += ",readonly"
		}
		args = append(args, "--mount", v)
	}
	entry, cmd := s.Entrypoint, s.Command
	if entry == "" {
		entry, cmd = "sleep", []string{"infinity"}
	}
	args = append(args, "--entrypoint", entry, s.Image)
	return append(args, cmd...)
}

// Run runs a docker command, returning stdout.
func Run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, Bin, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), fmt.Errorf("%s %s: %s", Bin, firstArg(args), msg)
	}
	return out.String(), nil
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// Info is what `docker version` says about the daemon.
type Info struct {
	Client string
	Server string
}

// Ping reports whether the CLI is installed and the daemon answers.
func Ping(ctx context.Context) (Info, error) {
	if _, err := exec.LookPath(Bin); err != nil {
		return Info{}, fmt.Errorf("%s is not installed", Bin)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := Run(ctx, "version", "--format", "{{.Client.Version}} {{.Server.Version}}")
	f := strings.Fields(out)
	if err != nil || len(f) < 2 {
		if err == nil {
			err = errors.New("no answer from the daemon")
		}
		return Info{}, fmt.Errorf("%s is not running: %w", Bin, err)
	}
	return Info{Client: f[0], Server: f[1]}, nil
}

// State is a container as docker inspect reports it.
type State struct {
	Exists  bool
	Running bool
	Spec    string
}

// Inspect reports the container called name.
func Inspect(ctx context.Context, name string) State {
	out, err := Run(ctx, "inspect", "--type", "container", "--format",
		`{{.State.Running}} {{index .Config.Labels "`+specLabel+`"}}`, name)
	if err != nil {
		return State{}
	}
	f := strings.Fields(out)
	st := State{Exists: true}
	if len(f) > 0 {
		st.Running = f[0] == "true"
	}
	if len(f) > 1 {
		st.Spec = f[1]
	}
	return st
}

// Ensure leaves a container running exactly as spec says: started if it was
// stopped, replaced if it was made from another spec. Reports whether it was
// (re)created.
func Ensure(ctx context.Context, spec Spec) (bool, error) {
	st := Inspect(ctx, spec.Name)
	if st.Exists && st.Spec == spec.Hash() {
		if st.Running {
			return false, nil
		}
		if _, err := Run(ctx, "start", spec.Name); err == nil {
			return false, nil
		}
	}
	if st.Exists {
		if err := Remove(ctx, spec.Name); err != nil {
			return false, err
		}
	}
	if _, err := Run(ctx, spec.runArgs()...); err != nil {
		return false, err
	}
	return true, nil
}

// Remove deletes the container called name, running or not.
func Remove(ctx context.Context, name string) error {
	if !Inspect(ctx, name).Exists {
		return nil
	}
	_, err := Run(ctx, "rm", "-f", name)
	return err
}

// ImageExists reports whether the image is present locally.
func ImageExists(ctx context.Context, image string) bool {
	_, err := Run(ctx, "image", "inspect", "--format", "{{.Id}}", image)
	return err == nil
}

// ImageID is the content id of a local image, which changes whenever the
// image is rebuilt or repulled under the same tag.
func ImageID(ctx context.Context, image string) (string, error) {
	out, err := Run(ctx, "image", "inspect", "--format", "{{.Id}}", image)
	return strings.TrimSpace(out), err
}

// Build builds dockerfile (its text) as tag, with an empty context.
func Build(ctx context.Context, tag, dockerfile string) error {
	cmd := exec.CommandContext(ctx, Bin, "build", "-t", tag, "-")
	cmd.Stdin = strings.NewReader(dockerfile)
	var errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &errb, &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s build %s: %w: %s", Bin, tag, err, tail(errb.String(), 800))
	}
	return nil
}

// BuildFiles builds dockerfile as tag with files (relative path to content)
// as its context, writing the build's progress to out.
func BuildFiles(ctx context.Context, tag, dockerfile string, files map[string][]byte, out io.Writer) error {
	buildMu.Lock()
	defer buildMu.Unlock()
	if ImageExists(ctx, tag) {
		return nil
	}
	dir, err := os.MkdirTemp("", "oriel-build-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	for name, b := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		return err
	}
	args := []string{"build", "-t", tag}
	if filepath.Base(Bin) != "podman" {
		args = append(args, "--progress", "plain")
	}
	cmd := exec.CommandContext(ctx, Bin, append(args, dir)...)
	var errb bytes.Buffer
	w := io.MultiWriter(out, &errb)
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s build %s: %w: %s", Bin, tag, err, tail(errb.String(), 800))
	}
	return nil
}

// Output runs argv in a throwaway container from image and returns stdout.
func Output(ctx context.Context, image string, argv ...string) (string, error) {
	args := append([]string{"run", "--rm", "--network", "none", "--entrypoint", argv[0], image}, argv[1:]...)
	return Run(ctx, args...)
}

// EnsureNetwork creates a bridge network if it is missing. Internal networks
// have no route out.
func EnsureNetwork(ctx context.Context, name string, internal bool) error {
	if _, err := Run(ctx, "network", "inspect", name); err == nil {
		return nil
	}
	args := []string{"network", "create", "--label", "oriel=1"}
	if internal {
		args = append(args, "--internal")
	}
	_, err := Run(ctx, append(args, name)...)
	return err
}

// Connect attaches a container to a network, if it is not already on it.
func Connect(ctx context.Context, network, name string) error {
	out, err := Run(ctx, "inspect", "--format", "{{json .NetworkSettings.Networks}}", name)
	if err == nil && strings.Contains(out, `"`+network+`"`) {
		return nil
	}
	_, err = Run(ctx, "network", "connect", network, name)
	return err
}

// EnsureTmpfsVolume creates a volume held in memory, owned by uid:gid, if it
// is missing. Containers sharing it share unix sockets made in it.
func EnsureTmpfsVolume(ctx context.Context, name string, uid, gid int) error {
	if _, err := Run(ctx, "volume", "inspect", name); err == nil {
		return nil
	}
	_, err := Run(ctx, "volume", "create", "--label", "oriel=1", "--driver", "local",
		"--opt", "type=tmpfs", "--opt", "device=tmpfs",
		"--opt", fmt.Sprintf("o=uid=%d,gid=%d,mode=0700", uid, gid), name)
	return err
}

// RemoveVolume deletes a volume; a missing one is fine.
func RemoveVolume(ctx context.Context, name string) {
	_, _ = Run(ctx, "volume", "rm", name)
}

// RemoveNetwork deletes a network; a missing one is fine.
func RemoveNetwork(ctx context.Context, name string) {
	_, _ = Run(ctx, "network", "rm", name)
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// PrepareMountpoints creates, as the calling user, every mount target that
// lies inside another bind mount's source. Left to docker they would be made
// as root, in directories the user then cannot delete.
func PrepareMountpoints(mounts []Mount) error {
	for _, m := range mounts {
		if m.Volume {
			continue
		}
		for _, p := range mounts {
			if p.Target == m.Target || p.Volume {
				continue
			}
			rel, err := filepath.Rel(p.Target, m.Target)
			if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
				continue
			}
			host := filepath.Join(p.Source, rel)
			if _, err := os.Lstat(host); err == nil {
				continue
			}
			fi, err := os.Stat(m.Source)
			if err != nil {
				return err
			}
			if fi.IsDir() {
				if err := os.MkdirAll(host, 0o700); err != nil {
					return err
				}
				continue
			}
			if err := os.MkdirAll(filepath.Dir(host), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(host, nil, 0o600); err != nil {
				return err
			}
		}
	}
	return nil
}
