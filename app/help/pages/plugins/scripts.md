---
id: plugins/scripts
covers: [scripts.run, scripts.pick, plugin.scripts.*]
related: [plugins/install#bundled, terminal]
---

# Scripts

Scripts are shell code of your own, run on the session in front: deploy its projects, open its workspace in tmux.
The bundled scripts plugin (`oriel/scripts`) runs them. You keep them in `plugin.scripts.list`, a name for each
script and its code:

```lua
oriel.option["plugin.scripts.list"] = {
  ["Deploy to Tilt"] = "~/bin/tilt-deploy {session}",
  ["Open in tmux"] = [[#!/usr/bin/env fish
tmux new-session -d -s {session_name} -c {session} 2>/dev/null
tmux switch-client -t {session_name}]],
  ["Test"] = "cd {repo} && make test",
}
```

## Running one {#run}

Each script is a row in the omnibox. Type `$` first to see only your scripts (`$tilt`), or use `:Scripts`.
When it ends, a notice says whether it worked, with the last five lines it printed. It runs on your machine, in
the session's workspace, for up to ten minutes.

A job it leaves running in the background (`tilt up &`) carries on once the script ends, but the script's output
closes then, and a job still printing to it is stopped. Send its output elsewhere: `tilt up > /dev/null 2>&1 &`.

## Slots {#slots}

Each slot becomes a reference to an environment variable that holds its value, so a value with spaces or quotes
stays one word, whether you wrote the slot bare (`cd {repo}`) or in quotes of your own (`cd "{repo}"`,
`echo "Deploying {session_name}"`) or in a heredoc's body. A value is never pasted into the code, so a session's
name can't run as code. `${x}` is left for the shell. A quoted heredoc (`<<'EOF'`) expands nothing, so a slot in
one is an error: use `<<EOF`, and write any other `$` in it as `\$`.

- `{session}`: the session's workspace.
- `{session_name}`: the session's name.
- `{primary}`: the primary project's checkout, or the workspace when the session has no projects.
- `{repo}`: a project's checkout.
- `{repo_name}`: the project's name.

A script that names `{repo}` or `{repo_name}` runs for one project at a time, in its checkout: the omnibox has a row
for it per project. Any other `{name}` is a mistake, and the script doesn't run: write it with spaces (`{ name }`,
which awk, jq and Python read the same) for the text itself. `{{session}}` is a literal `{session}`, and any other
`{{word}}` stays as written, so a Go template's `{{end}}` reaches kubectl or helm whole.

The variables are `ORIEL_SCRIPT_SESSION`, `ORIEL_SCRIPT_SESSION_NAME`, `ORIEL_SCRIPT_PRIMARY`, and
`ORIEL_SCRIPT_REPO` and `ORIEL_SCRIPT_REPO_NAME` when it runs for a project. Slots work in shell code only (bash,
sh, zsh, dash, ksh, mksh, ash, fish): a `#!` script in another language, such as Python, reads these variables,
and a slot in it is an error. To run one per project, call it from a line of shell: `python3 ~/bin/deploy.py {repo}`.

## Which shell {#shell}

The code runs with bash, or sh where there's no bash. Start it with a `#!` line to pick another
(`#!/usr/bin/env fish`, `#!/usr/bin/env python3`): that interpreter runs the code, read from a pipe rather than a
file, so `$0` isn't a path to the script.
