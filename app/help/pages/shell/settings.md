---
id: shell/settings
covers: [app.overlay.settings]
related: [shell/theme, shell/help]
---

# Settings

`:Settings` or `⌘,` opens the page: every option, by section, with what it does, its value and where that value
comes from. Settings live in `~/.config/oriel/settings.toml`, and a project's in `.oriel/settings.toml`. Edit
them here or by hand; both apply at once, and a change here rewrites only its own line.

## Sections {#sections}

One per option prefix (`editor`, `ui`, `whichkey`), or the section a plugin gives. The dot counts the options
set away from their default.

## Search {#search}

Words match an option's name, help, vim aliases and value. `@modified` keeps the ones set anywhere, `@project`
the ones the project file sets, and `@plugin:id` a plugin's.

## Scope {#scope}

What the page edits: your settings file, this project's, or this session (never written to disk). Later ones
win: defaults, your file, `init.lua`, the project's file, the session.

## Options {#rows}

Each row shows its value and where it was set: default, user, project, session, or `set in init.lua:12`. A
value set in Lua can't be changed here; change it in Lua. A bar on the left marks a value that isn't the
default. A list is a row per item: edit one in place and press Enter, `✕` removes it, and the field under them
adds one; an item that's wrong says why on its row. A list with known values (environment variables, servers) is a
menu you tick them in. A table of named values says how many it has; **Edit…** opens it in a dialog where you add,
change and remove entries, then Save (Escape or Cancel leaves it as it was).

## Reset {#reset}

`↺` resets an option at the scope you're editing: its line leaves the file. Reset section and Reset all do the
same for more; Undo takes the last change back.

## As TOML {#toml}

Shows the line you'd write by hand for each option, and the Lua.

## Project settings {#trust}

A project's settings file applies only once you trust it, and again each time it changes: Oriel asks with a
notice. Your own edits to it from this page are trusted. The repos your sessions work in bring their own project
files too, each trusted the same way.

## Sandbox {#sandbox}

The `sandbox` section is how sandboxed agents run: their image, memory and processes, the host variables they
get, network rules (`allow *.golang.org:443`), gateway rules (`deny oriel__push`), and extra MCP servers for the
gateway, each a URL or a command line (`npx -y @foo/mcp`). New sessions run sandboxed unless `sandbox.default` says
`host`. A project's file can add its own rules and image; a worker on another machine reads its own repos' files,
trusted there. Changes reach the worker at once and apply to the sandboxes it opens next.
