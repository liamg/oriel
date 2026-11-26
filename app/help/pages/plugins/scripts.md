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
  ["Deploy to Tilt"] = '~/bin/tilt-deploy "$ORIEL_SCRIPT_SESSION"',
  ["Open in tmux"] = [[#!/usr/bin/env fish
tmux new-session -d -s $ORIEL_SCRIPT_SESSION_NAME -c $ORIEL_SCRIPT_SESSION 2>/dev/null
tmux switch-client -t $ORIEL_SCRIPT_SESSION_NAME]],
  ["Test"] = 'cd "$ORIEL_SCRIPT_REPO" && make test',
}
```

## Running one {#run}

Each script is a row in the omnibox. Type `$` first to see only your scripts (`$tilt`), or use `:Scripts`.
When it ends, a notice says whether it worked, with the last five lines it printed. It runs on your machine, in
the session's workspace, for up to ten minutes.

A job it leaves running in the background (`tilt up &`) carries on once the script ends, but the script's output
closes then, and a job still printing to it is stopped. Send its output elsewhere: `tilt up > /dev/null 2>&1 &`.

## The session's values {#values}

The code runs exactly as written. The session's values are in its environment:

- `ORIEL_SCRIPT_SESSION`: the session's workspace.
- `ORIEL_SCRIPT_SESSION_NAME`: the session's name.
- `ORIEL_SCRIPT_PRIMARY`: the primary project's checkout, or the workspace when the session has no projects.
- `ORIEL_SCRIPT_REPO`: a project's checkout.
- `ORIEL_SCRIPT_REPO_NAME`: the project's name.

A script that mentions `ORIEL_SCRIPT_REPO` or `ORIEL_SCRIPT_REPO_NAME` anywhere in its text runs for one project at
a time, in its checkout: the omnibox has a row for it per project. That works in any language.

Quote the variables as you would any other (`cd "$ORIEL_SCRIPT_REPO"`). Anyone who can rename a session sets its
name, so treat `ORIEL_SCRIPT_SESSION_NAME` as untrusted text. Pass it as an argument, never inside a string that
another shell runs (`ssh host "…"`, `sh -c "…"`, `eval`), where it would run as code.

Only the list entry's text is checked for `ORIEL_SCRIPT_REPO`, so a script file that reads it needs it passed from
the entry: `~/bin/deploy "$ORIEL_SCRIPT_REPO"`. Write `"${ORIEL_SCRIPT_REPO:?}"` to fail when it's empty rather
than run in the wrong place.

## What a script can reach {#reach}

- It runs on your machine, as you, even for a sandboxed session. `make` or `tilt up` in a project runs the Makefile
  or Tiltfile the session's agent may have written, outside its sandbox.
- It gets Oriel's whole environment, so any tokens Oriel was started with reach it.
- Only you start one: from the omnibox, `:Scripts`, or a key or command of your own config. No plugin can run
  `scripts.run`.
- The lines a notice shows are kept with your notices. Don't print secrets.
- Any plugin can read `plugin.scripts.list`, though none can change it. Keep secrets out of it: have the script
  read them from a file or your keychain.

## Which shell {#shell}

The code runs with bash, or sh where there's no bash. Start it with a `#!` line to pick another
(`#!/usr/bin/env fish`, `#!/usr/bin/env python3`): that interpreter runs the code, read from a pipe rather than a
file, so `$0` isn't a path to the script.
