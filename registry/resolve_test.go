package registry_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/registry"
)

// trace runs keys through a resolver and writes what came out: "run:<cmd>" or "key:<k>", and "wait" when it waits.
func trace(r *registry.Resolver, modes []string, t0 time.Time, keys ...string) string {
	var out []string
	add := func(acts []registry.Action) {
		for _, a := range acts {
			if a.Keymap != nil {
				out = append(out, "run:"+a.Keymap.Command)
			}
			for _, k := range a.Keys {
				out = append(out, "key:"+k)
			}
		}
	}
	now := t0
	for _, k := range keys {
		if k == "TIMEOUT" {
			now = r.Deadline()
			add(r.Timeout(now))
			continue
		}
		add(r.Feed(modes, k, now))
	}
	if len(r.Pending()) > 0 {
		out = append(out, "wait")
	}
	return strings.Join(out, " ")
}

func bind(r registry.Registry, mode, keys, cmd string) {
	r.Keymap(registry.Keymap{Mode: mode, Keys: keys, Command: cmd})
}

func TestResolveAmbiguous(t *testing.T) {
	// ref:   nvim/map.txt *map-ambiguous*
	// quote: "If you type a space, then \"foo\" will get inserted, plus the space.  If you type \"a\", then \"bar\" will get inserted."
	r := registry.New()
	bind(r, "i", "aa", "foo")
	bind(r, "i", "aaa", "bar")
	t0 := time.Unix(0, 0)
	for _, tt := range []struct{ keys, want string }{
		{"a a", "wait"},
		{"a a <Space>", "run:foo key: "},
		{"a a a", "run:bar"},
		{"a b", "key:a key:b"},
		{"a a TIMEOUT", "run:foo"},
	} {
		res := registry.NewResolver(r)
		keys := strings.Fields(strings.ReplaceAll(tt.keys, "<Space>", "\x00"))
		for i := range keys {
			keys[i] = strings.ReplaceAll(keys[i], "\x00", " ")
		}
		if got := trace(res, []string{"i"}, t0, keys...); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.keys, got, tt.want)
		}
	}
}

