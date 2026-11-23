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

## Slots {#slots}

These are filled in before the code runs, each in single quotes, so a value with spaces stays one word. `${x}` is
left for the shell.

- `{session}`: the session's workspace.
- `{session_name}`: the session's name.
- `{primary}`: the primary project's checkout, or the workspace when the session has no projects.
- `{repo}`: a project's checkout.
- `{repo_name}`: the project's name.

A script that names `{repo}` or `{repo_name}` runs for one project at a time, in its checkout: the omnibox has a row
for it per project. Any other `{name}` is a mistake, and the script doesn't run.

## Which shell {#shell}

The code runs with bash, or sh where there's no bash. Start it with a `#!` line to pick another
(`#!/usr/bin/env fish`, `#!/usr/bin/env python3`): it's written to a file and run as a program.
