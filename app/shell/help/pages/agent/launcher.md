---
id: agent/launcher
covers: [app.overlay.launcher]
related: [agent/console, shell/start, agent/plugins]
---

# New session

The launcher starts a session: `⌘N`, **New Session** on the start view, or `session.new` from anywhere (with
`kind`, `prompt`, `ticket`, `pr`, `dir`, `repos` or `provider` filled in). `⌘↵` starts it, `esc` closes it.

## Prompt

What the agent should do. A **ticket** names the branch and goes to the agent too. **Review PR** asks for a pull
request's URL instead; a pull request's link pasted into the prompt picks Review PR with the link filled in.

## Ticket

A ticket's ID in the ticket field (`ENG-2231`) expands into a card once a tracker's plugin knows it (Linear's does):
its key and state, its title, the start of its description, and what's on it (attachments, a linked PR, comments).
The ticket, with its comments and attachments, goes to the agent as context after your prompt; with no prompt of
your own, the ticket is the request. **✕** takes it off. Start waits while the card is still being looked up.

## Mode

The kind of session, and so the prompt pack its agent starts with: **Work** (the default) goes straight to the
worktrees; **Plan** drafts docs first and makes worktrees at Start work; **Review PR** checks out someone else's change
read-only. Work and Plan start with no repos too: a chat, or work whose repos come later. `h` and `l` move along the
strip.

The other kinds aren't modes here: a **job** runs from the Jobs page (`:help jobs`), and **New repo**, **Direct** or
any kind starts with `session.start {kind}` (from Lua, the omnibox or Ori). `session.new {kind}` opens the launcher on
any kind, and a plugin's kind is a mode here when it asks to be (`Launcher` on its kind).

## Agent

Claude, Codex or another provider, with its model, effort and what's left of its plan; or no agent at all. An agent
in a **Direct** session edits your real files. A provider that isn't signed in, or whose login has stopped working,
says so instead of its plan. A plugin can add an agent here, or take a built-in's place (`:help agent/plugins`).

## Repositories

The repos the session works on: each gets a worktree, all on one branch. Pick a registered repo with **+ add**, or
type a checkout's path. The branch is named from the ticket and prompt unless you name it. To register a repo
first, run `repos.add` with its `path`, or `repos.clone` with a `url`.

## Runs in

Whether the agent runs in a sandbox (with the gateway for git and gh; on unless the `sandbox.default` setting says
`host`), and whether the sandbox's network is open or asks per new host.

**Sandbox type** shows when this machine can run more than one kind of sandbox: `docker`, a container, or `seatbelt`,
macOS's own sandbox. It starts at the `sandbox.backend` setting; the session keeps the one you pick.

## Recent prompts

The prompts recent sessions started with. Choosing one fills in its prompt, mode and repos again.

## Pre-flight

What starting will do: when worktrees are made, how many repos are fetched, the agent and its prompt pack, and where
it runs.

## Save as job

**Save as job** keeps the prompt to run again: it opens the New job dialog with the prompt, the agent and where it
runs filled in, and the launcher closes. A job names no repo or branch, so the repos you picked aren't kept: name
them in the prompt. See Jobs (`:help jobs`).
