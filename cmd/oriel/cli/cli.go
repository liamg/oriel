package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/claude"
	"github.com/liamg/oriel/agent/codex"
	"github.com/liamg/oriel/agent/hostexec"
	"github.com/liamg/oriel/agent/packs"
	"github.com/liamg/oriel/app"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/sandbox/agentcmd"
	"github.com/liamg/oriel/update"
	"github.com/liamg/oriel/wire"
	"github.com/liamg/oriel/worker"
)

// orielHome is ~/.oriel, the worker's: sessions, workspaces, repos, its socket.
func orielHome() string {
	if d := os.Getenv("ORIEL_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".oriel")
}

// newWorker is this machine's worker with the built-in providers and prompt packs; handlers add methods to each app
// connection.
func newWorker(home string, log *slog.Logger, id core.WorkerID, name string, handlers ...func(wire.Conn)) (*worker.Worker, error) {
	providers := &agent.Providers{}
	providers.Register(claude.Provider{ProbeDir: filepath.Join(home, "probe")})
	providers.Register(&codex.Provider{})
	if _, err := packs.LoadDir(filepath.Join(app.DefaultConfigDir(), "agents")); err != nil {
		log.Warn("prompt packs", "err", err)
	}
	sandboxes := worker.NewSandboxFactory(nil)
	// A built-in kind's pack is the built-in one, or the user's version the app sends (prompts.set); agents/ adds kinds.
	builtinOr := func(name string) (agent.Pack, bool) {
		if src, ok := packs.Source(name); ok {
			p, err := packs.FromText(src, name)
			return p, err == nil
		}
		return packs.Get(name)
	}
	w, err := worker.New(worker.Options{ID: id, Name: name, Home: home, Providers: providers, Exec: hostexec.Exec{}, Sandbox: sandboxes, Packs: builtinOr, Log: log, Version: version,
		TrustPath: filepath.Join(app.DefaultConfigDir(), "trusted.toml"), Handlers: handlers})
	if err != nil {
		return nil, err
	}
	sandboxes.Attach(w)
	return w, nil
}

// runWorker is `oriel worker`: this machine's session host, on its socket, or serving an app elsewhere.
func runWorker(args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("oriel worker", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", orielHome(), "where sessions, workspaces and the socket live")
	connect := fs.String("connect", "", "an app's address to serve, host:port (a remote worker)")
	fingerprint := fs.String("fingerprint", "", "the app's certificate fingerprint, to pin")
	key := fs.String("key", os.Getenv("ORIEL_WORKER_KEY"), "this worker's key, from the app")
	name := fs.String("name", "", "this worker's name (the machine's if empty)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	host, _ := os.Hostname()
	var err error
	var id core.WorkerID // this machine's
	if *connect != "" {
		if *name == "" {
			*name = host
		}
		id = core.WorkerID(*name)
	}
	var w *worker.Worker
	var restarting chan struct{}
	if *connect == "" {
		if w, err = newWorker(*home, log, id, *name); err != nil {
			return err
		}
	} else if w, restarting, err = updatableWorker(*home, log, id, *name); err != nil || w == nil {
		return err
	}
	defer w.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go w.SampleLoad(ctx, 5*time.Second)
	go func() {
		if err := w.ReadUsage(ctx); err != nil {
			log.Warn("usage", "err", err)
		}
	}()
	go w.RunSchedules(ctx, 30*time.Second)
	if *connect == "" {
		log.Info("worker listening", "socket", worker.SocketPath(*home))
		return w.Listen(ctx, worker.SocketPath(*home))
	}
	hello := wire.Hello{Name: *name, Key: *key, OS: runtime.GOOS, Arch: runtime.GOARCH, Hostname: host, Version: version}
	log.Info("serving the app", "app", *connect, "name", *name, "certificate", wire.Words(*fingerprint))
	for ctx.Err() == nil {
		err := w.ServeApp(ctx, *connect, *fingerprint, hello)
		select {
		case <-restarting:
			select {} // the update re-execs this process
		default:
		}
		var rejected *wire.ErrRejected
		if errors.As(err, &rejected) {
			return err
		}
		log.Warn("lost the app; trying again", "err", err)
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
	}
	return nil
}

// updatableWorker is a remote worker that takes updates the app streams to it (distribution.md). It boots through
// the installer first: after a swap, starting the worker is the new version's health check, and a failure rolls back
// and re-execs onto the previous binary. restarting closes when an update's restart has begun.
func updatableWorker(home string, log *slog.Logger, id core.WorkerID, name string) (*worker.Worker, chan struct{}, error) {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return nil, nil, err
	}
	in := &update.Installer{Target: exe, Dir: filepath.Join(home, "updates")}
	restarting := make(chan struct{})
	var w *worker.Worker
	rcv := &update.Receiver{Installer: in, Keys: update.Keys(), Version: version, Restart: func() {
		close(restarting)
		w.HandOver()
		log.Error("re-exec after an update", "err", update.Reexec(in.Executable()))
		os.Exit(1)
	}}
	start := func(context.Context) (err error) {
		w, err = newWorker(home, log, id, name, rcv.Serve)
		return err
	}
	out, err := in.Boot(context.Background(), start)
	if out.RolledBack != nil {
		log.Warn("update rolled back", "version", out.RolledBack.Version, "reason", out.RolledBack.Reason)
		if w != nil {
			w.Close()
		}
		return nil, nil, update.Reexec(in.Executable())
	}
	if err != nil {
		return nil, nil, err
	}
	if out.Confirmed != "" {
		log.Info("updated", "version", out.Confirmed)
	}
	if w == nil {
		if err := start(context.Background()); err != nil {
			return nil, nil, err
		}
	}
	return w, restarting, nil
}

// runHook is `oriel hook …`, which the agents' side of Oriel runs: Claude's mod, Codex's MCP server.
func runHook(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "mcp" {
		return codex.MCPMain(stdin, stdout, os.Environ())
	}
	return claude.HookMain(args, stdin, stdout, stderr)
}

// runSandboxed runs what a sandbox's container asks of the oriel binary inside it.
func runSandboxed(args []string) (bool, int) {
	handled, err := agentcmd.Main(args)
	if !handled {
		return false, 0
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "oriel:", err)
		return true, 1
	}
	return true, 0
}

// localWorker is this machine's worker for the app, started if it isn't running.
func localWorker() (worker.Client, error) { return localClient() }

func localClient() (worker.Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return worker.DialLocal(ctx, orielHome(), "")
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(s string) error { *m = append(*m, s); return nil }

// sessionSpec is what `oriel new|review|init` starts, from its flags and arguments, run in wd; rest is the
// arguments after the flags.
func sessionSpec(cmd string, args []string, wd string, stderr io.Writer) (spec core.SessionSpec, rest []string, err error) {
	fs := flag.NewFlagSet("oriel "+cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var repos multi
	fs.Var(&repos, "repo", "a repo, by name or path (more than one for a multi-repo session)")
	kind := fs.String("kind", "work", "plan, work, review, tour, job, newrepo, direct or scratch")
	ticket := fs.String("ticket", "", "the ticket this is for")
	variant := fs.String("variant", "", "which of its kind's variants: a tour's walkthrough, onboarding, archaeology, showcase, demo, incident or feasibility")
	provider := fs.String("provider", "claude", `the agent: claude, codex, or "" for none`)
	model := fs.String("model", "", "the agent's model")
	effort := fs.String("effort", "", "the agent's effort")
	template := fs.String("template", "empty", "init: empty, go-module, go-cli, or a git URL")
	dir := fs.String("dir", "", "init: where the repo goes (~/src if empty)")
	sandboxed := fs.Bool("sandbox", false, "run the agent in a container")
	// Flags may follow the arguments: oriel new "fix stale search results" --repo search.
	for {
		if err := fs.Parse(args); err != nil {
			return spec, nil, err
		}
		if args = fs.Args(); len(args) == 0 {
			break
		}
		rest, args = append(rest, args[0]), args[1:]
	}
	spec = core.SessionSpec{Kind: core.Kind(*kind), Ticket: *ticket, Variant: *variant, Provider: *provider, Model: *model, Effort: *effort,
		Sandbox: core.SandboxPolicy{Container: *sandboxed}}
	for _, r := range repos {
		if strings.ContainsRune(r, filepath.Separator) && !filepath.IsAbs(r) {
			r = filepath.Join(wd, r)
		}
		spec.Repos = append(spec.Repos, core.RepoID(r))
	}
	switch cmd {
	case "new":
		spec.Prompt = strings.Join(rest, " ")
		if spec.Kind == core.KindDirect {
			spec.Dir = wd
			break
		}
		if len(spec.Repos) == 0 && spec.Kind != core.KindScratch && spec.Kind != core.KindPlan {
			spec.Repos = []core.RepoID{core.RepoID(wd)}
		}
	case "review":
		if len(rest) != 1 {
			return spec, rest, errors.New("usage: oriel review <pr-url> [--repo r]")
		}
		spec.Kind, spec.PR = core.KindReview, rest[0]
	case "init":
		if len(rest) != 1 {
			return spec, rest, errors.New("usage: oriel init <name> [--template go-cli] [--dir ~/src]")
		}
		spec.Kind, spec.Name, spec.Template, spec.Dir = core.KindNewRepo, rest[0], *template, *dir
	}
	return spec, rest, nil
}

// runSessionCLI starts and lists sessions on this machine's worker (sessions.md → Starting one).
func runSessionCLI(cmd string, args []string, stdout, stderr io.Writer) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	spec, rest, err := sessionSpec(cmd, args, wd, stderr)
	if err != nil {
		return err
	}
	c, err := localClient()
	if err != nil {
		return err
	}
	defer c.Conn.Close()
	ctx := context.Background()
	switch cmd {
	case "repos":
		if len(rest) == 2 && rest[0] == "add" {
			r, err := c.AddRepoPath(ctx, rest[1])
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "%s\t%s\n", r.ID, r.Path)
			return nil
		}
		if len(rest) >= 2 && len(rest) <= 3 && rest[0] == "clone" {
			req := worker.CloneReq{URL: rest[1]}
			if len(rest) == 3 {
				req.Name = rest[2]
			}
			r, err := c.CloneRepo(ctx, req)
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "%s\t%s\n", r.ID, r.Path)
			return nil
		}
		list, err := c.Repos(ctx)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		for _, r := range list {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", r.ID, r.Path, r.BaseBranch)
		}
		return tw.Flush()
	case "sessions":
		return listSessions(ctx, c, stdout)
	}
	id, err := c.Start(ctx, spec)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, id)
	return nil
}

func listSessions(ctx context.Context, c worker.Client, stdout io.Writer) error {
	sessions, err := c.Sessions(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	for _, s := range sessions {
		if !s.Archived {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.ID, s.Kind, s.Status, s.Branch)
		}
	}
	return tw.Flush()
}
