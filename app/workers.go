package app

import (
	"cmp"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/wire"
	"github.com/liamg/oriel/worker"
)

// Workers (AI-15, ui.md 45): this machine's worker and remote ones. The app listens for remote workers on
// workers.listen, letting in those whose keys are in workers.json; a worker let in joins the store through the
// sessions extension, as this machine's does.
const (
	WorkersKeyMode   = "workers"
	WorkerDialogMode = "workers.add"
	defaultListen    = ":7437"
	workersFile      = "workers.json"
)

// workerName is what a remote worker may be called: it's in the command line and the keys file.
var workerName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// workerKey is a worker the app knows, as the keys file keeps it: a remote one's key's hash, and any worker's label.
type workerKey struct {
	Name    core.WorkerID `json:"name"`
	Label   string        `json:"label,omitempty"`
	Hash    string        `json:"hash,omitempty"` // wire.HashKey of its key; the key itself is never kept
	Revoked bool          `json:"revoked,omitempty"`
	Added   time.Time     `json:"added,omitzero"`
	Seen    time.Time     `json:"seen,omitzero"` // when it last connected or went away
	Addr    string        `json:"addr,omitempty"`
}

func (k workerKey) remote() bool { return k.Name != core.LocalWorker && (k.Hash != "" || k.Revoked) }

// refusal is a worker turned away at the door.
type refusal struct {
	name   string
	from   string
	reason string
	at     time.Time
}

type workersExt struct {
	disposer
	sh  *shell
	h   ext.Host
	now func() time.Time

	mu      sync.Mutex
	keys    []workerKey
	path    string // the keys file
	certDir string
	ln      net.Listener
	addr    string // workers.listen as last applied
	bound   string // the address it listens on
	fp      string // the certificate's fingerprint
	lnErr   string
	refused []refusal // newest first

	open    bool
	sel     core.WorkerID
	confirm string // what a Revoke or Forget asks "Sure?" about
	err     string
	dlg     *workerDialog
}

// workerDialog is Add a worker, or Rename when rename is set.
type workerDialog struct {
	rename  core.WorkerID
	name    string
	listen  string
	invalid string
	focus   bool
	added   *pairing
}

// pairing is what Add a worker hands the other machine: the command, with its key shown this once.
type pairing struct {
	name    core.WorkerID
	command string
	words   string
	copied  bool
}

func (x *workersExt) Manifest() ext.Manifest { return ext.Manifest{ID: "workers"} }

