package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/liamg/oriel/app"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/update"
)

// releaseRepo is where Oriel's releases are published.
const releaseRepo = "liamg/oriel"

// healthWait bounds how long a new version has to open its window after a swap before it's rolled back.
const healthWait = 2 * time.Minute

// selfInstaller is the installed copy of Oriel this process runs from: the .app bundle on macOS when it's in one,
// else the binary. Its updates are staged in the state directory (distribution.md).
func selfInstaller() (*update.Installer, string, error) {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return nil, "", err
	}
	in := &update.Installer{Target: exe, Dir: filepath.Join(logs.StateDir(), "updates")}
	if bundle, inner, ok := appBundle(exe); ok {
		in.Target, in.Exe = bundle, inner
		return in, update.KindBundle, nil
	}
	return in, update.KindBinary, nil
}

// appBundle splits /Applications/Oriel.app/Contents/MacOS/oriel into the bundle and the executable inside it.
func appBundle(exe string) (bundle, inner string, ok bool) {
	i := strings.LastIndex(exe, ".app"+string(filepath.Separator)+"Contents"+string(filepath.Separator))
	if i < 0 {
		return "", "", false
	}
	return exe[:i+4], exe[i+5:], true
}

// ghToken is the user's GitHub token, from their gh login, for a private repo's releases; "" without one.
func ghToken(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(string(out)), nil
}

// newUpdater is the updater for this copy, reading releases from src (GitHub's if nil); nil when this build trusts
// no release key, as a build CI didn't make doesn't.
func newUpdater(src update.Source) (*update.Updater, error) {
	keys := update.Keys()
	if len(keys) == 0 {
		return nil, errors.New("this build of Oriel has no release key, so it can't update itself")
	}
	if len(ext.Native()) > 0 {
		return nil, errors.New("this is a custom build (oriel build --with): a release would drop its plugins, so rebuild it instead")
	}
	in, kind, err := selfInstaller()
	if err != nil {
		return nil, err
	}
	if src == nil {
		src = &update.GitHub{Repo: releaseRepo, Token: ghToken}
	}
	return &update.Updater{Source: src, Keys: keys, Version: version, Installer: in, Kind: kind,
		CacheDir: filepath.Join(in.Dir, "downloads")}, nil
}

// appUpdates sets cfg up to update Oriel, and confirms or rolls back a swap: a new version must open its window
// within healthWait, and one that doesn't, or crashed on the start before, is replaced by the previous one.
func appUpdates(cfg *app.Config) {
	in, _, err := selfInstaller()
	if err != nil {
		return
	}
	started := make(chan struct{})
	cfg.Started = func() { close(started) }
	go func() {
		out, err := in.Boot(context.Background(), func(ctx context.Context) error {
			select {
			case <-started:
				return nil
			case <-time.After(healthWait):
				return fmt.Errorf("its window didn't open within %s", healthWait)
			}
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "oriel: update:", err)
		}
		if out.RolledBack != nil {
			fmt.Fprintf(os.Stderr, "oriel: %s rolled back: %s\n", out.RolledBack.Version, out.RolledBack.Reason)
			fmt.Fprintln(os.Stderr, "oriel:", update.Reexec(in.Executable()))
			os.Exit(1)
		}
	}()
	u, err := newUpdater(nil)
	if err != nil {
		return
	}
	cfg.Updates = &app.Updates{Updater: u, Relaunch: func() error { return relaunch(in) }}
}

// relaunch starts the installed Oriel again, on its own, with this one's arguments.
func relaunch(in *update.Installer) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" && in.Exe != "" {
		cmd = exec.Command("open", append([]string{"-n", in.Target, "--args"}, os.Args[1:]...)...)
	} else {
		cmd = exec.Command(in.Executable(), os.Args[1:]...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	return cmd.Start()
}

// runUpdate is `oriel update`: check for a newer Oriel and install it, without the app.
func runUpdate(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("oriel update", flag.ContinueOnError)
	fs.SetOutput(stderr)
	check := fs.Bool("check", false, "only say whether there's a newer release")
	channel := fs.String("channel", update.Stable, "stable or nightly")
	from := fs.String("from", "", "a release directory (manifest.json, manifest.sig and the artifacts) instead of GitHub")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var src update.Source
	if *from != "" {
		src = update.Dir(*from)
	}
	u, err := newUpdater(src)
	if err != nil {
		return err
	}
	return updateSelf(context.Background(), u, *channel, *check, stdout)
}

func updateSelf(ctx context.Context, u *update.Updater, channel string, check bool, stdout io.Writer) error {
	if v, ok := u.Ready(); ok {
		fmt.Fprintf(stdout, "Oriel %s is installed: restart Oriel to run it\n", v)
		return nil
	}
	m, err := u.Check(ctx, channel)
	if errors.Is(err, update.ErrUpToDate) {
		fmt.Fprintf(stdout, "Oriel %s is the newest on the %s channel\n", u.Version, channel)
		return nil
	}
	if err != nil {
		return err
	}
	if check {
		fmt.Fprintf(stdout, "Oriel %s is out (you have %s): run oriel update to install it\n", m.Version, u.Version)
		return nil
	}
	if err := u.Install(ctx); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Oriel %s is installed: restart Oriel to run it\n", m.Version)
	return nil
}