func TestResolveTimeout(t *testing.T) {
	// ref:   nvim/map.txt *map-typing*
	// quote: "If the 'timeout' option is on (which is the default) Vim will only wait for one second (or as long as specified with the 'timeoutlen' option).  After that it assumes that the 'q' is to be interpreted as such."
	r := registry.New()
	bind(r, "n", "qq", "twice")
	res := registry.NewResolver(r)
	t0 := time.Unix(100, 0)
	if got := trace(res, []string{"n"}, t0, "q"); got != "wait" {
		t.Fatalf("q: %q", got)
	}
	if d := res.Deadline().Sub(t0); d != time.Second {
		t.Errorf("waits %v, want 1s", d)
	}
	if acts := res.Timeout(t0.Add(999 * time.Millisecond)); len(acts) != 0 {
		t.Errorf("timed out early: %v", acts)
	}
	if got := trace(res, []string{"n"}, t0, "TIMEOUT"); got != "key:q" {
		t.Errorf("after the timeout: %q, want key:q", got)
	}

	// ref:   nvim/options.txt *'timeoutlen'*
	// quote: "Time in milliseconds to wait for a mapped sequence to complete."
	if err := r.Options().Set("keymap.timeoutlen", registry.LayerUser, registry.At{}, 300*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	res.Feed([]string{"n"}, "q", t0)
	if d := res.Deadline().Sub(t0); d != 300*time.Millisecond {
		t.Errorf("waits %v, want 300ms", d)
	}

	// ref:   nvim/options.txt *'timeout'*
	// quote: "if <c-f> is pressed and 'timeout' is set, Nvim will wait 'timeoutlen' milliseconds for any key that can follow <c-f> in a mapping."
	r.Options().Set("keymap.timeout", registry.LayerUser, registry.At{}, false)
	res = registry.NewResolver(r)
	res.Feed([]string{"n"}, "q", t0)
	if !res.Deadline().IsZero() {
		t.Error("waits for a timeout with keymap.timeout off")
	}
}

func TestResolveLongestPrefixThenRest(t *testing.T) {
	r := registry.New()
	bind(r, "n", "x", "x")
	bind(r, "n", "xyz", "xyz")
	bind(r, "n", "yq", "yq")
	got := trace(registry.NewResolver(r), []string{"n"}, time.Unix(0, 0), "x", "y", "q")
	if got != "run:x run:yq" {
		t.Errorf("x y q: %q, want run:x run:yq", got)
	}
}

func TestResolvePanelLocalFirst(t *testing.T) {
	// ref:   nvim/map.txt *map-precedence*
	// quote: "When a buffer-local mapping is the same as a global mapping, Vim will use the buffer-local mapping."
	r := registry.New()
	bind(r, "n", "x", "delete")
	bind(r, "requests", "x", "thread.resolve")
	bind(r, "n", "gg", "top")
	modes := []string{"requests", "n"}
	if got := trace(registry.NewResolver(r), modes, time.Unix(0, 0), "x"); got != "run:thread.resolve" {
		t.Errorf("x in requests: %q", got)
	}
	if got := trace(registry.NewResolver(r), modes, time.Unix(0, 0), "g", "g"); got != "run:top" {
		t.Errorf("gg in requests: %q", got)
	}
	if got := trace(registry.NewResolver(r), []string{"n"}, time.Unix(0, 0), "x"); got != "run:delete" {
		t.Errorf("x in n: %q", got)
	}
}

func TestLeaderIsExpandedWhenDefined(t *testing.T) {
	// ref:   nvim/map.txt *mapleader*
	// quote: "Note that the value of \"g:mapleader\" is used at the moment the mapping is defined.  Changing \"g:mapleader\" after that has no effect for already defined mappings."
	r := registry.New()
	if err := r.Options().Set("keymap.leader", registry.LayerUser, registry.At{}, ","); err != nil {
		t.Fatal(err)
	}
	bind(r, "n", "<Leader>A", "comma")
	r.Options().Set("keymap.leader", registry.LayerUser, registry.At{}, ";")
	bind(r, "n", "<Leader>B", "semi")
	t0 := time.Unix(0, 0)
	for keys, want := range map[string]string{",A": "run:comma", ";B": "run:semi", ";A": "key:; key:A"} {
		ks := strings.Split(keys, "")
		if got := trace(registry.NewResolver(r), []string{"n"}, t0, ks...); got != want {
			t.Errorf("%s: %q, want %q", keys, got, want)
		}
	}
}

func TestKeymapsAndCommandsListSources(t *testing.T) {
	r := registry.New()
	a := registry.WithSource(r, "builtin:git")
	b := registry.WithSource(r, "init.lua:14")
	a.Command(registry.Command{ID: "git.commit", Title: "Commit"})
	b.Command(registry.Command{ID: "Ship", Title: "Commit, then raise"})
	b.Keymap(registry.Keymap{Mode: "n", Keys: "<leader>rr", Command: "agent.review"})
	a.Keymap(registry.Keymap{Mode: "n", Keys: "<C-g>c", Command: "git.commit"})

	var cmds []string
	for _, c := range r.Commands() {
		cmds = append(cmds, fmt.Sprintf("%s|%s|%s", c.ID, c.Title, c.Source))
	}
	if got, want := strings.Join(cmds, "\n"), "Ship|Commit, then raise|init.lua:14\ngit.commit|Commit|builtin:git"; !strings.Contains(got, want) {
		t.Errorf("commands:\n%s\nwant to contain:\n%s", got, want)
	}
	var keys []string
	for _, k := range r.Keymaps() {
		keys = append(keys, fmt.Sprintf("%s|%s|%s|%s", k.Mode, k.Keys, k.Command, k.Source))
	}
	if got, want := strings.Join(keys, "\n"), "n|<C-g>c|git.commit|builtin:git\nn|<Space>rr|agent.review|init.lua:14"; got != want {
		t.Errorf("keymaps:\n%s\nwant:\n%s", got, want)
	}
}
