---
id: agent/launcher
covers: [app.overlay.launcher]
related: [agent/console, shell/start]
---

# New session

The launcher starts a session: `⌘N`, **New Session** on the start view, or `session.new` from anywhere (with
`kind`, `prompt`, `ticket`, `pr`, `dir`, `repos` or `provider` filled in). `⌘↵` starts it, `esc` closes it.

## Prompt

What the agent should do. A **ticket** names the branch and goes to the agent too. Some modes ask for more first:
**Review PR** a pull request's URL, **New repo** a name, a template (empty, a Go module, a Go CLI, or a git URL) and
where it goes, **Direct** the directory to open.

## Mode

The kind of session, and so the prompt pack its agent starts with: **Plan** drafts docs first and makes worktrees at
Start work; **Work** goes straight to the worktrees; **Review PR** checks out someone else's change read-only;
**Tour** explains how something works; **Job** runs to done on its own; **New repo** starts from nothing; **Direct**
opens a directory as it is, with no worktree; **Scratch** has no repo. Plugins add kinds to the worker, and they show
here too. `h` and `l` move along the strip.

## Agent

Claude, Codex or another provider, with its model, effort and what's left of its plan; or no agent at all. An agent
in a **Direct** session edits your real files.

## Repositories

The repos the session works on: each gets a worktree, all on one branch. Pick a registered repo with **+ add**, or
type a checkout's path. The branch is named from the ticket and prompt unless you name it.

A repo the chosen worker hasn't got says **not on** that worker, with **Clone**: give it the URL (filled from your
checkout's remote) and it's cloned onto the worker, with that machine's own git and credentials, before the session
starts.

## Run on

The worker the session runs on, whether its agent runs in a sandbox (a container, with the gateway for git and gh;
on unless the `sandbox.default` setting says `host`), and whether the sandbox's network is open or asks per new
host.

## Recent prompts

The prompts recent sessions started with. Choosing one fills in its prompt, mode and repos again.

## Pre-flight

What starting will do: when worktrees are made, how many repos are fetched, the agent and its prompt pack, and where
it runs.
