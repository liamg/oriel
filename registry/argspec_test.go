package registry

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A command that declares its args refuses a key it doesn't take, a value of the wrong type and a required key left
// out, naming the command and the key; one that declares none takes anything.
func TestRunChecksDeclaredArgs(t *testing.T) {
	r := New()
	var got Args
	r.Command(Command{ID: "file.open", Ex: []string{"Open"}, Args: []Arg{
		{Name: "path", Type: ArgString, Required: true},
		{Name: "line", Type: ArgInt},
		{Name: "split", Type: ArgBool},
	}, Run: func(_ Ctx, a Args) error { got = a; return nil }})
	r.Command(Command{ID: "any.run", Run: func(Ctx, Args) error { return nil }})
	r.Command(Command{ID: "quiet.run", Args: NoArgs, Run: func(Ctx, Args) error { return nil }})
	r.Command(Command{ID: "relay.run", Args: []Arg{{Name: "to", Type: ArgString}, MoreArgs}, Run: func(Ctx, Args) error { return nil }})
	run := func(id string, a Args) error { return r.Run(context.Background(), id, a) }

	if err := run("file.open", Args{"path": "a.go", "line": 3.0, "split": true}); err != nil || got.Int("line") != 3 {
		t.Fatalf("a run with what it takes: %v, %v", err, got)
	}
	if err := run("file.open", Args{"path": "a.go", "line": "12"}); err != nil {
		t.Errorf("a line written as digits: %v", err)
	}
	for _, tc := range []struct {
		args      Args
		key, want string
	}{
		{Args{"path": "a.go", "lien": 3}, "lien", "isn't one it takes: it takes path, line, split"},
		{Args{"path": "a.go", "line": "three"}, "line", "is a string, want int"},
		{Args{"path": "a.go", "line": 2.5}, "line", "is a number, want int"},
		{Args{"path": 7}, "path", "is a number, want string"},
		{Args{"line": 1}, "path", "is required"},
	} {
		err := run("file.open", tc.args)
		var ae *ArgError
		if !errors.As(err, &ae) || ae.Command != "file.open" || ae.Key != tc.key || ae.Why != tc.want {
			t.Errorf("%v: err = %v, want %q %s", tc.args, err, tc.key, tc.want)
		}
	}
	if err := r.RunEx(context.Background(), "Open", "a.go", true); err == nil || !strings.Contains(err.Error(), `"args"`) {
		t.Errorf("an ex line comes as args, which file.open doesn't take: %v", err)
	}
	if err := run("file.open", Args{"path": "a.go", "bang": true}); err != nil {
		t.Errorf("a command with an ex name takes its !: %v", err)
	}
	if err := run("any.run", Args{"whatever": 1}); err != nil {
		t.Errorf("a command declaring no args takes anything: %v", err)
	}
	if err := run("quiet.run", Args{"x": 1}); err == nil || !strings.Contains(err.Error(), "it takes none") {
		t.Errorf("NoArgs: %v", err)
	}
	if err := run("quiet.run", Args{"x": nil}); err != nil {
		t.Errorf("a key left nil is as good as absent: %v", err)
	}
	if err := run("relay.run", Args{"to": "x", "id": "s1", "then": []any{}}); err != nil {
		t.Errorf("MoreArgs takes keys it doesn't name: %v", err)
	}
	if err := run("relay.run", Args{"to": 1}); err == nil {
		t.Error("MoreArgs still checks the keys it names")
	}
}

func TestArgTypesTakeWhatArgsReads(t *testing.T) {
	for _, tc := range []struct {
		t    ArgType
		yes  []any
		nope []any
	}{
		{ArgText, []any{"x", 3, 2.5}, []any{true, []any{}}},
		{ArgFloat, []any{1, 1.5}, []any{"1"}},
		{ArgList, []any{[]string{"a"}, []any{"a"}, []Args{{}}, "a, b"}, []any{1, map[string]any{}}},
		{ArgMap, []any{map[string]any{}, Args{}}, []any{[]any{}, "x"}},
		{ArgAny, []any{1, "x", nil}, nil},
	} {
		for _, v := range tc.yes {
			if !tc.t.takes(v) {
				t.Errorf("%s doesn't take %#v", tc.t, v)
			}
		}
		for _, v := range tc.nope {
			if tc.t.takes(v) {
				t.Errorf("%s takes %#v", tc.t, v)
			}
		}
	}
}
