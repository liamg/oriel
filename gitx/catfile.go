package gitx

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// batchIdle is how long a repo's cat-file --batch is kept with nothing asked of it.
const batchIdle = 10 * time.Second

// catFile is one long-lived git cat-file --batch: it reads any number of objects in one git, where a git per
// object costs a process each.
type catFile struct {
	mu    sync.Mutex
	cmd   *exec.Cmd
	in    io.WriteCloser
	out   *bufio.Reader
	timer *time.Timer
}

var batches = struct {
	sync.Mutex
	by map[string]*catFile
}{by: map[string]*catFile{}}

// object is an object's contents by a spec cat-file names it by ("<hash>:<path>"), from the repo's batch.
func (r Repo) object(ctx context.Context, spec string) ([]byte, bool, error) {
	key := r.Dir + "\x00" + strings.Join(r.Env, "\x00")
	batches.Lock()
	c := batches.by[key]
	if c == nil {
		c = &catFile{}
		batches.by[key] = c
	}
	batches.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cmd == nil {
		if err := c.start(r); err != nil {
			return nil, false, err
		}
	}
	c.timer.Reset(batchIdle)
	b, ok, err := c.read(spec)
	if err != nil {
		c.stop()
	}
	return b, ok, err
}

func (c *catFile) start(r Repo) error {
	cmd := exec.Command("git", "-C", r.Dir, "cat-file", "--batch")
	cmd.Env = append(append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C"), r.Env...)
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	c.cmd, c.in, c.out = cmd, in, bufio.NewReaderSize(out, 64<<10)
	if c.timer == nil {
		c.timer = time.AfterFunc(batchIdle, func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.stop()
		})
	}
	return nil
}

func (c *catFile) stop() {
	if c.cmd == nil {
		return
	}
	c.in.Close()
	c.cmd.Wait()
	c.cmd, c.in, c.out = nil, nil, nil
}

// read asks for spec: "<oid> <type> <size>\n<contents>\n", or "<spec> missing\n".
func (c *catFile) read(spec string) ([]byte, bool, error) {
	if strings.ContainsAny(spec, "\n") {
		return nil, false, nil
	}
	if _, err := io.WriteString(c.in, spec+"\n"); err != nil {
		return nil, false, err
	}
	head, err := c.out.ReadString('\n')
	if err != nil {
		return nil, false, err
	}
	if strings.HasSuffix(head, " missing\n") || strings.HasSuffix(head, " ambiguous\n") {
		return nil, false, nil
	}
	f := strings.Fields(head)
	if len(f) != 3 {
		return nil, false, fmt.Errorf("cat-file: %q", head)
	}
	n, err := strconv.Atoi(f[2])
	if err != nil {
		return nil, false, fmt.Errorf("cat-file: %q", head)
	}
	b := make([]byte, n+1)
	if _, err := io.ReadFull(c.out, b); err != nil {
		return nil, false, err
	}
	return b[:n], f[1] == "blob", nil
}