func (x *workersExt) Activate(_ context.Context, h ext.Host) error {
	x.h = h
	if x.now == nil {
		x.now = time.Now
	}
	x.path = filepath.Join(x.sh.cfg.ConfigDir, workersFile)
	x.certDir = filepath.Join(x.sh.cfg.StateDir, "workers")
	if err := x.loadKeys(); err != nil {
		h.Log().Warn("workers", "file", x.path, "err", err)
	}
	x.add(h.Option(registry.OptionSpec{Name: "workers.listen", Type: registry.String, Default: "", Scopes: registry.ScopeUser,
		Validate: func(v any) error {
			if s := v.(string); s != "" {
				if _, _, err := net.SplitHostPort(s); err != nil {
					return fmt.Errorf(`an address to listen on, host:port or ":7437": %v`, err)
				}
			}
			return nil
		},
		Help: `Where Oriel listens for workers on other machines, ":7437" or "10.0.0.2:7437"; empty doesn't listen. Add a worker sets it.`}))
	x.add(onDispose(h.Options().Watch("workers.listen", x.relisten)))
	x.relisten()
	x.add(onDispose(x.closeListener))
	x.add(onDispose(h.Subscribe(core.Filter{}, func(ev core.Event) {
		switch ev := ev.(type) {
		case core.WorkerChanged:
			if l := x.label(ev.Worker.ID); l != "" && l != ev.Worker.Label {
				h.Do(func() { _ = x.sh.cfg.Store.Apply(core.WorkerRenamed{ID: ev.Worker.ID, Label: l}) })
			}
		case core.WorkerGone:
			x.seen(ev.ID, "", ev.At)
		}
	})))
	dialog := func(registry.Args) bool { return x.dlg != nil }
	for _, c := range []registry.Command{
		{ID: "Workers", Title: "The Workers page; :Workers {name}", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.openPage(core.WorkerID(strings.TrimSpace(a.String("args"))))
		}},
		{ID: "workers.open", Title: "Open the Workers page, at a worker by {name}", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.openPage(core.WorkerID(a.String("name")))
		}},
		{ID: "workers.add", Title: "Add a worker on another machine: the dialog, or with {name} (and {listen}, when Oriel isn't listening yet) its key and the command to run there",
			Run: func(_ registry.Ctx, a registry.Args) error {
				if a.String("name") == "" {
					x.dlg = &workerDialog{focus: true}
					return nil
				}
				return x.pair(core.WorkerID(a.String("name")), a.String("listen"))
			}},
		{ID: "workers.rename", Title: "Rename a worker in Oriel: {name}, the selected one by default, {to} a new name, or the dialog",
			Run: func(_ registry.Ctx, a registry.Args) error {
				id := x.target(a)
				if _, ok := a["to"]; !ok {
					x.dlg = &workerDialog{rename: id, name: x.title(id), focus: true}
					return nil
				}
				return x.rename(id, a.String("to"))
			}},
		{ID: "workers.revoke", Title: "Revoke a worker's key: {name}; it's cut off and refused after", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.revoke(x.target(a))
		}},
		{ID: "workers.forget", Title: "Forget a worker that isn't connected: {name}; its key goes too", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.forget(x.target(a))
		}},
		{ID: "workers.restart", Title: "Restart this machine's worker: its agents stop and come back on the new one",
			Available: func(registry.Args) bool { return x.restartable() }, Run: func(registry.Ctx, registry.Args) error { return x.sh.restartLocal() }},
		{ID: "workers.dialog.save", Title: "Add or rename the worker the dialog holds", Available: dialog,
			Run: func(registry.Ctx, registry.Args) error { x.submit(); return nil }},
	} {
		x.add(h.Command(c))
	}
	for _, k := range []registry.Keymap{
		{Mode: WorkersKeyMode, Keys: "a", Command: "workers.add", Desc: "Add a worker"},
		{Mode: WorkersKeyMode, Keys: "r", Command: "workers.rename", Desc: "Rename the worker"},
		{Mode: WorkerDialogMode, Keys: "<D-CR>", Command: "workers.dialog.save", Desc: "Add"},
	} {
		x.add(h.Keymap(k))
	}
	x.add(h.Contribute(registry.RailEntry{Name: "workers", Icon: "workers", Title: "Workers", Command: "workers.open", Scope: registry.ScopeGlobalEntry,
		Order: 40, Note: func(string) string { return x.figure() }, Active: func(string) bool { return x.open }}))
	x.add(h.Contribute(registry.StartEntry{Name: "workers", Icon: "workers", Title: "Workers", Desc: "The machines that run sessions, and adding more",
		Command: "workers.open", Order: 90, Link: true, Note: x.figure}))
	x.add(h.View(OverlayPrefix+"workers", registry.ViewFunc(func(registry.ViewCtx) ui.Node { return &ui.Func{Key: "workers", Fn: x.page} })))
	x.add(h.View(OverlayPrefix+"workers.dialog", registry.ViewFunc(func(registry.ViewCtx) ui.Node { return &ui.Func{Key: "workersdlg", Fn: x.dialog} })))
	return nil
}

func (x *workersExt) openPage(id core.WorkerID) error {
	x.open, x.err, x.confirm = true, "", ""
	if id == "" {
		return nil
	}
	for _, r := range x.rows(x.h.State()) {
		if r.w.ID == id || strings.EqualFold(r.w.Title(), string(id)) {
			x.sel = r.w.ID
			return nil
		}
	}
	return fmt.Errorf("no worker %q", id)
}

// target is the worker {name} names, by ID or title, else the selected one.
func (x *workersExt) target(a registry.Args) core.WorkerID {
	name := a.String("name")
	if name == "" {
		return x.selected()
	}
	for _, r := range x.rows(x.h.State()) {
		if string(r.w.ID) == name || r.w.Title() == name {
			return r.w.ID
		}
	}
	return core.WorkerID(name)
}

func (x *workersExt) selected() core.WorkerID {
	rows := x.rows(x.h.State())
	for _, r := range rows {
		if r.w.ID == x.sel {
			return r.w.ID
		}
	}
	if len(rows) > 0 {
		return rows[0].w.ID
	}
	return ""
}

