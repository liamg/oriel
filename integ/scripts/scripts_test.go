package scripts_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/plugintest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

func TestMain(m *testing.M) { plugintest.Main(m) }

// scene is a work session in front on a workspace of two projects, with the plugin loaded and scripts set.
func scene(t *testing.T, name string, scripts map[string]string) (*plugintest.App, string) {
	t.Helper()
	ws := t.TempDir()
	for _, p := range []string{"api", "web"} {
		if err := os.MkdirAll(filepath.Join(ws, "repos", p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a := plugintest.New(t, plugintest.Options{})
	a.Apply(core.SessionSynced{Session: core.SessionState{ID: "s1", Name: name, Kind: core.KindWork, Dir: ws, Projects: []core.Project{
		{Name: "api", Dir: filepath.Join(ws, "repos", "api"), Primary: true}, {Name: "web", Dir: filepath.Join(ws, "repos", "web")}}}},
		core.SessionFocused{ID: "s1"})
	a.Load("plugin", nil)
	a.SetOption("plugin.scripts.list", scripts)
	return a, ws
}

// noticeWith waits for a notice holding text, and returns it.
func noticeWith(a *plugintest.App, text string) core.Notification {
	var got core.Notification
	a.Until("a notice with "+text, func() bool {
		for _, n := range a.Store().State().Notifications {
			if strings.Contains(n.Text, text) {
				got = n
				return true
			}
		}
		return false
	})
	return got
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A script for the whole session runs in the workspace with its slots filled in, each a single shell word however
// it's spelled; ${x} is the shell's. The notice shows the last lines it printed, stderr too.
func TestScriptForTheSession(t *testing.T) {
	a, ws := scene(t, "it's tilt", map[string]string{
		"Deploy": `printf '%s|%s|%s' {session} {session_name} "${HOME:+home}" > slots.txt
for i in 1 2 3 4 5 6; do echo line$i; done
echo warned >&2`,
	})
	a.Run("scripts.run", registry.Args{"name": "Deploy"})
	n := noticeWith(a, "Deploy")
	if got, want := read(t, filepath.Join(ws, "slots.txt")), ws+"|it's tilt|home"; got != want {
		t.Errorf("slots = %q, want %q", got, want)
	}
	if n.Severity != core.SevInfo || strings.Contains(n.Text, "line2") || !strings.Contains(n.Text, "line3") || !strings.Contains(n.Text, "warned") {
		t.Errorf("notice = %v %q", n.Severity, n.Text)
	}
}

// A slot inside double or single quotes, or in a $(…) or `…` within them, is filled in as that quoting needs, so
// `cd "{repo}"` works as written, and the value stays one word that the shell reads nothing in.
func TestScriptWithQuotedSlots(t *testing.T) {
	name := "it's \"a  b\" $HOME \\ `id`"
	a, ws := scene(t, name, map[string]string{
		"Deploy": `# don't let the quote in this comment count
printf '%s|%s|%s|%s' "{session_name}" "Deploying {session_name}!" '{session_name}' "{session_name}s" > quoted.txt
printf '%s|%s|%s' "$(printf '%s' {session_name})" "$(printf '%s' '{session_name}')" "` + "`printf '%s' {session_name}`" + `" > nested.txt`,
	})
	a.Run("scripts.run", registry.Args{"name": "Deploy"})
	noticeWith(a, "Deploy")
	if got, want := read(t, filepath.Join(ws, "quoted.txt")), name+"|Deploying "+name+"!|"+name+"|"+name+"s"; got != want {
		t.Errorf("quoted.txt = %q, want %q", got, want)
	}
	if got, want := read(t, filepath.Join(ws, "nested.txt")), name+"|"+name+"|"+name; got != want {
		t.Errorf("in $(…) and backticks: %q, want %q", got, want)
	}
}

// A slot the quote tracker reads wrongly gives wrong text, never code: here the escaped quote in $'…', which it
// doesn't follow.
func TestScriptSlotNeverRunsAsCode(t *testing.T) {
	a, ws := scene(t, "x$(touch pwned)`touch pwned2`", map[string]string{"Deploy": `echo $'it\'s' > /dev/null
echo {session_name} > out.txt`})
	a.Run("scripts.run", registry.Args{"name": "Deploy"})
	noticeWith(a, "Deploy")
	for _, f := range []string{"pwned", "pwned2"} {
		if _, err := os.Stat(filepath.Join(ws, f)); err == nil {
			t.Errorf("the session name ran as code: %s exists", f)
		}
	}
	if got := read(t, filepath.Join(ws, "out.txt")); got == "" {
		t.Error("the script didn't run")
	}
}

// A slot in a heredoc's body reads as its value, and the code after the heredoc is read afresh: the body's
// apostrophe opens nothing.
func TestScriptSlotInAHeredoc(t *testing.T) {
	name := "it's x$(touch pwned)"
	a, ws := scene(t, name, map[string]string{"Deploy": `cat <<EOF > here.txt
name: {session_name}s
it's done
EOF
cat <<-'EOF' > /dev/null
	it's done
	EOF
printf '%s' {session_name} > after.txt`})
	a.Run("scripts.run", registry.Args{"name": "Deploy"})
	noticeWith(a, "Deploy")
	if got, want := read(t, filepath.Join(ws, "here.txt")), "name: "+name+"s\nit's done\n"; got != want {
		t.Errorf("here.txt = %q, want %q", got, want)
	}
	if got := read(t, filepath.Join(ws, "after.txt")); got != name {
		t.Errorf("after the heredocs: %q, want %q", got, name)
	}
	if _, err := os.Stat(filepath.Join(ws, "pwned")); err == nil {
		t.Error("the session name ran as code")
	}
}

// Nothing expands in a quoted heredoc's body, so a slot can't give its value there: it's refused, before anything
// runs.
func TestScriptSlotInAQuotedHeredoc(t *testing.T) {
	for _, q := range []string{`'EOF'`, `"EOF"`, `\EOF`} {
		a, ws := scene(t, "tilt", map[string]string{"Apply": "touch ran.txt\ncat <<" + q + "\nname: {session_name}\nEOF"})
		a.Run("scripts.run", registry.Args{"name": "Apply"})
		if n := noticeWith(a, "quoted heredoc"); n.Severity != core.SevError || !strings.Contains(n.Text, "{session_name}") {
			t.Errorf("<<%s: notice = %+v", q, n)
		}
		if _, err := os.Stat(filepath.Join(ws, "ran.txt")); err == nil {
			t.Errorf("<<%s: the script ran", q)
		}
	}
}

// A #! line that runs a shell through env with a variable set is still a shell, and fills slots.
func TestScriptWithAShebangThroughEnv(t *testing.T) {
	a, ws := scene(t, "tilt", map[string]string{"Env": "#!/usr/bin/env FOO=1 sh\nprintf '%s|%s' {session_name} \"$FOO\" > env.txt"})
	a.Run("scripts.run", registry.Args{"name": "Env"})
	noticeWith(a, "Env")
	if got := read(t, filepath.Join(ws, "env.txt")); got != "tilt|1" {
		t.Errorf("env.txt = %q", got)
	}
}

// A #! script for an interpreter that isn't a shell can't use slots: it reads the environment instead.
func TestScriptSlotInAShebangThatIsntAShell(t *testing.T) {
	a, _ := scene(t, "tilt", map[string]string{"Py": "#!/usr/bin/env python3\nprint('{session_name}')"})
	a.Run("scripts.run", registry.Args{"name": "Py"})
	if n := noticeWith(a, "ORIEL_SCRIPT_SESSION_NAME"); n.Severity != core.SevError {
		t.Errorf("notice = %+v", n)
	}
}

// A script naming {repo} or {repo_name} runs in the project it's run for, and needs one.
func TestScriptForAProject(t *testing.T) {
	a, ws := scene(t, "tilt", map[string]string{"Up": `printf '%s|%s' {repo_name} "$(pwd)" > {repo}/up.txt`})
	a.Run("scripts.run", registry.Args{"name": "Up", "project": "web"})
	noticeWith(a, "Up")
	dir := filepath.Join(ws, "repos", "web")
	real, _ := filepath.EvalSymlinks(dir)
	if got := read(t, filepath.Join(dir, "up.txt")); got != "web|"+dir && got != "web|"+real {
		t.Errorf("up.txt = %q", got)
	}

	a.Run("scripts.run", registry.Args{"name": "Up"})
	if n := noticeWith(a, "say which project"); n.Severity != core.SevError {
		t.Errorf("without a project: %+v", n)
	}
}

// A project given for a script that doesn't run per project is left out of its notice, as it ran in the workspace.
func TestScriptForTheSessionGivenAProject(t *testing.T) {
	a, _ := scene(t, "tilt", map[string]string{"Deploy": "true"})
	a.Run("scripts.run", registry.Args{"name": "Deploy", "project": "api"})
	if n := noticeWith(a, "Deploy"); !strings.HasPrefix(n.Text, "Deploy finished") {
		t.Errorf("notice = %q", n.Text)
	}
}

// A script starting with #! runs under the interpreter it names.
func TestScriptWithAShebang(t *testing.T) {
	a, ws := scene(t, "tilt", map[string]string{"Who": "#!/bin/sh\nprintf '%s' \"$0\" > shebang.txt"})
	a.Run("scripts.run", registry.Args{"name": "Who"})
	noticeWith(a, "Who")
	if got := read(t, filepath.Join(ws, "shebang.txt")); got == "sh" || got == "bash" || got == "" {
		t.Errorf("$0 = %q: it ran as -c code, not as a file", got)
	}
}

// Slots are in the environment too, unquoted, for an interpreter that doesn't read shell quoting.
func TestScriptSlotsInTheEnvironment(t *testing.T) {
	a, ws := scene(t, "it's tilt", map[string]string{"Env": "#!/bin/sh\n" +
		`printf '%s|%s|%s|%s|%s' "$ORIEL_SCRIPT_SESSION" "$ORIEL_SCRIPT_SESSION_NAME" "$ORIEL_SCRIPT_PRIMARY" "$ORIEL_SCRIPT_REPO" "$ORIEL_SCRIPT_REPO_NAME" > "$ORIEL_SCRIPT_SESSION/env.txt" # {repo}`})
	a.Run("scripts.run", registry.Args{"name": "Env", "project": "web"})
	noticeWith(a, "Env · web finished")
	web := filepath.Join(ws, "repos", "web")
	if got, want := read(t, filepath.Join(ws, "env.txt")), ws+"|it's tilt|"+filepath.Join(ws, "repos", "api")+"|"+web+"|web"; got != want {
		t.Errorf("env = %q, want %q", got, want)
	}
}

// A #! script runs with no file of its own, so a temp folder mounted noexec doesn't stop it and a timeout leaves
// nothing behind. Its stdin isn't its code.
func TestScriptWithAShebangNeedsNoTempFile(t *testing.T) {
	a, ws := scene(t, "tilt", map[string]string{"Who": "#!/bin/sh -e\nread -r line || true\nprintf '%s|%s' \"$line\" \"$0\" > who.txt"})
	tmp := os.TempDir()
	a.Run("scripts.run", registry.Args{"name": "Who"})
	noticeWith(a, "Who finished")
	stdin, file, _ := strings.Cut(read(t, filepath.Join(ws, "who.txt")), "|")
	if stdin != "" || strings.HasPrefix(file, tmp) {
		t.Errorf("stdin = %q, run from %q in the temp folder %q", stdin, file, tmp)
	}
}

// A #! line saved with a Windows line ending still names its interpreter.
func TestScriptWithAShebangAndACarriageReturn(t *testing.T) {
	a, ws := scene(t, "tilt", map[string]string{"Crlf": "#!/bin/sh\r\nprintf ok > crlf.txt"})
	a.Run("scripts.run", registry.Args{"name": "Crlf"})
	noticeWith(a, "Crlf")
	if got := read(t, filepath.Join(ws, "crlf.txt")); got != "ok" {
		t.Errorf("crlf.txt = %q", got)
	}
}

// A script that fails is an error notice with why and its last lines.
func TestScriptThatFails(t *testing.T) {
	a, _ := scene(t, "tilt", map[string]string{"Boom": "echo boom; exit 3"})
	a.Run("scripts.run", registry.Args{"name": "Boom"})
	n := noticeWith(a, "boom")
	if n.Severity != core.SevError || !strings.Contains(n.Text, "Boom failed") {
		t.Errorf("notice = %v %q", n.Severity, n.Text)
	}
}

// {primary} is the primary project's checkout, and a script naming it still runs once, for the whole session.
func TestScriptWithThePrimaryProject(t *testing.T) {
	a, ws := scene(t, "tilt", map[string]string{"Open": `printf '%s' {primary} > primary.txt`})
	// web is primary but not first, so first-in-the-list and primary differ.
	a.Apply(core.SessionSynced{Session: core.SessionState{ID: "s1", Name: "tilt", Kind: core.KindWork, Dir: ws, Projects: []core.Project{
		{Name: "api", Dir: filepath.Join(ws, "repos", "api")}, {Name: "web", Dir: filepath.Join(ws, "repos", "web"), Primary: true}}}})
	a.Run("scripts.run", registry.Args{"name": "Open"})
	noticeWith(a, "Open finished")
	if got, want := read(t, filepath.Join(ws, "primary.txt")), filepath.Join(ws, "repos", "web"); got != want {
		t.Errorf("{primary} = %q, want %q", got, want)
	}
	a.Run("scripts.pick", nil)
	a.UntilText("Open")
	if a.Has("Open · web") {
		t.Errorf("a script naming {primary} is listed per project:\n%s", a.Text())
	}
}

// The list is code run under this plugin's exec grant, so only your own config may set it.
func TestScriptsListIsYoursAlone(t *testing.T) {
	a, _ := scene(t, "tilt", nil)
	if sp, ok := a.Registry().Options().Spec("plugin.scripts.list"); !ok || !sp.UserOnly {
		t.Errorf("plugin.scripts.list = %+v, want user_only", sp)
	}
}

// A slot the plugin doesn't know is an error, and nothing runs.
func TestUnknownSlot(t *testing.T) {
	a, ws := scene(t, "tilt", map[string]string{"Odd": "touch ran.txt {nope}"})
	a.Run("scripts.run", registry.Args{"name": "Odd"})
	if n := noticeWith(a, "{nope}"); n.Severity != core.SevError || !strings.Contains(n.Text, "{ nope }") {
		t.Errorf("notice = %+v", n)
	}
	if _, err := os.Stat(filepath.Join(ws, "ran.txt")); err == nil {
		t.Error("it ran")
	}
}

// {{slot}} is a literal {slot}; any other {{word}} is left as written, so a Go template's {{end}} reaches kubectl or
// helm whole.
func TestScriptWithALiteralBrace(t *testing.T) {
	a, ws := scene(t, "tilt", map[string]string{"Lit": `printf '%s|%s|%s' '{{range .items}}{{else}}{{end}}' "{{session}}" {session} > lit.txt`})
	a.Run("scripts.run", registry.Args{"name": "Lit"})
	noticeWith(a, "Lit finished")
	if got, want := read(t, filepath.Join(ws, "lit.txt")), "{{range .items}}{{else}}{{end}}|{session}|"+ws; got != want {
		t.Errorf("lit.txt = %q, want %q", got, want)
	}
}

// The picker lists each script once, and a project's script once for each project.
func TestScriptsPicker(t *testing.T) {
	a, _ := scene(t, "tilt", map[string]string{"Deploy": "true", "Up": "cd {repo}"})
	a.Run("scripts.pick", nil)
	a.UntilText("Deploy", "Up · api", "Up · web")
}

// $ in the omnibox narrows it to the scripts.
func TestScriptsPrefix(t *testing.T) {
	a, _ := scene(t, "tilt", map[string]string{"Deploy": "true", "Up": "cd {repo}"})
	a.Run("omnibox.open", registry.Args{"query": "$"})
	a.UntilText("Deploy", "Up · api", "Up · web")
	if a.Has("Commands") {
		t.Errorf("$ isn't narrowed to scripts:\n%s", a.Text())
	}
}
