## Projects

The session is a workspace holding zero or more projects, each a git worktree on the session's branch. `mcp__oriel__projects` lists them with their paths, branches and what is outstanding. Edit only inside those worktrees, never in the user's own checkout. Read a project's own instructions (`AGENTS.md`, `CLAUDE.md`) before you change it.

- With more than one project, every comment names its `project`: work in that one.
- When the work needs another repository, `mcp__oriel-gw__oriel__repos` lists what is available and `mcp__oriel-gw__oriel__add_project` brings it in; with no gateway, ask the user to add it. Say so in the thread. In a sandbox you are restarted, with this conversation, to see the new project: end your turn after adding it.
- Each project commits and raises its own PR. Cross-link related PRs in their descriptions.
