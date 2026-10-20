---
id: agent/workspace
covers: [workspace.open, workspace.rebuild, workspace.restart, agent.permissions.dock, term.host]
related: [agent/permissions, agent/console]
---

# Workspace

Where the session in front's agent works, the sandbox it runs in, and what it may do. Open it from **Workspace** in
the sidebar, or `workspace.open` (with `{tab}` for one tab).

## Overview

The session, the sandbox's backend, container and image, and every directory the session uses: each project's
checkout, the session's state, the scratchpad (Claude Code's temp dir), the sandbox's own directory, the agent's
home, Claude's config, the transcripts and the shared Go cache. A directory the agent sees somewhere else says where
("in the sandbox: /tmp/claude-501"). **Browse** opens it in Files; **Copy** copies its path. A running container's
mounts follow, each read-only or read-write.

**Restart sandbox** stops the agent once its turn ends and starts it again in a new sandbox, resumed: the
conversation carries on.

**Sandbox terminal** opens a shell in the sandbox, beside the agent: every terminal you open while a sandboxed
session is in front runs there. **Host terminal** (`term.host`) opens one on your machine instead, in the
workspace, with your own access.

## Files

The scratchpad, or any of the session's directories, as a tree. Opening a file opens it in the editor.

## Image

The image the sandbox runs, where it comes from (the base image, `sandbox.image`, or a repo's
`.oriel/sandbox.Dockerfile`), and its Dockerfile. **Rebuild image** builds it again without the cache, so its
downloads are fetched afresh, then restarts the agent on it once its turn ends.

A repo's Dockerfile is read from its default branch, never from a session's branch: a change the agent makes to it
takes effect once it's merged. An image named in your settings is run as it is: Oriel doesn't build it.

## Build log

The end of the image's last build, following a rebuild as it runs. **Copy** copies it.

## Permissions

What the sandboxed agent may do and the connections it made: see [Permissions](agent/permissions).
`agent.permissions.dock` docks the same view at the bottom instead.

A session whose agent runs on your machine has its directories only: no sandbox, image or permissions.
