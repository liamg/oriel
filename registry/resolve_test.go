package registry_test

import (
	"errors"
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

func TestResolveNowait(t *testing.T) {
	// ref:   nvim/map.txt *:map-<nowait>*
	// quote: "Then the mapping will be used when it matches, Vim does not wait for more characters to be typed."
	r := registry.New()
	bind(r, "n", "[c", "hunk.prev")
	r.Keymap(registry.Keymap{Mode: "card", Keys: "[", Command: "widen", Nowait: true})
	bind(r, "card", "]", "narrow")
	bind(r, "n", "]c", "hunk.next")
	modes := []string{"card", "n"}
	if got := trace(registry.NewResolver(r), modes, time.Unix(0, 0), "["); got != "run:widen" {
		t.Errorf("[ with <nowait>: %q, want it run at once", got)
	}
	if got := trace(registry.NewResolver(r), modes, time.Unix(0, 0), "]"); got != "wait" {
		t.Errorf("] without <nowait>: %q, want it to wait for ]c", got)
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

// A key that starts a command of the target's own (an editor's g) makes the keys after it its argument: they reach it
// unmapped, as vim reads them, once it has taken the first. Keys behind it wait for Pump (a frame).
func TestResolveCommandArgumentsAreNotMapped(t *testing.T) {
	// ref:   nvim/map.txt *map-error*
	// quote: "Note that the second character (argument) of the commands @zZtTfF[]rm'`\"v and CTRL-X is not mapped."
	r := registry.New()
	bind(r, "n", "gd", "definition")
	bind(r, "n", "grr", "references")
	res := registry.NewResolver(r)
	engine := "" // what the target holds of a command so far
	waiting := map[string]bool{"g": true}
	res.Raw = func() bool { return engine != "" }
	t0 := time.Unix(0, 0)
	take := func(acts []registry.Action) (got []string) {
		for _, a := range acts {
			if a.Keymap != nil {
				got = append(got, "run:"+a.Keymap.Command)
			}
			for _, k := range a.Keys {
				got = append(got, "key:"+k)
				if engine += k; !waiting[engine] {
					engine = ""
				}
			}
		}
		return got
	}
	var got []string
	got = append(got, take(res.Feed([]string{"n"}, "g", t0))...)
	got = append(got, take(res.Feed([]string{"n"}, "g", t0))...)
	if strings.Join(got, " ") != "key:g" || !res.Queued() {
		t.Fatalf("gg typed together: %v, queued %v", got, res.Queued())
	}
	// A frame: the target has taken the first g and waits for its argument.
	got = take(res.Pump([]string{"n"}, t0))
	if strings.Join(got, " ") != "key:g" || res.Queued() || len(res.Pending()) != 0 {
		t.Errorf("after the frame: %v, pending %v", got, res.Pending())
	}
	// The next frame: gg is done, and gd still runs its mapping.
	take(res.Pump([]string{"n"}, t0))
	if got := trace(res, []string{"n"}, t0, "g", "d"); got != "run:definition" {
		t.Errorf("gd = %q", got)
	}
}

// A mapping that is also the start of longer ones runs at once on Accept (which-key's ⏎), while held; keys that
// aren't a mapping of their own accept nothing.
func TestResolveAccept(t *testing.T) {
	r := registry.New()
	bind(r, "n", "gt", "threads")
	bind(r, "n", "gtt", "nearest")
	res := registry.NewResolver(r)
	t0 := time.Unix(0, 0)
	if got := trace(res, []string{"n"}, t0, "g", "t"); got != "wait" {
		t.Fatalf("gt: %q", got)
	}
	res.Hold()
	if k := res.Complete(); k == nil || k.Command != "threads" {
		t.Fatalf("complete = %+v", k)
	}
	acts := res.Accept(t0)
	if len(acts) != 1 || acts[0].Keymap == nil || acts[0].Keymap.Command != "threads" || len(res.Pending()) != 0 {
		t.Errorf("accept ran %+v, left %v", acts, res.Pending())
	}
	res.Feed([]string{"n"}, "g", t0)
	if res.Complete() != nil || res.Accept(t0) != nil || len(res.Pending()) != 1 {
		t.Errorf("g alone accepted something")
	}
}

// An exclusive mode keeps to its own keys: the modes after it lend only what its Allow lets through, and don't make
// it wait for their longer keys.
func TestResolveExclusiveMode(t *testing.T) {
	r := registry.New()
	bind(r, "start", "j", "start.next")
	bind(r, "global", "<D-n>", "session.new")
	bind(r, "global", "<D-k>", "omnibox.open")
	bind(r, "n", "<Space><Space>", "picker.files")
	r.Contribute(registry.ExclusiveKeys{Mode: "start", Allow: func(k registry.Keymap) bool { return k.Command == "session.new" }})
	modes := []string{"start", "n", "global"}
	for keys, want := range map[string]string{"j": "run:start.next", "<D-n>": "run:session.new", "<D-k>": "key:<D-k>", "<Space>": "key:<Space>"} {
		if got := trace(registry.NewResolver(r), modes, time.Unix(0, 0), keys); got != want {
			t.Errorf("%s: %q, want %q", keys, got, want)
		}
	}
	if got := trace(registry.NewResolver(r), []string{"n", "global"}, time.Unix(0, 0), "<D-k>"); got != "run:omnibox.open" {
		t.Errorf("⌘K outside the exclusive mode: %q", got)
	}
}

// A contribution registered through a plugin's registry says that plugin, unless it names another source, and a
// fault in it is that plugin's.
func TestContributionsTakeTheirPluginsSource(t *testing.T) {
	r := registry.New()
	a := registry.WithSource(r, "builtin:stats")
	a.Contribute(registry.Busy{Name: "reads", Busy: func() bool { return false }})
	a.Contribute(registry.Busy{Name: "theirs", Busy: func() bool { return false }, Source: "other"})
	for name, want := range map[string]string{"reads": "builtin:stats", "theirs": "other"} {
		c, ok := r.Contribution(registry.Busy{}.ContributionKind(), name)
		if !ok {
			t.Fatalf("no busy %s", name)
		}
		if got := registry.SourceOf(c); got != want {
			t.Errorf("%s's source is %q, want %q", name, got, want)
		}
	}
	var got []registry.Fault
	r.OnFault(func(f registry.Fault) { got = append(got, f) })
	r.TurnOff(registry.Busy{}.ContributionKind(), "reads", errors.New("broke"))
	if len(got) != 1 || got[0].Source != "builtin:stats" {
		t.Errorf("the fault: %+v, want it builtin:stats's", got)
	}
}
