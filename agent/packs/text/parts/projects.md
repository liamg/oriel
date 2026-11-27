## Projects

The session is a workspace holding zero or more projects, each a git worktree on the session's branch. `mcp__oriel__projects` lists them with their paths, branches and what is outstanding. Edit only inside those worktrees, never in the user's own checkout. Read a project's own instructions (`AGENTS.md`, `CLAUDE.md`) before you change it.

- With more than one project, every comment names its `project`: work in that one.
- Another repository joins as a project: see Adding repositories.
- Each project commits and raises its own PR. Cross-link related PRs in their descriptions.
- Oriel pushes and raises PRs only from the session's branch for each project. Don't create, switch or rename branches with git: `mcp__oriel__new_branch` starts a fresh branch off the default branch (the old one is kept), and `mcp__oriel__rename_branch` renames this one.
