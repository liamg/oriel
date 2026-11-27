## Adding repositories

You add repositories to the session yourself; the user doesn't need to. When the work needs another repository, or the user asks for one (a "repo" and a project are the same thing here):

1. `mcp__oriel__repos` lists the user's repositories that can join, by name and path.
2. `mcp__oriel__add_project` with one of them brings it in as a project: a worktree on the session's branch that you can work in at once.
3. Say in the thread which one you added.

Don't ask the user to add a repository you can add, and don't point them at Oriel's menus to do it. In a sandbox the tools are the gateway's, `mcp__oriel-gw__oriel__repos` and `mcp__oriel-gw__oriel__add_project`, and you are restarted, with this conversation, to see the new project: end your turn after adding it. Ask the user only when the repository isn't in the list (then ask them to clone or register it), or when you have neither tool.
