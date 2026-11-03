package shell_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/plugin"
)

// keeper keeps a line of text per directory in a StateFile.
type keeper struct {
	kit.Disposer
	files string // where each directory's file goes
	f     *kit.StateFile

	mu     sync.Mutex
	text   string
	loads  []string
	leaves int
}

func (p *keeper) Manifest() plugin.Manifest { return plugin.Manifest{ID: "test/keeper"} }
func (p *keeper) Activate(_ context.Context, h plugin.Host) error {
	p.f = kit.NewStateFile(h, kit.StateFileOpts{Name: "keeper",
		Path: func() string {
			if d := kit.WindowDir(h); d != "" {
				return filepath.Join(p.files, filepath.Base(d)+".txt")
			}
			return ""
		},
		Load: func(b []byte) {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.text = string(b)
			p.loads = append(p.loads, string(b))
		},
		Leave: func() []byte {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.leaves++
			return []byte(p.text + " left")
		}})
	p.Add(kit.OnDispose(p.f.Close))
	return nil
}

// waitFile waits for the file at path to hold want.
func waitFile(t *testing.T, path, want string) {
	t.Helper()
	var got []byte
	for deadline := time.Now().Add(apptest.Timeout); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if got, _ = os.ReadFile(path); string(got) == want {
			return
		}
	}
	t.Fatalf("%s holds %q, want %q", path, got, want)
}

func (p *keeper) loaded() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.loads...)
}

// A state file per directory is read off the loop as the window starts and moves, its own writes aren't handed back
// as changes, something else's are, and the directory left keeps what Leave says.
func TestAStateFileFollowsTheWindow(t *testing.T) {
	files, a, b := t.TempDir(), filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	for _, d := range []string{a, b} {
		os.MkdirAll(d, 0o755)
	}
	os.WriteFile(filepath.Join(files, "a.txt"), []byte("kept in a"), 0o644)
	os.WriteFile(filepath.Join(files, "b.txt"), []byte("kept in b"), 0o644)
	p := &keeper{files: files}
	r := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), WorkDir: a, Plugins: []plugin.Plugin{p}})
	r.WaitFor("a's file loaded", func() bool { return strings.Join(p.loaded(), "|") == "kept in a" })

	r.Do(func() error { p.text = "written"; p.f.Write([]byte("written")); return nil })
	waitFile(t, filepath.Join(files, "a.txt"), "written")
	os.WriteFile(filepath.Join(files, "a.txt"), []byte("edited by hand"), 0o644)
	r.WaitFor("the edit loaded, the write not", func() bool { return strings.Join(p.loaded(), "|") == "kept in a|edited by hand" })

	if err := r.Do(func() error { w, _ := kit.WindowOf(r.Host); return w.Reopen(b, "") }); err != nil {
		t.Fatal(err)
	}
	r.WaitFor("b's file loaded", func() bool { l := p.loaded(); return len(l) == 3 && l[2] == "kept in b" })
	waitFile(t, filepath.Join(files, "a.txt"), "edited by hand left")
	r.WaitGone("Opening")
}