// figure is the sidebar's: how many workers are online.
func (x *workersExt) figure() string {
	n := 0
	for _, w := range x.h.State().Workers {
		if w.Connected {
			n++
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d online", n)
}

// The keys file.

func (x *workersExt) loadKeys() error {
	b, err := os.ReadFile(x.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var f struct {
		Workers []workerKey `json:"workers"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	x.mu.Lock()
	x.keys = f.Workers
	x.mu.Unlock()
	return nil
}

// saveLocked writes the keys file, owner-only. x.mu is held.
func (x *workersExt) saveLocked() error {
	b, err := json.MarshalIndent(struct {
		Workers []workerKey `json:"workers"`
	}{x.keys}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(x.path), 0o700); err != nil {
		return err
	}
	tmp := x.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, x.path)
}

// edit changes a worker's entry, making it if fn asks, and saves.
func (x *workersExt) edit(id core.WorkerID, fn func(k *workerKey) bool) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	i := slices.IndexFunc(x.keys, func(k workerKey) bool { return k.Name == id })
	k := workerKey{Name: id}
	if i >= 0 {
		k = x.keys[i]
	}
	if !fn(&k) {
		return nil
	}
	if i >= 0 {
		x.keys[i] = k
	} else {
		x.keys = append(x.keys, k)
	}
	return x.saveLocked()
}

func (x *workersExt) key(id core.WorkerID) (workerKey, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	i := slices.IndexFunc(x.keys, func(k workerKey) bool { return k.Name == id })
	if i < 0 {
		return workerKey{}, false
	}
	return x.keys[i], true
}

func (x *workersExt) label(id core.WorkerID) string {
	k, _ := x.key(id)
	return k.Label
}

// hash is a remote worker's key's hash, for the handshake: none for this machine's or a revoked one.
func (x *workersExt) hash(name string) (string, bool) {
	k, ok := x.key(core.WorkerID(name))
	if !ok || k.Name == core.LocalWorker || k.Revoked || k.Hash == "" {
		return "", false
	}
	return k.Hash, true
}

func (x *workersExt) seen(id core.WorkerID, addr string, at time.Time) {
	if id == core.LocalWorker || at.IsZero() {
		return
	}
	_ = x.edit(id, func(k *workerKey) bool {
		if !k.remote() {
			return false
		}
		k.Seen = at
		if addr != "" {
			k.Addr = addr
		}
		return true
	})
}

// The listener.

// relisten applies workers.listen: it stops listening, and listens again on the new address.
func (x *workersExt) relisten() {
	v, _, _ := x.h.Options().Get("workers.listen", registry.At{})
	addr, _ := v.(string)
	x.mu.Lock()
	if addr == x.addr && (x.ln != nil || addr == "") {
		x.mu.Unlock()
		return
	}
	x.closeLocked()
	x.addr = addr
	if addr == "" {
		x.mu.Unlock()
		return
	}
	cert, fp, err := wire.LoadOrCreateCert(x.certDir)
	if err != nil {
		x.lnErr = "the certificate: " + err.Error()
		x.mu.Unlock()
		return
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		x.lnErr = err.Error()
		x.mu.Unlock()
		x.h.Log().Warn("workers: can't listen", "addr", addr, "err", err)
		return
	}
	x.ln, x.fp, x.bound = ln, fp, ln.Addr().String()
	x.mu.Unlock()
	x.h.Log().Info("listening for workers", "addr", x.bound, "certificate", wire.Words(fp))
	go x.accept(ln, wire.ServerTLS(cert))
	x.h.Redraw()
}

func (x *workersExt) closeListener() {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.closeLocked()
}

func (x *workersExt) closeLocked() {
	if x.ln != nil {
		x.ln.Close()
	}
	x.ln, x.bound, x.lnErr = nil, "", ""
}

func (x *workersExt) accept(ln net.Listener, cfg *tls.Config) {
	auth := wire.KeyHashAuth(x.hash)
	for {
		raw, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			p, err := wire.Accept(raw, cfg, auth)
			if err != nil {
				x.refuse(err)
				return
			}
			x.joined(p)
		}()
	}
}

func (x *workersExt) refuse(err error) {
	var rej *wire.ErrRejected
	if !errors.As(err, &rej) {
		x.h.Log().Info("workers: a connection that didn't finish the handshake", "err", err)
		return
	}
	r := refusal{name: rej.Hello.Name, reason: cmp.Or(rej.Welcome.Reason, rej.Welcome.Status), at: x.now()}
	x.h.Log().Warn("refused a worker", "name", r.name, "reason", r.reason)
	x.mu.Lock()
	x.refused = append([]refusal{r}, x.refused[:min(len(x.refused), 19)]...)
	x.mu.Unlock()
	x.h.Redraw()
}

// joined hands a worker that's let in to the sessions extension, which mirrors it as it does this machine's.
func (x *workersExt) joined(p *wire.Peer) {
	id := core.WorkerID(p.Hello.Name)
	x.seen(id, p.Remote.String(), x.now())
	x.sh.whenSessions(func(s *sessions) { s.serve(s.ctx, s.h, worker.Client{Conn: p.Conn}, id) })
}

// listening is the address workers dial and the certificate's fingerprint, or "" while Oriel isn't listening.
func (x *workersExt) listening() (addr, fp, failed string) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.ln == nil {
		return "", "", x.lnErr
	}
	return x.bound, x.fp, ""
}

// dialAddr is the address a worker elsewhere dials: the listener's, with this machine's name when it listens on
// every address.
func dialAddr(bound string) string {
	host, port, err := net.SplitHostPort(bound)
	if err != nil {
		return bound
	}
	if ip := net.ParseIP(host); host == "" || ip != nil && ip.IsUnspecified() {
		host, _ = os.Hostname()
	}
	return net.JoinHostPort(host, port)
}

// The actions.

// pair makes a key for a new worker, listening first if Oriel isn't, and shows the command to run there.
func (x *workersExt) pair(id core.WorkerID, listen string) error {
	if err := x.checkName(id); err != nil {
		return err
	}
	if v, _, _ := x.h.Options().Get("workers.listen", registry.At{}); v == "" {
		addr := cmp.Or(strings.TrimSpace(listen), defaultListen)
		if err := x.h.Run(context.Background(), "settings.set", registry.Args{"name": "workers.listen", "value": addr, "scope": "user"}); err != nil {
			return err
		}
		x.relisten()
	}
	bound, fp, failed := x.listening()
	if bound == "" {
		return fmt.Errorf("Oriel can't listen for workers: %s", cmp.Or(failed, "workers.listen is empty"))
	}
	key, err := wire.NewKey()
	if err != nil {
		return err
	}
	now := x.now()
	if err := x.edit(id, func(k *workerKey) bool {
		*k = workerKey{Name: id, Label: k.Label, Hash: wire.HashKey(key), Added: now}
		return true
	}); err != nil {
		return err
	}
	x.dlg = &workerDialog{added: &pairing{name: id, words: wire.Words(fp),
		command: fmt.Sprintf("oriel worker --connect %s --fingerprint %s --key %s --name %s", dialAddr(bound), fp, key, id)}}
	x.open, x.sel = true, id
	x.h.Redraw()
	return nil
}

func (x *workersExt) checkName(id core.WorkerID) error {
	if !workerName.MatchString(string(id)) {
		return fmt.Errorf("%q: a worker's name is letters, digits, '-', '_' and '.'", id)
	}
	if id == core.LocalWorker {
		return errors.New(`"local" is this machine's worker`)
	}
	if k, ok := x.key(id); ok && k.remote() && !k.Revoked {
		return fmt.Errorf("there's a worker called %s already", id)
	}
	for _, w := range x.h.State().Workers {
		if w.ID == id && w.Connected {
			return fmt.Errorf("there's a worker called %s already", id)
		}
	}
	return nil
}

func (x *workersExt) rename(id core.WorkerID, to string) error {
	if id == "" {
		return errors.New("no worker to rename")
	}
	to = strings.TrimSpace(to)
	own := string(id)
	for _, w := range x.h.State().Workers {
		if w.ID == id {
			own = cmp.Or(w.Name, own)
		}
	}
	if to == own {
		to = ""
	}
	if err := x.edit(id, func(k *workerKey) bool { k.Label = to; return true }); err != nil {
		return err
	}
	if slices.ContainsFunc(x.h.State().Workers, func(w core.WorkerState) bool { return w.ID == id }) {
		return x.sh.cfg.Store.Apply(core.WorkerRenamed{ID: id, Label: to})
	}
	return nil
}

func (x *workersExt) revoke(id core.WorkerID) error {
	k, ok := x.key(id)
	switch {
	case id == core.LocalWorker:
		return errors.New("this machine's worker has no key: it's reached on its socket")
	case !ok || !k.remote():
		return fmt.Errorf("no worker %s with a key", id)
	}
	if err := x.edit(id, func(k *workerKey) bool { k.Hash, k.Revoked = "", true; return true }); err != nil {
		return err
	}
	if x.sh.sessions != nil {
		if c, ok := x.sh.sessions.byWorker()[id]; ok {
			c.Conn.Close()
		}
	}
	return nil
}

func (x *workersExt) forget(id core.WorkerID) error {
	if id == core.LocalWorker {
		return errors.New("this machine's worker can't be forgotten")
	}
	st := x.h.State()
	i := slices.IndexFunc(st.Workers, func(w core.WorkerState) bool { return w.ID == id })
	if i >= 0 && st.Workers[i].Connected {
		return fmt.Errorf("%s is online: revoke it first", st.Workers[i].Title())
	}
	x.mu.Lock()
	n := len(x.keys)
	x.keys = slices.DeleteFunc(x.keys, func(k workerKey) bool { return k.Name == id })
	var err error
	if len(x.keys) != n {
		err = x.saveLocked()
	}
	x.mu.Unlock()
	if err != nil {
		return err
	}
	if i >= 0 {
		return x.sh.cfg.Store.Apply(core.WorkerForgotten{ID: id})
	}
	return nil
}

// restartable says whether this machine's worker can be restarted: Oriel starts it, it's connected, and no drain runs.
func (x *workersExt) restartable() bool {
	if x.sh.cfg.LocalWorker == nil || x.sh.sessions == nil || x.sh.draining.Load() != nil {
		return false
	}
	_, ok := x.sh.sessions.byWorker()[core.LocalWorker]
	return ok
}

// restartLocal restarts this machine's worker when asked to, through the drain a rebuild uses.
func (sh *shell) restartLocal() error {
	x := sh.sessions
	if x == nil || sh.cfg.LocalWorker == nil {
		return errors.New("Oriel doesn't start this machine's worker here")
	}
	c, ok := x.byWorker()[core.LocalWorker]
	if !ok {
		return errors.New("this machine's worker isn't connected")
	}
	r := newDrain(c, core.LocalWorker)
	r.restart, r.asked = true, true
	sh.drain(r)
	return nil
}

// submit is the dialog's Add, or Rename's Save.
func (x *workersExt) submit() {
	d := x.dlg
	if d == nil {
		return
	}
	if d.added != nil {
		x.dlg = nil
		return
	}
	var err error
	if d.rename != "" {
		if err = x.rename(d.rename, d.name); err == nil {
			x.dlg = nil
		}
	} else {
		err = x.pair(core.WorkerID(strings.TrimSpace(d.name)), d.listen)
	}
	if err != nil {
		d.invalid = err.Error()
	}
}

// title is how a worker reads, from the store or the keys file.
func (x *workersExt) title(id core.WorkerID) string {
	for _, w := range x.h.State().Workers {
		if w.ID == id {
			return w.Title()
		}
	}
	if l := x.label(id); l != "" {
		return l
	}
	return string(id)
}

// workerRow is a worker on the page: as the store has it, and its entry in the keys file.
type workerRow struct {
	w     core.WorkerState
	key   workerKey
	known bool // in the store: connected now, or since Oriel opened
}

func (r workerRow) local() bool { return r.w.ID == core.LocalWorker }

// rows are the workers: this machine's first, then by name; known workers that aren't connected too.
func (x *workersExt) rows(st core.State) []workerRow {
	x.mu.Lock()
	keys := slices.Clone(x.keys)
	x.mu.Unlock()
	var out []workerRow
	for _, w := range st.Workers {
		r := workerRow{w: w, known: true}
		if i := slices.IndexFunc(keys, func(k workerKey) bool { return k.Name == w.ID }); i >= 0 {
			r.key = keys[i]
		}
		out = append(out, r)
	}
	for _, k := range keys {
		if !k.remote() || slices.ContainsFunc(out, func(r workerRow) bool { return r.w.ID == k.Name }) {
			continue
		}
		out = append(out, workerRow{w: core.WorkerState{ID: k.Name, Label: k.Label, Since: k.Seen}, key: k})
	}
	slices.SortStableFunc(out, func(a, b workerRow) int {
		if a.local() != b.local() {
			if a.local() {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.w.Title()), strings.ToLower(b.w.Title()))
	})
	return out
}

// refusals are the recent refusals of a worker by name.
func (x *workersExt) refusals(id core.WorkerID) []refusal {
	x.mu.Lock()
	defer x.mu.Unlock()
	var out []refusal
	for _, r := range x.refused {
		if r.name == string(id) {
			out = append(out, r)
		}
	}
	return out
}
